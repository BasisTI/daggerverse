// Package config lê e valida o arquivo declarativo de pipeline (`ci/pipeline.toml`)
// usado pelos orchestrators do Daggerverse.
//
// O objetivo do pacote é ser a ÚNICA fonte de verdade de um projeto: a mesma
// configuração que descreve como cada target é buildado também deriva o mapa de
// imagens consumido por CheckImages/Promote (ver ProjectImages), tornando
// impossível o drift entre "o que se builda" e "o que se promove".
package config

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/pelletier/go-toml/v2"
)

// SchemaVersion é a única versão de schema suportada por este pacote.
const SchemaVersion = 1

// TargetType identifica o tipo de build de um target.
type TargetType string

const (
	// TypeMaven builda com o módulo maven (Jib).
	TypeMaven TargetType = "maven"
	// TypeNpm builda com o módulo npm.
	TypeNpm TargetType = "npm"
	// TypeUv builda com o módulo uv (Python).
	TypeUv TargetType = "uv"
	// TypeDockerfile builda a partir de um Dockerfile no repositório.
	TypeDockerfile TargetType = "dockerfile"
	// TypeCustom marca um target cujo build é implementado à mão no orchestrator
	// do projeto. O orchestrator genérico falha se encontrar um target custom.
	TypeCustom TargetType = "custom"
)

// Version files padrão por tipo de target.
const (
	VersionFileMaven = "pom.xml"
	VersionFileNpm   = "package.json"
	VersionFileUv    = "pyproject.toml"
)

// DefaultDockerfile é o nome de Dockerfile assumido quando o target não define um.
const DefaultDockerfile = "Dockerfile"

// RepoRoot é o valor de source-path que significa "monte a raiz do repositório".
const RepoRoot = "."

// Config é o conteúdo completo de um `ci/pipeline.toml`.
type Config struct {
	SchemaVersion int               `toml:"schema-version"`
	Project       Project           `toml:"project"`
	Defaults      Defaults          `toml:"defaults"`
	Targets       map[string]Target `toml:"targets"`
}

// Project traz metadados do repositório como um todo.
type Project struct {
	// Group é o grupo no registry: basis-registry.basis.com.br/<group>/<image>.
	Group string `toml:"group"`
}

// Defaults agrupa os defaults por tipo de build, aplicáveis a todos os targets.
type Defaults struct {
	Maven MavenDefaults `toml:"maven"`
	Npm   NpmDefaults   `toml:"npm"`
	Uv    UvDefaults    `toml:"uv"`
}

// MavenDefaults são os defaults dos targets type = "maven".
type MavenDefaults struct {
	Image     string `toml:"image"`
	UseDocker bool   `toml:"use-docker"`
	// SonarPluginVersion fixa a versão do sonar-maven-plugin. Vazio usa o default
	// do módulo maven — que também é fixo, nunca "a última release".
	SonarPluginVersion string `toml:"sonar-plugin-version"`
}

// NpmDefaults são os defaults dos targets type = "npm".
type NpmDefaults struct {
	BuildImage string `toml:"build-image"`
	RunImage   string `toml:"run-image"`
}

// UvDefaults são os defaults dos targets type = "uv".
type UvDefaults struct {
	BuildImage string `toml:"build-image"`
	RunImage   string `toml:"run-image"`
}

