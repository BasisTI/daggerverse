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
