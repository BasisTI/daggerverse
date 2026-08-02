// Dagger module to build maven Projects
package main

import (
	"context"
	"dagger/maven/internal/dagger"
	"encoding/xml"
	"fmt"
	"strings"
)

// DefaultMavenCacheName identifies the cache volume used for the Maven local repository.
const (
	DefaultMavenCacheName = "maven-cache"
	// BaseWorkdir is the working directory inside the container where builds are executed.
	BaseWorkdir = "/app"
	// RevisionPlaceholder is the CI-friendly versioning placeholder a reactor parent POM declares
	// as its <version>. The real value lives in the <revision> property and is overridden per build
	// with -Drevision=<version>, which is what flatten-maven-plugin resolves at install time.
	RevisionPlaceholder = "${revision}"
)

var DefaultDockerHosts = []string{"registry-1.docker.io", "registry.hub.docker.com", "quay.io", "ghcr.io"}

type pomProject struct {
	// XMLName is used to ensure we're parsing the <project> element.
	XMLName xml.Name `xml:"project"`
	// Version captures the content of the <version> tag.
	Version string `xml:"version"`
	// Properties captures the <properties> block, needed to resolve ${revision} versions.
	Properties struct {
		Revision string `xml:"revision"`
	} `xml:"properties"`
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
	// Build the target module from the reactor root instead of in isolation
	ReactorMode bool
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
	// image for executing Builds. The default is deliberately not the -alpine variant: alpine is
	// musl-based and the node binary frontend-maven-plugin downloads is glibc-linked, so frontend
	// builds die on it.
	// +default="maven:3.9.11-eclipse-temurin-21"
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
	// Build the target module from the reactor root, with -pl and -am, instead of mounting the
	// module alone. Required by multi-module projects whose modules depend on sibling modules, and
	// by parents that version themselves through a revision property.
	// +default=false
	reactorMode bool,
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
		ReactorMode:         reactorMode,
		UseJib:              useJib,
		UseDocker:           useDocker,
	}
	return m
}

// NewBaseContainer initializes the base container with caches and the optional Docker daemon.
func (m *Maven) NewBaseContainer() *dagger.Container {
	container := dag.Container().From(m.Image).WithWorkdir(BaseWorkdir)
	if m.UseCache {
		container = container.WithMountedCache("/root/.m2", dag.CacheVolume(DefaultMavenCacheName))
	}
	if m.UseDocker {
		container = m.withDocker(container)
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

// GetVersion reads pom.xml from the given directory and returns the project version.
//
// In reactor mode the directory is the reactor root, so the POM read here is the parent that
// declares the version for the whole build; a <version>${revision}</version> there is resolved
// against the <revision> property, which is where CI-friendly reactors keep the actual number.
func (m *Maven) GetVersion(ctx context.Context, moduleDir *dagger.Directory) (string, error) {
	if moduleDir == nil {
		return "", fmt.Errorf("cannot get version: maven directory is not set")
	}
	project, err := m.readProject(ctx, moduleDir.File("pom.xml"))
	if err != nil {
		return "", err
	}
	version := strings.TrimSpace(project.Version)
	if version == "" {
		return "", fmt.Errorf("could not find a <version> tag inside the <project> tag in pom.xml")
	}
	if m.ReactorMode && version == RevisionPlaceholder {
		revision := strings.TrimSpace(project.Properties.Revision)
		if revision == "" {
			return "", fmt.Errorf(
				"pom.xml declares <version>%s</version> but no <revision> property under <properties>",
				RevisionPlaceholder)
		}
		return revision, nil
	}
	return version, nil
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
