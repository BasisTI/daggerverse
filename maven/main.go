// Dagger module to build Maven projects.
package main

import (
	"context"
	"dagger/maven/internal/dagger"
	"fmt"
	"time"

	"github.com/BasisTI/daggerverse/gitlabci"
)

// FullBuild executes the three-stage pipeline (build/test, Sonar, Docker publish) for a single Maven module.
//
// With ReactorMode off, source is the module directory itself and the module is built in
// isolation. With ReactorMode on, source is the reactor root and module is the path of the
// target module inside it.
func (m *Maven) FullBuild(ctx context.Context,
	source *dagger.Directory,
// Module name. In reactor mode, the module path relative to the reactor root.
	module string,
// Digest do commit atual (sha completo)
	commitSha string,
// Versão da aplicação no formato CalVer.BuildNumber
	version string,
// +optional
	sonarConfig *SonarConfig,
// +optional
	dockerConfig *DockerBuildConfig,
// GitLab configuration for reporting commit statuses
// +optional
	gitlabConfig *GitLabConfig,
// Caminho do módulo relativo à raiz do repositório. Quando informado, `source` é a RAIZ DO
// REPOSITÓRIO e não o diretório do módulo: a árvore inteira é montada em /app e o build roda em
// /app/<modulePath>.
//
// É o que permite ao Sonar fazer blame. O .git vive na raiz do repositório e o índice do git
// referencia os arquivos pelo caminho a partir dela, então montar o .git dentro do diretório do
// módulo NÃO funciona: o git passaria a resolver `pom.xml` contra a entrada `pom.xml` da raiz --
// arquivo diferente -- e todo arquivo voltaria como "Not Committed Yet". Para o Sonar isso é pior
// do que não ter SCM nenhum: em vez de ficar cego, ele trata 100% do código como novo.
//
// Ignorado em ReactorMode, que já monta a raiz por construção.
// +optional
	modulePath string,
) (*ModuleBuildResult, error) {

	// moduleDir é onde o módulo vive dentro de /app; rootMounted diz se a árvore montada é a raiz
	// do repositório (com o .git) ou apenas o diretório do módulo.
	moduleDir := module
	rootMounted := m.ReactorMode
	if !m.ReactorMode && modulePath != "" {
		moduleDir = modulePath
		rootMounted = true
	}

	var stages []PipelineStage
	if m.ReactorMode {
		// No versions:set here. A CI-friendly reactor versions itself through ${revision}, so the
		// POMs are left untouched and every invocation carries -Drevision instead — rewriting the
		// POMs would fight flatten-maven-plugin and break the sibling dependencies -am resolves.
		stages = []PipelineStage{
			{
				DisplayName: "Build and Test",
				Goals:       []string{"clean", "verify"},
				Options:     reactorOptions(module, version, "-am"),
			},
		}
	} else {
		// Sem versão informada não se reescreve o pom: é o caso do check de qualidade, que não
		// publica nada. A versão declarada no pom -- a última publicada -- é a que vale, e é ela
		// que o Sonar registra como projectVersion. Forçar uma constante aqui congelaria a âncora
		// do período de new code em PREVIOUS_VERSION.
		if version != "" {
			stages = append(stages, PipelineStage{
				DisplayName: "Set version",
				Goals:       []string{"versions:set"},
				Options:     []string{"-DnewVersion=" + version, "-DgenerateBackupPoms=false"},
			})
		}
		stages = append(stages, PipelineStage{
			DisplayName: "Build and Test",
			Goals:       []string{"clean", "verify"},
		})
	}

	if sonarConfig != nil {
		sonarStage, err := m.configureSonar(ctx, sonarConfig, module)
		if err != nil {
			return nil, err
		}
		if m.ReactorMode {
			sonarStage.Options = append(sonarReactorOptions(module, version), sonarStage.Options...)
		}
		stages = append(stages, sonarStage)
	}

	// Jib is the only publish path this module knows. In reactor mode UseJib gates it explicitly, so
	// a reactor whose target module carries no jib plugin can still run build and Sonar.
	imageUrl := ""
	if dockerConfig != nil && (!m.ReactorMode || m.UseJib) {
		dockerStage, err := m.configureDockerPublish(ctx, dockerConfig, commitSha, version)
		if err != nil {
			return nil, err
		}
		if m.ReactorMode {
			dockerStage.Options = append(reactorOptions(module, version), dockerStage.Options...)
		}
		stages = append(stages, dockerStage)
		imageUrl = dockerConfig.fullImageReference()
	}

	var gitlabClient *gitlabci.Client
	if gitlabConfig != nil {
		token, err := gitlabConfig.TokenSecret.Plaintext(ctx)
		if err != nil {
			return nil, fmt.Errorf("get gitlab token: %w", err)
		}
		gitlabClient = &gitlabci.Client{
			BaseURL:   gitlabConfig.Host,
			Token:     token,
			ProjectID: gitlabConfig.ProjectID,
			Ref:       gitlabConfig.Ref,
		}
	}

	buildResult, err := m.executeStages(ctx, source, module, moduleDir, rootMounted, stages, gitlabClient, commitSha)
	if err != nil {
		return nil, err
	}
	buildResult.ImageUrl = imageUrl
	return buildResult, nil
}

