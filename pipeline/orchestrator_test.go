package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// O design genérico permite testar com tipos triviais: Dir = string (o path
// montado) e Secret = string.

type fakeOps struct {
	// receivedPaths é o ProjectConfig deserializado que chegou a GetChangedProjects.
	receivedPaths ProjectConfig
	// changed são os nomes (possivelmente sintéticos) devolvidos.
	changed []string
	// mounts registra os paths pedidos a GetSubDirectory, em ordem.
	mounts []string
	// built registra os nomes de módulos buildados, em ordem.
	built []string
}

func (f *fakeOps) ops() DaggerOps[string, string] {
	return DaggerOps[string, string]{
		GetChangedProjects: func(_ context.Context, _ string, _, pathsJson string) ([]string, error) {
			f.receivedPaths = ProjectConfig{}
			if err := json.Unmarshal([]byte(pathsJson), &f.receivedPaths); err != nil {
				return nil, err
			}
			return f.changed, nil
		},
		GetSubDirectory: func(source, path string) string {
			f.mounts = append(f.mounts, path)
			if path == RepoRoot {
				return source
			}
			return source + "/" + path
		},
		TagWithSha: func(context.Context, string, string, string, string) error { return nil },
	}
}

func (f *fakeOps) buildStrategy() BuildStrategy[string, string] {
	return func(_ context.Context, source, module, _, _, _, _ string, _ string) (string, error) {
		f.built = append(f.built, module+"@"+source)
		return "registry/img/" + module, nil
	}
}

func publish(t *testing.T, f *fakeOps, targets map[string]BuildTarget[string, string]) PublishResult {
	t.Helper()
	res, err := PublishAll(context.Background(), f.ops(), targets, "src", "main", "sha1", "1.0.0", "registry", "user", "pass")
	if err != nil {
		t.Fatalf("PublishAll: %v", err)
	}
	return res
}

func TestPublishAllRegistersSyntheticTriggers(t *testing.T) {
	f := &fakeOps{}
	targets := map[string]BuildTarget[string, string]{
		"core": {
			Build:             f.buildStrategy(),
			MountPath:         RepoRoot,
			VersionFile:       "pom.xml",
			RootVersionFile:   true,
			ExtraTriggerPaths: []string{"contracts", "pom.xml"},
		},
		"lightdash-content": {
			Build:             f.buildStrategy(),
			ExtraTriggerPaths: []string{"rh-dp/dbtrh"},
		},
	}

	publish(t, f, targets)

	want := ProjectConfig{
		"core":                        "core",
		"core##trigger0":              "contracts",
		"core##trigger1":              "pom.xml",
		"lightdash-content":           "lightdash-content",
		"lightdash-content##trigger0": "rh-dp/dbtrh",
	}
	if !reflect.DeepEqual(f.receivedPaths, want) {
		t.Errorf("ProjectConfig enviado =\n%v\nquer\n%v", f.receivedPaths, want)
	}
}

func TestPublishAllNormalizesTriggerNames(t *testing.T) {
	f := &fakeOps{
		// GetChangedProjects devolve o nome sintético (o path do trigger mudou),
		// e também o nome real duplicado — ambos devem colapsar em um build.
		changed: []string{"lightdash-content##trigger0", "lightdash-content", "core##trigger1"},
	}
	targets := map[string]BuildTarget[string, string]{
		"core":              {Build: f.buildStrategy(), ExtraTriggerPaths: []string{"contracts", "pom.xml"}},
		"lightdash-content": {Build: f.buildStrategy(), ExtraTriggerPaths: []string{"rh-dp/dbtrh"}},
	}

	res := publish(t, f, targets)

	// Ordem preservada da resposta original, sem duplicatas.
	if want := []string{"lightdash-content@src/lightdash-content", "core@src/core"}; !reflect.DeepEqual(f.built, want) {
		t.Errorf("builds = %v, quer %v", f.built, want)
	}
	if lines := strings.Count(strings.TrimSpace(res.Published), "\n") + 1; lines != 2 {
		t.Errorf("esperava 2 imagens publicadas, obteve %q", res.Published)
	}
}

