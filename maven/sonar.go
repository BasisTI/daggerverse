package main

import (
	"context"
	"fmt"

	"dagger/maven/internal/dagger"
)

// Sonar Inspections Configuration
type SonarConfig struct {
	// Host Sonarqube instance address
	Host string
	// TokenSecret keeps the credential encrypted until we need to use it.
	TokenSecret *dagger.Secret
	// ProjectKey Optional, Key of the project in Sonar
	ProjectKey string
	// WaitForQualityGate Wait for quality gate analysis to complete
	WaitForQualityGate bool
	// ExtraOptions Extra Options if needed, like
	// TODO ver sonar.qualitygate.timeout
	// TODO ver sonar.branch.name
	ExtraOptions []string
}

func (m *Maven) NewSonarConfig(
	host string,
	tokenSecret *dagger.Secret,
	// Wait for quality gate analysis to complete
	// +default=true
	waitForQualityGate bool,
	// +optional
	extraOptions []string) (*SonarConfig, error) {
	retorno := SonarConfig{}
	if host == "" || tokenSecret == nil {
		return nil, fmt.Errorf("host or token is empty")
	}
	retorno.Host = host
	retorno.TokenSecret = tokenSecret
	retorno.WaitForQualityGate = waitForQualityGate
	retorno.ExtraOptions = extraOptions
	return &retorno, nil
}

// Helper to build options with maven sonar properties
func (m *Maven) buildSonarOptions(ctx context.Context, config *SonarConfig) ([]string, error) {
	if config == nil {
		return nil, nil
	}

	var options []string

	if config.Host != "" {
		options = append(options, fmt.Sprintf("-Dsonar.host.url=%s", config.Host))
	}

	token, err := config.TokenSecret.Plaintext(ctx)
	if err != nil {
		return nil, fmt.Errorf("get sonar token: %w", err)
	}
	options = append(options, fmt.Sprintf("-Dsonar.token=%s", token))

	if config.ProjectKey != "" {
		options = append(options, fmt.Sprintf("-Dsonar.projectKey=%s", config.ProjectKey))
	}

	if config.WaitForQualityGate {
		options = append(options, "-Dsonar.qualitygate.wait=true")
	}

	if config.ExtraOptions != nil {
		options = append(options, config.ExtraOptions...)
	}

	return options, nil
}