// Target descreve um artefato buildável do repositório.
//
// Campos vazios são resolvidos pelos helpers Effective* / Resolve; nunca leia
// os campos crus quando precisar do valor efetivo.
type Target struct {
	Type TargetType `toml:"type"`

	// Path é o path de change detection. Default: o nome do target.
	Path string `toml:"path"`
	// SourcePath é o diretório montado no build. Default: o Path efetivo
	// (ou RepoRoot quando Reactor = true). "." significa a raiz do repo.
	SourcePath string `toml:"source-path"`
	// Image é o nome da imagem no registry. Default: o nome do target.
	Image string `toml:"image"`
	// VersionFile é o arquivo de versão. Default por tipo (ver EffectiveVersionFile).
	VersionFile string `toml:"version-file"`
	// RootVersionFile indica que VersionFile é relativo à RAIZ do repositório,
	// e não ao SourcePath (caso típico de reactor multi-módulo).
	RootVersionFile bool `toml:"root-version-file"`
	// ExtraTriggerPaths são paths adicionais que disparam o build deste target.
	ExtraTriggerPaths []string `toml:"extra-trigger-paths"`
	// Sonar indica que o target participa do CheckQuality.
	Sonar bool `toml:"sonar"`
	// SonarProjectKey é a chave do projeto no SonarQube. Default: o nome do
	// target. Existe porque a chave é o identificador permanente da análise no
	// servidor -- renomeá-la órfã todo o histórico --, e o nome do target é
	// escolhido pelo repositório, sem saber que num servidor compartilhado
	// nomes como `frontend` ou `snf` colidem entre projetos.
	SonarProjectKey string `toml:"sonar-project-key"`
	// QualityType declara com que build system o CÓDIGO do target é analisado,
	// independentemente de como sua IMAGEM é construída. Existe para o caso em
	// que os dois divergem: uma imagem construída por Dockerfile próprio cujo
	// código-fonte é, ainda assim, um projeto uv/maven/npm com testes e análise
	// estática. Vazio significa "o mesmo que type" (ver EffectiveQualityType).
	QualityType TargetType `toml:"quality-type"`

	// --- maven ---

	// MavenImage sobrescreve defaults.maven.image.
	MavenImage string `toml:"maven-image"`
	// UseDocker sobrescreve defaults.maven.use-docker (ponteiro para distinguir
	// "ausente" de "false").
	UseDocker *bool `toml:"use-docker"`
	// SonarPluginVersion sobrescreve defaults.maven.sonar-plugin-version.
	SonarPluginVersion string `toml:"sonar-plugin-version"`
	// Reactor indica build multi-módulo: monta a raiz e builda com -pl <module> -am.
	Reactor bool `toml:"reactor"`
	// Module é o path do módulo no reactor. Obrigatório quando Reactor = true.
	Module string `toml:"module"`
	// ExtraOptions são opções extras passadas ao build.
	ExtraOptions []string `toml:"extra-options"`

	// --- uv ---

	// UvBuildImage sobrescreve defaults.uv.build-image.
	UvBuildImage string `toml:"uv-build-image"`
	// UvRunImage sobrescreve defaults.uv.run-image.
	UvRunImage string `toml:"uv-run-image"`
	// RunSubdir é o subdiretório de execução dentro do build (ex: "src").
	RunSubdir string `toml:"run-subdir"`
	// Customizations são customizações de build do módulo uv (ex: "dbt", "dlt").
	Customizations []string `toml:"customizations"`

	// --- dockerfile ---

	// Dockerfile é o path do Dockerfile relativo ao SourcePath efetivo.
	Dockerfile string `toml:"dockerfile"`
}

// ResolvedTarget é um Target com todos os defaults já aplicados.
// É a forma recomendada de consumo: nenhum campo precisa de fallback.
type ResolvedTarget struct {
	Name string
	Type TargetType
	// QualityType é o build system usado no check de qualidade. Nunca é vazio:
	// quando o target não declara `quality-type`, é igual a Type.
	QualityType TargetType

	Path              string
	SourcePath        string
	Image             string
	VersionFile       string
	RootVersionFile   bool
	ExtraTriggerPaths []string
	Sonar             bool
	// SonarProjectKey só é significativo quando Sonar = true.
	SonarProjectKey string

	// MavenImage e UseDocker só são significativos para Type/QualityType == TypeMaven.
	MavenImage         string
	UseDocker          bool
	SonarPluginVersion string
	Reactor            bool
	Module             string
	ExtraOptions       []string

	// UvBuildImage/UvRunImage/RunSubdir/Customizations só são significativos
	// para Type/QualityType == TypeUv.
	UvBuildImage   string
	UvRunImage     string
	RunSubdir      string
	Customizations []string

	// NpmBuildImage/NpmRunImage só são significativos para Type/QualityType == TypeNpm.
	NpmBuildImage string
	NpmRunImage   string

	// Dockerfile só é significativo para Type == TypeDockerfile.
	Dockerfile string
}

// VersionFilePath retorna o path do arquivo de versão relativo à raiz do repo,
// ou "" se o target não tem arquivo de versão.
//
// O arquivo de versão acompanha o projeto (Path), não o diretório montado para o
// build (SourcePath): um target com SourcePath "." — workspace uv, reactor Maven —
// ainda tem seu pyproject.toml/pom.xml dentro do próprio Path.
func (rt ResolvedTarget) VersionFilePath() string {
	if rt.VersionFile == "" {
		return ""
	}
	if rt.RootVersionFile {
		return rt.VersionFile
	}
	if rt.Path == RepoRoot || rt.Path == "" {
		return rt.VersionFile
	}
	return rt.Path + "/" + rt.VersionFile
}

