package main

import (
	"fmt"

	"dagger/maven/internal/dagger"
)

// ModuleBuildResult represents the aggregated outcome of a Maven module build.
type ModuleBuildResult struct {
	Artifacts      *dagger.Directory
	Container      *dagger.Container
	ImageUrl       string
	ExecutedStages []string
	Stdout         []string
	Stderr         []string
}

// StageBuildResult captures the container state, logs, and artifacts produced by a single stage.
type StageBuildResult struct {
	Container *dagger.Container
	Artifacts *dagger.Directory
	Stdout    string
	Stderr    string
}

// PipelineStage represents a single set of Maven goals executed within the pipeline.
type PipelineStage struct {
	DisplayName string
	Command     []string
	Goals       []string
	Options     []string
}

// DockerBuildConfig contains the information required to execute the Jib Maven plugin and push images.
type DockerBuildConfig struct {
	Registry       string
	Group          string
	Image          string
	Tag            string
	Username       string
	PasswordSecret *dagger.Secret
	Options        []string
}

// SonarConfig stores the data required to invoke SonarQube analysis for a module.
type SonarConfig struct {
	Host               string
	TokenSecret        *dagger.Secret
	ProjectKey         string
	WaitForQualityGate bool
	ExtraOptions       []string
}

// NewDockerBuildConfig creates a DockerBuildConfig tailored for Maven builds.
func (m *Maven) NewDockerBuildConfig(registry, group, username string, passwordSecret *dagger.Secret) DockerBuildConfig {
	return DockerBuildConfig{
		Registry:       registry,
		Group:          group,
		Username:       username,
		PasswordSecret: passwordSecret,
	}
}

// NewSonarConfig validates Maven Sonar settings and returns a reusable configuration struct.
func (m *Maven) NewSonarConfig(host string, tokenSecret *dagger.Secret, waitForQualityGate bool, extraOptions []string) (*SonarConfig, error) {
	if host == "" {
		return nil, fmt.Errorf("host is empty")
	}
	if tokenSecret == nil {
		return nil, fmt.Errorf("token secret is empty")
	}
	return &SonarConfig{
		Host:               host,
		TokenSecret:        tokenSecret,
		WaitForQualityGate: waitForQualityGate,
		ExtraOptions:       extraOptions,
	}, nil
}

func (c *DockerBuildConfig) imageReference(defaultTag string) string {
	ref := c.Registry
	if c.Group != "" {
		ref = fmt.Sprintf("%s/%s", ref, c.Group)
	}
	if c.Image != "" {
		ref = fmt.Sprintf("%s/%s", ref, c.Image)
	}
	tag := c.Tag
	if tag == "" {
		tag = defaultTag
	}
	return fmt.Sprintf("%s:%s", ref, tag)
}

//type WithContainerFunc func(r *Container) *Container
