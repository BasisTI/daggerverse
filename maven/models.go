package main

import (
	"dagger/maven/internal/dagger"
)

// Global Configuration types

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
	Goals       []string
	Options     []string
}

// JibConfig contains the information required to execute the Jib Maven plugin and push images.
type JibConfig struct {
	Registry string
	Group    string // External (env_vars)
	Image    string // From Pipeline Stage
	Tag      string
	Username string // External (env_vars)
	// PasswordSecret keeps the credential encrypted until it needs to be read.
	PasswordSecret *dagger.Secret
	Options        []string // From Pipeline Stage
}

// NewJibConfig creates a JibConfig pre-populated with registry coordinates and credentials.
func (m *Maven) NewJibConfig(
	registry string,
	group string,
	username string,
	passwordSecret *dagger.Secret) JibConfig {
	return JibConfig{
		Registry:       registry,
		Group:          group,
		Username:       username,
		PasswordSecret: passwordSecret,
	}
}
