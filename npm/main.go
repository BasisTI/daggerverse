package main

import (
	"context"
	"dagger/npm/internal/dagger"
	"encoding/json"
	"fmt"
	"time"

	"github.com/BasisTI/daggerverse/gitlabci"
	"github.com/BasisTI/daggerverse/pipeline/sonarargs"
)

const DefaultNpmCacheName = "npm-cache"
const DefaultWorkdir = "/app"
const DefaultBuildDir = "dist"

var NpmRumCmd = []string{"npm", "run"}

// Npm represents an npm project and provides helpers to run common build steps inside a Dagger container.
type Npm struct {
	BuildImage    string
	RunImage      string
	UseCache      bool
	Source        *dagger.Directory
	NodeContainer *dagger.Container
	// ModulePath é o caminho do projeto relativo à raiz do repositório. Quando preenchido, Source é
	// a RAIZ DO REPOSITÓRIO e não o diretório do projeto. Ver a doc do parâmetro em New.
	ModulePath string
	// TODO: put BuildResultDir here (example: "dist")
}

// Workdir é o diretório onde os comandos rodam dentro do container: /app quando Source já é o
// projeto, /app/<ModulePath> quando Source é a raiz do repositório.
func (n *Npm) Workdir() string {
	if n.ModulePath == "" {
		return DefaultWorkdir
	}
	return DefaultWorkdir + "/" + n.ModulePath
}

// packageJSONPath é o caminho do package.json dentro de Source, que segue a mesma regra do Workdir.
func (n *Npm) packageJSONPath() string {
	if n.ModulePath == "" {
		return "package.json"
	}
	return n.ModulePath + "/package.json"
}

type packageJSON struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// New constructs an npm helper bound to the provided source directory and runtime configuration.
func New(
	// Directory with node/npm source code
	source *dagger.Directory,
	// BuildImage name for building application
	// +default="node:22.16.0-alpine3.22"
	buildImage string,
	// BuildImage name for building application
	// +default="nginx:1.27.3-alpine-slim"
	runImage string,
	// Use Npm Cache
	// +default=true
	useCache bool,
	// Caminho do projeto relativo à raiz do repositório. Quando informado, `source` é a RAIZ DO
	// REPOSITÓRIO e não o diretório do projeto: a árvore inteira é montada em /app e os comandos
	// rodam em /app/<modulePath>.
	//
	// É o que permite ao Sonar fazer blame. O .git só existe na raiz e o índice do git referencia
	// os arquivos pelo caminho a partir dela, então montar só o diretório do projeto (com ou sem o
	// .git dentro) deixa o scanner sem SCM -- e sem SCM não há análise de PR nem período de new
	// code por diff.
	// +optional
	modulePath string) *Npm {
	return &Npm{
		BuildImage: buildImage,
		RunImage:   runImage,
		UseCache:   useCache,
		Source:     source,
		ModulePath: modulePath,
	}
}

// NewContainer creates a fresh container primed with the project source and optional cache.
func (n *Npm) NewContainer() *dagger.Container {
	container := dag.Container().From(n.BuildImage)
	if n.UseCache {
		container = container.WithMountedCache("/root/.npm", dag.CacheVolume(DefaultNpmCacheName))
	}
	if n.Source != nil {
		container = container.WithDirectory(DefaultWorkdir, n.Source, dagger.ContainerWithDirectoryOpts{Exclude: []string{"**/node_modules", "**/dist"}})
	}
	return container.WithWorkdir(n.Workdir())
}

// Container returns the memoised container, initialising it if needed.
func (n *Npm) Container() *dagger.Container {
	if n.NodeContainer == nil {
		n.NodeContainer = n.NewContainer()
	}
	return n.NodeContainer
}

// GetAngularDistDir returns the Angular distribution directory produced by the build.
func (n *Npm) GetAngularDistDir() *dagger.Directory {
	return n.Container().Directory(fmt.Sprintf("%s/dist/browser", DefaultWorkdir))
}

// getProjectName reads the project name from package.json.
func (n *Npm) getProjectName(ctx context.Context) string {
	contents, err := n.Source.File(n.packageJSONPath()).Contents(ctx)
	if err != nil {
		return ""
	}
	var pkg packageJSON
	if err := json.Unmarshal([]byte(contents), &pkg); err != nil {
		return ""
	}
	return pkg.Name
}

