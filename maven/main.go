// Dagger module to build Maven projects.
package main

import (
	"context"
	"dagger/maven/internal/dagger"
	"fmt"
	"time"

	"github.com/BasisTI/daggerverse/gitlabci"
)

// FullBuildModules orchestrates build, test, Sonar analysis, and image publishing for each module in order.
func (m *Maven) FullBuildModules(
	ctx context.Context,
	source *dagger.Directory,
// Digest do commit atual (sha completo)
	commitSha string,
// Versão da aplicação no formato CalVer.BuildNumber
	version string,
	modules []string,
// +optional
	sonarConfig *SonarConfig,
// +optional
	dockerConfig *DockerBuildConfig,
// Emitir marcadores de seção do GitLab CI no log
// +default=false
	useGitlabSections bool,
) ([]*ModuleBuildResult, error) {
	results := make([]*ModuleBuildResult, 0)
	for _, module := range modules {
		result, err := m.FullBuild(ctx, source, module, commitSha, version, sonarConfig, dockerConfig, useGitlabSections)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

// FullBuild executes the three-stage pipeline (build/test, Sonar, Docker publish) for a single Maven module.
func (m *Maven) FullBuild(ctx context.Context,
	source *dagger.Directory,
// Module name
	module string,
// Digest do commit atual (sha completo)
	commitSha string,
// Versão da aplicação no formato CalVer.BuildNumber
	version string,
// +optional
	sonarConfig *SonarConfig,
// +optional
	dockerConfig *DockerBuildConfig,
// Emitir marcadores de seção do GitLab CI no log
// +default=false
	useGitlabSections bool,
) (*ModuleBuildResult, error) {

	stages := []PipelineStage{
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

	if sonarConfig != nil {
		sonarStage, err := m.configureSonar(ctx, sonarConfig, module)
		if err != nil {
			return nil, err
		}
		stages = append(stages, sonarStage)
	}

	imageUrl := ""
	if dockerConfig != nil {
		dockerStage, err := m.configureDockerPublish(ctx, dockerConfig, commitSha, version)
		if err != nil {
			return nil, err
		}
		stages = append(stages, dockerStage)
		imageUrl = dockerConfig.fullImageReference()
	}

	buildResult, err := m.executeStages(ctx, source, module, stages, useGitlabSections)
	if err != nil {
		return nil, err
	}
	buildResult.ImageUrl = imageUrl
	return buildResult, nil
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
		Goals:       []string{"sonar:sonar"},
		Options:     sonarOptions,
	}, nil
}

// executeStages runs the provided pipeline stages sequentially using a shared container.
func (m *Maven) executeStages(
	ctx context.Context,
	source *dagger.Directory,
	module string,
	stages []PipelineStage,
	useGitlabSections bool,
) (*ModuleBuildResult, error) {
	stageContainer := m.Container()
	result := &ModuleBuildResult{}
	for _, stage := range stages {
		sectionID := ""
		if useGitlabSections {
			sectionID = gitlabci.SanitizeID(module, stage.DisplayName)
			gitlabci.SectionStart(sectionID, fmt.Sprintf("%s: %s", module, stage.DisplayName))
		}
		buildResultStage, err := m.executeStage(ctx, source, module, stage, stageContainer)
		if sectionID != "" {
			gitlabci.SectionEnd(sectionID)
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

// executeStage mounts the module source, runs the Maven goals for a stage, and captures outputs.
func (m *Maven) executeStage(
	ctx context.Context,
	source *dagger.Directory,
	module string,
	stage PipelineStage,
	stageContainer *dagger.Container) (*StageBuildResult, error) {
	moduleDir := fmt.Sprintf("%s/%s", BaseWorkdir, module)
	stageContainer = stageContainer.
		WithDirectory(moduleDir, source, dagger.ContainerWithDirectoryOpts{Exclude: []string{"target"}}).
		WithWorkdir(moduleDir).
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
