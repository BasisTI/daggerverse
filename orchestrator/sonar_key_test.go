package main

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strings"
	"testing"

	"github.com/BasisTI/daggerverse/pipeline/config"
)

// TestQualityStrategiesUseResolvedSonarKey trava a regressão que motivou o campo
// `sonar-project-key`: as três estratégias de check recebem `module` como
// argumento e durante muito tempo o usaram como chave do projeto no SonarQube.
//
// Os dois identificadores só coincidem por acaso. Em `checkMaven`, `module` é o
// path do módulo no reactor; a chave é o identificador permanente da análise no
// servidor. Como as estratégias são closures que só rodam com um engine Dagger,
// a asserção é sobre a árvore sintática -- é o que dá para verificar sem subir
// um build inteiro.
func TestQualityStrategiesUseResolvedSonarKey(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "strategies.go", nil, 0)
	if err != nil {
		t.Fatalf("parse strategies.go: %v", err)
	}

	for _, fn := range []string{"checkMaven", "checkNpm", "checkUv"} {
		t.Run(fn, func(t *testing.T) {
			decl := findFunc(file, fn)
			if decl == nil {
				t.Fatalf("função %s não encontrada em strategies.go", fn)
			}
			call := findCall(decl, "NewSonarConfig")
			if call == nil {
				t.Fatalf("%s não chama NewSonarConfig", fn)
			}

			var src strings.Builder
			if err := printer.Fprint(&src, fset, call); err != nil {
				t.Fatalf("imprimir chamada: %v", err)
			}
			got := src.String()

			if !strings.Contains(got, "rt.SonarProjectKey") {
				t.Errorf("%s não usa a chave resolvida do target:\n%s", fn, got)
			}
			// `module` chegando ao NewSonarConfig é exatamente o bug antigo.
			for _, forbidden := range []string{"ProjectKey: module", ", module,", ", module)"} {
				if strings.Contains(got, forbidden) {
					t.Errorf("%s passa `module` como chave do Sonar (%q):\n%s", fn, forbidden, got)
				}
			}
		})
	}
}

// TestReportShowsSonarKey garante que o job de lint da configuração mostra a
// chave: é por ela que se confere, antes de abrir a MR, que o projeto existe no
// servidor.
func TestReportShowsSonarKey(t *testing.T) {
	cfg, err := config.Load([]byte(`
schema-version = 1
[project]
group = "contavinculada"
[targets.frontend]
type = "npm"
sonar = true
sonar-project-key = "contavinculada-frontend"
[targets.snf]
type = "maven"
sonar = true
[targets.scripts]
type = "dockerfile"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	report := renderReport(cfg, "ci/pipeline.toml")

	for _, fragment := range []string{
		"sonar-key:    contavinculada-frontend",
		// Sem o campo, a chave continua sendo o nome do target -- e o relatório
		// diz qual é, em vez de deixar implícito.
		"sonar-key:    snf",
	} {
		if !strings.Contains(report, fragment) {
			t.Errorf("relatório não contém %q:\n%s", fragment, report)
		}
	}

	// Target sem sonar não tem chave: mostrar uma sugeriria um projeto que não
	// existe no servidor.
	if strings.Count(report, "sonar-key:") != 2 {
		t.Errorf("sonar-key deveria aparecer só nos 2 targets com sonar:\n%s", report)
	}
}

func findFunc(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}

// findCall devolve a primeira chamada a um método com o nome dado dentro da
// função.
func findCall(fn *ast.FuncDecl, method string) *ast.CallExpr {
	var found *ast.CallExpr
	ast.Inspect(fn, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == method {
			found = call
			return false
		}
		return true
	})
	return found
}
