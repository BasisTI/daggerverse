package main

import "fmt"

// Sonar Inspections Configuration
type SonarConfig struct {
	// Host Sonarqube instance address
	Host string
	// Token Global Analysis Token
	Token string
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
	token string,
	// Wait for quality gate analysis to complete
	// +default=true
	waitForQualityGate bool,
	// +optional
	extraOptions []string) (*SonarConfig, error) {
	retorno := SonarConfig{}
	if host == "" || token == "" {
		return nil, fmt.Errorf("host or token is empty")
	}
	retorno.Host = host
	retorno.Token = token
	retorno.WaitForQualityGate = waitForQualityGate
	retorno.ExtraOptions = extraOptions
	return &retorno, nil
}

// Helper to build options with maven sonar properties
func (m *Maven) buildSonarOptions(config *SonarConfig) []string {
	if config == nil {
		return nil
	}

	var options []string

	if config.Host != "" {
		options = append(options, fmt.Sprintf("-Dsonar.host.url=%s", config.Host))
	}

	if config.Token != "" {
		options = append(options, fmt.Sprintf("-Dsonar.login=%s", config.Token))
	}

	if config.ProjectKey != "" {
		options = append(options, fmt.Sprintf("-Dsonar.projectKey=%s", config.ProjectKey))
	}

	if config.WaitForQualityGate {
		options = append(options, "-Dsonar.qualitygate.wait=true")
	}

	if config.ExtraOptions != nil {
		options = append(options, config.ExtraOptions...)
	}

	return options
}