// FullBuild executes a typical npm pipeline: install dependencies, run tests, build assets, optional Sonar analysis, and image metadata preparation.
func (n *Npm) FullBuild(ctx context.Context,
	// Git commit SHA for image labels
	// +optional
	commitSha string,
	// Application version for image tag. When empty, falls back to package.json version.
	// +optional
	version string,
	// +optional
	sonarConfig *SonarConfig,
	// +optional
	dockerConfig *DockerBuildConfig,
	// GitLab configuration for reporting commit statuses
	// +optional
	gitlabConfig *GitLabConfig,
) (*BuildResult, error) {
	if n.Source == nil {
		return nil, fmt.Errorf("source is nil")
	}

	if version != "" {
		updated, err := n.withPackageJsonVersion(ctx, version)
		if err != nil {
			return nil, fmt.Errorf("failed to set version in package.json: %w", err)
		}
		n.Source = updated
		n.NodeContainer = nil
	}

	stages := []PipelineStage{
		{DisplayName: "Install Dependencies", Command: []string{"npm", "ci"}},
		{DisplayName: "Build Production Bundle", Command: []string{"npm", "run", "build"}},
	}

	if sonarConfig != nil {
		sonarOptions, err := n.buildSonarOptions(ctx, sonarConfig)
		if err != nil {
			return nil, err
		}
		stages = append(stages,
			PipelineStage{
				DisplayName: "Run SonarQube Analysis",
				Command:     []string{"sonar-scanner"},
				Options:     sonarOptions,
				Image:       sonarConfig.Image,
				Owner:       "scanner-cli",
			})
	}
	var gitlabClient *gitlabci.Client
	if gitlabConfig != nil {
		token, err := gitlabConfig.TokenSecret.Plaintext(ctx)
		if err != nil {
			return nil, fmt.Errorf("get gitlab token: %w", err)
		}
		gitlabClient = &gitlabci.Client{
			BaseURL:   gitlabConfig.Host,
			Token:     token,
			ProjectID: gitlabConfig.ProjectID,
			Ref:       gitlabConfig.Ref,
		}
	}

	result, err := n.executeStages(ctx, stages, gitlabClient, commitSha)
	if err != nil {
		return nil, err
	}

	if dockerConfig != nil {
		if version == "" {
			version = n.GetVersionOrDefault(ctx, "latest")
		}
		created := time.Now().Format(time.RFC3339)
		container := result.Container
		container = container.From(n.RunImage).
			WithLabel("org.opencontainers.image.version", version).
			WithLabel("org.opencontainers.image.created", created).
			WithDirectory("/usr/share/nginx/html/", result.Artifacts).
			WithRegistryAuth(dockerConfig.Registry, dockerConfig.Username, dockerConfig.PasswordSecret)
		if commitSha != "" {
			container = container.WithLabel("org.opencontainers.image.revision", commitSha)
		}
		publishedImage, err := container.Publish(ctx, dockerConfig.imageReference(version))
		if err != nil {
			return nil, err
		}
		result.ImageUrl = publishedImage
	}

	return result, nil
}

// executeStages runs each pipeline stage sequentially while collecting logs and artifacts.
func (n *Npm) executeStages(ctx context.Context, stages []PipelineStage, gitlabClient *gitlabci.Client, commitSha string) (*BuildResult, error) {
	var statusPrefix string
	if gitlabClient != nil {
		statusPrefix = n.getProjectName(ctx)
	}
	stageContainer := n.Container()
	result := &BuildResult{}
	for _, stage := range stages {
		statusName := ""
		if statusPrefix != "" {
			statusName = fmt.Sprintf("%s: %s", statusPrefix, stage.DisplayName)
			_ = gitlabClient.SetCommitStatus(commitSha, gitlabci.StateRunning, statusName, "")
		}
		stageResult, err := n.executeStage(ctx, stageContainer, stage)
		if statusName != "" {
			if err != nil {
				_ = gitlabClient.SetCommitStatus(commitSha, gitlabci.StateFailed, statusName, "")
			} else {
				_ = gitlabClient.SetCommitStatus(commitSha, gitlabci.StateSuccess, statusName, "")
			}
		}
		if err != nil {
			return nil, err
		}
		stageContainer = stageResult.Container
		result.Stdout = append(result.Stdout, stageResult.Stdout)
		result.Stderr = append(result.Stderr, stageResult.Stderr)
		result.ExecutedStages = append(result.ExecutedStages, stage.DisplayName)
		result.Artifacts = stageContainer.Directory(fmt.Sprintf("%s/%s", DefaultWorkdir, DefaultBuildDir))
	}
	result.Container = stageContainer
	return result, nil
}

