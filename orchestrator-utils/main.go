// Módulo de utilitários Git para orchestrators de CI/CD.
//
// Fornece funções reutilizáveis para detecção de mudanças e consulta de commits,
// permitindo que orchestrators de diferentes projetos compartilhem a mesma lógica.
package main

import (
	"context"
	"dagger/orchestrator-utils/internal/dagger"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
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
	// Ref Git a partir da qual buscar (ex: "origin/develop"). Se vazio, usa HEAD.
	// +optional
	ref string,
) (string, error) {
	args := []string{"git", "log", "-n", "1", "--first-parent", "--pretty=format:%H",
		"--grep=^\\[skip ci\\]", "--invert-grep"}

	// Se um ref foi especificado, usá-lo como ponto de partida em vez de HEAD
	if ref != "" {
		args = append(args, ref)
	}

	args = append(args, "--", path)

	out, err := dag.Container().From("alpine/git").
		WithWorkdir("/src").WithDirectory("/src", source).
		WithExec([]string{"git", "config", "--global", "--add", "safe.directory", "/src"}).
		WithExec(args).
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
		lastSha, err := u.GetLastCommitSha(ctx, source, repoPath, "")
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

// bumpPomVersion substitui a versão do projeto num pom.xml usando o XML decoder.
// Encontra a primeira <version> filha direta de <project> (depth 2) e a substitui,
// preservando toda a formatação original do arquivo.
func bumpPomVersion(content, version string) (string, error) {
	decoder := xml.NewDecoder(strings.NewReader(content))
	depth := 0
	for {
		tok, err := decoder.Token()
		if err != nil {
			return "", fmt.Errorf("pom.xml: no project <version> found")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth == 2 && t.Name.Local == "version" {
				// Ler o conteúdo atual da tag
				var oldVersion string
				if err := decoder.DecodeElement(&oldVersion, &t); err != nil {
					return "", fmt.Errorf("pom.xml: failed to decode <version>: %w", err)
				}
				// Substituir apenas esta ocorrência no conteúdo original
				target := "<version>" + oldVersion + "</version>"
				idx := strings.Index(content, target)
				if idx < 0 {
					return "", fmt.Errorf("pom.xml: could not locate <version>%s</version> in source", oldVersion)
				}
				replacement := "<version>" + version + "</version>"
				return content[:idx] + replacement + content[idx+len(target):], nil
			}
		case xml.EndElement:
			depth--
		}
	}
}

// bumpPackageJsonVersion substitui a versão num package.json usando encoding/json.
// Preserva a formatação original substituindo apenas o valor do campo "version".
func bumpPackageJsonVersion(content, version string) (string, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return "", fmt.Errorf("package.json: %w", err)
	}

	oldVersionRaw, ok := raw["version"]
	if !ok {
		return "", fmt.Errorf("package.json: no \"version\" field found")
	}

	var oldVersion string
	if err := json.Unmarshal(oldVersionRaw, &oldVersion); err != nil {
		return "", fmt.Errorf("package.json: invalid version value: %w", err)
	}

	// Substituir no conteúdo original para preservar formatação
	oldQuoted := `"` + oldVersion + `"`
	newQuoted := `"` + version + `"`

	// Encontrar a posição exata: procurar "version" seguido do valor
	versionKeyIdx := strings.Index(content, `"version"`)
	if versionKeyIdx < 0 {
		return "", fmt.Errorf("package.json: could not locate \"version\" key in source")
	}
	// Procurar o valor após a chave
	afterKey := content[versionKeyIdx:]
	valueIdx := strings.Index(afterKey, oldQuoted)
	if valueIdx < 0 {
		return "", fmt.Errorf("package.json: could not locate version value in source")
	}
	absIdx := versionKeyIdx + valueIdx
	return content[:absIdx] + newQuoted + content[absIdx+len(oldQuoted):], nil
}

// pyProject representa a estrutura mínima de um pyproject.toml para extrair [project].version.
type pyProject struct {
	Project struct {
		Version string `toml:"version"`
	} `toml:"project"`
}

