package main

import (
	"dagger/maven/internal/dagger"
)

// Global Configuration types

// ModuleBuildResult represents the output of a Maven build
type ModuleBuildResult struct {
	Artifacts      *dagger.Directory
	Container      *dagger.Container
	ImageUrl       string
	ExecutedStages []string
	Stdout         []string
	Stderr         []string
}

// ModuleBuildResult represents the output of a Maven build
type StageBuildResult struct {
	Container *dagger.Container
	Artifacts *dagger.Directory
	Stdout    string
	Stderr    string
}

// PipelineStage represents a stage in a Maven pipeline
type PipelineStage struct {
	DisplayName string
	Goals       []string
	Options     []string
}

// Maven Jib
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
