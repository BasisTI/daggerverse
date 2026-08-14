package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func loadFixture(t *testing.T, name string) *Config {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".toml"))
	if err != nil {
		t.Fatalf("ler fixture %s: %v", name, err)
	}
	cfg, err := Load(data)
	if err != nil {
		t.Fatalf("Load(%s): %v", name, err)
	}
	return cfg
}

// TestFixturesProjectImages é o teste-chave anti-drift: ProjectImages() deve
// cobrir EXATAMENTE os targets declarados — todos presentes, nada além.
func TestFixturesProjectImages(t *testing.T) {
	tests := []struct {
		fixture string
		group   string
		images  map[string][]string
	}{
		{
			fixture: "contavinculada",
			group:   "contavinculada",
			images: map[string][]string{
				"contavinculada/contavinculada": {"contavinculada"},
				"contavinculada/integracaosgo":  {"integracaosgo"},
				"contavinculada/snf":            {"snf"},
				"contavinculada/frontend":       {"frontend"},
			},
		},
		{
			fixture: "licitacao",
			group:   "licitacao",
			images: map[string][]string{
				"licitacao/licitacao":           {"licitacao"},
				"licitacao/integracao":          {"integracao"},
				"licitacao/frontend":            {"frontend"},
				"licitacao/processos_judiciais": {"scrapers/processos_judiciais"},
				"licitacao/painel_licitacoes":   {"painel_licitacoes"},
				"licitacao/carga_editais":       {"carga_editais"},
				"licitacao/comprasnet_nodriver": {"scrapers/comprasnet_nodriver"},
				"licitacao/mte":                 {"scrapers/mte"},
				"licitacao/cct_mte":             {"scrapers/cct_mte"},
			},
		},
		{
			fixture: "kaizenstat",
			group:   "kaizenstat",
			images: map[string][]string{
				"kaizenstat/judge-admin":          {"apps/judge-admin"},
				"kaizenstat/judge-api":            {"apps/judge-api"},
				"kaizenstat/judge-worker":         {"apps/judge-worker"},
				"kaizenstat/judge-scheduler":      {"apps/judge-scheduler"},
				"kaizenstat/judge-dashboard":      {"apps/judge-dashboard"},
				"kaizenstat/hiring_salary_update": {"scripts/hiring_salary_update"},
				"kaizenstat/hiring_vagas_update":  {"scripts/hiring_vagas_update"},
			},
		},
		{
			// Targets custom NÃO são buildados pelo orchestrator genérico,
			// mas continuam existindo no registry e portanto em ProjectImages.
			//
			// lightdash-content carrega o extra-trigger-path junto: é ele que faz
			// check-images e promote acharem a imagem reconstruída por um commit
			// no projeto dbt, e não a de um build anterior.
			fixture: "colaboradados",
			group:   "colaboradados",
			images: map[string][]string{
				"colaboradados/beneficios":        {"apps/beneficios"},
				"colaboradados/rh-dp":             {"rh-dp"},
				"colaboradados/comercial":         {"comercial"},
				"colaboradados/financeiro":        {"financeiro"},
				"colaboradados/lightdash-content": {"lightdash-content", "rh-dp/dbtrh"},
			},
		},
		{
			fixture: "triagem",
			group:   "triagem",
			images: map[string][]string{
				"triagem/triagem-core":     {"triagem-core", "triagem-contracts", "pom.xml"},
				"triagem/triagem-ingestao": {"triagem-ingestao", "triagem-contracts", "pom.xml"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.fixture, func(t *testing.T) {
			cfg := loadFixture(t, tc.fixture)

			if cfg.Project.Group != tc.group {
				t.Errorf("group = %q, quer %q", cfg.Project.Group, tc.group)
			}

			got := cfg.ProjectImages()
			if !reflect.DeepEqual(got, tc.images) {
				t.Errorf("ProjectImages() =\n%v\nquer\n%v", got, tc.images)
			}

			// Nada além dos targets: uma entrada por target, e cada chave
			// derivada de um nome de target existente.
			if len(got) != len(cfg.Targets) {
				t.Errorf("ProjectImages() tem %d entradas para %d targets", len(got), len(cfg.Targets))
			}
			for _, name := range cfg.TargetNames() {
				key := cfg.ImagePath(name)
				if _, ok := got[key]; !ok {
					t.Errorf("target %q ausente em ProjectImages() (chave %q)", name, key)
				}
			}
		})
	}
}

// TestTriggerPaths cobre a regra que liga build e promoção: o conjunto de paths
// que reconstrói a imagem tem que ser o mesmo que a resolve no registry.
func TestTriggerPaths(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		target  string
		want    []string
	}{
		{
			name:    "path primário vem primeiro, extras na ordem declarada",
			fixture: "triagem",
			target:  "triagem-core",
			want:    []string{"triagem-core", "triagem-contracts", "pom.xml"},
		},
		{
			name:    "target sem extras devolve só o path efetivo",
			fixture: "colaboradados",
			target:  "beneficios",
			want:    []string{"apps/beneficios"},
		},
		{
			name:    "extra que repete o path primário não duplica",
			fixture: "colaboradados",
			target:  "lightdash-content",
			want:    []string{"lightdash-content", "rh-dp/dbtrh"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := loadFixture(t, tt.fixture)
			if got := cfg.TriggerPaths(tt.target); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("TriggerPaths(%q) = %v, quer %v", tt.target, got, tt.want)
			}
		})
	}
}

