package main

import (
	"reflect"
	"strings"
	"testing"
)

// Os caminhos de cada caso reproduzem o que o export de AnalysisInputs devolve, relativos ao módulo.
var (
	simplePaths = []string{
		"target/classes/demo/Calc.class",
		"target/classes/demo/Util.class",
		"target/test-classes/demo/CalcTest.class",
		"target/surefire-reports/TEST-demo.CalcTest.xml",
		"target/site/jacoco/jacoco.xml",
		"target/jacoco.exec",
		"target/generated-sources/annotations/demo/Mapper.java",
	}
	// Agregador: o pom-pai só tem um recurso; o código e os testes estão no filho.
	aggregatorPaths = []string{
		"target/classes/version.txt",
		"child/target/classes/demo/Calc.class",
		"child/target/test-classes/demo/CalcTest.class",
		"child/target/surefire-reports/TEST-demo.CalcTest.xml",
		"child/target/site/jacoco/jacoco.xml",
	}
	// Surefire e JaCoCo em caminhos que o pom configurou.
	customPaths = []string{
		"target/classes/demo/Calc.class",
		"target/test-classes/demo/CalcTest.class",
		"target/tests/TEST-demo.CalcTest.xml",
		"target/coverage/jacoco.xml",
	}
)

func TestInventoryFromPaths(t *testing.T) {
	tests := []struct {
		name  string
		paths []string
		want  []ModuleInventory
	}{
		{"módulo simples", simplePaths, []ModuleInventory{{Path: ".", Classes: 2, TestClasses: 1, TestReports: 1, Coverage: 1, Exec: 1}}},
		{"agregador com filho", aggregatorPaths, []ModuleInventory{
			{Path: ".", Classes: 0},
			{Path: "child", Classes: 1, TestClasses: 1, TestReports: 1, Coverage: 1},
		}},
		{"caminhos configurados", customPaths, []ModuleInventory{{Path: ".", Classes: 1, TestClasses: 1, TestReports: 1, Coverage: 1}}},
		{"diretórios, manifesto e o que não está em target/ são ignorados", []string{
			"target/", "target/classes/", ManifestName, "pom.xml", "src/Foo.java", "child/target/classes/demo/A.class",
		}, []ModuleInventory{{Path: "child", Classes: 1}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := inventoryFromPaths(tc.paths)
			// O módulo raiz sem nada contável aparece com zeros quando tem arquivos no target/.
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("inventário = %+v, quer %+v", got, tc.want)
			}
		})
	}
}

func TestReuseProblem(t *testing.T) {
	const sha = "0123456789abcdef"
	good := newManifest("api", sha, simplePaths)

	tests := []struct {
		name    string
		m       Manifest
		commit  string
		current []string
		wantErr string // vazio: pode reaproveitar
	}{
		{name: "build íntegro do mesmo commit", m: good, commit: sha, current: simplePaths},
		{name: "agregador com filho íntegro", m: newManifest("api", sha, aggregatorPaths), commit: sha, current: aggregatorPaths},
		{name: "caminhos configurados íntegros", m: newManifest("api", sha, customPaths), commit: sha, current: customPaths},

		// Relatório velho: o diretório é de outro commit.
		{name: "relatório de outro commit", m: good, commit: "fedcba9876543210", current: simplePaths, wantErr: "commit"},
		{name: "commit da análise vazio", m: good, commit: "", current: simplePaths, wantErr: "commit"},

		// Relatório parcial: o que chegou tem menos do que o publish exportou.
		{name: "sem relatórios de teste nem JaCoCo", m: good, commit: sha, wantErr: "difere", current: []string{
			"target/classes/demo/Calc.class", "target/classes/demo/Util.class", "target/test-classes/demo/CalcTest.class",
		}},
		{name: "perdeu o módulo filho", m: newManifest("api", sha, aggregatorPaths), commit: sha, wantErr: "ausente", current: []string{
			"target/classes/version.txt",
		}},
		{name: "chegou módulo que o manifesto não tem", m: good, commit: sha, wantErr: "não consta", current: append(append([]string{}, simplePaths...), "extra/target/classes/X.class")},

		// Outro target, ou outro formato.
		{name: "manifesto de outro target", m: newManifest("web", sha, simplePaths), commit: sha, current: simplePaths, wantErr: "target"},
		{name: "schema desconhecido", m: Manifest{Schema: 99, Target: "api", CommitSha: sha, Modules: good.Modules}, commit: sha, current: simplePaths, wantErr: "schema"},

		// Incoerência do próprio build: classes de teste sem nenhum relatório.
		{name: "testes que não rodaram", m: newManifest("api", sha, []string{
			"target/classes/demo/Calc.class", "target/test-classes/demo/CalcTest.class",
		}), commit: sha, wantErr: "nenhum relatório de teste", current: []string{
			"target/classes/demo/Calc.class", "target/test-classes/demo/CalcTest.class",
		}},
		{name: "nada compilado", m: newManifest("api", sha, []string{"target/surefire-reports/TEST-a.xml"}), commit: sha,
			wantErr: "nenhuma classe", current: []string{"target/surefire-reports/TEST-a.xml"}},

		// Projeto sem testes: legítimo, e diferente de relatórios perdidos.
		{name: "projeto sem testes", m: newManifest("api", sha, []string{"target/classes/demo/Calc.class"}), commit: sha,
			current: []string{"target/classes/demo/Calc.class"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := reuseProblem(tc.m, "api", tc.commit, inventoryFromPaths(tc.current))
			if tc.wantErr == "" {
				if got != "" {
					t.Fatalf("recusou um build íntegro: %s", got)
				}
				return
			}
			if got == "" {
				t.Fatalf("aceitou; esperava recusar com %q", tc.wantErr)
			}
			if !strings.Contains(got, tc.wantErr) {
				t.Errorf("motivo = %q, quer conter %q", got, tc.wantErr)
			}
		})
	}
}

func TestManifestRoundTrip(t *testing.T) {
	m := newManifest("api", "abc", aggregatorPaths)
	raw, err := m.marshal()
	if err != nil {
		t.Fatal(err)
	}
	back, err := parseManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m, back) {
		t.Errorf("manifesto mudou na ida e volta: %+v vs %+v", m, back)
	}
	if _, err := parseManifest("{não é json"); err == nil {
		t.Error("manifesto ilegível deveria dar erro")
	}
}
