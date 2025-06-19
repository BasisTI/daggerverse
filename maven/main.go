package main

import (
	"context"
	"dagger/maven/internal/dagger"
	"fmt"
)

// Runs mvn verify to build the application
func MvnVerify(
// Directory with the maven module
	source *dagger.Directory,
// Run clean goal before verify
	cleanFirst bool) *Maven {
	args := []string{}
	if cleanFirst {
		args = append(args, "clean")
	}
	args = append(args, "verify")
	return NewMaven(source).MavenBuild(args)
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
func MvnVerifyPublishWithJib(ctx context.Context,
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
	return MvnVerify(source, true).PublishWithJib(ctx, registry, image, username, password)
}
