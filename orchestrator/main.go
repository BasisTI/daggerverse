// Orchestrator genérico de CI/CD dirigido por um arquivo declarativo.
//
// Todo o comportamento vem de um `ci/pipeline.toml` do projeto consumidor: os
// targets declarados lá viram simultaneamente as estratégias de build, os checks
// de qualidade e o mapa de imagens usado por CheckImages/Promote. Isso torna
// impossível o drift entre "o que se builda" e "o que se promove", e dispensa o
// módulo Dagger por projeto que existia até a versão 2.x.
package main

import (
	"context"
	"dagger/orchestrator/internal/dagger"
	"fmt"
	"sort"
	"strings"

	"github.com/BasisTI/daggerverse/gitlabci"
	"github.com/BasisTI/daggerverse/pipeline"
	"github.com/BasisTI/daggerverse/pipeline/config"
)

// Orchestrator expõe as funções de pipeline dirigidas pela configuração
// declarativa do projeto.
type Orchestrator struct {
	// Source é a raiz do repositório do projeto consumidor.
	Source *dagger.Directory
	// ConfigPath é o caminho do arquivo declarativo, relativo à raiz.
	ConfigPath string
}

// New constrói o orchestrator a partir da raiz do repositório e do caminho do
// arquivo de configuração.
func New(
	// Diretório raiz do repositório do projeto.
	// +defaultPath="."
	source *dagger.Directory,
	// Caminho do arquivo declarativo de pipeline, relativo à raiz do repositório.
	// +default="ci/pipeline.toml"
	configPath string,
) *Orchestrator {
	return &Orchestrator{Source: source, ConfigPath: configPath}
}

// loadConfig lê e valida o arquivo declarativo do projeto.
func (o *Orchestrator) loadConfig(ctx context.Context) (*config.Config, error) {
	contents, err := o.Source.File(o.ConfigPath).Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("não foi possível ler %s: %w", o.ConfigPath, err)
	}
	cfg, err := config.Load([]byte(contents))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", o.ConfigPath, err)
	}
	return cfg, nil
}

// Validate carrega e valida o arquivo declarativo, devolvendo um relatório
// legível com os targets resolvidos e o mapa de imagens derivado.
//
// Não faz nenhuma chamada de rede: é o job de lint da configuração.
func (o *Orchestrator) Validate(ctx context.Context) (string, error) {
	cfg, err := o.loadConfig(ctx)
	if err != nil {
		return "", err
	}
	return renderReport(cfg, o.ConfigPath), nil
}

// CheckQuality roda testes e análise estática (Sonar) nos targets alterados que
// declaram `sonar = true`.
func (o *Orchestrator) CheckQuality(
	ctx context.Context,
	// Branch base para comparar as mudanças (ex: "origin/develop").
	baseBranch string,
	// Digest do commit atual (sha completo) para reporting de status.
	commitSha string,
	// URL do SonarQube.
	sonarHost string,
	// Token de autenticação do SonarQube.
	sonarToken *dagger.Secret,
	// Se true, para no primeiro target que falhar.
	// +default=false
	stopOnFirstFail bool,
	// URL base do GitLab (ex: CI_SERVER_URL). Se vazio, commit statuses não são reportados.
	// +optional
	gitlabHost string,
	// Token com scope `api` para autenticação no GitLab.
	// +optional
	gitlabToken *dagger.Secret,
	// ID numérico do projeto no GitLab (ex: CI_PROJECT_ID).
	// +optional
	gitlabProjectId string,
	// Branch da pipeline que está reportando (ex: CI_COMMIT_REF_NAME).
	//
	// Sem ela o GitLab anexa cada commit status à pipeline mais recente do SHA. Se
	// uma segunda pipeline nascer no mesmo commit durante o build -- um push em
	// develop que também é head de uma MR, por exemplo --, o status terminal vai
	// para a pipeline nova e a original trava em "running" para sempre.
	// +optional
	gitlabRef string,
	// IID da merge request (ex: CI_MERGE_REQUEST_IID). Junto com as duas branches seguintes,
	// transforma a análise em análise de Pull Request: o código novo passa a ser o diff contra a
	// base, em vez do período de new code do projeto, e o SonarQube decora a MR no GitLab.
	//
	// Os três são necessários. Faltando qualquer um, roda análise de branch -- que é o certo fora
	// de uma MR, e é o que acontece no job de develop.
	// +optional
	mergeRequestId string,
	// Branch de origem da merge request (ex: CI_MERGE_REQUEST_SOURCE_BRANCH_NAME).
	// +optional
	mergeRequestSourceBranch string,
	// Branch de destino da merge request (ex: CI_MERGE_REQUEST_TARGET_BRANCH_NAME).
	// +optional
	mergeRequestTargetBranch string,
) error {
	cfg, err := o.loadConfig(ctx)
	if err != nil {
		return err
	}
	if err := errCustomTargets(cfg, "check-quality"); err != nil {
		return err
	}
	sonarExtra := pullRequestOptions(mergeRequestId, mergeRequestSourceBranch, mergeRequestTargetBranch)
	if sonarExtra == nil {
		fmt.Println("ℹ️  Sem parâmetros de merge request: rodando análise de branch (sem decoração na MR).")
	} else {
		fmt.Printf("🔀 Análise de Pull Request !%s (%s → %s)\n",
			mergeRequestId, mergeRequestSourceBranch, mergeRequestTargetBranch)
	}
	targets, err := qualityTargets(cfg, sonarExtra)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		fmt.Println("✅ Nenhum target com sonar = true. Nada a verificar.")
		return nil
	}
	glClient, err := newGitLabClient(ctx, gitlabHost, gitlabToken, gitlabProjectId, gitlabRef)
	if err != nil {
		return err
	}
	return pipeline.CheckQuality(ctx, daggerOps(glClient), targets, o.Source,
		baseBranch, commitSha, sonarHost, sonarToken, stopOnFirstFail)
}

