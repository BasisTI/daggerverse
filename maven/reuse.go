package main

import (
	"context"
	"dagger/maven/internal/dagger"
)

// AnalysisInputIncludes seleciona, na árvore de um módulo, o que pode ir para a análise: todo
// `target/`, do módulo e dos filhos. Os caminhos relativos ao módulo são preservados.
//
// O "**/" casa um ou mais diretórios, então o target/ da raiz do módulo entra à parte.
var AnalysisInputIncludes = []string{"target/**", "**/target/**"}

// AnalysisInputExcludes tira do target/ o que a análise não lê e o que não deve viajar num artefato.
//
// A seleção é por exclusão, e não por lista de arquivos conhecidos, de propósito: o pom pode mandar
// o surefire e o JaCoCo para qualquer diretório (`reportsDirectory`, `outputDirectory`) e apontar o
// Sonar para lá (`sonar.junit.reportPaths`, `sonar.coverage.jacoco.xmlReportPaths`). Uma lista de
// globs fixos descartaria esses relatórios e a análise perderia testes e cobertura sem erro. O
// que sobra depois das exclusões segue o caminho que o build lhe deu.
//
// Fora, por tamanho e por não serem lidos: jar, war, pacotes, HTML e demais assets do relatório do
// JaCoCo, os .txt do surefire e logs.
//
// Fora, por poderem carregar segredo: recursos filtrados (`*.properties`, `*.yml`, `*.env`) e
// material de chave e certificado. Os recursos de `classes/` e `test-classes/` saem todos aqui e os
// `.class` voltam por AnalysisClassIncludes: o scanner só lê bytecode de lá.
var AnalysisInputExcludes = []string{
	"**/target/classes/**",
	"**/target/test-classes/**",
	"target/classes/**",
	"target/test-classes/**",
	"**/target/**/*.jar", "**/target/**/*.war", "**/target/**/*.ear", "**/target/**/*.zip",
	"**/target/**/*.tar", "**/target/**/*.tar.gz", "**/target/**/*.tgz", "**/target/**/*.original",
	"**/target/**/*.html", "**/target/**/*.css", "**/target/**/*.js", "**/target/**/*.map",
	"**/target/**/*.png", "**/target/**/*.gif", "**/target/**/*.jpg", "**/target/**/*.svg",
	"**/target/**/*.ico", "**/target/**/*.woff", "**/target/**/*.woff2", "**/target/**/*.ttf",
	"**/target/**/*.txt", "**/target/**/*.log", "**/target/**/*.dump", "**/target/**/*.dumpstream",
	"**/target/**/*.properties", "**/target/**/*.yml", "**/target/**/*.yaml", "**/target/**/*.env",
	"**/target/**/*.pem", "**/target/**/*.key", "**/target/**/*.p12", "**/target/**/*.pfx",
	"**/target/**/*.jks", "**/target/**/*.crt", "**/target/**/*.keystore",
	"**/target/jib-*", "**/target/node/**", "**/target/node_modules/**", "**/target/frontend/**",
	"**/target/maven-archiver/**", "**/target/maven-status/**", "**/target/dependency-check*",
	"target/jib-*", "target/node/**", "target/node_modules/**", "target/frontend/**",
	"target/maven-archiver/**", "target/maven-status/**", "target/dependency-check*",
}

// AnalysisClassIncludes devolve ao recorte o bytecode de `classes/` e `test-classes/`
// (sonar.java.binaries e sonar.java.test.binaries), sem os recursos que ficam ao lado dele.
var AnalysisClassIncludes = []string{
	"target/classes/**/*.class", "**/target/classes/**/*.class",
	"target/test-classes/**/*.class", "**/target/test-classes/**/*.class",
}

