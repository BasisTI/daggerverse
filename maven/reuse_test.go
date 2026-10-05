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

func matchesAny(globs []string, p string) bool {
	for _, glob := range globs {
		if globToRegexp(glob).MatchString(p) {
			return true
		}
	}
	return false
}

// collectedByAnalysisInputs reproduz a seleção de AnalysisInputs: o que passa das inclusões e das
// exclusões, mais o bytecode devolvido por AnalysisClassIncludes.
func collectedByAnalysisInputs(p string) bool {
	if matchesAny(AnalysisClassIncludes, p) {
		return true
	}
	return matchesAny(AnalysisInputIncludes, p) && !matchesAny(AnalysisInputExcludes, p)
}

func TestAnalysisInputs(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		// Bytecode, do módulo e dos filhos.
		{"target/classes/br/com/basis/App.class", true},
		{"target/test-classes/br/com/basis/AppTest.class", true},
		{"child/target/classes/demo/Calc.class", true},
		{"services/child/target/test-classes/demo/CalcTest.class", true},
		// Relatórios nos caminhos padrão.
		{"target/surefire-reports/TEST-br.com.basis.AppTest.xml", true},
		{"target/failsafe-reports/TEST-br.com.basis.AppIT.xml", true},
		{"target/site/jacoco/jacoco.xml", true},
		{"target/site/jacoco-it/jacoco.xml", true},
		{"target/jacoco.exec", true},
		{"target/generated-sources/annotations/br/com/basis/Mapper.java", true},
		// Relatórios em caminhos configurados no pom (surefire reportsDirectory, jacoco outputDirectory).
		{"target/tests/TEST-demo.CalcTest.xml", true},
		{"target/coverage/jacoco.xml", true},
		{"child/target/tests/TEST-demo.CalcTest.xml", true},
		{"child/target/coverage/jacoco.xml", true},
		// Fora: tamanho e o que a análise não lê.
		{"target/app-1.0.0.jar", false},
		{"child/target/child-1.0.jar", false},
		{"target/app.jar.original", false},
		{"target/surefire-reports/br.com.basis.AppTest.txt", false},
		{"target/surefire-reports/br.com.basis.AppTest-output.txt", false},
		{"target/site/jacoco/index.html", false},
		{"target/site/jacoco/jacoco-resources/prettify.js", false},
		{"target/site/jacoco/jacoco-resources/report.gif", false},
		{"target/site/jacoco/jacoco.csv", true},
		{"target/maven-archiver/pom.properties", false},
		{"target/jib-image.digest", false},
		{"target/node/node", false},
		{"target/dependency-check-report.json", false},
		// Fora: recursos filtrados podem carregar segredo; só o bytecode fica em classes/.
		{"target/classes/application.yml", false},
		{"target/classes/application-prod.properties", false},
		{"target/classes/static/app.js", false},
		{"target/test-classes/application-test.yml", false},
		{"child/target/classes/secrets.env", false},
		{"target/tests/client.p12", false},
		// Fora do target/ não entra nada.
		{"src/main/java/demo/Calc.java", false},
		{"pom.xml", false},
		{"child/src/test/java/demo/CalcTest.java", false},
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
