package main

import (
	"context"
	"dagger/uv/internal/dagger"
	"fmt"
	"time"

	"github.com/BasisTI/daggerverse/pipeline/sonarargs"
	"github.com/pelletier/go-toml/v2"
)

type PyProject struct {
	Project struct {
		Version string `toml:"version"`
		Name    string `toml:"name"`
	} `toml:"project"`
}

type Uv struct {
	BuildImage string
	RunImage   string
	UseCache   bool
	Source     *dagger.Directory
	RunSubdir  string
	// ModulePath é o caminho do projeto relativo à raiz do repositório. Quando preenchido, Source é
	// a raiz do repositório e não o projeto -- é como o check de qualidade monta a árvore, para que
	// o .git chegue ao scanner. Vazio quando Source já é o próprio projeto, que é o caso do build.
	ModulePath string
	// Customizations list of Build Container customizations that will be executed. Valid values: "dbt" and  "dlt"
	Customizations []string
	buildContainer *dagger.Container
}

func New(
	source *dagger.Directory,
	// +default="ghcr.io/astral-sh/uv:0.8.19-python3.12-bookworm-slim"
	buildImage string,
	// +default="python:3.12-slim-bookworm"
	runImage string,
	// +optional
	runSubdir string,
	// +optional
	modulePath string,
	// +default=true
	useCache bool,
	// Build container customizations. Valid values: "dbt" and "dlt".
	// +optional
	customizations []string) *Uv {
	return &Uv{
		BuildImage:     buildImage,
		RunImage:       runImage,
		RunSubdir:      runSubdir,
		ModulePath:     modulePath,
		UseCache:       useCache,
		Source:         source,
		Customizations: customizations,
	}
}

// func (n *Npm) FullBuild(ctx context.Context, sonarConfig *SonarConfig, dockerConfig *DockerBuildConfig) (*BuildResult, error) {

const (
	DefaultUvCacheName = "uv-cache"
	DefaultUvCachePath = "/root/.cache/uv"
	WorkDir            = "/app"
)

// Workdir é o diretório onde os comandos rodam dentro do container: /app quando Source já é o
// projeto, /app/<ModulePath> quando Source é a raiz do repositório.
func (u *Uv) Workdir() string {
	return joinPath(WorkDir, u.ModulePath)
}

// moduleDir recorta o projeto de dentro de Source. É o que as leituras ancoradas na raiz do
// projeto usam -- pyproject.toml, uv.lock -- para continuarem funcionando quando Source é a raiz
// do repositório.
func (u *Uv) moduleDir() *dagger.Directory {
	if u.ModulePath == "" {
		return u.Source
	}
	return u.Source.Directory(u.ModulePath)
}

func joinPath(base, sub string) string {
	if sub == "" {
		return base
	}
	return base + "/" + sub
}

func (u *Uv) FullBuild(
	ctx context.Context,
	// Git commit SHA for image labels
	// +optional
	commitSha string,
	// Application version for image tag. When empty, falls back to pyproject.toml version.
	// +optional
	version string,
	// +optional
	sonarConfig *SonarConfig,
	// +optional
	dockerConfig *DockerBuildConfig) (*BuildResult, error) {

	buildResult := BuildResult{}

	if version == "" {
		pyProject, err := GetPythonVersion(ctx, u.moduleDir())
		if err != nil {
			return nil, err
		}
		version = pyProject.Project.Version
	}
	builder := u.BuildContainer()
	buildDirectory := builder.Directory(u.Workdir())
	buildResult.Artifacts = buildDirectory
	path := fmt.Sprintf("%s/%s", WorkDir, u.RunSubdir)
	created := time.Now().Format(time.RFC3339)
	appContainer := dag.Container().
		From(u.RunImage).
		WithLabel("org.opencontainers.image.version", version).
		WithLabel("org.opencontainers.image.created", created).
		WithDirectory(WorkDir, buildDirectory).
		WithWorkdir(path).
		WithEnvVariable("PATH", "/app/.venv/bin:$PATH", dagger.ContainerWithEnvVariableOpts{Expand: true}).
		WithEntrypoint([]string{"python", fmt.Sprintf("%s/%s", path, "main.py")})
	if commitSha != "" {
		appContainer = appContainer.WithLabel("org.opencontainers.image.revision", commitSha)
	}
	if sonarConfig != nil {
		_, err := u.runSonarAnalysis(ctx, sonarConfig, builder, &buildResult)
		if err != nil {
			return &buildResult, err
		}
	}

	if dockerConfig != nil {
		dockerConfig.Tag = version
		err := u.publishDockerImage(ctx, dockerConfig, appContainer, &buildResult)
		if err != nil {
			return &buildResult, err
		}
	}

	return &buildResult, nil
}

func (u *Uv) publishDockerImage(
	ctx context.Context,
	dockerConfig *DockerBuildConfig,
	appContainer *dagger.Container,
	buildResult *BuildResult) error {
	publishedImage, err := appContainer.
		WithRegistryAuth(dockerConfig.Registry, dockerConfig.Username, dockerConfig.PasswordSecret).
		Publish(ctx, dockerConfig.imageReference("latest"))
	if err != nil {
		return err
	}
	buildResult.ImageUrl = publishedImage
	return nil
}

