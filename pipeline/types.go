package pipeline

import "context"

// Version file constants

const VfMaven = "pom.xml"
const VfUv = "pyproject.toml"
const VfNpm = "package.json"

// ProjectConfig maps Path to Service name.
type ProjectConfig map[string]string

// BuildStrategy é a assinatura genérica de uma função de build.
type BuildStrategy[Dir any, Secret any] func(
	ctx context.Context, source Dir,
	module, commitSha, version, registry, registryUser string,
	registryPassword Secret,
) (string, error)

// BuildTarget associa um método de build a um path opcional no repositório.
type BuildTarget[Dir any, Secret any] struct {
	Build          BuildStrategy[Dir, Secret]
	Path           string
	VersionFile    string // arquivo de versão relativo ao path (ex: "pom.xml", "package.json", "pyproject.toml")
	EntryPointInfo string // Extra info to customize entrypoint, optional
}

// PublishResult contém o resultado de PublishAll.
type PublishResult struct {
	Published    string   // imagens publicadas (uma por linha)
	VersionFiles []string // paths relativos ao repo dos arquivos de versão bumped (ex: "admin_backend/pom.xml")
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
	module, sonarHost string, sonarToken Secret,
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