// Load faz o parse do TOML em modo estrito (campo desconhecido = erro) e valida
// o resultado.
func Load(data []byte) (*Config, error) {
	var cfg Config
	dec := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse pipeline config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// TargetNames retorna os nomes dos targets em ordem determinística (alfabética).
func (c *Config) TargetNames() []string {
	names := make([]string, 0, len(c.Targets))
	for name := range c.Targets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// TargetNamesByType retorna os nomes dos targets de um tipo, em ordem alfabética.
func (c *Config) TargetNamesByType(t TargetType) []string {
	var names []string
	for _, name := range c.TargetNames() {
		if c.Targets[name].Type == t {
			names = append(names, name)
		}
	}
	return names
}

// SonarTargetNames retorna os nomes dos targets com sonar = true, em ordem alfabética.
func (c *Config) SonarTargetNames() []string {
	var names []string
	for _, name := range c.TargetNames() {
		if c.Targets[name].Sonar {
			names = append(names, name)
		}
	}
	return names
}

// HasCustomTargets informa se existe algum target type = "custom".
// O orchestrator genérico deve falhar quando isso for verdadeiro.
func (c *Config) HasCustomTargets() bool {
	for _, t := range c.Targets {
		if t.Type == TypeCustom {
			return true
		}
	}
	return false
}

// EffectivePath retorna o path de change detection do target (default: o nome).
func (c *Config) EffectivePath(name string) string {
	t := c.Targets[name]
	if t.Path != "" {
		return t.Path
	}
	return name
}

// EffectiveSourcePath retorna o diretório a ser montado no build.
// Default: RepoRoot quando reactor = true, senão o Path efetivo.
func (c *Config) EffectiveSourcePath(name string) string {
	t := c.Targets[name]
	if t.SourcePath != "" {
		return t.SourcePath
	}
	if t.Reactor {
		return RepoRoot
	}
	return c.EffectivePath(name)
}

// EffectiveImage retorna o nome da imagem no registry (sem grupo, sem tag).
func (c *Config) EffectiveImage(name string) string {
	if img := c.Targets[name].Image; img != "" {
		return img
	}
	return name
}

// EffectiveSonarProjectKey retorna a chave do projeto no SonarQube (default: o
// nome do target). Só é significativa para targets com sonar = true.
func (c *Config) EffectiveSonarProjectKey(name string) string {
	if key := c.Targets[name].SonarProjectKey; key != "" {
		return key
	}
	return name
}

// SonarProjectKeys retorna as chaves do SonarQube dos targets com sonar = true,
// na ordem alfabética dos targets.
func (c *Config) SonarProjectKeys() []string {
	names := c.SonarTargetNames()
	keys := make([]string, 0, len(names))
	for _, name := range names {
		keys = append(keys, c.EffectiveSonarProjectKey(name))
	}
	return keys
}

// EffectiveQualityType retorna o build system que analisa o código do target no
// CheckQuality: o `quality-type` declarado ou, na ausência dele, o próprio
// `type`. Targets custom devolvem TypeCustom e continuam sem quality no
// orchestrator genérico.
func (c *Config) EffectiveQualityType(name string) TargetType {
	t := c.Targets[name]
	if t.QualityType != "" {
		return t.QualityType
	}
	return t.Type
}

// EffectiveVersionFile retorna o arquivo de versão do target, aplicando o
// default por tipo: maven -> pom.xml, npm -> package.json,
// uv/dockerfile -> pyproject.toml. Targets custom não têm default.
//
// O default segue o tipo EFETIVO DE QUALITY, e não o tipo de build: um target
// dockerfile com `quality-type = "maven"` é um projeto Maven cuja imagem vem de
// um Dockerfile, e seu arquivo de versão é o pom.xml. Sem `quality-type` os dois
// coincidem, então o comportamento histórico não muda.
func (c *Config) EffectiveVersionFile(name string) string {
	t := c.Targets[name]
	if t.VersionFile != "" {
		return t.VersionFile
	}
	switch c.EffectiveQualityType(name) {
	case TypeMaven:
		return VersionFileMaven
	case TypeNpm:
		return VersionFileNpm
	case TypeUv, TypeDockerfile:
		return VersionFileUv
	default:
		return ""
	}
}

// EffectiveMavenImage retorna a imagem maven do target (override ou default).
func (c *Config) EffectiveMavenImage(name string) string {
	if img := c.Targets[name].MavenImage; img != "" {
		return img
	}
	return c.Defaults.Maven.Image
}

// EffectiveSonarPluginVersion retorna a versão fixada do sonar-maven-plugin (override do
// target, default do projeto, ou "" para o módulo maven aplicar o default dele).
func (c *Config) EffectiveSonarPluginVersion(name string) string {
	if v := c.Targets[name].SonarPluginVersion; v != "" {
		return v
	}
	return c.Defaults.Maven.SonarPluginVersion
}

// EffectiveUseDocker retorna se o build maven precisa de um daemon Docker.
func (c *Config) EffectiveUseDocker(name string) bool {
	if v := c.Targets[name].UseDocker; v != nil {
		return *v
	}
	return c.Defaults.Maven.UseDocker
}

// EffectiveUvBuildImage retorna a imagem de build uv (override ou default).
func (c *Config) EffectiveUvBuildImage(name string) string {
	if img := c.Targets[name].UvBuildImage; img != "" {
		return img
	}
	return c.Defaults.Uv.BuildImage
}

// EffectiveUvRunImage retorna a imagem de runtime uv (override ou default).
func (c *Config) EffectiveUvRunImage(name string) string {
	if img := c.Targets[name].UvRunImage; img != "" {
		return img
	}
	return c.Defaults.Uv.RunImage
}

// EffectiveNpmBuildImage retorna a imagem de build npm.
func (c *Config) EffectiveNpmBuildImage(string) string {
	return c.Defaults.Npm.BuildImage
}

// EffectiveNpmRunImage retorna a imagem de runtime npm.
func (c *Config) EffectiveNpmRunImage(string) string {
	return c.Defaults.Npm.RunImage
}

// EffectiveDockerfile retorna o Dockerfile do target relativo ao SourcePath efetivo.
func (c *Config) EffectiveDockerfile(name string) string {
	if df := c.Targets[name].Dockerfile; df != "" {
		return df
	}
	return DefaultDockerfile
}

// Resolve materializa um target com todos os defaults aplicados.
// Retorna erro se o target não existir.
func (c *Config) Resolve(name string) (ResolvedTarget, error) {
	t, ok := c.Targets[name]
	if !ok {
		return ResolvedTarget{}, fmt.Errorf("target %q não existe na configuração", name)
	}
	return ResolvedTarget{
		Name:              name,
		Type:              t.Type,
		QualityType:       c.EffectiveQualityType(name),
		Path:              c.EffectivePath(name),
		SourcePath:        c.EffectiveSourcePath(name),
		Image:             c.EffectiveImage(name),
		VersionFile:       c.EffectiveVersionFile(name),
		RootVersionFile:   t.RootVersionFile,
		ExtraTriggerPaths: t.ExtraTriggerPaths,
		Sonar:             t.Sonar,
		SonarProjectKey:   c.EffectiveSonarProjectKey(name),
		MavenImage:         c.EffectiveMavenImage(name),
		UseDocker:          c.EffectiveUseDocker(name),
		SonarPluginVersion: c.EffectiveSonarPluginVersion(name),
		Reactor:           t.Reactor,
		Module:            t.Module,
		ExtraOptions:      t.ExtraOptions,
		UvBuildImage:      c.EffectiveUvBuildImage(name),
		UvRunImage:        c.EffectiveUvRunImage(name),
		RunSubdir:         t.RunSubdir,
		Customizations:    t.Customizations,
		NpmBuildImage:     c.EffectiveNpmBuildImage(name),
		NpmRunImage:       c.EffectiveNpmRunImage(name),
		Dockerfile:        c.EffectiveDockerfile(name),
	}, nil
}

// ResolveAll materializa todos os targets, em ordem alfabética de nome.
func (c *Config) ResolveAll() []ResolvedTarget {
	out := make([]ResolvedTarget, 0, len(c.Targets))
	for _, name := range c.TargetNames() {
		rt, err := c.Resolve(name)
		if err != nil {
			continue // impossível: name veio de TargetNames
		}
		out = append(out, rt)
	}
	return out
}
