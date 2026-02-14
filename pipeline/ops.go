package pipeline

import (
	"context"

	"github.com/BasisTI/daggerverse/gitlabci"
)

// DaggerOps encapsula as operações que dependem do Dagger SDK gerado.
// Cada orchestrator implementa estas callbacks com seu próprio internal/dagger.
type DaggerOps[Dir any, Secret any] struct {
	// GetChangedProjects retorna os nomes dos projetos alterados.
	GetChangedProjects func(ctx context.Context, source Dir, baseBranch, pathsJson string) ([]string, error)
	// GetSubDirectory retorna um subdiretório do source.
	GetSubDirectory func(source Dir, path string) Dir
	// TagWithSha adiciona tag sha-{commitSha} às imagens publicadas.
	TagWithSha func(ctx context.Context, published, commitSha string, registryUser string, registryPassword Secret) error
	// GitLabClient, quando não-nil, é usado para reportar commit statuses ao GitLab.
	GitLabClient *gitlabci.Client
}
