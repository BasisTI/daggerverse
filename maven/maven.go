// Dagger module to build maven Projects
package main

import (
	"context"
	"dagger/maven/internal/dagger"
	"encoding/xml"
	"fmt"
)

// DefaultMavenCacheName identifies the cache volume used for the Maven local repository.
const (
	DefaultMavenCacheName = "maven-cache"
	// BaseWorkdir is the working directory inside the container where builds are executed.
	BaseWorkdir = "/app"
)

var DefaultDockerHosts = []string{"registry-1.docker.io", "registry.hub.docker.com", "quay.io", "ghcr.io"}

type pomProject struct {
	// XMLName is used to ensure we're parsing the <project> element.
	XMLName xml.Name `xml:"project"`
	// Version captures the content of the <version> tag.
	Version string `xml:"version"`
}

// Maven models the configuration and reusable container used to run Maven-based builds.
type Maven struct {
	// image for executing Builds
	Image string
	// Use Maven Wrapper
	UseMvnw bool
	// Use Cache for Maven repository
	UseCache bool
	// Use default maven parameters for CI builds
	UseDefaultCiOptions bool
	// Extra maven options, for example properties: "-Dmyprop=1"
	ExtraOptions []string
	// Optional Parent POM for multi-modules buils
	ParentPom *dagger.File
	// Container used to run the builds
	baseContainer *dagger.Container
	UseJib        bool
	// Bind a Docker daemon to the build, for test suites that use Testcontainers
	UseDocker bool
}

// DefaultMvnCiOptions lists the flags automatically added when UseDefaultCiOptions is enabled.
var DefaultMvnCiOptions = []string{"--batch-mode", "--errors", "-Dmaven.test.failure.ignore=true"}

// New constructs a Maven helper ready to execute builds with the provided base options.
func New(
	// image for executing Builds
	// +default="maven:3.9.9-eclipse-temurin-21-alpine"
	buildImage string,
	// Use Maven Wrapper
	// +default=false
	useMvnw bool,
	// Use Cache for Maven repository (true recommended)
	// +default=true
	useCache bool,
	// Use default maven parameters for CI builds
	// +default=true
	useDefaultCiOptions bool,
	// Extra maven options, for example properties: "-Dmyprop=1"
	// +optional
	extraOptions []string,
	// Parent Pom if multi-module project
	// +optional
	parentPom *dagger.File,
	// +default=true
	useJib bool,
	// Bind a Docker daemon to the build. Required by test suites that use Testcontainers: the
	// Maven image ships no daemon, and without one they fail with "Could not find a valid Docker
	// environment". Off by default because it costs a dind container per build.
	// +default=false
	useDocker bool) *Maven {
	m := &Maven{
		Image:               buildImage,
		UseMvnw:             useMvnw,
		UseCache:            useCache,
		UseDefaultCiOptions: useDefaultCiOptions,
		ExtraOptions:        extraOptions,
		ParentPom:           parentPom,
		UseJib:              useJib,
		UseDocker:           useDocker,
	}
	return m
}

// NewBaseContainer initializes the base container with caches and optional parent POM installation.
func (m *Maven) NewBaseContainer() *dagger.Container {
	container := dag.Container().From(m.Image).WithWorkdir(BaseWorkdir)
	if m.UseCache {
		container = container.WithMountedCache("/root/.m2", dag.CacheVolume(DefaultMavenCacheName))
	}
	if m.UseDocker {
		container = m.withDocker(container)
	}
	if m.ParentPom != nil {
		container = container.
			WithFile(fmt.Sprintf("%s/%s", BaseWorkdir, "pom.xml"), m.ParentPom).
			WithWorkdir(BaseWorkdir).
			WithExec(m.getFullMvnModuleCommand([]string{"-N"}, []string{"install"}))
	}
	return container
}

// Container ensures a base container exists and returns it for further customization.
func (m *Maven) Container() *dagger.Container {
	if m.baseContainer == nil {
		m.baseContainer = m.NewBaseContainer()
	}
	return m.baseContainer
}

func (m *Maven) getFullMvnCommand(goals []string) []string {
	return m.getFullMvnModuleCommand(nil, goals)
}

func (m *Maven) getFullMvnModuleCommand(moduleOptions []string, goals []string) []string {
	var execCmd []string
	if m.UseMvnw {
		execCmd = append(execCmd, "./mvnw")
	} else {
		execCmd = append(execCmd, "mvn")
	}
	if m.UseDefaultCiOptions {
		execCmd = append(execCmd, DefaultMvnCiOptions...)
	}
	if m.ExtraOptions != nil {
		execCmd = append(execCmd, m.ExtraOptions...)
	}
	if moduleOptions != nil {
		execCmd = append(execCmd, moduleOptions...)
	}
	return append(execCmd, goals...)
}

// GetVersion reads the module pom.xml (falling back to the parent POM) and returns the version value.
func (m *Maven) GetVersion(ctx context.Context, moduleDir *dagger.Directory) (string, error) {
	if moduleDir == nil {
		return "", fmt.Errorf("cannot get version: maven directory is not set")
	}
	project, err := m.readProject(ctx, moduleDir.File("pom.xml"))
	if err != nil {
		return "", err
	}
	if project.Version == "" {
		if m.ParentPom != nil {
			project, err = m.readProject(ctx, m.ParentPom)
		}
		if err != nil || project.Version == "" {
			return "", fmt.Errorf("could not find a <version> tag inside the <project> tag in pom.xml or Parent")
		}
	}
	return project.Version, nil
}

// GetVersionOrDefault returns the module version or the provided default when it cannot be resolved.
func (m *Maven) GetVersionOrDefault(ctx context.Context, moduleDir *dagger.Directory, defaultVersion string) string {
	version, err := m.GetVersion(ctx, moduleDir)
	if err != nil {
		version = defaultVersion
	}
	return version
}

// readProject reads and unmarshals a pom.xml file into a lightweight pomProject representation.
func (m *Maven) readProject(ctx context.Context, pomFile *dagger.File) (*pomProject, error) {
	pomXML, err := pomFile.Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read pom.xml from directory: %w", err)
	}
	var project pomProject
	if err := xml.Unmarshal([]byte(pomXML), &project); err != nil {
		return nil, fmt.Errorf("failed to parse pom.xml: %w", err)
	}
	return &project, nil
}