// PublishAll constrói e publica as imagens de todos os targets alterados e, se
// gitRemoteUrl for informado, commita os arquivos de versão bumpados.
//
// Retorna a lista de imagens publicadas, uma por linha.
func (o *Orchestrator) PublishAll(
	ctx context.Context,
	// Branch base para comparar as mudanças (ex: "origin/develop").
	baseBranch string,
	// Digest do commit atual (sha completo).
	commitSha string,
	// Versão da aplicação no formato CalVer.
	version string,
	// Registry Docker para onde as imagens serão publicadas.
	registry string,
	// Nome de usuário para autenticação no registry.
	registryUser string,
	// Senha do usuário para autenticação no registry.
	registryPassword *dagger.Secret,
	// URL base do GitLab (ex: CI_SERVER_URL). Se vazio, commit statuses não são reportados.
	// +optional
	gitlabHost string,
	// Token com scope `api` para autenticação no GitLab.
	// +optional
	gitlabToken *dagger.Secret,
	// ID numérico do projeto no GitLab (ex: CI_PROJECT_ID).
	// +optional
	gitlabProjectId string,
	// Branch da pipeline que está reportando (ex: CI_COMMIT_REF_NAME).
	//
	// Sem ela o GitLab anexa cada commit status à pipeline mais recente do SHA. Se
	// uma segunda pipeline nascer no mesmo commit durante o build -- um push em
	// develop que também é head de uma MR, por exemplo --, o status terminal vai
	// para a pipeline nova e a original trava em "running" para sempre.
	// +optional
	gitlabRef string,
	// URL do repositório Git com credenciais para push das versões bumpadas.
	// Se vazio, o bump de versão não é commitado.
	// +optional
	gitRemoteUrl string,
	// Branch Git para push das versões bumpadas.
	// +optional
	// +default="develop"
	gitBranch string,
) (string, error) {
	cfg, err := o.loadConfig(ctx)
	if err != nil {
		return "", err
	}
	if err := errCustomTargets(cfg, "publish-all"); err != nil {
		return "", err
	}
	targets, err := buildTargets(cfg)
	if err != nil {
		return "", err
	}
	glClient, err := newGitLabClient(ctx, gitlabHost, gitlabToken, gitlabProjectId, gitlabRef)
	if err != nil {
		return "", err
	}

	result, err := pipeline.PublishAll(ctx, daggerOps(glClient), targets, o.Source,
		baseBranch, commitSha, version, registry, registryUser, registryPassword)
	if err != nil {
		return "", err
	}

	// Commit e push das versões bumpadas de volta ao repositório. A supressão do
	// pipeline vem do push option `-o ci.skip` dentro do orchestrator-utils, e
	// não de um prefixo "[skip ci]" na mensagem.
	if result.Published != "" && gitRemoteUrl != "" && len(result.VersionFiles) > 0 {
		commitMsg := fmt.Sprintf("Bump versão para %s", version)
		if err := dag.OrchestratorUtils().BumpAndCommitVersions(
			ctx, o.Source, result.VersionFiles, version, commitMsg, gitBranch, gitRemoteUrl,
		); err != nil {
			return "", fmt.Errorf("falha ao commitar as versões bumpadas: %w", err)
		}
	}

	return result.Published, nil
}