// bumpPyprojectVersion substitui [project].version num pyproject.toml usando go-toml.
// Preserva a formatação original localizando o valor exato no conteúdo.
func bumpPyprojectVersion(content, version string) (string, error) {
	var cfg pyProject
	if err := toml.Unmarshal([]byte(content), &cfg); err != nil {
		return "", fmt.Errorf("pyproject.toml: %w", err)
	}

	oldVersion := cfg.Project.Version
	if oldVersion == "" {
		return "", fmt.Errorf("pyproject.toml: no [project].version found")
	}

	// Localizar 'version = "oldVersion"' após a seção [project]
	projectIdx := strings.Index(content, "[project]")
	if projectIdx < 0 {
		return "", fmt.Errorf("pyproject.toml: [project] section not found")
	}

	afterProject := content[projectIdx:]
	target := `"` + oldVersion + `"`
	valueIdx := strings.Index(afterProject, target)
	if valueIdx < 0 {
		return "", fmt.Errorf("pyproject.toml: could not locate version value in [project] section")
	}

	absIdx := projectIdx + valueIdx
	replacement := `"` + version + `"`
	return content[:absIdx] + replacement + content[absIdx+len(target):], nil
}

// bumpFileVersion substitui a versão no conteúdo do arquivo, baseado no tipo.
func bumpFileVersion(content, version, filename string) (string, error) {
	switch filename {
	case "pom.xml":
		return bumpPomVersion(content, version)
	case "package.json":
		return bumpPackageJsonVersion(content, version)
	case "pyproject.toml":
		return bumpPyprojectVersion(content, version)
	default:
		return "", fmt.Errorf("unsupported version file type: %s", filename)
	}
}

// BumpAndCommitVersions faz bump de versão nos arquivos especificados e commita+push.
// O tipo de bump (Maven, npm, Python) é deduzido automaticamente pelo nome do arquivo
// (pom.xml, package.json, pyproject.toml).
func (u *OrchestratorUtils) BumpAndCommitVersions(
	ctx context.Context,
	// Diretório raiz do repositório Git.
	source *dagger.Directory,
	// Arquivos de versão a bumpar (ex: ["admin_backend/pom.xml", "frontend/package.json"]).
	versionFiles []string,
	// Nova versão a aplicar.
	version string,
	// Mensagem do commit (será prefixada com [skip ci]).
	message string,
	// Branch de destino para o push.
	branch string,
	// URL do remote Git com credenciais.
	remoteUrl string,
) error {
	if len(versionFiles) == 0 {
		return nil
	}

	bumpedSource := source
	for _, vf := range versionFiles {
		content, err := source.File(vf).Contents(ctx)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", vf, err)
		}

		modified, err := bumpFileVersion(content, version, filepath.Base(vf))
		if err != nil {
			return err
		}

		bumpedSource = bumpedSource.WithNewFile(vf, modified)
		fmt.Printf("BumpAndCommitVersions: %s -> %s\n", vf, version)
	}

	return u.CommitAndPush(ctx, bumpedSource, versionFiles, message, branch, remoteUrl)
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
// relendo a versão original do label OCI e fazendo crane copy com tag production-{version}.
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
	// Usuário para autenticação nos registries.
	registryUser string,
	// Senha para autenticação nos registries.
	registryPass *dagger.Secret,
	// Registry de destino (production). Se vazio, usa srcRegistry.
	// +optional
	dstRegistry string,
	// Ref Git da branch onde as imagens foram construídas (ex: "origin/develop").
	// Se vazio, busca a partir de HEAD.
	// +optional
	buildBranch string,
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
		lastSha, err := u.GetLastCommitSha(ctx, source, repoPath, buildBranch)
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

		dstImageRef := fmt.Sprintf("%s/%s:production-%s", dstRegistry, imagePath, originalVersion)

		fmt.Printf("📦 Promovendo: %s -> %s\n", srcImageRef, dstImageRef)

		_, err = crane.WithExec([]string{"crane", "copy", srcImageRef, dstImageRef}).Sync(ctx)
		if err != nil {
			return fmt.Errorf("falha na promoção de %s: %w", imagePath, err)
		}
	}
	return nil
}
