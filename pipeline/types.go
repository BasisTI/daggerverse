package pipeline

import "context"

// Version file constants

const VfMaven = "pom.xml"
const VfUv = "pyproject.toml"
const VfNpm = "package.json"

// RepoRoot é o valor de MountPath que significa "monte a raiz do repositório".
const RepoRoot = "."

// ProjectConfig maps Path to Service name.
type ProjectConfig map[string]string

// BuildStrategy é a assinatura genérica de uma função de build.
//
// As estratégias fecham sobre a configuração específica do target (imagem,
// módulo do reactor, customizações, Dockerfile, ...) via closure; a lib não
// carrega mais nenhum campo de configuração opaco.
type BuildStrategy[Dir any, Secret any] func(
	ctx context.Context, source Dir,
	module, commitSha, version, registry, registryUser string,
	registryPassword Secret,
) (string, error)

// BuildTarget associa um método de build a um path opcional no repositório.
type BuildTarget[Dir any, Secret any] struct {
	Build BuildStrategy[Dir, Secret]
	// Path é o path de change detection. Se vazio, usa o nome do target.
	Path string
	// MountPath é o diretório entregue à estratégia de build. Se vazio, usa
	// SourcePath. O valor RepoRoot (".") significa a raiz do repositório —
	// nesse caso o callback GetSubDirectory deve devolver o próprio source.
	MountPath string
	// VersionFile é o arquivo de versão (ex: "pom.xml", "package.json",
	// "pyproject.toml"). Relativo a SourcePath, ou à raiz do repositório
	// quando RootVersionFile é true.
	VersionFile string
	// RootVersionFile indica que VersionFile é relativo à RAIZ do repositório
	// (caso típico de builds reactor, em que vários targets compartilham o
	// mesmo pom.xml de raiz).
	RootVersionFile bool
	// ExtraTriggerPaths são paths adicionais que também disparam o build deste
	// target (ex: uma lib compartilhada ou o pom.xml da raiz).
	ExtraTriggerPaths []string
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

// EffectiveMountPath retorna o diretório a ser montado no build.
// Se MountPath estiver vazio, usa SourcePath. O valor RepoRoot (".") é
// repassado como está para GetSubDirectory, que deve tratá-lo devolvendo o
// próprio source.
func (bt BuildTarget[D, S]) EffectiveMountPath(key string) string {
	if bt.MountPath != "" {
		return bt.MountPath
	}
	return bt.SourcePath(key)
}

// VersionFilePath retorna o path do arquivo de versão relativo à raiz do
// repositório, ou "" quando o target não possui arquivo de versão.
func (bt BuildTarget[D, S]) VersionFilePath(key string) string {
	if bt.VersionFile == "" {
		return ""
	}
	if bt.RootVersionFile {
		return bt.VersionFile
	}
	dir := bt.SourcePath(key)
	if dir == "" || dir == RepoRoot {
		return bt.VersionFile
	}
	return dir + "/" + bt.VersionFile
}

// QualityStrategy é a assinatura genérica de um check de qualidade.
type QualityStrategy[Dir any, Secret any] func(
	ctx context.Context, source Dir,
	module, sonarHost string, sonarToken Secret,
) error

// QualityTarget associa um check de qualidade a um path opcional no repositório.
type QualityTarget[Dir any, Secret any] struct {
	Check QualityStrategy[Dir, Secret]
	// Path é o path de change detection. Se vazio, usa o nome do target.
	Path string
	// MountPath é o diretório entregue ao check. Se vazio, usa SourcePath.
	// O valor RepoRoot (".") significa a raiz do repositório.
	MountPath string
	// ExtraTriggerPaths são paths adicionais que também disparam o check
	// (ex: uma lib compartilhada alterada deve reanalisar o app).
	ExtraTriggerPaths []string
}

// SourcePath retorna o path do projeto no repositório.
func (qt QualityTarget[D, S]) SourcePath(key string) string {
	if qt.Path != "" {
		return qt.Path
	}
	return key
}

// EffectiveMountPath retorna o diretório a ser montado no check.
func (qt QualityTarget[D, S]) EffectiveMountPath(key string) string {
	if qt.MountPath != "" {
		return qt.MountPath
	}
	return qt.SourcePath(key)
}