// CheckImages verifica se todas as imagens derivadas da configuração existem no
// registry antes da promoção.
//
// Targets custom NÃO são ignorados: suas imagens existem no registry e precisam
// ser verificadas como qualquer outra.
func (o *Orchestrator) CheckImages(
	ctx context.Context,
	// URL do registry Docker.
	registry string,
	// Usuário para autenticação no registry.
	registryUser string,
	// Senha para autenticação no registry.
	registryPassword *dagger.Secret,
	// Ref Git da branch onde as imagens foram construídas (ex: "origin/develop").
	// +optional
	// +default="origin/develop"
	buildBranch string,
) error {
	imagesJson, err := o.projectImagesJson(ctx)
	if err != nil {
		return err
	}
	return dag.OrchestratorUtils().CheckImages(ctx, o.Source, imagesJson, registry,
		dagger.OrchestratorUtilsCheckImagesOpts{
			RegistryUser: registryUser,
			RegistryPass: registryPassword,
			BuildBranch:  buildBranch,
		})
}

// Promote promove as imagens derivadas da configuração de staging para produção.
//
// Assim como CheckImages, inclui as imagens de targets custom.
func (o *Orchestrator) Promote(
	ctx context.Context,
	// Registry de origem (staging).
	srcRegistry string,
	// Usuário para autenticação nos registries.
	registryUser string,
	// Senha para autenticação nos registries.
	registryPassword *dagger.Secret,
	// Registry de destino (produção). Se vazio, usa srcRegistry.
	// +optional
	dstRegistry string,
	// Ref Git da branch onde as imagens foram construídas (ex: "origin/develop").
	// +optional
	// +default="origin/develop"
	buildBranch string,
) error {
	imagesJson, err := o.projectImagesJson(ctx)
	if err != nil {
		return err
	}
	return dag.OrchestratorUtils().Promote(ctx, o.Source, imagesJson, srcRegistry, registryUser, registryPassword,
		dagger.OrchestratorUtilsPromoteOpts{
			DstRegistry: dstRegistry,
			BuildBranch: buildBranch,
		})
}

// projectImagesJson devolve o mapa imagem->path derivado da configuração, no
// formato aceito pelo orchestrator-utils.
func (o *Orchestrator) projectImagesJson(ctx context.Context) (string, error) {
	cfg, err := o.loadConfig(ctx)
	if err != nil {
		return "", err
	}
	return cfg.ProjectImagesJSON()
}

// daggerOps liga as callbacks genéricas da lib pipeline ao orchestrator-utils.
func daggerOps(gitlabClient *gitlabci.Client) pipeline.DaggerOps[*dagger.Directory, *dagger.Secret] {
	return pipeline.DaggerOps[*dagger.Directory, *dagger.Secret]{
		GetChangedProjects: func(ctx context.Context, source *dagger.Directory, baseBranch, pathsJson string) ([]string, error) {
			return dag.OrchestratorUtils().GetChangedProjects(ctx, source, baseBranch, pathsJson)
		},
		GetSubDirectory: getSubDirectory,
		TagWithSha: func(ctx context.Context, published, commitSha string, registryUser string, registryPassword *dagger.Secret) error {
			return dag.OrchestratorUtils().TagWithSha(ctx, published, commitSha, registryUser, registryPassword)
		},
		GitLabClient: gitlabClient,
	}
}

// getSubDirectory devolve o subdiretório montado no build.
//
// RepoRoot (".") significa "monte a raiz": é assim que builds reactor e
// workspaces uv/monorepo enxergam o repositório inteiro.
func getSubDirectory(source *dagger.Directory, path string) *dagger.Directory {
	if path == "" || path == pipeline.RepoRoot {
		return source
	}
	return source.Directory(path)
}

// newGitLabClient monta o cliente de commit statuses, ou (nil, nil) quando a
// configuração do GitLab não foi informada.
func newGitLabClient(ctx context.Context, gitlabHost string, gitlabToken *dagger.Secret, gitlabProjectID, gitlabRef string) (*gitlabci.Client, error) {
	if gitlabHost == "" || gitlabToken == nil || gitlabProjectID == "" {
		return nil, nil
	}
	token, err := gitlabToken.Plaintext(ctx)
	if err != nil {
		return nil, fmt.Errorf("get gitlab token: %w", err)
	}
	return &gitlabci.Client{
		BaseURL:   gitlabHost,
		Token:     token,
		ProjectID: gitlabProjectID,
		Ref:       gitlabRef,
	}, nil
}

