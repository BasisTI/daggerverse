// Módulo de utilitários Git para orchestrators de CI/CD.
//
// Fornece funções reutilizáveis para detecção de mudanças e consulta de commits,
// permitindo que orchestrators de diferentes projetos compartilhem a mesma lógica.
package main

import (
	"context"
	"dagger/orchestrator-utils/internal/dagger"
	"encoding/json"
	"fmt"
	"strings"
)

// OrchestratorUtils fornece utilitários Git para pipelines de CI/CD.
type OrchestratorUtils struct{}

// GetChangedProjects retorna os nomes dos projetos que tiveram mudanças
// comparando a branch atual com a branch base.
//
// projectPathsJson deve ser um JSON no formato {"nome":"path",...}
// mapeando nomes de projeto aos seus paths no repositório.
func (u *OrchestratorUtils) GetChangedProjects(
	ctx context.Context,
	// Diretório raiz do repositório Git.
	source *dagger.Directory,
	// Branch base para comparação (ex: "origin/develop").
	baseBranch string,
	// JSON com mapeamento nome->path dos projetos. Ex: {"backend":"backend","frontend":"frontend"}
	projectPathsJson string,
) ([]string, error) {
	var projectPaths map[string]string
	if err := json.Unmarshal([]byte(projectPathsJson), &projectPaths); err != nil {
		return nil, err
	}

	output, err := dag.Container().From("alpine/git").
		WithWorkdir("/src").WithDirectory("/src", source).
		WithExec([]string{"git", "config", "--global", "--add", "safe.directory", "/src"}).
		WithExec([]string{"git", "diff", "--name-only", baseBranch + "...HEAD"}).
		Stdout(ctx)
	if err != nil {
		return nil, err
	}

	affected := make(map[string]bool)
	for _, file := range strings.Split(strings.TrimSpace(output), "\n") {
		for name, path := range projectPaths {
			if strings.HasPrefix(file, path) {
				affected[name] = true
			}
		}
	}

	var list []string
	for k := range affected {
		list = append(list, k)
	}
	return list, nil
}

// GetLastCommitSha retorna o SHA do último commit que alterou o path especificado,
// ignorando commits automáticos de CI (prefixados com [skip ci]).
func (u *OrchestratorUtils) GetLastCommitSha(
	ctx context.Context,
	// Diretório raiz do repositório Git.
	source *dagger.Directory,
	// Path do projeto no repositório.
	path string,
) (string, error) {
	out, err := dag.Container().From("alpine/git").
		WithWorkdir("/src").WithDirectory("/src", source).
		WithExec([]string{"git", "config", "--global", "--add", "safe.directory", "/src"}).
		WithExec([]string{"git", "log", "-n", "1", "--first-parent", "--pretty=format:%H",
			"--grep=^\\[skip ci\\]", "--invert-grep", "--", path}).
		Stdout(ctx)
	return strings.TrimSpace(out), err
}

// TagWithSha adiciona uma tag sha-{commitSha} a cada imagem publicada.
// publishedImages contém uma imagem por linha no formato retornado por Publish
// (ex: registry/path:tag@sha256:...).
func (u *OrchestratorUtils) TagWithSha(
	ctx context.Context,
	// Imagens publicadas (uma por linha, formato: registry/path:tag@sha256:...)
	publishedImages string,
	// SHA completo do commit
	commitSha string,
	// Usuário para autenticação no registry.
	registryUser string,
	// Senha para autenticação no registry.
	registryPass *dagger.Secret,
) error {
	lines := strings.Split(strings.TrimSpace(publishedImages), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Formato: registry/path:tag@sha256:digest
		// Precisamos extrair registry/path (sem :tag) para montar a ref
		// crane tag aceita a ref completa (com digest) e cria a nova tag
		imageRef := line

		// Extrair registry para auth: tudo antes do primeiro /
		registry := strings.SplitN(imageRef, "/", 2)[0]

		shaTag := "sha-" + commitSha

		fmt.Printf("🏷️  Tagging %s as %s\n", imageRef, shaTag)

		crane := dag.Container().From("gcr.io/go-containerregistry/crane:debug").
			WithSecretVariable("REGISTRY_PASS", registryPass).
			WithExec([]string{"sh", "-c",
				fmt.Sprintf("crane auth login %s -u %s -p \"$REGISTRY_PASS\"", registry, registryUser)}).
			WithExec([]string{"crane", "tag", imageRef, shaTag})

		_, err := crane.Sync(ctx)
		if err != nil {
			return fmt.Errorf("failed to tag %s: %w", imageRef, err)
		}
	}
	return nil
}

