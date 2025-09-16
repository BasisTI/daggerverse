// Dagger module to build maven Projects
package main

import (
	"context"
	"dagger/maven/internal/dagger"
	"fmt"
)

var DefaultMvnCiOptions = []string{"--batch-mode", "--errors", "-Dmaven.test.failure.ignore=true"}

// Configuration types and helpers for the Maven Module
// Maven Module
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
	BaseContainer *dagger.Container
}

// BuildResult represents the output of a Maven build
type BuildResult struct {
	Artifacts *dagger.Directory
	Container *dagger.Container
	Stdout    string
	Stderr    string
}

// PipelineStage represents a stage in a Maven pipeline
type PipelineStage struct {
	ModuleName  string
	DisplayName string
	Goals       []string
	Options     []string
}

// Sonar Inspections Configuration
type SonarConfig struct {
	// Host Sonarqube instance address
	Host string
	// Token Global Analysis Token
	Token string
	// ProjectKey Optional, Key of the project in Sonar
	ProjectKey string
	// WaitForQualityGate Wait for quality gate analysis to complete
	WaitForQualityGate bool
	// ExtraOptions Extra Options if needed, like
	// TODO ver sonar.qualitygate.timeout
	// TODO ver sonar.branch.name
	ExtraOptions []string
}

// Maven Jib
type JibConfig struct {
	Registry string
	Image    string // From Pipeline Stage
	Tag      string
	Username string   // External (env_vars)
	Password string   // External (env_vars)
	Options  []string // From Pipeline Stage
}

func NewSonarConfig(
	host string,
	token string,
	// Wait for quality gate analysis to complete
	// +default=true
	waitForQualityGate bool) (*SonarConfig, error) {
	retorno := SonarConfig{}
	if host == "" || token == "" {
		return nil, fmt.Errorf("host or token is empty")
	}
	return &retorno, nil
}

// Helper to build options with maven sonar properties
func (m *Maven) buildSonarOptions(config *SonarConfig) []string {
	if config == nil {
		return nil
	}

	var options []string

	if config.Host != "" {
		options = append(options, fmt.Sprintf("-Dsonar.host.url=%s", config.Host))
	}

	if config.Token != "" {
		options = append(options, fmt.Sprintf("-Dsonar.login=%s", config.Token))
	}

	if config.ProjectKey != "" {
		options = append(options, fmt.Sprintf("-Dsonar.projectKey=%s", config.ProjectKey))
	}

	if config.WaitForQualityGate {
		options = append(options, "-Dsonar.qualitygate.wait=true")
	}

	if config.ExtraOptions != nil {
		options = append(options, config.ExtraOptions...)
	}

	return options
}

// New creates a new Maven Module
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
	parentPom *dagger.File) *Maven {
	return &Maven{
		Image:               buildImage,
		UseMvnw:             useMvnw,
		UseCache:            useCache,
		UseDefaultCiOptions: useDefaultCiOptions,
		ExtraOptions:        extraOptions,
		ParentPom:           parentPom,
	}
}

// MvnVerify runs mvn verify goal to build the application and run UT and IT
func (m *Maven) MvnVerify(cleanFirst bool) *dagger.Directory {
	var args []string
	if cleanFirst {
		args = append(args, "clean")
	}
	args = append(args, "verify")
	return m.MavenBuild(args).GetBuildDir()
}

// PublishWithJib runs JIB through mvn jib:build to build and publish a Docker Image
func (m *Maven) PublishWithJib(ctx context.Context,
	registry string,
	image string,
	username string,
	password *dagger.Secret) (*Maven, error) {
	plaintextPwd, err := password.Plaintext(ctx)
	if err != nil {
		return nil, err
	}
	return m.MavenBuild([]string{"jib:build",
		fmt.Sprintf("-Djib.to.image=%s/%s", registry, image),
		fmt.Sprintf("-Djib.to.auth.username=%s", username),
		fmt.Sprintf("-Djib.to.auth.password=%s", plaintextPwd)}), nil
}

// MvnVerifyPublishWithJib runs mvn clean verify to build the application then publish the imagem with Jib
func (m *Maven) MvnVerifyPublishWithJib(
	ctx context.Context,
	// Docker registry for image publishing
	registry string,
	// Image name with tag (can contain groups, i.e.: a/b/c:1.0)
	image string,
	// Username for login to the registry
	username string,
	// Password for login to the registry
	password *dagger.Secret) (*dagger.Directory, error) {
	_, err := m.MvnVerify(true).Entries(ctx)
	if err != nil {
		return nil, err
	}
	_, err = m.PublishWithJib(ctx, registry, image, username, password)
	if err != nil {
		return nil, err
	}
	return m.GetBuildDir(), nil
}

// Run Sonar Analysis using Sonar Maven Plugin
func (m *Maven) MvnSonarAnalysis(ctx context.Context, sonarHostUrl string, token *dagger.Secret) (*Maven, error) {
	plaintextToken, err := token.Plaintext(ctx)
	if err != nil {
		return nil, err
	}
	return m.MavenBuild([]string{"org.sonarsource.scanner.maven:sonar-maven-plugin:sonar",
		fmt.Sprintf("-Dsonar.token=%s", plaintextToken),
		fmt.Sprintf("-Dsonar.host.url=%s", sonarHostUrl)}), nil
}