// AnalysisInputs recorta de um módulo construído o que a análise do Sonar precisa.
//
// Recebe a `Tree` do resultado de FullBuild -- a árvore do módulo depois do build, com o target/
// dele e o de cada módulo filho -- e devolve um diretório com os mesmos caminhos relativos ao
// módulo, pronto para voltar à árvore por AnalyzeFromReports.
func (m *Maven) AnalysisInputs(
	// A árvore do módulo, como devolvida em `tree` por FullBuild.
	tree *dagger.Directory,
) *dagger.Directory {
	rest := dag.Directory().WithDirectory("/", tree, dagger.DirectoryWithDirectoryOpts{
		Include: AnalysisInputIncludes,
		Exclude: AnalysisInputExcludes,
	})
	classes := dag.Directory().WithDirectory("/", tree, dagger.DirectoryWithDirectoryOpts{
		Include: AnalysisClassIncludes,
	})
	return rest.WithDirectory("/", classes)
}

// reusePrepOptions são as opções do estágio que recoloca no repositório local as dependências
// irmãs de um reactor, quando a análise roda num engine que não tem o cache do build.
//
// O `install` roda sem testes e sem o Dependency-Check: o que se quer é só o artefato dos
// módulos irmãos em ~/.m2, para o `-f <módulo>/pom.xml` do Sonar resolver o classpath. O
// maven-install-plugin do pom-esqueleto do reactor está amarrado à fase verify, por isso o
// goal é `install` e não `package`.
func reusePrepOptions(module string) []string {
	return reactorOptions(module, "", "-am",
		"-DskipTests", "-Dmaven.test.skip=true", "-DskipITs",
		"-Ddependency-check.skip=true", "-Dodc.skip=true")
}

// AnalyzeFromReports roda só a análise do Sonar de um módulo, sobre o resultado de um build já feito.
//
// É o par de FullBuild para o job de análise de branch: o `verify` já rodou no job de publish, e
// repeti-lo custa o tempo de todos os testes. Aqui os arquivos de AnalysisInputs voltam à árvore do
// módulo e o Maven roda o `sonar:sonar` -- que resolve o classpath das bibliotecas sozinho
// (o goal exige resolução de dependências) e não recompila nada.
//
// Quem chama é responsável por só passar inputs do mesmo commit e completos: esta função não os
// confere. A conferência é do orchestrator, com o manifesto que o publish grava.
//
// Num build comum o `sonar:sonar` já era uma segunda invocação do Maven sobre o target/ deixado
// pela primeira; o que muda é de onde vem esse target/, não o que o scanner enxerga.
//
// Em reactor, as dependências irmãs precisam estar no ~/.m2: um estágio de `install` sem testes
// as garante, e custa pouco quando o cache do build está no mesmo engine.
func (m *Maven) AnalyzeFromReports(ctx context.Context,
	source *dagger.Directory,
	// Module name. In reactor mode, the module path relative to the reactor root.
	module string,
	// O que AnalysisInputs recortou da árvore do módulo no build.
	reports *dagger.Directory,
	sonarConfig *SonarConfig,
	// Caminho do módulo relativo à raiz do repositório; mesma semântica do modulePath de FullBuild.
	// +optional
	modulePath string,
) (*ModuleBuildResult, error) {
	// Nenhum teste roda aqui: o daemon do Testcontainers custaria um dind sem ninguém para usá-lo.
	m.UseDocker = false

	moduleDir := module
	rootMounted := m.ReactorMode
	if !m.ReactorMode && modulePath != "" {
		moduleDir = modulePath
		rootMounted = true
	}

	var stages []PipelineStage
	if m.ReactorMode {
		stages = append(stages, PipelineStage{
			DisplayName: "Resolve sibling modules",
			Goals:       []string{"install"},
			Options:     reusePrepOptions(module),
		})
	}
	sonarStage, err := m.configureSonar(ctx, sonarConfig, module)
	if err != nil {
		return nil, err
	}
	if m.ReactorMode {
		sonarStage.Options = append(sonarReactorOptions(module, ""), sonarStage.Options...)
	}
	stages = append(stages, sonarStage)

	return m.executeStages(ctx, source, module, moduleDir, rootMounted, reports, stages, nil, "")
}
