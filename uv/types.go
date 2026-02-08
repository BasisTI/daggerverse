package main

import (
	"dagger/uv/internal/dagger"
	"fmt"
)

// BuildResult aggregates the outcome of the npm pipeline for a single project.
type BuildResult struct {
	Artifacts      *dagger.Directory
	Container      *dagger.Container
	ImageUrl       string
	ExecutedStages []string
	Stdout         []string
	Stderr         []string
}

// StageResult captures the intermediate state produced by a single pipeline stage.
type StageResult struct {
	Container *dagger.Container
	Artifacts *dagger.Directory
	Stdout    string
	Stderr    string
}

// PipelineStage describes a command (or legacy goals/options) executed in sequence.
type PipelineStage struct {
	DisplayName string
	Command     []string
	Goals       []string
	Options     []string
}

// DockerBuildConfig stores the data needed to construct an image reference and authenticate pushes.
type DockerBuildConfig struct {
	Registry       string
	Group          string
	Image          string
	Tag            string
	Username       string
	PasswordSecret *dagger.Secret
	Options        []string
}

// SonarConfig encapsulates the parameters required to run SonarQube analysis from npm.
type SonarConfig struct {
	AnalysisImage      string
	Host               string
	TokenSecret        *dagger.Secret
	ProjectKey         string
	CacheKey           string
	UseCache           bool
	WaitForQualityGate bool
	WorkDir            string
	ExtraOptions       []string
}

// NewDockerBuildConfig returns a DockerBuildConfig initialised with registry metadata and credentials.
func (u *Uv) NewDockerBuildConfig(registry string, group string, image string, username string, passwordSecret *dagger.Secret) DockerBuildConfig {
	return DockerBuildConfig{
		Registry:       registry,
		Group:          group,
		Image:          image,
		Username:       username,
		PasswordSecret: passwordSecret,
	}
}

// NewSonarConfig validates the provided inputs and returns an npm-specific Sonar configuration.
func (u *Uv) NewSonarConfig(host string,
	tokenSecret *dagger.Secret,
	projectKey string,
	// +default="sonarsource/sonar-scanner-cli:11.4.0.2044_7.2.0"
	analysisImage string,
	// +default="SONARQUBE-CACHE"
	cacheKey string,
	// +default=true
	useCache bool,
	// +default=true
	waitForQualityGate bool,
	// +default="/usr/src"
	workDir string,
	// +optional
	extraOptions []string) (*SonarConfig, error) {
	if host == "" {
		return nil, fmt.Errorf("host is empty")
	}
	if tokenSecret == nil {
		return nil, fmt.Errorf("token secret is empty")
	}
	return &SonarConfig{
		Host:               host,
		TokenSecret:        tokenSecret,
		AnalysisImage:      analysisImage,
		ProjectKey:         projectKey,
		CacheKey:           cacheKey,
		UseCache:           useCache,
		WaitForQualityGate: waitForQualityGate,
		ExtraOptions:       extraOptions,
	}, nil
}

// imageReference formats a fully-qualified image reference, defaulting the tag when absent.
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

var buildCustomizations = map[string]dagger.WithContainerFunc{
	"dlt": func(c *dagger.Container) *dagger.Container {
		return c.WithoutDirectory(".dlt")
	},
	"dbt": func(c *dagger.Container) *dagger.Container {
		return c.WithoutDirectory(".env")
	},
}