// CheckImages verifica se todas as imagens de um projeto existem no registry
// usando a tag sha-{lastCommitSha} de cada subprojeto.
//
// projectImagesJson mapeia "caminho_imagem_no_registry" -> "path_no_repo".
// Ex: {"licitacao/admin_backend":"admin_backend"}
func (u *OrchestratorUtils) CheckImages(
	ctx context.Context,
	// Diretório raiz do repositório Git.
	source *dagger.Directory,
	// JSON {"imagem_no_registry":"repo_path"}
	projectImagesJson string,
	// URL do registry Docker.
	registry string,
	// Usuário para autenticação no registry.
	// +optional
	registryUser string,
	// Senha para autenticação no registry.
	// +optional
	registryPass *dagger.Secret,
) error {
	var projectImages map[string]string
	if err := json.Unmarshal([]byte(projectImagesJson), &projectImages); err != nil {
		return fmt.Errorf("failed to parse projectImagesJson: %w", err)
	}

	crane := dag.Container().From("gcr.io/go-containerregistry/crane:debug")
	if registryUser != "" && registryPass != nil {
		crane = crane.
			WithSecretVariable("REGISTRY_PASS", registryPass).
			WithExec([]string{"sh", "-c",
				fmt.Sprintf("crane auth login %s -u %s -p \"$REGISTRY_PASS\"", registry, registryUser)})
	}

	var missing []string
	for imagePath, repoPath := range projectImages {
		lastSha, err := u.GetLastCommitSha(ctx, source, repoPath)
		if err != nil {
			return fmt.Errorf("failed to get last commit SHA for %s: %w", repoPath, err)
		}

		tag := fmt.Sprintf("%s/%s:sha-%s", registry, imagePath, lastSha)
		fmt.Printf("🔎 Verificando existência: %s\n", tag)

		_, err = crane.WithExec([]string{"crane", "manifest", tag}).Sync(ctx)
		if err != nil {
			fmt.Printf("❌ Faltante: %s\n", tag)
			missing = append(missing, imagePath)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("imagens não encontradas para: %v — verifique a pipeline de develop", missing)
	}
	return nil
}

// CommitAndPush commits specified files and pushes to a remote branch.
// The commit message is automatically prefixed with [skip ci] to prevent
// triggering a new pipeline (supported natively by GitLab CI).
func (u *OrchestratorUtils) CommitAndPush(
	ctx context.Context,
	// Diretório raiz do repositório Git com as alterações.
	source *dagger.Directory,
	// Arquivos a commitar (ex: ["odoo-modules.yaml", "addons/rh_basis/__manifest__.py"]).
	files []string,
	// Mensagem do commit (será prefixada com [skip ci]).
	message string,
	// Branch de destino para o push.
	branch string,
	// URL do remote Git com credenciais (ex: https://token:xxx@gitlab.com/org/repo.git).
	remoteUrl string,
) error {
	if len(files) == 0 {
		fmt.Println("CommitAndPush: nenhum arquivo especificado, pulando")
		return nil
	}

	git := dag.Container().From("alpine/git").
		WithEnvVariable("GIT_SSL_CAINFO", "/etc/ssl/certs/ca-certificates.crt").
		WithDirectory("/src", source).
		WithWorkdir("/src").
		WithExec([]string{"git", "config", "--global", "--add", "safe.directory", "/src"}).
		WithExec([]string{"git", "config", "user.email", "ci-bot@basis.com.br"}).
		WithExec([]string{"git", "config", "user.name", "CI Bot"})

	// Stage each file
	for _, f := range files {
		git = git.WithExec([]string{"git", "add", f})
	}

	// Commit
	commitMsg := fmt.Sprintf("[skip ci] %s", message)
	git = git.WithExec([]string{"git", "commit", "-m", commitMsg})

	// Push
	git = git.WithExec([]string{"git", "push", remoteUrl, "HEAD:" + branch})

	_, err := git.Sync(ctx)
	if err != nil {
		return fmt.Errorf("commit-and-push failed: %w", err)
	}

	fmt.Printf("CommitAndPush: pushed %d file(s) to %s\n", len(files), branch)
	return nil
}

// GitLabConfig stores the data needed to report commit statuses to GitLab.
type GitLabConfig struct {
	Host        string         // URL base (ex: "https://gitlab.com")
	TokenSecret *dagger.Secret // Token com scope `api`
	ProjectID   string         // ID numérico ou path URL-encoded
}

// NewGitLabConfig validates the provided inputs and returns a GitLab configuration for commit statuses.
func (u *OrchestratorUtils) NewGitLabConfig(
	// URL base do GitLab (ex: "https://gitlab.com").
	host string,
	// Token com scope `api` para autenticação.
	tokenSecret *dagger.Secret,
	// ID numérico ou path URL-encoded do projeto.
	projectID string,
) (*GitLabConfig, error) {
	if host == "" {
		return nil, fmt.Errorf("host is empty")
	}
	if tokenSecret == nil {
		return nil, fmt.Errorf("token secret is empty")
	}
	if projectID == "" {
		return nil, fmt.Errorf("project ID is empty")
	}
	return &GitLabConfig{
		Host:        host,
		TokenSecret: tokenSecret,
		ProjectID:   projectID,
	}, nil
}

// Promote promove imagens de um registry de staging para production,
// relendo a versão original do label OCI e fazendo crane copy.
//
// projectImagesJson mapeia "caminho_imagem_no_registry" -> "path_no_repo".
func (u *OrchestratorUtils) Promote(
	ctx context.Context,
	// Diretório raiz do repositório Git.
	source *dagger.Directory,
	// JSON {"imagem_no_registry":"repo_path"}
	projectImagesJson string,
	// Registry de origem (staging).
	srcRegistry string,
	// Registry de destino (production). Se vazio, usa srcRegistry.
	// +optional
	dstRegistry string,
	// Usuário para autenticação nos registries.
	registryUser string,
	// Senha para autenticação nos registries.
	registryPass *dagger.Secret,
) error {
	if dstRegistry == "" {
		dstRegistry = srcRegistry
	}

	var projectImages map[string]string
	if err := json.Unmarshal([]byte(projectImagesJson), &projectImages); err != nil {
		return fmt.Errorf("failed to parse projectImagesJson: %w", err)
	}

	crane := dag.Container().From("gcr.io/go-containerregistry/crane:debug").
		WithSecretVariable("REGISTRY_PASS", registryPass).
		WithExec([]string{"sh", "-c",
			fmt.Sprintf("crane auth login %s -u %s -p \"$REGISTRY_PASS\"", srcRegistry, registryUser)})

	if dstRegistry != srcRegistry {
		crane = crane.WithExec([]string{"sh", "-c",
			fmt.Sprintf("crane auth login %s -u %s -p \"$REGISTRY_PASS\"", dstRegistry, registryUser)})
	}

	for imagePath, repoPath := range projectImages {
		lastSha, err := u.GetLastCommitSha(ctx, source, repoPath)
		if err != nil {
			return fmt.Errorf("failed to get last commit SHA for %s: %w", repoPath, err)
		}

		srcImageRef := fmt.Sprintf("%s/%s:sha-%s", srcRegistry, imagePath, lastSha)

		fmt.Printf("🔍 Inspecionando imagem: %s\n", srcImageRef)

		// Lê label de versão da imagem
		originalVersion, err := dag.Container().From(srcImageRef).Label(ctx, "org.opencontainers.image.version")
		if err != nil || originalVersion == "" {
			fmt.Printf("⚠️  Skipping %s: Label de versão não encontrado\n", imagePath)
			continue
		}

		dstImageRef := fmt.Sprintf("%s/%s:%s", dstRegistry, imagePath, originalVersion)

		fmt.Printf("📦 Promovendo: %s -> %s\n", srcImageRef, dstImageRef)

		_, err = crane.WithExec([]string{"crane", "copy", srcImageRef, dstImageRef}).Sync(ctx)
		if err != nil {
			return fmt.Errorf("falha na promoção de %s: %w", imagePath, err)
		}
	}
	return nil
}