func TestPublishAllUnknownTargetFails(t *testing.T) {
	f := &fakeOps{changed: []string{"fantasma"}}
	targets := map[string]BuildTarget[string, string]{
		"core": {Build: f.buildStrategy()},
	}
	_, err := PublishAll(context.Background(), f.ops(), targets, "src", "main", "sha1", "1.0.0", "registry", "user", "pass")
	if err == nil || !strings.Contains(err.Error(), "fantasma") {
		t.Errorf("esperava erro para target desconhecido, obteve %v", err)
	}
}

func TestPublishAllDeduplicatesVersionFiles(t *testing.T) {
	f := &fakeOps{changed: []string{"core", "ingestao", "app"}}
	targets := map[string]BuildTarget[string, string]{
		// Dois targets reactor apontando para o mesmo pom.xml da raiz.
		"core":     {Build: f.buildStrategy(), MountPath: RepoRoot, VersionFile: "pom.xml", RootVersionFile: true},
		"ingestao": {Build: f.buildStrategy(), MountPath: RepoRoot, VersionFile: "pom.xml", RootVersionFile: true},
		"app":      {Build: f.buildStrategy(), Path: "apps/app", VersionFile: "package.json"},
	}

	res := publish(t, f, targets)

	want := []string{"pom.xml", "apps/app/package.json"}
	if !reflect.DeepEqual(res.VersionFiles, want) {
		t.Errorf("VersionFiles = %v, quer %v", res.VersionFiles, want)
	}
}

func TestPublishAllMountPathSemantics(t *testing.T) {
	f := &fakeOps{changed: []string{"reactor-mod", "plain", "override"}}
	targets := map[string]BuildTarget[string, string]{
		// reactor: change detection em "triagem-core", mount na raiz.
		"reactor-mod": {Build: f.buildStrategy(), Path: "triagem-core", MountPath: RepoRoot},
		// sem MountPath: monta o próprio SourcePath.
		"plain": {Build: f.buildStrategy(), Path: "apps/plain"},
		// MountPath explícito diferente do path de detecção.
		"override": {Build: f.buildStrategy(), Path: "scripts/override", MountPath: "scripts"},
	}

	publish(t, f, targets)

	if want := []string{RepoRoot, "apps/plain", "scripts"}; !reflect.DeepEqual(f.mounts, want) {
		t.Errorf("mounts = %v, quer %v", f.mounts, want)
	}
	// MountPath "." entrega o próprio source ao build (contrato do callback).
	if want := []string{"reactor-mod@src", "plain@src/apps/plain", "override@src/scripts"}; !reflect.DeepEqual(f.built, want) {
		t.Errorf("builds = %v, quer %v", f.built, want)
	}
}

func TestPublishAllNoChanges(t *testing.T) {
	f := &fakeOps{changed: nil}
	res := publish(t, f, map[string]BuildTarget[string, string]{
		"core": {Build: f.buildStrategy(), VersionFile: "pom.xml"},
	})
	if res.Published != "" || res.VersionFiles != nil {
		t.Errorf("esperava resultado vazio, obteve %+v", res)
	}
}

