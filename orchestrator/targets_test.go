package main

import (
	"dagger/orchestrator/internal/dagger"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/BasisTI/daggerverse/pipeline/config"
)

// loadTestdata carrega um dos TOMLs reais de projeto usados como fixture.
func loadTestdata(t *testing.T, name string) *config.Config {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".toml"))
	if err != nil {
		t.Fatalf("ler fixture %s: %v", name, err)
	}
	cfg, err := config.Load(data)
	if err != nil {
		t.Fatalf("carregar fixture %s: %v", name, err)
	}
	return cfg
}

var allFixtures = []string{"colaboradados", "contavinculada", "kaizenstat", "licitacao", "triagem"}

// TestBuildTargetsCoversEveryNonCustomTarget garante que todo target buildável
// da configuração vira um BuildTarget, e que os custom ficam de fora.
func TestBuildTargetsCoversEveryNonCustomTarget(t *testing.T) {
	for _, fixture := range allFixtures {
		t.Run(fixture, func(t *testing.T) {
			cfg := loadTestdata(t, fixture)
			targets, err := buildTargets(cfg)
			if err != nil {
				t.Fatalf("buildTargets: %v", err)
			}

			var want []string
			for _, rt := range cfg.ResolveAll() {
				if rt.Type != config.TypeCustom {
					want = append(want, rt.Name)
				}
			}
			got := make([]string, 0, len(targets))
			for name, target := range targets {
				got = append(got, name)
				if target.Build == nil {
					t.Errorf("target %q sem estratégia de build", name)
				}
			}
			sort.Strings(want)
			sort.Strings(got)
			if !reflect.DeepEqual(want, got) {
				t.Errorf("targets = %v, esperado %v", got, want)
			}
		})
	}
}

