package main

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
)

// Sentinela que nenhum arquivo do export pode conter: simula o valor de um segredo do ambiente do
// build que um recurso filtrado gravou em target/.
const secretSentinel = "SENTINEL_PRIVATE_NVD_REVIEW2"

const (
	jacocoXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><!DOCTYPE report PUBLIC "-//JACOCO//DTD Report 1.1//EN" "report.dtd"><report name="x"><counter type="LINE" missed="0" covered="1"/></report>`
	suiteXML  = `<?xml version="1.0" encoding="UTF-8"?>
<testsuite name="demo.CalcTest" tests="1" failures="0"><testcase name="good"/></testsuite>`
)

// filterFixture é a árvore de um módulo depois do build. O mapa é caminho -> conteúdo.
var filterFixture = map[string]string{
	// Fica: bytecode, do módulo e do filho.
	"target/classes/demo/Calc.class":          "bytecode",
	"target/test-classes/demo/CalcTest.class": "bytecode",
	"child/target/classes/demo/Child.class":   "bytecode",
	"child/target/test-classes/demo/T.class":  "bytecode",
	"target/outro-diretorio/demo/Extra.class": "bytecode",
	// Fica: relatórios de teste, reconhecidos pelo conteúdo, em qualquer lugar e com qualquer nome.
	"target/surefire-reports/TEST-demo.CalcTest.xml": suiteXML,
	"target/tests/qualquer-nome.xml":                 suiteXML,
	"child/target/failsafe-reports/TEST-demo.IT.xml": `<testsuites><testsuite name="a"/></testsuites>`,
	// Fica: cobertura.
	"target/site/jacoco/jacoco.xml":              jacocoXML,
	"target/coverage/relatorio-de-cobertura.xml": jacocoXML,
	"target/jacoco.exec":                         "exec",
	"child/target/jacoco-it.exec":                "exec",
	// Fica: fontes geradas.
	"target/generated-sources/annotations/demo/Mapper.java": "class Mapper {}",
	"target/generated-test-sources/demo/T.kt":               "class T",

	// Fora: o caso do revisor. Recursos filtrados com o segredo, com qualquer nome e em qualquer lugar.
	"target/generated-resources/auth.xml":        "<auth><password>" + secretSentinel + "</password></auth>",
	"target/generated-resources/.env.production": "NVD_API_KEY=" + secretSentinel,
	"target/token.json":                          `{"token":"` + secretSentinel + `"}`,
	"target/settings.xml":                        "<settings><password>" + secretSentinel + "</password></settings>",
	"target/.env.production":                     "NVD_API_KEY=" + secretSentinel,
	"target/classes/application.properties":      "db.password=" + secretSentinel,
	"target/classes/application.yml":             "password: " + secretSentinel,
	"target/test-classes/application-test.yml":   "password: " + secretSentinel,
	"child/target/classes/secrets.env":           secretSentinel,
	"target/tests/client.p12":                    secretSentinel,
	// Um XML de nome enganoso: parece relatório e não é.
	"target/TEST-enganoso.xml":   "<config><password>" + secretSentinel + "</password></config>",
	"target/jacoco-enganoso.xml": "<report><password>" + secretSentinel + "</password></report>",
	// Fora: o que a análise não lê.
	"target/app-1.0.jar":                               "jar",
	"target/surefire-reports/demo.CalcTest.txt":        "texto",
	"target/surefire-reports/demo.CalcTest-output.txt": "saida",
	"target/site/jacoco/index.html":                    "<html/>",
	"target/site/jacoco/jacoco.csv":                    "csv",
	"target/maven-archiver/pom.properties":             "p",
	"target/generated-resources/Foo.java":              "class Foo {}",
	"src/main/java/demo/Calc.java":                     "class Calc {}",
	"pom.xml":                                          "<project/>",
	"node_modules/pkg/target/x.class":                  "bytecode",
}

var filterKept = []string{
	"target/classes/demo/Calc.class", "target/test-classes/demo/CalcTest.class",
	"child/target/classes/demo/Child.class", "child/target/test-classes/demo/T.class",
	"target/outro-diretorio/demo/Extra.class",
	"target/surefire-reports/TEST-demo.CalcTest.xml", "target/tests/qualquer-nome.xml",
	"child/target/failsafe-reports/TEST-demo.IT.xml",
	"target/site/jacoco/jacoco.xml", "target/coverage/relatorio-de-cobertura.xml",
	"target/jacoco.exec", "child/target/jacoco-it.exec",
	"target/generated-sources/annotations/demo/Mapper.java", "target/generated-test-sources/demo/T.kt",
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, content := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func runScript(t *testing.T, script, in, out string) string {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "IN="+in, "OUT="+out)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script falhou: %v\n%s", err, output)
	}
	return string(output)
}

func listTree(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			paths = append(paths, rel)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	return paths
}

// O filtro escolhe por conteúdo: leva exatamente o que o scanner lê, e nenhum arquivo do export pode
// conter o sentinela -- nem os recursos filtrados do caso do revisor, nem XML de nome enganoso.
func TestAnalysisFilterSelectsByContent(t *testing.T) {
	in, out := t.TempDir(), t.TempDir()
	writeTree(t, in, filterFixture)
	runScript(t, analysisFilterScript, in, out)

	got := listTree(t, out)
	want := append(append([]string{}, filterKept...), AnalysisChecksumFile)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("export = %v\n quer   %v", got, want)
	}
	for _, p := range got {
		raw, err := os.ReadFile(filepath.Join(out, p))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), secretSentinel) {
			t.Errorf("o export leva o segredo em %s", p)
		}
	}
}

func TestAnalysisFilterEmptyTree(t *testing.T) {
	in, out := t.TempDir(), t.TempDir()
	writeTree(t, in, map[string]string{"pom.xml": "<project/>", "src/A.java": "class A {}"})
	runScript(t, analysisFilterScript, in, out)
	if got := listTree(t, out); len(got) != 0 {
		t.Errorf("export de módulo sem target/ = %v, quer vazio", got)
	}
}

// A conferência pega o que a contagem do manifesto não vê: relatório truncado, vazio ou trocado.
func TestAnalysisVerify(t *testing.T) {
	mk := func(t *testing.T) string {
		in, out := t.TempDir(), t.TempDir()
		writeTree(t, in, filterFixture)
		runScript(t, analysisFilterScript, in, out)
		return out
	}
	t.Run("íntegro", func(t *testing.T) {
		out := mk(t)
		if got := strings.TrimSpace(runScript(t, analysisVerifyScript, out, "")); got != analysisVerifyOK {
			t.Errorf("recusou um export íntegro: %s", got)
		}
	})
	for name, mutate := range map[string]func(string) error{
		"relatório truncado": func(out string) error {
			return os.WriteFile(filepath.Join(out, "target/surefire-reports/TEST-demo.CalcTest.xml"), []byte("<testsuite name=\"demo"), 0o644)
		},
		"relatório vazio": func(out string) error {
			return os.WriteFile(filepath.Join(out, "target/surefire-reports/TEST-demo.CalcTest.xml"), nil, 0o644)
		},
		"jacoco vazio": func(out string) error {
			return os.WriteFile(filepath.Join(out, "target/site/jacoco/jacoco.xml"), nil, 0o644)
		},
		"classe alterada": func(out string) error {
			return os.WriteFile(filepath.Join(out, "target/classes/demo/Calc.class"), []byte("outro"), 0o644)
		},
		"arquivo perdido": func(out string) error {
			return os.Remove(filepath.Join(out, "child/target/failsafe-reports/TEST-demo.IT.xml"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			out := mk(t)
			if err := mutate(out); err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(runScript(t, analysisVerifyScript, out, "")); got == analysisVerifyOK {
				t.Error("aceitou o diretório adulterado")
			}
		})
	}
	t.Run("sem checksum", func(t *testing.T) {
		out := mk(t)
		os.Remove(filepath.Join(out, AnalysisChecksumFile))
		if got := runScript(t, analysisVerifyScript, out, ""); !strings.Contains(got, "sem "+AnalysisChecksumFile) {
			t.Errorf("saída = %q", got)
		}
	})
}

// O estágio de preparo do reactor não pode rodar testes nem o Dependency-Check: um deles
// repetiria o custo que esta análise existe para evitar.
func TestReusePrepOptions(t *testing.T) {
	got := reusePrepOptions("services/api")
	if want := []string{"-pl", "services/api", "-am"}; !reflect.DeepEqual(got[:3], want) {
		t.Fatalf("seletores = %v, quer %v", got[:3], want)
	}
	joined := strings.Join(got, " ")
	for _, flag := range []string{"-DskipTests", "-Dmaven.test.skip=true", "-Ddependency-check.skip=true"} {
		if !strings.Contains(joined, flag) {
			t.Errorf("faltou %s em %v", flag, got)
		}
	}
	if strings.Contains(joined, "-Drevision") {
		t.Errorf("a análise não reescreve a versão: %v", got)
	}
}

// pathWithout monta um diretório com atalhos para todas as ferramentas do PATH atual menos as
// indicadas, para simular uma imagem que não as tem.
func pathWithout(t *testing.T, missing ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		entries, err := os.ReadDir(p)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if slices.Contains(missing, e.Name()) {
				continue
			}
			link := filepath.Join(dir, e.Name())
			if _, err := os.Lstat(link); err == nil {
				continue
			}
			_ = os.Symlink(filepath.Join(p, e.Name()), link)
		}
	}
	return dir
}

// A verificação falha fechado: sem uma ferramenta que ela usa, o resultado é um motivo explícito e
// nunca o marcador de conclusão. Antes, sem `grep`, o diretório adulterado passava em branco.
func TestAnalysisVerifyFailsClosedWithoutTools(t *testing.T) {
	in, out := t.TempDir(), t.TempDir()
	writeTree(t, in, filterFixture)
	runScript(t, analysisFilterScript, in, out)
	// Adultera uma classe: com as ferramentas, a verificação a recusa.
	if err := os.WriteFile(filepath.Join(out, "target/classes/demo/Calc.class"), []byte("outro"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"grep", "sha256sum", "find", "head", "sed"} {
		t.Run("sem "+tool, func(t *testing.T) {
			cmd := exec.Command("/bin/sh", "-c", analysisVerifyScript)
			cmd.Env = []string{"IN=" + out, "PATH=" + pathWithout(t, tool)}
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("script falhou: %v\n%s", err, output)
			}
			got := strings.TrimSpace(string(output))
			if strings.Contains(got, analysisVerifyOK) {
				t.Errorf("declarou a verificação concluída sem %s: %q", tool, got)
			}
			if !strings.Contains(got, "ferramenta ausente na imagem: "+tool) {
				t.Errorf("motivo = %q, quer citar %s", got, tool)
			}
		})
	}
}