// renderReport monta o relatório textual devolvido por Validate.
func renderReport(cfg *config.Config, configPath string) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "✅ %s é válido (schema-version %d)\n", configPath, cfg.SchemaVersion)
	fmt.Fprintf(&sb, "Grupo: %s\n", cfg.Project.Group)
	fmt.Fprintf(&sb, "\nTargets (%d):\n", len(cfg.Targets))

	for _, rt := range cfg.ResolveAll() {
		fmt.Fprintf(&sb, "\n  %s\n", rt.Name)
		fmt.Fprintf(&sb, "    tipo:         %s\n", rt.Type)
		fmt.Fprintf(&sb, "    path:         %s\n", rt.Path)
		fmt.Fprintf(&sb, "    source-path:  %s\n", rt.SourcePath)
		fmt.Fprintf(&sb, "    imagem:       %s\n", cfg.ImagePath(rt.Name))
		fmt.Fprintf(&sb, "    version-file: %s\n", orDash(versionFilePath(rt)))
		fmt.Fprintf(&sb, "    sonar:        %t\n", rt.Sonar)
		if rt.QualityType != rt.Type {
			fmt.Fprintf(&sb, "    quality-type: %s\n", rt.QualityType)
		}
		if len(rt.ExtraTriggerPaths) > 0 {
			fmt.Fprintf(&sb, "    triggers:     %s\n", strings.Join(rt.ExtraTriggerPaths, ", "))
		}
		renderTypeFields(&sb, rt, rt.Type)
		// Quando o código é analisado por outro build system, os campos que o
		// check de qualidade vai usar também precisam aparecer no relatório.
		if rt.QualityType != rt.Type {
			fmt.Fprintf(&sb, "    quality (%s):\n", rt.QualityType)
			renderTypeFields(&sb, rt, rt.QualityType)
		}
	}

	images := cfg.ProjectImages()
	refs := make([]string, 0, len(images))
	for image := range images {
		refs = append(refs, image)
	}
	sort.Strings(refs)
	fmt.Fprintf(&sb, "\nImagens derivadas (%d) — usadas por check-images e promote:\n", len(refs))
	for _, image := range refs {
		fmt.Fprintf(&sb, "  %s -> %s\n", image, images[image])
	}

	if warnings := configWarnings(cfg); len(warnings) > 0 {
		sb.WriteString("\n⚠️  Avisos:\n")
		for _, w := range warnings {
			fmt.Fprintf(&sb, "  - %s\n", w)
		}
	}

	return sb.String()
}

// renderTypeFields escreve os campos específicos de um tipo de build. É chamado
// para o tipo de build do target e, quando divergente, para seu tipo de quality.
func renderTypeFields(sb *strings.Builder, rt config.ResolvedTarget, kind config.TargetType) {
	switch kind {
	case config.TypeMaven:
		fmt.Fprintf(sb, "    maven-image:  %s\n", orDash(rt.MavenImage))
		fmt.Fprintf(sb, "    use-docker:   %t\n", rt.UseDocker)
		if rt.Reactor {
			fmt.Fprintf(sb, "    reactor:      true (module %s)\n", rt.Module)
		}
		if len(rt.ExtraOptions) > 0 {
			fmt.Fprintf(sb, "    extra-opts:   %s\n", strings.Join(rt.ExtraOptions, " "))
		}
	case config.TypeNpm:
		fmt.Fprintf(sb, "    build-image:  %s\n", orDash(rt.NpmBuildImage))
		fmt.Fprintf(sb, "    run-image:    %s\n", orDash(rt.NpmRunImage))
	case config.TypeUv:
		fmt.Fprintf(sb, "    build-image:  %s\n", orDash(rt.UvBuildImage))
		fmt.Fprintf(sb, "    run-image:    %s\n", orDash(rt.UvRunImage))
		fmt.Fprintf(sb, "    run-subdir:   %s\n", orDash(rt.RunSubdir))
		if len(rt.Customizations) > 0 {
			fmt.Fprintf(sb, "    customiz.:    %s\n", strings.Join(rt.Customizations, ", "))
		}
	case config.TypeDockerfile:
		fmt.Fprintf(sb, "    dockerfile:   %s\n", rt.Dockerfile)
	}
}

// configWarnings lista o que é válido no schema mas o orchestrator genérico não
// consegue executar. São avisos, não erros: a configuração continua sendo a
// fonte de verdade de check-images e promote, que funcionam para todos os
// targets, inclusive os custom.
//
// `sonar = true` em target dockerfile não entra aqui: ou o target declara
// `quality-type` e o check roda de verdade, ou a configuração nem carrega —
// virou erro de validação em config.Validate.
func configWarnings(cfg *config.Config) []string {
	var warnings []string

	if custom := customTargetNames(cfg); len(custom) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"targets custom (%s): publish-all e check-quality do módulo genérico falham para eles; "+
				"o projeto precisa de um módulo Dagger próprio para buildá-los. "+
				"check-images e promote continuam cobrindo suas imagens normalmente",
			strings.Join(custom, ", ")))
	}

	return warnings
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