// runSonarAnalysis roda o scanner sobre a ÁRVORE DE FONTES, não sobre o diretório de build.
//
// O que está em jogo é o .git. O scanner precisa dele para saber quais linhas a merge request
// mudou; sem SCM ele marca o projeto INTEIRO como código novo, e o quality gate cobra da MR todo
// o débito histórico do target. Foi o que reprovou a MR 584 do licitacao: 126 violações, das
// quais 111 em arquivos que a MR nunca tocou, mais 9% de duplicação e 0% de hotspots revisados --
// tudo do passado, nada da mudança.
//
// O diretório de build não serviria de qualquer forma: ele é o /app depois do `uv sync`, com o
// .venv dentro. Na MR 584 o scanner gastou 42 dos 78 segundos de análise passeando por
// site-packages, reclamando de encoding em .pyc e .so.
//
// A contrapartida é que o build deixa de ser dependência implícita da análise: num job que só
// roda check-quality, um `uv sync` quebrado passaria verde. Daí o Sync explícito antes.
func (u *Uv) runSonarAnalysis(ctx context.Context, sonarConfig *SonarConfig, builder *dagger.Container, buildResult *BuildResult) (*BuildResult, error) {
	if _, err := builder.Sync(ctx); err != nil {
		return buildResult, fmt.Errorf("build falhou antes da análise do Sonar: %w", err)
	}
	sonarContainer, err := u.createSonarContainer(ctx, sonarConfig, u.Source)
	if err != nil {
		fmt.Println("Failed to create sonar container")
	} else {
		stdout, err := sonarContainer.Stdout(ctx)
		if err != nil {
			return buildResult, err
		}
		buildResult.Stdout = append(buildResult.Stdout, stdout)
		stderr, _ := sonarContainer.Stderr(ctx)
		buildResult.Stderr = append(buildResult.Stderr, stderr)
	}
	return buildResult, nil
}

func (u *Uv) BuildContainer() *dagger.Container {
	if u.buildContainer == nil {
		u.buildContainer = u.NewContainer()
	}
	return u.buildContainer
}

func (u *Uv) NewContainer() *dagger.Container {
	buildContainer := u.buildContainer
	if buildContainer == nil {
		moduleDir := u.moduleDir()
		buildContainer = dag.Container().From(u.BuildImage).
			WithWorkdir(u.Workdir()).
			WithEnvVariable("UV_COMPILE_BYTECODE", "1").
			WithEnvVariable("UV_LINK_MODE", "copy").
			WithEnvVariable("PYTHONUNBUFFERED", "1").
			WithEnvVariable("UV_PYTHON_DOWNLOADS", "0").
			WithFiles(u.Workdir(), []*dagger.File{moduleDir.File("uv.lock"), moduleDir.File("pyproject.toml")}).
			WithExec([]string{"uv", "sync", "--frozen", "--no-install-project", "--no-dev"}).
			WithDirectory(WorkDir, u.Source)
		for _, name := range u.Customizations {
			fn, ok := buildCustomizations[name]
			if ok {
				buildContainer = buildContainer.With(fn)
			}
		}
		buildContainer = buildContainer.WithExec([]string{"uv", "sync", "--no-dev"})
		if u.UseCache {
			buildContainer = buildContainer.WithMountedCache(DefaultUvCachePath, dag.CacheVolume(DefaultUvCacheName))
		}
	}
	return buildContainer
}

func (u Uv) createSonarContainer(
	context context.Context,
	config *SonarConfig,
	source *dagger.Directory) (*dagger.Container, error) {
	sonarContainer := dag.Container().
		From(config.AnalysisImage).
		WithDirectory(config.WorkDir, source, dagger.ContainerWithDirectoryOpts{Owner: "scanner-cli"}).
		// O scanner roda do subdiretório do módulo, mas com a árvore inteira montada: é assim que
		// ele acha o .git subindo a árvore, e é o que faz os caminhos do blame baterem com os do
		// índice do git. Analisar direto o subdiretório recortado deixaria o projeto sem SCM.
		WithWorkdir(joinPath(config.WorkDir, u.ModulePath))
	if config.UseCache {
		sonarContainer = sonarContainer.WithMountedCache(
			"/opt/sonar-scanner/.sonar/cache",
			dag.CacheVolume(config.CacheKey),
			dagger.ContainerWithMountedCacheOpts{Owner: "scanner-cli"})
	}
	sonarCommand, err := u.buildSonarCommand(context, config)
	if err != nil {
		return nil, err
	}
	return sonarContainer.WithExec(sonarCommand), nil
}

func GetPythonVersion(ctx context.Context, source *dagger.Directory) (*PyProject, error) {
	pyproject := source.File("pyproject.toml")
	if pyproject == nil {
		pathname, _ := source.Name(ctx)
		return nil, fmt.Errorf("pyproject.toml not found in  %s", pathname)
	}
	data, _ := pyproject.Contents(ctx)
	var cfg PyProject
	if err := toml.Unmarshal([]byte(data), &cfg); err != nil {
		panic(err)
	}
	return &cfg, nil
}

func (u *Uv) buildSonarCommand(ctx context.Context, config *SonarConfig) ([]string, error) {
	options, err := u.buildSonarOptions(ctx, config)
	if err != nil {
		return nil, err
	}
	command := []string{"sonar-scanner"}
	return append(command, options...), nil
}

// buildSonarOptions converts the Sonar configuration into CLI arguments for the scanner.
//
// A montagem da lista fica em pipeline/sonarargs, compartilhada com os módulos
// maven e npm; aqui ficam apenas as bordas específicas do módulo: config nil vira
// erro (diferente de maven/npm, que devolvem (nil, nil)) e o token é lido do Secret.
func (u *Uv) buildSonarOptions(ctx context.Context, config *SonarConfig) ([]string, error) {
	if config == nil {
		return nil, fmt.Errorf("sonar configuration is nil")
	}

	token, err := config.TokenSecret.Plaintext(ctx)
	if err != nil {
		return nil, fmt.Errorf("get sonar token: %w", err)
	}

	return sonarargs.BuildOptions(
		config.Host,
		token,
		config.ProjectKey,
		config.WaitForQualityGate,
		config.ExtraOptions,
	), nil
}
