package main

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// globToRegexp traduz os padrões de AnalysisInputGlobs para expressão regular, com a semântica do
// Include do Dagger: `**` casa qualquer número de diretórios (inclusive zero) e `*` não cruza `/`.
func globToRegexp(glob string) *regexp.Regexp {
	var sb strings.Builder
	sb.WriteString("^")
	for i := 0; i < len(glob); i++ {
		switch {
		case strings.HasPrefix(glob[i:], "**/"):
			sb.WriteString("(.*/)?")
			i += 2
		case strings.HasPrefix(glob[i:], "**"):
			sb.WriteString(".*")
			i++
		case glob[i] == '*':
			sb.WriteString("[^/]*")
		case glob[i] == '.':
			sb.WriteString(`\.`)
		default:
			sb.WriteByte(glob[i])
		}
	}
	sb.WriteString("$")
	return regexp.MustCompile(sb.String())
}

func collectedByAnalysisInputs(p string) bool {
	for _, glob := range AnalysisInputGlobs {
		if globToRegexp(glob).MatchString(p) {
			return true
		}
	}
	return false
}

func TestAnalysisInputGlobs(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"classes/br/com/basis/App.class", true},
		{"classes/application.yml", true},
		{"test-classes/br/com/basis/AppTest.class", true},
		{"generated-sources/annotations/br/com/basis/Mapper.java", true},
		{"surefire-reports/TEST-br.com.basis.AppTest.xml", true},
		{"failsafe-reports/TEST-br.com.basis.AppIT.xml", true},
		{"site/jacoco/jacoco.xml", true},
		{"site/jacoco-it/jacoco.xml", true},
		{"jacoco.exec", true},
		{"jacoco-it.exec", true},
		// Fora: o grosso do tamanho, que a análise não lê.
		{"app-1.0.0.jar", false},
		{"surefire-reports/br.com.basis.AppTest.txt", false},
		{"site/jacoco/index.html", false},
		{"site/jacoco/jacoco.csv", false},
		{"dependency-check-report.json", false},
		{"maven-archiver/pom.properties", false},
	}
	for _, tc := range tests {
		if got := collectedByAnalysisInputs(tc.path); got != tc.want {
			t.Errorf("%s: recolhido = %t, quer %t", tc.path, got, tc.want)
		}
	}
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
