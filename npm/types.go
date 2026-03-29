package main

import (
	"dagger/npm/internal/dagger"
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
	Owner       string
	Goals       []string
	Options     []string
	Image       string
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
	Host               string
	TokenSecret        *dagger.Secret
	ProjectKey         string
	WaitForQualityGate bool
	ExtraOptions       []string
	Image              string
}

// NewDockerBuildConfig returns a DockerBuildConfig initialised with registry metadata and credentials.
func (n *Npm) NewDockerBuildConfig(registry string, group string, image string, username string, passwordSecret *dagger.Secret) DockerBuildConfig {
	return DockerBuildConfig{
		Registry:       registry,
		Group:          group,
		Image:          image,
		Username:       username,
		PasswordSecret: passwordSecret,
	}
}

// NewSonarConfig validates the provided inputs and returns an npm-specific Sonar configuration.
func (n *Npm) NewSonarConfig(host string,
	tokenSecret *dagger.Secret,
	projectKey string,
// +default=true
	waitForQualityGate bool,
// +optional
	extraOptions []string,
// +default="sonarsource/sonar-scanner-cli:12.0.0.3214_8.0.1"
	image string) (*SonarConfig, error) {
	if host == "" {
		return nil, fmt.Errorf("host is empty")
	}
	if tokenSecret == nil {
		return nil, fmt.Errorf("token secret is empty")
	}
	return &SonarConfig{
		Host:               host,
		TokenSecret:        tokenSecret,
		ProjectKey:         projectKey,
		WaitForQualityGate: waitForQualityGate,
		ExtraOptions:       extraOptions,
		Image:              image,
	}, nil
}

// GitLabConfig stores the data needed to report commit statuses to GitLab.
type GitLabConfig struct {
	Host        string         // URL base (ex: "https://gitlab.com")
	TokenSecret *dagger.Secret // Token com scope `api`
	ProjectID   string         // ID numérico ou path URL-encoded
}

// NewGitLabConfig validates the provided inputs and returns a GitLab configuration for commit statuses.
func (n *Npm) NewGitLabConfig(host string, tokenSecret *dagger.Secret, projectID string) (*GitLabConfig, error) {
	if host == "" {
		return nil, fmt.Errorf("host is empty")
	}
	if tokenSecret == nil {
		return nil, fmt.Errorf("token secret is empty")
	}
	if projectID == "" {
		return nil, fmt.Errorf("project ID is empty")
	}
	return &GitLabConfig{
		Host:        host,
		TokenSecret: tokenSecret,
		ProjectID:   projectID,
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
