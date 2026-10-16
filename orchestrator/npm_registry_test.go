package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestNpmRecebeCredencialDoRegistro garante que todo módulo npm instanciado pelas estratégias
// passa por npmOpts com a credencial do registro. Um dag.Npm montado sem ela faria o `npm ci`
// daquele caminho voltar ao E401 do Nexus, sem nada no orchestrator acusar.
func TestNpmRecebeCredencialDoRegistro(t *testing.T) {
	src, err := os.ReadFile("strategies.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)
	calls := regexp.MustCompile(`dag\.Npm\(source, ([^\n]*)\)\n`).FindAllStringSubmatch(code, -1)
	if len(calls) != strings.Count(code, "dag.Npm(") || len(calls) == 0 {
		t.Fatalf("dag.Npm( fora do formato esperado em strategies.go: o teste perdeu o alvo")
	}
	for _, c := range calls {
		if c[1] != "opts" && !strings.HasPrefix(c[1], "npmOpts(rt, registryCredential{") {
			t.Errorf("dag.Npm(source, %s) não passa pela credencial do registro", c[1])
		}
	}
	for _, m := range regexp.MustCompile(`npmOpts\(rt, ([^)]*)\)`).FindAllStringSubmatch(code, -1) {
		if m[1] == "registryCredential{}" {
			t.Errorf("npmOpts com credencial vazia em strategies.go")
		}
	}
}