func TestProjectImagesJSON(t *testing.T) {
	cfg := loadFixture(t, "triagem")
	raw, err := cfg.ProjectImagesJSON()
	if err != nil {
		t.Fatalf("ProjectImagesJSON: %v", err)
	}
	var got map[string][]string
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("json inválido %q: %v", raw, err)
	}
	if !reflect.DeepEqual(got, cfg.ProjectImages()) {
		t.Errorf("round-trip divergente: %v", got)
	}
}

func TestEffectiveValues(t *testing.T) {
	contavinculada := loadFixture(t, "contavinculada")
	licitacao := loadFixture(t, "licitacao")
	kaizenstat := loadFixture(t, "kaizenstat")
	colaboradados := loadFixture(t, "colaboradados")
	triagem := loadFixture(t, "triagem")

	tests := []struct {
		name string
		got  any
		want any
	}{
		// path / source-path / image default = nome do target
		{"path default", contavinculada.EffectivePath("snf"), "snf"},
		{"source-path default", contavinculada.EffectiveSourcePath("snf"), "snf"},
		{"image default", contavinculada.EffectiveImage("snf"), "snf"},

		// path explícito propaga para source-path
		{"path explícito", licitacao.EffectivePath("processos_judiciais"), "scrapers/processos_judiciais"},
		{"source-path segue path", licitacao.EffectiveSourcePath("processos_judiciais"), "scrapers/processos_judiciais"},

		// source-path "." separa mount de change detection
		{"kaizenstat path", kaizenstat.EffectivePath("hiring_vagas_update"), "scripts/hiring_vagas_update"},
		{"kaizenstat mount raiz", kaizenstat.EffectiveSourcePath("hiring_vagas_update"), RepoRoot},

		// version files por tipo
		{"vf maven", contavinculada.EffectiveVersionFile("snf"), VersionFileMaven},
		{"vf npm", contavinculada.EffectiveVersionFile("frontend"), VersionFileNpm},
		{"vf uv", licitacao.EffectiveVersionFile("painel_licitacoes"), VersionFileUv},
		{"vf dockerfile", kaizenstat.EffectiveVersionFile("judge-api"), VersionFileUv},
		{"vf custom", colaboradados.EffectiveVersionFile("rh-dp"), ""},
		{"vf explícito", triagem.EffectiveVersionFile("triagem-core"), "pom.xml"},

		// maven defaults e overrides
		{"maven image default", contavinculada.EffectiveMavenImage("snf"), "maven:3.9.11-eclipse-temurin-21"},
		{"maven image triagem", triagem.EffectiveMavenImage("triagem-core"), "maven:3.9.11-eclipse-temurin-25"},
		{"use-docker false", contavinculada.EffectiveUseDocker("snf"), false},
		{"use-docker true", kaizenstat.EffectiveUseDocker("judge-admin"), true},
		{"use-docker triagem", triagem.EffectiveUseDocker("triagem-ingestao"), true},

		// uv
		{"uv build image", licitacao.EffectiveUvBuildImage("painel_licitacoes"), "ghcr.io/astral-sh/uv:python3.12-bookworm-slim"},
		{"uv run image", licitacao.EffectiveUvRunImage("painel_licitacoes"), "python:3.12-slim-bookworm"},

		// npm
		{"npm build image", contavinculada.EffectiveNpmBuildImage("frontend"), "node:22-alpine"},
		{"npm run image", contavinculada.EffectiveNpmRunImage("frontend"), "nginx:alpine"},

		// dockerfile
		{"dockerfile default", licitacao.EffectiveDockerfile("mte"), DefaultDockerfile},
		{"dockerfile explícito", kaizenstat.EffectiveDockerfile("judge-api"), "apps/judge-api/Dockerfile"},

		// quality-type: ausente = o próprio type; presente = o declarado
		{"quality-type default maven", contavinculada.EffectiveQualityType("snf"), TypeMaven},
		{"quality-type default npm", contavinculada.EffectiveQualityType("frontend"), TypeNpm},
		{"quality-type default uv", licitacao.EffectiveQualityType("painel_licitacoes"), TypeUv},
		{"quality-type default dockerfile", kaizenstat.EffectiveQualityType("judge-api"), TypeDockerfile},
		{"quality-type default custom", colaboradados.EffectiveQualityType("rh-dp"), TypeCustom},
		{"quality-type declarado", licitacao.EffectiveQualityType("mte"), TypeUv},
		{"quality-type não muda o type", licitacao.Targets["mte"].Type, TypeDockerfile},

		// custom targets
		{"tem custom", colaboradados.HasCustomTargets(), true},
		{"não tem custom", contavinculada.HasCustomTargets(), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !reflect.DeepEqual(tc.got, tc.want) {
				t.Errorf("= %v, quer %v", tc.got, tc.want)
			}
		})
	}
}

