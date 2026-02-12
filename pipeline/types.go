package pipeline

import "context"

// ProjectConfig mapeia Path -> Nome do Serviço.
type ProjectConfig map[string]string

// BuildStrategy é a assinatura genérica de uma função de build.
type BuildStrategy[Dir any, Secret any] func(
	ctx context.Context, source Dir,
	commitSha, version, registry, registryUser string,
	registryPassword Secret,
) (string, error)

// BuildTarget associa um método de build a um path opcional no repositório.
type BuildTarget[Dir any, Secret any] struct {
	Build BuildStrategy[Dir, Secret]
	Path  string
}

// SourcePath retorna o path do projeto no repositório.
// Se Path estiver vazio, usa a chave (nome do serviço).
func (bt BuildTarget[D, S]) SourcePath(key string) string {
	if bt.Path != "" {
		return bt.Path
	}
	return key
}

// QualityStrategy é a assinatura genérica de um check de qualidade.
type QualityStrategy[Dir any, Secret any] func(
	ctx context.Context, source Dir,
	sonarHost string, sonarToken Secret,
) error

// QualityTarget associa um check de qualidade a um path opcional no repositório.
type QualityTarget[Dir any, Secret any] struct {
	Check QualityStrategy[Dir, Secret]
	Path  string
}

// SourcePath retorna o path do projeto no repositório.
func (qt QualityTarget[D, S]) SourcePath(key string) string {
	if qt.Path != "" {
		return qt.Path
	}
	return key
}