// executeStage mounts sources, runs the stage command, and captures logs plus dist artifacts.
func (n *Npm) executeStage(ctx context.Context, container *dagger.Container, stage PipelineStage) (*StageResult, error) {
	cmd := stage.Command
	if len(cmd) == 0 {
		cmd = append([]string{"npm"}, stage.Goals...)
	}
	if len(stage.Options) > 0 {
		cmd = append(cmd, stage.Options...)
	}
	var stageContainer *dagger.Container
	var returnContainer *dagger.Container
	// If the PipelineStage has its own image, use it, but, return the original image.
	// Only mount n.Source for stages with a separate image (e.g. Sonar), since they
	// start from a fresh container. Regular stages reuse the previous container which
	// already has the source and any files generated by earlier stages (e.g. version.ts
	// produced by postinstall scripts).
	if stage.Image != "" {
		returnContainer = container
		stageContainer = dag.Container().From(stage.Image)
		containerOpts := dagger.ContainerWithDirectoryOpts{Exclude: []string{"**/node_modules", "**/" + DefaultBuildDir}}
		if stage.Owner != "" {
			containerOpts.Owner = stage.Owner
		}
		stageContainer = stageContainer.
			WithDirectory(DefaultWorkdir, n.Source, containerOpts).
			WithWorkdir(n.Workdir())
	} else {
		stageContainer = container
	}
	stageContainer = stageContainer.WithExec(cmd)

	stdout, err := stageContainer.Stdout(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get stdout: %w", err)
	}
	stderr, err := stageContainer.Stderr(ctx)
	if err != nil {
		stderr = ""
	}

	if returnContainer == nil {
		returnContainer = stageContainer
	}
	artifactsDir := stageContainer.Directory(fmt.Sprintf("%s/%s", n.Workdir(), DefaultBuildDir))
	return &StageResult{
		Container: returnContainer,
		Stdout:    stdout,
		Stderr:    stderr,
		Artifacts: artifactsDir,
	}, nil
}

// buildSonarOptions converts the Sonar configuration into CLI arguments for the scanner.
//
// A montagem da lista fica em pipeline/sonarargs, compartilhada com os módulos
// maven e uv; aqui ficam apenas as bordas específicas do módulo: config nil vira
// (nil, nil) e o token é lido do Secret.
func (n *Npm) buildSonarOptions(ctx context.Context, config *SonarConfig) ([]string, error) {
	// TODO see: sonar.projectName and sonar.projectVersion
	if config == nil {
		return nil, nil
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

// withPackageJsonVersion reads package.json, replaces the version field and returns the updated source directory.
func (n *Npm) withPackageJsonVersion(ctx context.Context, version string) (*dagger.Directory, error) {
	contents, err := n.Source.File(n.packageJSONPath()).Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read package.json: %w", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(contents), &raw); err != nil {
		return nil, fmt.Errorf("failed to parse package.json: %w", err)
	}
	versionJSON, _ := json.Marshal(version)
	raw["version"] = versionJSON
	updated, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to serialize package.json: %w", err)
	}
	return n.Source.WithNewFile(n.packageJSONPath(), string(updated)+"\n"), nil
}

func (n *Npm) GetVersion(ctx context.Context) (string, error) {
	if n.Source == nil {
		return "", fmt.Errorf("cannot get NPM version: npm directory is not set")
	}
	pkgJSON, err := n.Source.File(n.packageJSONPath()).Contents(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to read package.json from directory: %w", err)
	}
	var pkg packageJSON
	if err := json.Unmarshal([]byte(pkgJSON), &pkg); err != nil {
		return "", fmt.Errorf("failed to parse package.json: %w", err)
	}
	if pkg.Version == "" {
		return "", fmt.Errorf("could not find 'version' key in package.json")
	}
	return pkg.Version, nil
}

func (n *Npm) GetVersionOrDefault(ctx context.Context, defaultVersion string) string {
	version, err := n.GetVersion(ctx)
	if err != nil {
		version = defaultVersion
	}
	return version
}
