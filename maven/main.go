// Dagger module to build maven Projects
package main

import (
	"context"
	"dagger/maven/internal/dagger"
	"fmt"
)

var DefaultMvnCiOptions = []string{"--batch-mode", "--errors", "-Dmaven.test.failure.ignore=true"}

type Maven struct {
	Image               string
	UseMvnw             bool
	UseCache            bool
	UseDefaultCiOptions bool
	Dir                 *dagger.Directory
	MavenContainer      *dagger.Container
}

func New(
// +optional
	source *dagger.Directory,
// +default="maven:3.9.9-eclipse-temurin-21-alpine"
	image string,
// +default=false
	useMvnw bool,
// +default=true
	useCache bool,
// +default=true
	useDefaultCiOptions bool) *Maven {
	return &Maven{
		Image:               image,
		UseMvnw:             useMvnw,
		UseCache:            useCache,
		UseDefaultCiOptions: useDefaultCiOptions,
		Dir:                 source,
	}
}

// Runs mvn verify to build the application
func (m *Maven) MvnVerify(
// Run clean goal before verify goal
	cleanFirst bool) *Maven {
	var args []string
	if cleanFirst {
		args = append(args, "clean")
	}
	args = append(args, "verify")
	return m.MavenBuild(args)
}

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

// Runs mvn clean verify to build the application then publish the imagem with Jib
func (m *Maven) MvnVerifyPublishWithJib(ctx context.Context,
// Directory with the maven module
	source *dagger.Directory,
// Registry address to publish the image
	registry string,
// Image name with tag (can contain groups, i.e.: a/b/c:1.0)
	image string,
// Username for login to the registry
	username string,
// Password for login to the registry
	password *dagger.Secret) (*Maven, error) {
	return m.MvnVerify(source, true).PublishWithJib(ctx, registry, image, username, password)
}
