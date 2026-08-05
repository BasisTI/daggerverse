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
) (*ModuleBuildResult, error) {

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
		stages = []PipelineStage{
			{
				DisplayName: "Set version",
				Goals:       []string{"versions:set"},
				Options:     []string{"-DnewVersion=" + version, "-DgenerateBackupPoms=false"},
			},
			{
				DisplayName: "Build and Test",
				Goals:       []string{"clean", "verify"},
			},
		}
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
		}
	}

	buildResult, err := m.executeStages(ctx, source, module, stages, gitlabClient, commitSha)
	if err != nil {
		return nil, err
	}
	buildResult.ImageUrl = imageUrl
	return buildResult, nil
}

// reactorOptions builds the selectors every mvn invocation needs in reactor mode: the target module
// and the revision that stands in for ${revision} in the parent POM.
func reactorOptions(module, version string, extra ...string) []string {
	options := []string{"-pl", module, "-Drevision=" + version}
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
	return []string{"-f", module + "/pom.xml", "-Drevision=" + version}
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
	moduleSonarConfig := *sonarConfig
	moduleSonarConfig.ProjectKey = module
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
	stages []PipelineStage,
	gitlabClient *gitlabci.Client,
	commitSha string,
) (*ModuleBuildResult, error) {
	// Outside reactor mode `source` is the module itself, mounted under its own name. In reactor
	// mode `source` is the whole reactor: -pl/-am need every sibling module on disk, so the root is
	// mounted at /app and that is also where mvn runs from.
	mountPath := fmt.Sprintf("%s/%s", BaseWorkdir, module)
	excludes := []string{"target"}
	if m.ReactorMode {
		mountPath = BaseWorkdir
		excludes = []string{"target", "**/target"}
	}
	stageContainer := m.Container().
		WithDirectory(mountPath, source, dagger.ContainerWithDirectoryOpts{Exclude: excludes}).
		WithWorkdir(mountPath)
	result := &ModuleBuildResult{}
	for _, stage := range stages {
		statusName := ""
		if gitlabClient != nil {
			statusName = fmt.Sprintf("%s: %s", module, stage.DisplayName)
			_ = gitlabClient.SetCommitStatus(commitSha, gitlabci.StateRunning, statusName, "")
		}
		buildResultStage, err := m.executeStage(ctx, module, stage, stageContainer)
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
	module string,
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
	moduleDir := fmt.Sprintf("%s/%s", BaseWorkdir, module)
	artifactsDir := stageContainer.Directory(fmt.Sprintf("%s/target", moduleDir))
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
