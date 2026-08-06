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
	// Image contains the image name without tag, for instance: registry/group/name
	Image string
	// Tag image tag
	Tag string
	// Username to connect to a private registry
	Username string
	// Password for the user to connect to a private registry
	PasswordSecret *dagger.Secret
}

func (r DockerBuildConfig) fullImageReference() string {
	ref := r.Image
	if r.Tag != "" {
		ref = fmt.Sprintf("%s:%s", ref, r.Tag)
	}
	return ref
}

// SonarConfig stores the data required to invoke SonarQube analysis for a module.
type SonarConfig struct {
	Host               string
	TokenSecret        *dagger.Secret
	ProjectKey         string
	WaitForQualityGate bool
	ExtraOptions       []string
}

// GitLabConfig stores the data needed to report commit statuses to GitLab.
type GitLabConfig struct {
	Host        string         // URL base (ex: "https://gitlab.com")
	TokenSecret *dagger.Secret // Token com scope `api`
	ProjectID   string         // ID numérico ou path URL-encoded
	Ref         string         // Branch da pipeline (ex: CI_COMMIT_REF_NAME)
}

// NewGitLabConfig validates the provided inputs and returns a GitLab configuration for commit statuses.
func (m *Maven) NewGitLabConfig(host string, tokenSecret *dagger.Secret, projectID string,
	// Branch da pipeline que reporta. Sem ela o status terminal pode ser anexado a
	// outra pipeline do mesmo commit, deixando a original travada em "running".
	// +optional
	ref string) (*GitLabConfig, error) {
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
		Ref:         ref,
	}, nil
}

// NewDockerBuildConfig creates a DockerBuildConfig tailored for Maven builds.
func (m *Maven) NewDockerBuildConfig(
	image string,
	tag string,
	username string,
	passwordSecret *dagger.Secret) DockerBuildConfig {
	return DockerBuildConfig{
		Image:          image,
		Tag:            tag,
		Username:       username,
		PasswordSecret: passwordSecret,
	}
}

// NewSonarConfig validates Maven Sonar settings and returns a reusable configuration struct.
func (m *Maven) NewSonarConfig(host string, tokenSecret *dagger.Secret, waitForQualityGate bool, extraOptions []string,
	// Chave do projeto no SonarQube. Se vazia, FullBuild usa o nome do módulo.
	// +optional
	projectKey string) (*SonarConfig, error) {
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
	}, nil
}
