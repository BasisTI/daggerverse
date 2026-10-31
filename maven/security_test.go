package main

import (
	"path"
	"strings"
	"testing"
)

// matchInclude imita o filtro Include do Dagger para os padrões usados aqui: "**/" casa um ou
// mais diretórios à esquerda, o resto segue path.Match.
func matchInclude(pattern, p string) bool {
	if rest, ok := strings.CutPrefix(pattern, "**/"); ok {
		parts := strings.Split(p, "/")
		for i := 1; i < len(parts); i++ {
			if ok, _ := path.Match(rest, strings.Join(parts[i:], "/")); ok {
				return true
			}
		}
		return false
	}
	ok, _ := path.Match(pattern, p)
	return ok
}

func TestSecurityReportIncludes(t *testing.T) {
	collected := func(p string) bool {
		for _, pat := range securityReportIncludes() {
			if matchInclude(pat, p) {
				return true
			}
		}
		return false
	}
	for _, p := range []string{
		"target/dependency-check-report.html",
		"target/dependency-check-report.json",
		"target/dependency-check-junit.xml",
		"core/target/dependency-check-junit.xml",
		"libs/core/target/dependency-check-report.json",
	} {
		if !collected(p) {
			t.Errorf("%s deveria ser coletado", p)
		}
	}
	for _, p := range []string{"target/classes/App.class", "target/surefire-reports/TEST-x.xml", "dependency-check-junit.xml"} {
		if collected(p) {
			t.Errorf("%s não deveria ser coletado", p)
		}
	}
}

func TestSecurityDataOptions(t *testing.T) {
	if got := securityDataOptions(false); len(got) != 0 {
		t.Errorf("sem base não pode haver opção: %v", got)
	}
	got := securityDataOptions(true)
	want := []string{"-DdataDirectory=/odc-data", "-DautoUpdate=false"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("opções = %v, quer %v", got, want)
	}
}

func TestUpdateSecurityDataCommand(t *testing.T) {
	cmd := strings.Join(updateSecurityDataCommand("13.0.0"), " ")
	for _, want := range []string{
		"org.owasp:dependency-check-maven:13.0.0:update-only",
		"-DdataDirectory=/odc-data", "-DnvdApiKeyEnvironmentVariable=NVD_API_KEY", "-DnvdMaxRetryCount=60",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("comando sem %q: %s", want, cmd)
		}
	}
	if strings.Contains(cmd, "nvdApiKey=") {
		t.Errorf("a chave não pode ir na linha de comando: %s", cmd)
	}
}
