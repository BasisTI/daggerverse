package main

import (
	"context"
	"fmt"
)

// buildSonarOptions renders the Maven command-line options required to execute Sonar analysis.
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
