package main

import (
	"context"
	"fmt"

	"github.com/BasisTI/daggerverse/pipeline/sonarargs"
)

// buildSonarOptions renders the Maven command-line options required to execute Sonar analysis.
//
// A montagem da lista fica em pipeline/sonarargs, compartilhada com os módulos
// npm e uv; aqui ficam apenas as bordas específicas do módulo: config nil vira
// (nil, nil) e o token é lido do Secret.
func (m *Maven) buildSonarOptions(ctx context.Context, config *SonarConfig) ([]string, error) {
	if config == nil {
		return nil, nil
	}

	token, err := config.TokenSecret.Plaintext(ctx)
	if err != nil {
		return nil, fmt.Errorf("get sonar token: %w", err)
	}

	return sonarargs.BuildOptions(
		config.Host,
		token,
		config.ProjectKey,
		config.WaitForQualityGate,
		config.ExtraOptions,
	), nil
}
