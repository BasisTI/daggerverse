package main

import (
	"context"
	"dagger/uv/internal/dagger"
	"fmt"

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
// +default=true
	useCache bool) *Uv {
	return &Uv{
		BuildImage: buildImage,
		RunImage:   runImage,
		RunSubdir:  runSubdir,
		UseCache:   useCache,
		Source:     source,
	}
}

// func (n *Npm) FullBuild(ctx context.Context, sonarConfig *SonarConfig, dockerConfig *DockerBuildConfig) (*BuildResult, error) {

const (
	DefaultUvCacheName = "uv-cache"
	DefaultUvCachePath = "/root/.cache/uv"
	WorkDir            = "/app"
)

func (u *Uv) FullBuild(
	ctx context.Context,
	sonarConfig *SonarConfig,
	dockerConfig *DockerBuildConfig) (*BuildResult, error) {

	buildResult := BuildResult{}

	pyProject, err := GetPythonVersion(ctx, u.Source)
	if err != nil {
		return nil, err
	}
	builder := u.BuildContainer()
	buildDirectory := builder.Directory(WorkDir)
	buildResult.Artifacts = buildDirectory
	path := fmt.Sprintf("%s/%s", WorkDir, u.RunSubdir)
	appContainer := dag.Container().
		From(u.RunImage).
		WithDirectory(WorkDir, buildDirectory).
		WithWorkdir(path).
		WithEnvVariable("PATH", "/app/.venv/bin:$PATH", dagger.ContainerWithEnvVariableOpts{Expand: true}).
		WithEntrypoint([]string{"python", fmt.Sprintf("%s/%s", path, "main.py")})
	if sonarConfig != nil {
		result, err := u.runSonarAnalysis(ctx, sonarConfig, buildDirectory, buildResult)
		if err != nil {
			return result, err
		}
	}

	if dockerConfig != nil {
		dockerConfig.Tag = pyProject.Project.Version
		result, err := u.publishDockerImage(ctx, dockerConfig, appContainer, buildResult)
		if err != nil {
			return result, err
		}
	}

	return &buildResult, nil
}

func (u *Uv) publishDockerImage(
	ctx context.Context,
	dockerConfig *DockerBuildConfig,
	appContainer *dagger.Container,
	buildResult BuildResult) (*BuildResult, error) {
	publishedImage, err := appContainer.
		WithRegistryAuth(dockerConfig.Registry, dockerConfig.Username, dockerConfig.PasswordSecret).
		Publish(ctx, dockerConfig.imageReference("latest"))
	if err != nil {
		return nil, err
	}
	buildResult.ImageUrl = publishedImage
	return nil, nil
}

func (u *Uv) runSonarAnalysis(ctx context.Context, sonarConfig *SonarConfig, buildDirectory *dagger.Directory, buildResult BuildResult) (*BuildResult, error) {
	sonarContainer, err := u.createSonarContainer(ctx, sonarConfig, buildDirectory)
	if err != nil {
		fmt.Println("Failed to create sonar container")
	} else {
		stdout, err := sonarContainer.Stdout(ctx)
		if err != nil {
			return nil, err
		}
		buildResult.Stdout = append(buildResult.Stdout, stdout)
		stderr, _ := sonarContainer.Stderr(ctx)
		buildResult.Stderr = append(buildResult.Stderr, stderr)
	}
	return nil, nil
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
		buildContainer = dag.Container().From(u.BuildImage).
			WithWorkdir(WorkDir).
			WithEnvVariable("UV_COMPILE_BYTECODE", "1").
			WithEnvVariable("UV_LINK_MODE", "copy").
			WithEnvVariable("PYTHONUNBUFFERED", "1").
			WithEnvVariable("UV_PYTHON_DOWNLOADS", "0").
			WithFiles(WorkDir, []*dagger.File{u.Source.File("uv.lock"), u.Source.File("pyproject.toml")}).
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
		WithWorkdir(config.WorkDir)
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
func (u *Uv) buildSonarOptions(ctx context.Context, config *SonarConfig) ([]string, error) {
	if config == nil {
		return nil, fmt.Errorf("sonar configuration is nil")
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