// TestBuildTargetMapping confere o mapeamento config -> BuildTarget para os
// casos que diferenciam os projetos entre si.
func TestBuildTargetMapping(t *testing.T) {
	type want struct {
		path              string
		mountPath         string
		versionFile       string
		rootVersionFile   bool
		versionFilePath   string
		extraTriggerPaths []string
	}
	cases := []struct {
		fixture string
		target  string
		want    want
	}{
		{
			// Caso simples: tudo default a partir do nome do target.
			fixture: "contavinculada", target: "contavinculada",
			want: want{path: "contavinculada", mountPath: "contavinculada",
				versionFile: "pom.xml", versionFilePath: "contavinculada/pom.xml"},
		},
		{
			// npm herda o version file package.json.
			fixture: "contavinculada", target: "frontend",
			want: want{path: "frontend", mountPath: "frontend",
				versionFile: "package.json", versionFilePath: "frontend/package.json"},
		},
		{
			// path explícito, source-path segue o path.
			fixture: "licitacao", target: "processos_judiciais",
			want: want{path: "scrapers/processos_judiciais", mountPath: "scrapers/processos_judiciais",
				versionFile: "pyproject.toml", versionFilePath: "scrapers/processos_judiciais/pyproject.toml"},
		},
		{
			// dockerfile monta a raiz mas vive num subdiretório: o version file
			// tem de ser o do subdiretório, não o da raiz.
			fixture: "kaizenstat", target: "judge-api",
			want: want{path: "apps/judge-api", mountPath: config.RepoRoot,
				versionFile: "pyproject.toml", versionFilePath: "apps/judge-api/pyproject.toml"},
		},
		{
			// reactor: monta a raiz e compartilha o pom.xml de raiz.
			fixture: "triagem", target: "triagem-core",
			want: want{path: "triagem-core", mountPath: config.RepoRoot,
				versionFile: "pom.xml", rootVersionFile: true, versionFilePath: "pom.xml",
				extraTriggerPaths: []string{"triagem-contracts", "pom.xml"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.fixture+"/"+tc.target, func(t *testing.T) {
			cfg := loadTestdata(t, tc.fixture)
			targets, err := buildTargets(cfg)
			if err != nil {
				t.Fatalf("buildTargets: %v", err)
			}
			bt, ok := targets[tc.target]
			if !ok {
				t.Fatalf("target %q ausente", tc.target)
			}
			if bt.Path != tc.want.path {
				t.Errorf("Path = %q, esperado %q", bt.Path, tc.want.path)
			}
			if bt.MountPath != tc.want.mountPath {
				t.Errorf("MountPath = %q, esperado %q", bt.MountPath, tc.want.mountPath)
			}
			if bt.VersionFile != tc.want.versionFile {
				t.Errorf("VersionFile = %q, esperado %q", bt.VersionFile, tc.want.versionFile)
			}
			if bt.RootVersionFile != tc.want.rootVersionFile {
				t.Errorf("RootVersionFile = %t, esperado %t", bt.RootVersionFile, tc.want.rootVersionFile)
			}
			if got := bt.VersionFilePath(tc.target); got != tc.want.versionFilePath {
				t.Errorf("VersionFilePath = %q, esperado %q", got, tc.want.versionFilePath)
			}
			if len(tc.want.extraTriggerPaths) > 0 &&
				!reflect.DeepEqual(bt.ExtraTriggerPaths, tc.want.extraTriggerPaths) {
				t.Errorf("ExtraTriggerPaths = %v, esperado %v", bt.ExtraTriggerPaths, tc.want.extraTriggerPaths)
			}
		})
	}
}

// TestReactorMountsRepoRootWithModulePath cobre a regra que separa reactor do
// resto: o diretório montado é a raiz e o módulo entregue ao maven é o path do
// módulo, não o nome do target.
func TestReactorMountsRepoRootWithModulePath(t *testing.T) {
	cfg := loadTestdata(t, "triagem")
	targets, err := buildTargets(cfg)
	if err != nil {
		t.Fatalf("buildTargets: %v", err)
	}

	for _, name := range []string{"triagem-core", "triagem-ingestao"} {
		bt := targets[name]
		if bt.EffectiveMountPath(name) != config.RepoRoot {
			t.Errorf("%s: MountPath efetivo = %q, esperado %q", name, bt.EffectiveMountPath(name), config.RepoRoot)
		}
		rt, err := cfg.Resolve(name)
		if err != nil {
			t.Fatalf("Resolve(%s): %v", name, err)
		}
		if got := mavenModule(rt, name); got != rt.Module {
			t.Errorf("%s: módulo maven = %q, esperado %q", name, got, rt.Module)
		}
	}

	// Fora do reactor, o módulo continua sendo o nome do target.
	other := loadTestdata(t, "contavinculada")
	rt, err := other.Resolve("snf")
	if err != nil {
		t.Fatalf("Resolve(snf): %v", err)
	}
	if got := mavenModule(rt, "snf"); got != "snf" {
		t.Errorf("módulo maven fora do reactor = %q, esperado \"snf\"", got)
	}
}

// TestReactorTargetsShareOneVersionFile documenta o motivo do RootVersionFile:
// dois targets reactor bumpam o mesmo pom.xml de raiz.
func TestReactorTargetsShareOneVersionFile(t *testing.T) {
	cfg := loadTestdata(t, "triagem")
	targets, err := buildTargets(cfg)
	if err != nil {
		t.Fatalf("buildTargets: %v", err)
	}
	for name, bt := range targets {
		if got := bt.VersionFilePath(name); got != "pom.xml" {
			t.Errorf("%s: version file = %q, esperado \"pom.xml\"", name, got)
		}
	}
}

// TestCustomTargetsFailWithClearMessage garante a mensagem de erro que orienta
// o projeto a manter um módulo Dagger próprio.
func TestCustomTargetsFailWithClearMessage(t *testing.T) {
	cfg := loadTestdata(t, "colaboradados")

	err := errCustomTargets(cfg, "publish-all")
	if err == nil {
		t.Fatal("esperado erro para configuração com targets custom")
	}
	msg := err.Error()
	for _, fragment := range []string{"publish-all", "custom", "módulo Dagger próprio", "check-images", "promote"} {
		if !strings.Contains(msg, fragment) {
			t.Errorf("mensagem não menciona %q:\n%s", fragment, msg)
		}
	}
	for _, name := range []string{"comercial", "financeiro", "lightdash-content", "rh-dp"} {
		if !strings.Contains(msg, name) {
			t.Errorf("mensagem não lista o target custom %q:\n%s", name, msg)
		}
	}

	// Configurações sem targets custom não são afetadas.
	for _, fixture := range []string{"contavinculada", "kaizenstat", "licitacao", "triagem"} {
		if err := errCustomTargets(loadTestdata(t, fixture), "publish-all"); err != nil {
			t.Errorf("%s: erro inesperado: %v", fixture, err)
		}
	}
}

// TestDerivedImagesMatchTargets é o teste anti-drift: o mapa consumido por
// check-images/promote tem de cobrir exatamente os targets declarados,
// inclusive os custom, que não são buildados aqui.
func TestDerivedImagesMatchTargets(t *testing.T) {
	for _, fixture := range allFixtures {
		t.Run(fixture, func(t *testing.T) {
			cfg := loadTestdata(t, fixture)
			images := cfg.ProjectImages()

			if len(images) != len(cfg.Targets) {
				t.Fatalf("imagens derivadas = %d, targets = %d", len(images), len(cfg.Targets))
			}
			for _, rt := range cfg.ResolveAll() {
				key := cfg.Project.Group + "/" + rt.Image
				path, ok := images[key]
				if !ok {
					t.Errorf("imagem %q ausente no mapa derivado", key)
					continue
				}
				if path != rt.Path {
					t.Errorf("imagem %q -> %q, esperado %q", key, path, rt.Path)
				}
			}

			// Todo target buildável aponta para o mesmo path de change detection
			// que sua imagem no mapa derivado.
			targets, err := buildTargets(cfg)
			if err != nil {
				t.Fatalf("buildTargets: %v", err)
			}
			for name, bt := range targets {
				rt, err := cfg.Resolve(name)
				if err != nil {
					t.Fatalf("Resolve(%s): %v", name, err)
				}
				if bt.SourcePath(name) != images[cfg.Project.Group+"/"+rt.Image] {
					t.Errorf("%s: path do build %q difere do path da imagem %q",
						name, bt.SourcePath(name), images[cfg.Project.Group+"/"+rt.Image])
				}
			}
		})
	}
}

// TestQualityTargetsFollowSonarFlag confere que o conjunto de checks vem do
// flag sonar e de mais nada.
func TestQualityTargetsFollowSonarFlag(t *testing.T) {
	cases := map[string][]string{
		"contavinculada": {"contavinculada", "frontend", "integracaosgo", "snf"},
		// Os três scrapers são type = "dockerfile" e mesmo assim entram: têm
		// sonar = true e quality-type = "uv".
		"licitacao":  {"cct_mte", "comprasnet_nodriver", "frontend", "integracao", "licitacao", "mte"},
		"triagem":    {"triagem-core", "triagem-ingestao"},
		"kaizenstat": nil,
	}
	for fixture, want := range cases {
		t.Run(fixture, func(t *testing.T) {
			cfg := loadTestdata(t, fixture)
			targets, err := qualityTargets(cfg)
			if err != nil {
				t.Fatalf("qualityTargets: %v", err)
			}
			got := make([]string, 0, len(targets))
			for name, target := range targets {
				got = append(got, name)
				if target.Check == nil {
					t.Errorf("target %q sem check de qualidade", name)
				}
			}
			sort.Strings(got)
			if len(got) == 0 {
				got = nil
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("quality targets = %v, esperado %v", got, want)
			}
		})
	}
}

// TestQualityTargetsMountPath cobre o reactor no check de qualidade: o Sonar
// também precisa da raiz montada.
func TestQualityTargetsMountPath(t *testing.T) {
	cfg := loadTestdata(t, "triagem")
	targets, err := qualityTargets(cfg)
	if err != nil {
		t.Fatalf("qualityTargets: %v", err)
	}
	for name, qt := range targets {
		if qt.EffectiveMountPath(name) != config.RepoRoot {
			t.Errorf("%s: MountPath efetivo = %q, esperado %q", name, qt.EffectiveMountPath(name), config.RepoRoot)
		}
	}
}

// TestUnsupportedFeaturesFailLoudly documenta o ponto em que o schema aceita
// mais do que os módulos de build sabem executar.
//
// Um target dockerfile sem quality-type não chega aqui na prática — a validação
// da configuração já o rejeita —, mas a estratégia continua sendo a última linha
// de defesa para quem monta um ResolvedTarget à mão.
func TestUnsupportedFeaturesFailLoudly(t *testing.T) {
	for _, rt := range []config.ResolvedTarget{
		{Name: "scraper", Type: config.TypeDockerfile, QualityType: config.TypeDockerfile, Sonar: true},
		{Name: "dbt", Type: config.TypeCustom, QualityType: config.TypeCustom, Sonar: true},
	} {
		if _, err := qualityStrategy(rt); err == nil {
			t.Errorf("%s: esperado erro sem build system de quality", rt.Name)
		} else if !strings.Contains(err.Error(), "sonar") || !strings.Contains(err.Error(), "quality-type") {
			t.Errorf("%s: mensagem pouco clara: %v", rt.Name, err)
		}
	}
}

// TestQualityStrategyDispatchesByQualityType é a garantia da funcionalidade:
// o check é escolhido pelo tipo EFETIVO DE QUALITY, não pelo tipo de build.
// Um target cuja imagem sai de um Dockerfile próprio mas cujo código é Python
// declara quality-type = "uv" e ganha o mesmo checkUv de um target uv.
func TestQualityStrategyDispatchesByQualityType(t *testing.T) {
	for _, qt := range []config.TargetType{config.TypeMaven, config.TypeNpm, config.TypeUv} {
		rt := config.ResolvedTarget{
			Name: "scraper", Type: config.TypeDockerfile, QualityType: qt, Sonar: true,
		}
		if _, err := qualityStrategy(rt); err != nil {
			t.Errorf("dockerfile com quality-type %q deveria ter estratégia: %v", qt, err)
		}
	}

	// E a fixture real: os scrapers do licitacao voltam a ter check de qualidade.
	cfg := loadTestdata(t, "licitacao")
	targets, err := qualityTargets(cfg)
	if err != nil {
		t.Fatalf("qualityTargets: %v", err)
	}
	for _, name := range []string{"comprasnet_nodriver", "mte", "cct_mte"} {
		qt, ok := targets[name]
		if !ok {
			t.Errorf("scraper %q ficou de fora do check-quality", name)
			continue
		}
		if qt.Check == nil {
			t.Errorf("scraper %q sem estratégia de check", name)
		}
		rt, err := cfg.Resolve(name)
		if err != nil {
			t.Fatalf("Resolve(%s): %v", name, err)
		}
		// O check roda pelo módulo uv, então os opts de uv precisam estar
		// preenchidos mesmo num target dockerfile.
		if opts := uvOpts(rt); opts.BuildImage == "" || opts.RunImage == "" {
			t.Errorf("scraper %q: uvOpts incompletos: %+v", name, opts)
		}
	}
}

// TestConfigWarnings garante que o relatório do Validate avisa sobre o que ele
// próprio não conseguiria executar, sem transformar isso em erro.
func TestConfigWarnings(t *testing.T) {
	if warnings := configWarnings(loadTestdata(t, "colaboradados")); len(warnings) != 1 {
		t.Fatalf("esperado 1 aviso para colaboradados, veio %d: %v", len(warnings), warnings)
	} else if !strings.Contains(warnings[0], "custom") {
		t.Errorf("aviso não menciona targets custom: %s", warnings[0])
	}

	for _, fixture := range []string{"contavinculada", "kaizenstat", "licitacao", "triagem"} {
		if warnings := configWarnings(loadTestdata(t, fixture)); len(warnings) != 0 {
			t.Errorf("%s: avisos inesperados: %v", fixture, warnings)
		}
	}
}

// TestValidateReportContent confere que o relatório traz o que o job de lint
// precisa mostrar: cada target e o mapa de imagens derivado.
func TestValidateReportContent(t *testing.T) {
	cfg := loadTestdata(t, "licitacao")
	report := renderReport(cfg, "ci/pipeline.toml")

	for _, fragment := range []string{
		"ci/pipeline.toml",
		"Grupo: licitacao",
		"painel_licitacoes",
		"run-subdir:   src",
		"licitacao/carga_editais -> carga_editais",
		"licitacao/processos_judiciais -> scrapers/processos_judiciais",
		"scrapers/mte/pyproject.toml",
		// O tipo de quality divergente aparece explicitamente, junto dos campos
		// do build system que vai analisar o código.
		"quality-type: uv",
		"quality (uv):",
	} {
		if !strings.Contains(report, fragment) {
			t.Errorf("relatório não contém %q:\n%s", fragment, report)
		}
	}

	// Targets em que build e quality coincidem não ganham a linha extra.
	if strings.Count(report, "quality-type:") != 3 {
		t.Errorf("quality-type deveria aparecer só nos 3 scrapers:\n%s", report)
	}

	kaizenstat := renderReport(loadTestdata(t, "kaizenstat"), "ci/pipeline.toml")
	if !strings.Contains(kaizenstat, "apps/judge-api/Dockerfile") {
		t.Errorf("relatório não mostra o Dockerfile do target:\n%s", kaizenstat)
	}
	// O relatório mostra o mesmo version file que o publish bumparia.
	if !strings.Contains(kaizenstat, "apps/judge-api/pyproject.toml") {
		t.Errorf("relatório não mostra o version file correto:\n%s", kaizenstat)
	}
}

// TestGetSubDirectoryTreatsRepoRoot cobre a regra que faz reactor e workspaces
// funcionarem: "." devolve o próprio source.
func TestGetSubDirectoryTreatsRepoRoot(t *testing.T) {
	source := dag.Directory().WithNewFile("marker", "x")
	if got := getSubDirectory(source, "."); got != source {
		t.Error("getSubDirectory(source, \".\") deveria devolver o próprio source")
	}
	if got := getSubDirectory(source, ""); got != source {
		t.Error("getSubDirectory(source, \"\") deveria devolver o próprio source")
	}
	if got := getSubDirectory(source, "apps/foo"); got == source {
		t.Error("getSubDirectory(source, \"apps/foo\") deveria devolver um subdiretório")
	}
}

// TestNewGitLabClientRequiresFullConfig cobre o caminho em que o reporte de
// commit status é simplesmente desligado: com qualquer uma das três partes da
// configuração ausente, PublishAll e CheckQuality seguem rodando sem GitLab em
// vez de falhar. Este comportamento era testado no módulo de cada projeto até a
// 2.x; ao centralizar o código aqui, o teste veio junto.
func TestNewGitLabClientRequiresFullConfig(t *testing.T) {
	ctx := t.Context()
	token := dag.SetSecret("gitlab-token-teste", "s3cr3t")

	for _, tc := range []struct {
		nome      string
		host      string
		token     *dagger.Secret
		projectID string
	}{
		{"sem host", "", token, "42"},
		{"sem token", "https://git.basis.com.br", nil, "42"},
		{"sem project id", "https://git.basis.com.br", token, ""},
		{"nada", "", nil, ""},
	} {
		t.Run(tc.nome, func(t *testing.T) {
			client, err := newGitLabClient(ctx, tc.host, tc.token, tc.projectID, "develop")
			if err != nil {
				t.Fatalf("configuração incompleta não deveria falhar: %v", err)
			}
			if client != nil {
				t.Errorf("esperado client nil, veio %+v", client)
			}
		})
	}

	client, err := newGitLabClient(ctx, "https://git.basis.com.br", token, "42", "develop")
	if err != nil {
		t.Fatalf("configuração completa: %v", err)
	}
	if client == nil {
		t.Fatal("configuração completa deveria produzir um client")
	}
	if client.BaseURL != "https://git.basis.com.br" || client.ProjectID != "42" || client.Token != "s3cr3t" {
		t.Errorf("client mal montado: %+v", client)
	}
}
