package main

import (
	"context"
	"dagger/npm/internal/dagger"
	"encoding/json"
	"fmt"
)

const DefaultNpmCacheName = "npm-cache"
const DefaultWorkdir = "/app"

var NpmRumCmd = []string{"npm", "run"}

// Npm represents an npm project and provides helpers to run common build steps inside a Dagger container.
type Npm struct {
	Image         string
	UseCache      bool
	Dir           *dagger.Directory
	NodeContainer *dagger.Container
}

type packageJSON struct {
	Version string `json:"version"`
}

// New constructs an npm helper bound to the provided source directory and runtime configuration.
func New(
	// Directory with node/npm source code
	source *dagger.Directory,
	// Image name for building application
	// +default="node:22.16.0-alpine3.22"
	buildImage string,
	// Use Npm Cache
	// +default=true
	useCache bool) *Npm {
	return &Npm{
		Image:    buildImage,
		UseCache: useCache,
		Dir:      source,
	}
}

// WithImagem overrides the base container image used for executing npm commands.
func (nc *Npm) WithImagem(image string) *Npm {
	nc.Image = image
	return nc
}

// WithUseCache toggles usage of the shared npm cache mount.
func (nc *Npm) WithUseCache(useCache bool) *Npm {
	nc.UseCache = useCache
	return nc
}

// WithDir updates the source directory mounted into the npm container.
func (nc *Npm) WithDir(dir *dagger.Directory) *Npm {
	nc.Dir = dir
	return nc
}

// NewContainer creates a fresh container primed with the project source and optional cache.
func (nc *Npm) NewContainer() *dagger.Container {
	container := dag.Container().From(nc.Image).WithWorkdir(DefaultWorkdir)
	if nc.UseCache {
		container = container.WithMountedCache("/root/.npm", dag.CacheVolume(DefaultNpmCacheName))
	}
	if nc.Dir != nil {
		container = container.WithDirectory(DefaultWorkdir, nc.Dir, dagger.ContainerWithDirectoryOpts{Exclude: []string{"node_modules", "dist"}})
	}
	return container
}

// Container returns the memoised container, initialising it if needed.
func (nc *Npm) Container() *dagger.Container {
	if nc.NodeContainer == nil {
		nc.NodeContainer = nc.NewContainer()
	}
	return nc.NodeContainer
}

// NpmRun executes an npm script via `npm run` and updates the cached container state.
func (nc *Npm) NpmRun(args []string) *Npm {
	container := nc.Container()
	nc.NodeContainer = container.WithExec(append(NpmRumCmd, args...))
	return nc
}

// GetAngularDistDir returns the Angular distribution directory produced by the build.
func (nc *Npm) GetAngularDistDir() *dagger.Directory {
	return nc.Container().Directory(fmt.Sprintf("%s/dist/browser", DefaultWorkdir))
}

// FullBuild executes a typical npm pipeline: install dependencies, run tests, build assets, optional Sonar analysis, and image metadata preparation.
func (n *Npm) FullBuild(ctx context.Context, sonarConfig *SonarConfig, dockerConfig *DockerBuildConfig) (*BuildResult, error) {
	if n.Dir == nil {
		return nil, fmt.Errorf("npm directory is not set")
	}

	stages := []PipelineStage{
		{DisplayName: "Install Dependencies", Command: []string{"npm", "ci"}},
		{DisplayName: "Run Unit Tests", Command: []string{"npm", "test", "--", "--watch=false"}},
		{DisplayName: "Build Production Bundle", Command: []string{"npm", "run", "build"}},
	}

	if sonarConfig != nil {
		sonarOptions, err := n.buildSonarOptions(ctx, sonarConfig)
		if err != nil {
			return nil, err
		}
		stages = append(stages, PipelineStage{
			DisplayName: "SonarQube Analysis",
			Command:     []string{"npx", "sonar-scanner"},
			Options:     sonarOptions,
		})
	}

	result, err := n.executeStages(ctx, stages)
	if err != nil {
		return nil, err
	}

	if dockerConfig != nil {
		result.ImageUrl = dockerConfig.imageReference("latest")
	}

	return result, nil
}

// executeStages runs each pipeline stage sequentially while collecting logs and artifacts.
func (n *Npm) executeStages(ctx context.Context, stages []PipelineStage) (*BuildResult, error) {
	stageContainer := n.Container()
	result := &BuildResult{}
	for _, stage := range stages {
		stageResult, err := n.executeStage(ctx, stageContainer, stage)
		if err != nil {
			return nil, err
		}
		stageContainer = stageResult.Container
		result.Stdout = append(result.Stdout, stageResult.Stdout)
		result.Stderr = append(result.Stderr, stageResult.Stderr)
		result.ExecutedStages = append(result.ExecutedStages, stage.DisplayName)
		result.Artifacts = stageResult.Artifacts
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

	stageContainer := container.
		WithDirectory(DefaultWorkdir, n.Dir, dagger.ContainerWithDirectoryOpts{Exclude: []string{"node_modules", "dist"}}).
		WithWorkdir(DefaultWorkdir).
		WithExec(cmd)

	stdout, err := stageContainer.Stdout(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get stdout: %w", err)
	}
	stderr, err := stageContainer.Stderr(ctx)
	if err != nil {
		stderr = ""
	}

	artifactsDir := stageContainer.Directory(fmt.Sprintf("%s/dist", DefaultWorkdir))

	return &StageResult{
		Container: stageContainer,
		Artifacts: artifactsDir,
		Stdout:    stdout,
		Stderr:    stderr,
	}, nil
}

// buildSonarOptions converts the Sonar configuration into CLI arguments for the scanner.
func (n *Npm) buildSonarOptions(ctx context.Context, config *SonarConfig) ([]string, error) {
	if config == nil {
		return nil, nil
	}

	var options []string

	if config.Host != "" {
		options = append(options, fmt.Sprintf("-Dsonar.host.url=%s", config.Host))
	}

	token, err := config.TokenSecret.Plaintext(ctx)
	if err != nil {
		return nil, fmt.Errorf("get sonar token: %w", err)
	}
	options = append(options, fmt.Sprintf("-Dsonar.token=%s", token))

	if config.ProjectKey != "" {
		options = append(options, fmt.Sprintf("-Dsonar.projectKey=%s", config.ProjectKey))
	}

	if config.WaitForQualityGate {
		options = append(options, "-Dsonar.qualitygate.wait=true")
	}

	if config.ExtraOptions != nil {
		options = append(options, config.ExtraOptions...)
	}

	return options, nil
}

func (n *Npm) GetVersion(ctx context.Context) (string, error) {
	if n.Dir == nil {
		return "", fmt.Errorf("cannot get NPM version: npm directory is not set")
	}
	pkgJSON, err := n.Dir.File("package.json").Contents(ctx)
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
