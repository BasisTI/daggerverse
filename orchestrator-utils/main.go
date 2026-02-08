// Módulo de utilitários Git para orchestrators de CI/CD.
//
// Fornece funções reutilizáveis para detecção de mudanças e consulta de commits,
// permitindo que orchestrators de diferentes projetos compartilhem a mesma lógica.
package main

import (
	"context"
	"dagger/orchestrator-utils/internal/dagger"
	"encoding/json"
	"strings"
)

// OrchestratorUtils fornece utilitários Git para pipelines de CI/CD.
type OrchestratorUtils struct{}

// GetChangedProjects retorna os nomes dos projetos que tiveram mudanças
// comparando a branch atual com a branch base.
//
// projectPathsJson deve ser um JSON no formato {"nome":"path",...}
// mapeando nomes de projeto aos seus paths no repositório.
func (u *OrchestratorUtils) GetChangedProjects(
	ctx context.Context,
	// Diretório raiz do repositório Git.
	source *dagger.Directory,
	// Branch base para comparação (ex: "origin/develop").
	baseBranch string,
	// JSON com mapeamento nome->path dos projetos. Ex: {"backend":"backend","frontend":"frontend"}
	projectPathsJson string,
) ([]string, error) {
	var projectPaths map[string]string
	if err := json.Unmarshal([]byte(projectPathsJson), &projectPaths); err != nil {
		return nil, err
	}

	output, err := dag.Container().From("alpine/git").
		WithWorkdir("/src").WithDirectory("/src", source).
		WithExec([]string{"git", "config", "--global", "--add", "safe.directory", "/src"}).
		WithExec([]string{"git", "diff", "--name-only", baseBranch + "...HEAD"}).
		Stdout(ctx)
	if err != nil {
		return nil, err
	}

	affected := make(map[string]bool)
	for _, file := range strings.Split(strings.TrimSpace(output), "\n") {
		for name, path := range projectPaths {
			if strings.HasPrefix(file, path) {
				affected[name] = true
			}
		}
	}

	var list []string
	for k := range affected {
		list = append(list, k)
	}
	return list, nil
}

// GetLastCommitSha retorna o SHA do último commit que alterou o path especificado.
func (u *OrchestratorUtils) GetLastCommitSha(
	ctx context.Context,
	// Diretório raiz do repositório Git.
	source *dagger.Directory,
	// Path do projeto no repositório.
	path string,
) (string, error) {
	out, err := dag.Container().From("alpine/git").
		WithWorkdir("/src").WithDirectory("/src", source).
		WithExec([]string{"git", "config", "--global", "--add", "safe.directory", "/src"}).
		WithExec([]string{"git", "log", "-n", "1", "--pretty=format:%H", "--", path}).
		Stdout(ctx)
	return strings.TrimSpace(out), err
}