func TestCheckQualityTriggersAndMounts(t *testing.T) {
	f := &fakeOps{changed: []string{"app##trigger0", "app"}}
	var checked []string
	targets := map[string]QualityTarget[string, string]{
		"app": {
			Check: func(_ context.Context, source, module, _ string, _ string) error {
				checked = append(checked, module+"@"+source)
				return nil
			},
			Path:              "apps/app",
			MountPath:         RepoRoot,
			ExtraTriggerPaths: []string{"libs/shared"},
		},
	}

	if err := CheckQuality(context.Background(), f.ops(), targets, "src", "main", "sha1", "https://sonar", "token", true); err != nil {
		t.Fatalf("CheckQuality: %v", err)
	}

	want := ProjectConfig{"app": "apps/app", "app##trigger0": "libs/shared"}
	if !reflect.DeepEqual(f.receivedPaths, want) {
		t.Errorf("ProjectConfig enviado = %v, quer %v", f.receivedPaths, want)
	}
	if !reflect.DeepEqual(checked, []string{"app@src"}) {
		t.Errorf("checks = %v, quer exatamente um check com a raiz montada", checked)
	}
}

func TestCheckQualityCollectsFailures(t *testing.T) {
	f := &fakeOps{changed: []string{"a", "b"}}
	failing := func(_ context.Context, _, _, _ string, _ string) error { return errors.New("boom") }
	ok := func(_ context.Context, _, _, _ string, _ string) error { return nil }
	targets := map[string]QualityTarget[string, string]{
		"a": {Check: failing},
		"b": {Check: ok},
	}

	err := CheckQuality(context.Background(), f.ops(), targets, "src", "main", "sha1", "https://sonar", "token", false)
	if err == nil || !strings.Contains(err.Error(), "a: boom") {
		t.Errorf("esperava agregação de falhas, obteve %v", err)
	}

	if err := CheckQuality(context.Background(), f.ops(), targets, "src", "main", "sha1", "https://sonar", "token", true); err == nil {
		t.Error("esperava falha imediata com stopOnFirstFail")
	}
}

func TestNormalizeTargetNames(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"vazio", nil, []string{}},
		{"sem triggers", []string{"a", "b"}, []string{"a", "b"}},
		{"strip sufixo", []string{"a##trigger0"}, []string{"a"}},
		{"dedup preservando ordem", []string{"b##trigger1", "a", "b", "b##trigger0", "a##trigger0"}, []string{"b", "a"}},
		{"sufixo com índice grande", []string{"a##trigger12"}, []string{"a"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeTargetNames(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("normalizeTargetNames(%v) = %v, quer %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestAddTriggerPathsSkipsEmpty(t *testing.T) {
	cfg := ProjectConfig{"a": "apps/a"}
	addTriggerPaths(cfg, "a", []string{"libs/x", "", "libs/y"})
	keys := make([]string, 0, len(cfg))
	for k := range cfg {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{"a", "a##trigger0", "a##trigger2"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("chaves = %v, quer %v", keys, want)
	}
}

func TestTargetPathHelpers(t *testing.T) {
	bt := BuildTarget[string, string]{}
	if got := bt.SourcePath("svc"); got != "svc" {
		t.Errorf("SourcePath = %q", got)
	}
	if got := bt.EffectiveMountPath("svc"); got != "svc" {
		t.Errorf("EffectiveMountPath = %q", got)
	}
	if got := bt.VersionFilePath("svc"); got != "" {
		t.Errorf("VersionFilePath sem VersionFile = %q, quer vazio", got)
	}

	root := BuildTarget[string, string]{Path: RepoRoot, VersionFile: "pom.xml"}
	if got := root.VersionFilePath("svc"); got != "pom.xml" {
		t.Errorf("VersionFilePath com SourcePath '.' = %q", got)
	}

	qt := QualityTarget[string, string]{Path: "apps/a", MountPath: RepoRoot}
	if got := qt.SourcePath("a"); got != "apps/a" {
		t.Errorf("QualityTarget.SourcePath = %q", got)
	}
	if got := qt.EffectiveMountPath("a"); got != RepoRoot {
		t.Errorf("QualityTarget.EffectiveMountPath = %q", got)
	}
	if got := (QualityTarget[string, string]{Path: "apps/a"}).EffectiveMountPath("a"); got != "apps/a" {
		t.Errorf("QualityTarget.EffectiveMountPath default = %q", got)
	}
}
