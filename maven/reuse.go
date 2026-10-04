package main

import (
	"context"
	"dagger/maven/internal/dagger"
)

// AnalysisInputGlobs são os arquivos do target/ que a análise do Sonar lê, com os caminhos
// relativos ao target/ do módulo.
//
// É o que o `sonar:sonar` precisa para analisar sem refazer o build: as classes compiladas
// (sonar.java.binaries e sonar.java.test.binaries), os relatórios de teste do surefire e do
// failsafe, o XML do JaCoCo e as fontes geradas. O jar, o war e o resto do target/ ficam de
// fora -- são o grosso do tamanho e a análise não os lê.
//
// Os relatórios .txt e .html do surefire também ficam de fora: o Sonar lê só o XML.
var AnalysisInputGlobs = []string{
	"classes/**",
	"test-classes/**",
	"generated-sources/**",
	"generated-test-sources/**",
	"surefire-reports/**/*.xml",
	"failsafe-reports/**/*.xml",
	"site/**/jacoco*.xml",
	"jacoco*.exec",
	"site/**/jacoco*.exec",
}

// AnalysisInputs recorta de um target/ o que a análise do Sonar precisa.
//
// Recebe o `Artifacts` de FullBuild -- o target/ do módulo depois do build -- e devolve um
// diretório com os mesmos caminhos relativos, pronto para ser devolvido ao target/ por
// AnalyzeFromReports.
func (m *Maven) AnalysisInputs(
	// O target/ do módulo, como devolvido em `artifacts` por FullBuild.
	target *dagger.Directory,
) *dagger.Directory {
	return dag.Directory().WithDirectory("/", target, dagger.DirectoryWithDirectoryOpts{
		Include: AnalysisInputGlobs,
	})
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
// repeti-lo custa o tempo de todos os testes. Aqui os arquivos de AnalysisInputs voltam ao target/
// do módulo e o Maven roda o `sonar:sonar` -- que resolve o classpath das bibliotecas sozinho
// (o goal exige resolução de dependências) e não recompila nada.
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
	// O que AnalysisInputs recortou do target/ do módulo no build.
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
