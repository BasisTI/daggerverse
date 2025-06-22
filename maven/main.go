// Dagger module to build maven Projects
package main

import (
	"context"
	"dagger/maven/internal/dagger"
	"fmt"
)

var DefaultMvnCiOptions = []string{"--batch-mode", "--errors", "-Dmaven.test.failure.ignore=true"}

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
	Dir                 *dagger.Directory
	MavenContainer      *dagger.Container
}

// New creates a new Maven Module
func New(
// Directory for maven goals execution
// +optional
	source *dagger.Directory,
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
	useDefaultCiOptions bool) *Maven {
	return &Maven{
		Image:               buildImage,
		UseMvnw:             useMvnw,
		UseCache:            useCache,
		UseDefaultCiOptions: useDefaultCiOptions,
		Dir:                 source,
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
