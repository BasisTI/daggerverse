package main

import (
	"context"
	"dagger/npm/internal/dagger"
	"encoding/json"
	"fmt"
	"time"
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
	// TODO: put BuildResultDir here (example: "dist")
}

type packageJSON struct {
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
	useCache bool) *Npm {
	return &Npm{
		BuildImage: buildImage,
		RunImage:   runImage,
		UseCache:   useCache,
		Source:     source,
	}
}

// NewContainer creates a fresh container primed with the project source and optional cache.
func (n *Npm) NewContainer() *dagger.Container {
	container := dag.Container().From(n.BuildImage).WithWorkdir(DefaultWorkdir)
	if n.UseCache {
		container = container.WithMountedCache("/root/.npm", dag.CacheVolume(DefaultNpmCacheName))
	}
	if n.Source != nil {
		container = container.WithDirectory(DefaultWorkdir, n.Source, dagger.ContainerWithDirectoryOpts{Exclude: []string{"node_modules", "dist"}})
	}
	return container
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
	dockerConfig *DockerBuildConfig) (*BuildResult, error) {
	if n.Source == nil {
		return nil, fmt.Errorf("source is nil")
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
	result, err := n.executeStages(ctx, stages)
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
	containerOpts := dagger.ContainerWithDirectoryOpts{Exclude: []string{"node_modules", DefaultBuildDir}}
	if stage.Owner != "" {
		containerOpts.Owner = stage.Owner
	}
	// If the PipelineStage has its own image, use it, but, return the original image
	if stage.Image != "" {
		returnContainer = container
		stageContainer = dag.Container().From(stage.Image)
	} else {
		stageContainer = container
	}
	stageContainer = stageContainer.
		WithDirectory(DefaultWorkdir, n.Source, containerOpts).
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

	if returnContainer == nil {
		returnContainer = stageContainer
	}
	artifactsDir := stageContainer.Directory(fmt.Sprintf("%s/", DefaultWorkdir, DefaultBuildDir))
	return &StageResult{
		Container: returnContainer,
		Stdout:    stdout,
		Stderr:    stderr,
		Artifacts: artifactsDir,
	}, nil
}

// buildSonarOptions converts the Sonar configuration into CLI arguments for the scanner.
func (n *Npm) buildSonarOptions(ctx context.Context, config *SonarConfig) ([]string, error) {
	// TODO see: sonar.projectName and sonar.projectVersion
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
	if n.Source == nil {
		return "", fmt.Errorf("cannot get NPM version: npm directory is not set")
	}
	pkgJSON, err := n.Source.File("package.json").Contents(ctx)
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