// reactorOptions builds the selectors every mvn invocation needs in reactor mode: the target module
// and the revision that stands in for ${revision} in the parent POM.
func reactorOptions(module, version string, extra ...string) []string {
	options := []string{"-pl", module}
	if version != "" {
		options = append(options, "-Drevision="+version)
	}
	return append(options, extra...)
}

// sonarReactorOptions seleciona o módulo por `-f <módulo>/pom.xml` em vez de `-pl <módulo>`.
//
// O scanner exige um "top level project" na sessão do Maven: ele chama
// session.getTopLevelProject(), que procura o projeto cujo diretório é a raiz de execução. Com
// `-pl <módulo>` rodando da raiz do reactor, a raiz de execução é a raiz do repositório mas o
// único projeto da sessão é o módulo -- nenhum casa, e a análise morre com "Maven session does
// not declare a top level project", depois do build inteiro já ter rodado.
//
// Com `-f <módulo>/pom.xml` a raiz de execução passa a ser o diretório do módulo, que é
// justamente o projeto da sessão. O escopo analisado também fica certo: um projeto no Sonar por
// deployable, sem arrastar a raiz e a lib compartilhada para dentro de cada um -- que é o que
// aconteceria acrescentando `-am` para dar um top level project à sessão.
//
// A contrapartida é depender de as dependências irmãs estarem no repositório local, já que não
// há reactor para resolvê-las. Isso não é requisito novo: o estágio do Jib também roda
// `-pl <módulo>` sem `-am` e já depende disso -- é por isso que o pom-esqueleto do reactor
// amarra o maven-install-plugin à fase `verify`.
func sonarReactorOptions(module, version string) []string {
	options := []string{"-f", module + "/pom.xml"}
	if version != "" {
		options = append(options, "-Drevision="+version)
	}
	return options
}

func (m *Maven) configureDockerPublish(
	ctx context.Context,
	dockerConfig *DockerBuildConfig,
// Digest do commit atual (sha completo)
	commitSha string,
// Versão da aplicação no formato CalVer.BuildNumber
	version string) (PipelineStage, error) {
	dockerOptions, err := m.buildDockerOptions(ctx, dockerConfig, commitSha, version)
	if err != nil {
		return PipelineStage{}, err
	}
	return PipelineStage{
		DisplayName: "Docker Build and Push",
		Goals:       []string{"jib:build"},
		Options:     dockerOptions,
	}, nil
}

func (m *Maven) configureSonar(ctx context.Context, sonarConfig *SonarConfig, module string) (PipelineStage, error) {
	// A chave do projeto no Sonar não é derivável do módulo quando o path no repositório difere do
	// nome do target (ex: target `beneficios` em `apps/beneficios`), então quem chama pode informá-la.
	moduleSonarConfig := *sonarConfig
	if moduleSonarConfig.ProjectKey == "" {
		moduleSonarConfig.ProjectKey = module
	}
	sonarOptions, err := m.buildSonarOptions(ctx, &moduleSonarConfig)
	if err != nil {
		return PipelineStage{}, err
	}
	return PipelineStage{
		DisplayName: "SonarQube Analysis",
		Goals:       []string{m.sonarGoal()},
		Options:     sonarOptions,
	}, nil
}

// sonarGoal invoca o scanner pelo groupId:artifactId, e não pelo prefixo `sonar:sonar`.
//
// O prefixo só resolve se o projeto declarar o sonar-maven-plugin: ele não está nos
// pluginGroups padrão do Maven (org.apache.maven.plugins, org.codehaus.mojo). Os quatro
// primeiros projetos migrados funcionaram por acaso -- são gerados pelo JHipster, que
// declara o plugin -- e o primeiro projeto que não é caiu com "No plugin found for prefix
// 'sonar'". Exigir a declaração em todo pom seria uma armadilha silenciosa: quebra na
// análise, depois de o build inteiro já ter rodado.
//
// A versão vem de SonarPluginVersion, e não do pom nem da última release do repositório. A
// forma qualificada com versão é a única que garante as duas coisas ao mesmo tempo: funciona
// em pom que não declara o plugin, e não fica à mercê de uma release nova da SonarSource
// quebrar todas as pipelines de um dia para o outro, sem commit em lugar nenhum.
//
// Um projeto que precise de outra versão a declara em ci/pipeline.toml, e não no pom: o pom
// deixa de influenciar qual scanner roda.
func (m *Maven) sonarGoal() string {
	version := m.SonarPluginVersion
	if version == "" {
		version = DefaultSonarPluginVersion
	}
	return "org.sonarsource.scanner.maven:sonar-maven-plugin:" + version + ":sonar"
}