// TestReactorSourcePathDefault cobre a regra "reactor => source-path default '.'"
// mesmo quando o TOML não declara source-path explicitamente.
func TestReactorSourcePathDefault(t *testing.T) {
	cfg, err := Load([]byte(`
schema-version = 1
[project]
group = "triagem"
[targets.triagem-core]
type = "maven"
reactor = true
module = "triagem-core"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.EffectiveSourcePath("triagem-core"); got != RepoRoot {
		t.Errorf("EffectiveSourcePath = %q, quer %q", got, RepoRoot)
	}
	if got := cfg.EffectivePath("triagem-core"); got != "triagem-core" {
		t.Errorf("EffectivePath = %q, quer %q", got, "triagem-core")
	}
}

func TestResolveAndVersionFilePath(t *testing.T) {
	triagem := loadFixture(t, "triagem")
	rt, err := triagem.Resolve("triagem-ingestao")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := ResolvedTarget{
		Name:              "triagem-ingestao",
		Type:              TypeMaven,
		QualityType:       TypeMaven,
		Path:              "triagem-ingestao",
		SourcePath:        RepoRoot,
		Image:             "triagem-ingestao",
		VersionFile:       "pom.xml",
		RootVersionFile:   true,
		ExtraTriggerPaths: []string{"triagem-contracts", "pom.xml"},
		Sonar:             true,
		SonarProjectKey:   "triagem-ingestao",
		MavenImage:        "maven:3.9.11-eclipse-temurin-25",
		UseDocker:         true,
		Reactor:           true,
		Module:            "triagem-ingestao",
		Dockerfile:        DefaultDockerfile,
	}
	if !reflect.DeepEqual(rt, want) {
		t.Errorf("Resolve() =\n%+v\nquer\n%+v", rt, want)
	}
	if got := rt.VersionFilePath(); got != "pom.xml" {
		t.Errorf("VersionFilePath (root) = %q, quer %q", got, "pom.xml")
	}

	licitacao := loadFixture(t, "licitacao")
	scraper, err := licitacao.Resolve("processos_judiciais")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := scraper.VersionFilePath(); got != "scrapers/processos_judiciais/pyproject.toml" {
		t.Errorf("VersionFilePath = %q", got)
	}

	// Um target que monta a raiz do repo (workspace uv) ainda tem o arquivo de
	// versão dentro do próprio Path — resolver contra o SourcePath devolveria o
	// pyproject.toml da raiz e faria o bump no arquivo errado.
	kaizenstat := loadFixture(t, "kaizenstat")
	workspace, err := kaizenstat.Resolve("hiring_vagas_update")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if workspace.SourcePath != RepoRoot {
		t.Fatalf("fixture mudou: SourcePath = %q, esperado %q", workspace.SourcePath, RepoRoot)
	}
	if got, want := workspace.VersionFilePath(), "scripts/hiring_vagas_update/pyproject.toml"; got != want {
		t.Errorf("VersionFilePath (mount raiz) = %q, quer %q", got, want)
	}

	if _, err := licitacao.Resolve("inexistente"); err == nil {
		t.Error("Resolve de target inexistente deveria falhar")
	}

	if n := len(licitacao.ResolveAll()); n != len(licitacao.Targets) {
		t.Errorf("ResolveAll devolveu %d targets, quer %d", n, len(licitacao.Targets))
	}
}

func TestTargetListings(t *testing.T) {
	licitacao := loadFixture(t, "licitacao")

	if got, want := licitacao.TargetNamesByType(TypeUv), []string{"carga_editais", "painel_licitacoes", "processos_judiciais"}; !reflect.DeepEqual(got, want) {
		t.Errorf("TargetNamesByType(uv) = %v, quer %v", got, want)
	}
	if got, want := licitacao.TargetNamesByType(TypeDockerfile), []string{"cct_mte", "comprasnet_nodriver", "mte"}; !reflect.DeepEqual(got, want) {
		t.Errorf("TargetNamesByType(dockerfile) = %v, quer %v", got, want)
	}
	if got, want := licitacao.SonarTargetNames(), []string{"cct_mte", "comprasnet_nodriver", "frontend", "integracao", "licitacao", "mte"}; !reflect.DeepEqual(got, want) {
		t.Errorf("SonarTargetNames = %v, quer %v", got, want)
	}

	// TargetNames é determinístico (ordenado) apesar do map subjacente.
	names := licitacao.TargetNames()
	if !sort.StringsAreSorted(names) {
		t.Errorf("TargetNames não ordenado: %v", names)
	}

	contavinculada := loadFixture(t, "contavinculada")
	if got, want := contavinculada.SonarTargetNames(), contavinculada.TargetNames(); !reflect.DeepEqual(got, want) {
		t.Errorf("todos os targets de contavinculada deveriam ter sonar: %v", got)
	}

	colaboradados := loadFixture(t, "colaboradados")
	if got, want := colaboradados.TargetNamesByType(TypeCustom), []string{"comercial", "financeiro", "lightdash-content", "rh-dp"}; !reflect.DeepEqual(got, want) {
		t.Errorf("TargetNamesByType(custom) = %v, quer %v", got, want)
	}
	if got := colaboradados.Targets["lightdash-content"].ExtraTriggerPaths; !reflect.DeepEqual(got, []string{"rh-dp/dbtrh"}) {
		t.Errorf("extra-trigger-paths = %v", got)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name    string
		toml    string
		wantErr string
	}{
		{
			name: "schema-version errado",
			toml: `
schema-version = 2
[project]
group = "x"
[targets.a]
type = "maven"
`,
			wantErr: "schema-version inválido",
		},
		{
			name: "schema-version ausente",
			toml: `
[project]
group = "x"
[targets.a]
type = "maven"
`,
			wantErr: "schema-version inválido",
		},
		{
			name: "group vazio",
			toml: `
schema-version = 1
[project]
group = ""
[targets.a]
type = "maven"
`,
			wantErr: "project.group é obrigatório",
		},
		{
			name: "sem targets",
			toml: `
schema-version = 1
[project]
group = "x"
`,
			wantErr: "ao menos um target",
		},
		{
			name: "type inválido",
			toml: `
schema-version = 1
[project]
group = "x"
[targets.a]
type = "gradle"
`,
			wantErr: `type "gradle" inválido`,
		},
		{
			name: "type ausente",
			toml: `
schema-version = 1
[project]
group = "x"
[targets.a]
path = "apps/a"
`,
			wantErr: "type é obrigatório",
		},
		{
			name: "reactor sem module",
			toml: `
schema-version = 1
[project]
group = "x"
[targets.a]
type = "maven"
reactor = true
`,
			wantErr: "reactor = true exige module",
		},
		{
			name: "imagem duplicada",
			toml: `
schema-version = 1
[project]
group = "x"
[targets.a]
type = "maven"
image = "app"
[targets.b]
type = "npm"
image = "app"
`,
			wantErr: `imagem "app" duplicada`,
		},
		{
			name: "imagem duplicada por default de nome",
			toml: `
schema-version = 1
[project]
group = "x"
[targets.app]
type = "maven"
[targets.b]
type = "npm"
image = "app"
`,
			wantErr: `imagem "app" duplicada`,
		},
		{
			name: "quality-type inválido",
			toml: `
schema-version = 1
[project]
group = "x"
[targets.a]
type = "dockerfile"
sonar = true
quality-type = "gradle"
`,
			wantErr: `quality-type "gradle" inválido`,
		},
		{
			name: "quality-type dockerfile",
			toml: `
schema-version = 1
[project]
group = "x"
[targets.a]
type = "dockerfile"
sonar = true
quality-type = "dockerfile"
`,
			wantErr: `quality-type "dockerfile" inválido`,
		},
		{
			name: "quality-type custom",
			toml: `
schema-version = 1
[project]
group = "x"
[targets.a]
type = "dockerfile"
sonar = true
quality-type = "custom"
`,
			wantErr: `quality-type "custom" inválido`,
		},
		{
			name: "quality-type sem sonar",
			toml: `
schema-version = 1
[project]
group = "x"
[targets.a]
type = "dockerfile"
quality-type = "uv"
`,
			wantErr: "quality-type só faz sentido com sonar = true",
		},
		{
			name: "dockerfile com sonar e sem quality-type",
			toml: `
schema-version = 1
[project]
group = "x"
[targets.a]
type = "dockerfile"
sonar = true
`,
			wantErr: `sonar = true em type = "dockerfile" exige quality-type`,
		},
		{
			name: "sonar-project-key sem sonar",
			toml: `
schema-version = 1
[project]
group = "x"
[targets.a]
type = "maven"
sonar-project-key = "x-a"
`,
			wantErr: "sonar-project-key só faz sentido com sonar = true",
		},
		{
			name: "sonar-project-key duplicada",
			toml: `
schema-version = 1
[project]
group = "x"
[targets.a]
type = "maven"
sonar = true
sonar-project-key = "x-comum"
[targets.b]
type = "maven"
sonar = true
sonar-project-key = "x-comum"
`,
			wantErr: `sonar-project-key "x-comum" duplicada entre os targets "a" e "b"`,
		},
		{
			name: "sonar-project-key colide com nome de target",
			toml: `
schema-version = 1
[project]
group = "x"
[targets.a]
type = "maven"
sonar = true
[targets.b]
type = "maven"
sonar = true
sonar-project-key = "a"
`,
			wantErr: `sonar-project-key "a" duplicada entre os targets "a" e "b"`,
		},
		{
			name: "campo desconhecido",
			toml: `
schema-version = 1
[project]
group = "x"
[targets.a]
type = "maven"
usedocker = true
`,
			wantErr: "parse pipeline config",
		},
		{
			name: "seção desconhecida",
			toml: `
schema-version = 1
[project]
group = "x"
[defaults.gradle]
image = "gradle"
[targets.a]
type = "maven"
`,
			wantErr: "parse pipeline config",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load([]byte(tc.toml))
			if err == nil {
				t.Fatalf("esperava erro contendo %q, obteve nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("erro = %q, esperava conter %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestQualityTypeResolvesBuildSystemFields cobre o motivo de ser do
// `quality-type`: uma imagem construída por Dockerfile próprio cujo CÓDIGO é
// analisado por outro build system. O ResolvedTarget precisa trazer tanto o
// Dockerfile do build quanto os campos de uv que o check de qualidade consome —
// senão a estratégia de quality receberia um target vazio.
func TestQualityTypeResolvesBuildSystemFields(t *testing.T) {
	cfg, err := Load([]byte(`
schema-version = 1
[project]
group = "licitacao"
[defaults.uv]
build-image = "ghcr.io/astral-sh/uv:python3.12-bookworm-slim"
run-image = "python:3.12-slim-bookworm"
[targets.scrapers_mte]
type = "dockerfile"
path = "scrapers/mte"
sonar = true
quality-type = "uv"
run-subdir = "src"
customizations = ["dlt"]
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	rt, err := cfg.Resolve("scrapers_mte")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if rt.Type != TypeDockerfile {
		t.Errorf("Type = %q, quer %q", rt.Type, TypeDockerfile)
	}
	if rt.QualityType != TypeUv {
		t.Errorf("QualityType = %q, quer %q", rt.QualityType, TypeUv)
	}
	if rt.Dockerfile != DefaultDockerfile {
		t.Errorf("Dockerfile = %q, quer %q", rt.Dockerfile, DefaultDockerfile)
	}
	if rt.UvBuildImage != "ghcr.io/astral-sh/uv:python3.12-bookworm-slim" {
		t.Errorf("UvBuildImage = %q (defaults de uv não chegaram ao target dockerfile)", rt.UvBuildImage)
	}
	if rt.UvRunImage != "python:3.12-slim-bookworm" {
		t.Errorf("UvRunImage = %q", rt.UvRunImage)
	}
	if rt.RunSubdir != "src" {
		t.Errorf("RunSubdir = %q, quer \"src\"", rt.RunSubdir)
	}
	if !reflect.DeepEqual(rt.Customizations, []string{"dlt"}) {
		t.Errorf("Customizations = %v, quer [dlt]", rt.Customizations)
	}
	if got, want := rt.VersionFilePath(), "scrapers/mte/pyproject.toml"; got != want {
		t.Errorf("VersionFilePath = %q, quer %q", got, want)
	}
}

// TestQualityTypeDrivesVersionFileDefault documenta que o default de
// version-file segue o build system que descreve o código, não o Dockerfile.
func TestQualityTypeDrivesVersionFileDefault(t *testing.T) {
	cfg, err := Load([]byte(`
schema-version = 1
[project]
group = "x"
[targets.a]
type = "dockerfile"
sonar = true
quality-type = "maven"
[targets.b]
type = "dockerfile"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.EffectiveVersionFile("a"); got != VersionFileMaven {
		t.Errorf("version file com quality-type maven = %q, quer %q", got, VersionFileMaven)
	}
	// Sem quality-type, o default histórico do tipo dockerfile não muda.
	if got := cfg.EffectiveVersionFile("b"); got != VersionFileUv {
		t.Errorf("version file sem quality-type = %q, quer %q", got, VersionFileUv)
	}
}

func TestUseDockerPointerDistinguishesAbsent(t *testing.T) {
	cfg, err := Load([]byte(`
schema-version = 1
[project]
group = "x"
[defaults.maven]
use-docker = true
[targets.a]
type = "maven"
[targets.b]
type = "maven"
use-docker = false
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.EffectiveUseDocker("a") {
		t.Error("target sem use-docker deveria herdar o default true")
	}
	if cfg.EffectiveUseDocker("b") {
		t.Error("target com use-docker = false deveria sobrescrever o default")
	}
}

// TestSonarProjectKey cobre o desacoplamento entre o nome do target e a chave
// do projeto no SonarQube. A chave é o identificador permanente da análise no
// servidor; o nome do target é escolha local do repositório.
func TestSonarProjectKey(t *testing.T) {
	cfg, err := Load([]byte(`
schema-version = 1
[project]
group = "contavinculada"
[targets.contavinculada]
type = "maven"
sonar = true
[targets.frontend]
type = "npm"
sonar = true
sonar-project-key = "contavinculada-frontend"
[targets.scripts]
type = "dockerfile"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Sem o campo, a chave continua sendo o nome do target: nenhum pipeline.toml
	// existente muda de comportamento ao subir de versão.
	if got, want := cfg.EffectiveSonarProjectKey("contavinculada"), "contavinculada"; got != want {
		t.Errorf("EffectiveSonarProjectKey(contavinculada) = %q, quer %q", got, want)
	}
	if got, want := cfg.EffectiveSonarProjectKey("frontend"), "contavinculada-frontend"; got != want {
		t.Errorf("EffectiveSonarProjectKey(frontend) = %q, quer %q", got, want)
	}

	// O ResolvedTarget é o que as estratégias consomem: a chave precisa chegar
	// resolvida lá, não como campo cru.
	rt, err := cfg.Resolve("frontend")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got, want := rt.SonarProjectKey, "contavinculada-frontend"; got != want {
		t.Errorf("ResolvedTarget.SonarProjectKey = %q, quer %q", got, want)
	}
	// A chave não interfere no nome da imagem: são identificadores de sistemas
	// diferentes e o default de ambos é o nome do target.
	if got, want := rt.Image, "frontend"; got != want {
		t.Errorf("ResolvedTarget.Image = %q, quer %q", got, want)
	}

	// SonarProjectKeys cobre exatamente os targets com sonar = true, na ordem
	// alfabética dos targets — é a fonte do seeding dos projetos no servidor.
	if got, want := cfg.SonarProjectKeys(), []string{"contavinculada", "contavinculada-frontend"}; !reflect.DeepEqual(got, want) {
		t.Errorf("SonarProjectKeys = %v, quer %v", got, want)
	}
}
