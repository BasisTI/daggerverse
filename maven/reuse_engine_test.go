package main

import (
	"context"
	"os"
	"slices"
	"testing"

	"dagger/maven/internal/dagger"
)

// Os testes abaixo sobem um build Maven de verdade no engine do Dagger (imagem e dependências vêm da
// rede), por isso só rodam com TG278_ENGINE_TESTS=1:
//
//	TG278_ENGINE_TESTS=1 dagger run go test -run TestEngine ./...
//
// Eles fixam o que os testes de glob não garantem: que o filtro do Dagger devolve, para um build real,
// os arquivos que a seleção por exclusão promete.

const mavenFixtureImage = "maven:3.9.11-eclipse-temurin-25"

const jacocoPlugin = `<plugin><groupId>org.jacoco</groupId><artifactId>jacoco-maven-plugin</artifactId><version>0.8.13</version>%s<executions><execution><goals><goal>prepare-agent</goal></goals></execution><execution><id>report</id><phase>verify</phase><goals><goal>report</goal></goals></execution></executions></plugin>`

func engineTest(t *testing.T) {
	t.Helper()
	if os.Getenv("TG278_ENGINE_TESTS") != "1" {
		t.Skip("defina TG278_ENGINE_TESTS=1 para rodar contra o engine do Dagger")
	}
}

const calcMain = `package demo; public class Calc { public int value(){return 42;} }`
const calcTest = `package demo; import org.junit.Test; import static org.junit.Assert.*; public class CalcTest { @Test public void good(){assertEquals(42,new Calc().value());} }`
const junitDep = `<dependencies><dependency><groupId>junit</groupId><artifactId>junit</artifactId><version>4.13.2</version><scope>test</scope></dependency></dependencies>`

// aggregateSource: um pom-pai que só gera um recurso e um módulo filho com código, teste e JaCoCo.
func aggregateSource() *dagger.Directory {
	parent := `<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion><groupId>test</groupId><artifactId>aggregate</artifactId><version>1.0</version><packaging>pom</packaging><modules><module>child</module></modules><properties><maven.compiler.release>21</maven.compiler.release></properties><build><plugins><plugin><artifactId>maven-antrun-plugin</artifactId><version>3.1.0</version><executions><execution><phase>generate-resources</phase><goals><goal>run</goal></goals><configuration><target><mkdir dir="${project.build.directory}/classes"/><echo file="${project.build.directory}/classes/version.txt">1.0</echo></target></configuration></execution></executions></plugin>` + sprintfJacoco("") + `</plugins></build></project>`
	child := `<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion><parent><groupId>test</groupId><artifactId>aggregate</artifactId><version>1.0</version></parent><artifactId>child</artifactId>` + junitDep + `</project>`
	return dag.Directory().WithNewFile("pom.xml", parent).WithNewFile("child/pom.xml", child).
		WithNewFile("child/src/main/java/demo/Calc.java", calcMain).
		WithNewFile("child/src/test/java/demo/CalcTest.java", calcTest)
}

// customPathsSource: surefire e JaCoCo gravam em target/tests e target/coverage.
func customPathsSource() *dagger.Directory {
	pom := `<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion><groupId>test</groupId><artifactId>custompaths</artifactId><version>1.0</version><properties><maven.compiler.release>21</maven.compiler.release><sonar.junit.reportPaths>${project.build.directory}/tests</sonar.junit.reportPaths><sonar.coverage.jacoco.xmlReportPaths>${project.build.directory}/coverage/jacoco.xml</sonar.coverage.jacoco.xmlReportPaths></properties>` + junitDep + `<build><plugins><plugin><artifactId>maven-surefire-plugin</artifactId><version>3.5.4</version><configuration><reportsDirectory>${project.build.directory}/tests</reportsDirectory></configuration></plugin>` + sprintfJacoco(`<configuration><outputDirectory>${project.build.directory}/coverage</outputDirectory></configuration>`) + `</plugins></build></project>`
	return dag.Directory().WithNewFile("pom.xml", pom).
		WithNewFile("src/main/java/demo/Calc.java", calcMain).
		WithNewFile("src/test/java/demo/CalcTest.java", calcTest).
		// Recurso filtrado com um valor que não pode viajar num artefato.
		WithNewFile("src/main/resources/application.properties", "db.password=segredo-nao-pode-sair\n")
}

func sprintfJacoco(configuration string) string {
	out := make([]byte, 0, len(jacocoPlugin)+len(configuration))
	for i := 0; i < len(jacocoPlugin); i++ {
		if jacocoPlugin[i] == '%' && i+1 < len(jacocoPlugin) && jacocoPlugin[i+1] == 's' {
			out = append(out, configuration...)
			i++
			continue
		}
		out = append(out, jacocoPlugin[i])
	}
	return string(out)
}

func exportedPaths(t *testing.T, source *dagger.Directory, module string) []string {
	t.Helper()
	ctx := context.Background()
	m := New(mavenFixtureImage, false, true, true, nil, false, false, false, "", nil)
	built, err := m.FullBuild(ctx, source, module, "", "", nil, nil, nil, "")
	if err != nil {
		t.Fatalf("FullBuild: %v", err)
	}
	paths, err := m.AnalysisInputs(built.Tree).Glob(ctx, "**/*")
	if err != nil {
		t.Fatalf("listar o export: %v", err)
	}
	return paths
}

func assertPaths(t *testing.T, paths []string, want, unwanted []string) {
	t.Helper()
	for _, p := range want {
		if !slices.Contains(paths, p) {
			t.Errorf("faltou no export: %s", p)
		}
	}
	for _, p := range unwanted {
		if slices.Contains(paths, p) {
			t.Errorf("não devia estar no export: %s", p)
		}
	}
	if t.Failed() {
		t.Logf("export: %v", paths)
	}
}

// O agregador não pode perder o filho: classes, testes e cobertura do filho viajam com o caminho
// que têm. O recurso do pai não é bytecode e fica de fora.
func TestEngineAggregateKeepsChildModules(t *testing.T) {
	engineTest(t)
	paths := exportedPaths(t, aggregateSource(), "aggregate")
	assertPaths(t, paths, []string{
		"child/target/classes/demo/Calc.class",
		"child/target/test-classes/demo/CalcTest.class",
		"child/target/surefire-reports/TEST-demo.CalcTest.xml",
		"child/target/site/jacoco/jacoco.xml",
	}, []string{
		"target/classes/version.txt",
		"child/target/child-1.0.jar",
	})
}

// Surefire e JaCoCo configurados em outros diretórios continuam no export, e o recurso filtrado com
// segredo não vai.
func TestEngineCustomReportPaths(t *testing.T) {
	engineTest(t)
	paths := exportedPaths(t, customPathsSource(), "custompaths")
	assertPaths(t, paths, []string{
		"target/classes/demo/Calc.class",
		"target/test-classes/demo/CalcTest.class",
		"target/tests/TEST-demo.CalcTest.xml",
		"target/coverage/jacoco.xml",
	}, []string{
		"target/classes/application.properties",
		"target/custompaths-1.0.jar",
	})
}