// executeStages runs the provided pipeline stages sequentially using a shared container.
func (m *Maven) executeStages(
	ctx context.Context,
	source *dagger.Directory,
	module string,
	moduleDir string,
	rootMounted bool,
	stages []PipelineStage,
	gitlabClient *gitlabci.Client,
	commitSha string,
) (*ModuleBuildResult, error) {
	// Com rootMounted, `source` é a raiz do repositório e vai inteira para /app -- é o que o
	// reactor precisa (-pl/-am exigem os módulos irmãos em disco) e o que dá ao Sonar um .git
	// coerente, já que aí todo arquivo fica no mesmo caminho que tem no índice do git.
	// Sem ele, `source` é o próprio diretório do módulo e é montado sob o nome dele.
	mountPath := fmt.Sprintf("%s/%s", BaseWorkdir, moduleDir)
	excludes := []string{"target"}
	if rootMounted {
		mountPath = BaseWorkdir
		excludes = []string{"target", "**/target"}
	}
	// O reactor roda da raiz e seleciona o módulo por -pl; os demais rodam dentro do módulo.
	workdir := fmt.Sprintf("%s/%s", BaseWorkdir, moduleDir)
	if m.ReactorMode {
		workdir = BaseWorkdir
	}
	stageContainer := m.Container().
		WithDirectory(mountPath, source, dagger.ContainerWithDirectoryOpts{Exclude: excludes}).
		WithWorkdir(workdir)
	result := &ModuleBuildResult{}
	for _, stage := range stages {
		statusName := ""
		if gitlabClient != nil {
			statusName = fmt.Sprintf("%s: %s", module, stage.DisplayName)
			_ = gitlabClient.SetCommitStatus(commitSha, gitlabci.StateRunning, statusName, "")
		}
		buildResultStage, err := m.executeStage(ctx, moduleDir, stage, stageContainer)
		if statusName != "" {
			if err != nil {
				_ = gitlabClient.SetCommitStatus(commitSha, gitlabci.StateFailed, statusName, "")
			} else {
				_ = gitlabClient.SetCommitStatus(commitSha, gitlabci.StateSuccess, statusName, "")
			}
		}
		if err != nil {
			return nil, err
		}
		stageContainer = buildResultStage.Container
		result.Stdout = append(result.Stdout, buildResultStage.Stdout)
		result.Stderr = append(result.Stderr, buildResultStage.Stderr)
		result.ExecutedStages = append(result.ExecutedStages, stage.DisplayName)
		result.Artifacts = buildResultStage.Artifacts
	}
	return result, nil
}

// executeStage runs the Maven goals for a stage and captures outputs.
func (m *Maven) executeStage(
	ctx context.Context,
	moduleDir string,
	stage PipelineStage,
	stageContainer *dagger.Container) (*StageBuildResult, error) {
	stageContainer = stageContainer.
		WithExec(m.getFullMvnModuleCommand(stage.Options, stage.Goals))
	stdout, err := stageContainer.Stdout(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get stdout: %w", err)
	}
	stderr, err := stageContainer.Stderr(ctx)
	if err != nil {
		// Non-fatal, stderr might be empty
		stderr = ""
	}
	artifactsDir := stageContainer.Directory(fmt.Sprintf("%s/%s/target", BaseWorkdir, moduleDir))
	return &StageBuildResult{
		Container: stageContainer,
		Artifacts: artifactsDir,
		Stdout:    stdout,
		Stderr:    stderr,
	}, nil
}

// buildDockerOptions materializes the Maven command-line arguments needed to run the Jib plugin.
func (m *Maven) buildDockerOptions(
	ctx context.Context,
	config *DockerBuildConfig,
// Digest do commit atual (sha completo)
	commitSha string,
// Versão da aplicação no formato CalVer.BuildNumber
	version string) ([]string, error) {
	if config == nil {
		return nil, nil
	}
	var options []string
	options = append(options, fmt.Sprintf("-Djib.to.image=%s", config.Image))
	if config.Tag != "" {
		options = append(options, fmt.Sprintf("-Djib.to.tags=%s", config.Tag))
	}
	if config.Username != "" {
		options = append(options, fmt.Sprintf("-Djib.to.auth.username=%s", config.Username))
	}
	if config.PasswordSecret != nil {
		password, err := config.PasswordSecret.Plaintext(ctx)
		if err != nil {
			return nil, fmt.Errorf("error getting registry password: %w", err)
		}
		options = append(options, fmt.Sprintf("-Djib.to.auth.password=%s", password))
	}
	created := time.Now().Format(time.RFC3339)
	options = append(options, fmt.Sprintf("-Djib.container.labels=org.opencontainers.image.revision=%s,"+
		"org.opencontainers.image.version=%s,org.opencontainers.image.created=%s", commitSha, version, created))
	return options, nil
}
