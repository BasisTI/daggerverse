// Dagger module to build maven Projects
package main

import (
	"context"
	"dagger/maven/internal/dagger"
	"fmt"
)

// FullBuildModules Builds all stages: compile+test, sonar, docker push for a list of modules
func (m *Maven) FullBuildModules(ctx context.Context, source *dagger.Directory, modules []string, sonarConfig *SonarConfig, jibConfig *JibConfig) ([]*ModuleBuildResult, error) {
	results := make([]*ModuleBuildResult, 0)
	for _, module := range modules {
		result, err := m.FullBuild(ctx, source, module, sonarConfig, jibConfig)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

// FullBuild Builds all stages: compile+test, sonar, docker push for a given maven module
func (m *Maven) FullBuild(ctx context.Context, source *dagger.Directory, module string, sonarConfig *SonarConfig, jibConfig *JibConfig) (*ModuleBuildResult, error) {

	moduleSonarConfig := *sonarConfig
	moduleJibConfig := *jibConfig
	moduleSonarConfig.ProjectKey = module
	moduleJibConfig.Image = module
	moduleJibConfig.Tag = m.GetVersionOrDefault(ctx, source, "lastest")
	stages := []PipelineStage{
		{
			DisplayName: "Build and Test",
			Goals:       []string{"clean", "verify"},
		},
		{
			DisplayName: "SonarQube Analysis",
			Goals:       []string{"sonar:sonar"},
			Options:     m.buildSonarOptions(&moduleSonarConfig),
		},
		{
			DisplayName: "Docker Build and Push with Jib",
			Goals:       []string{"jib:build"},
			Options:     m.buildJibOptions(&moduleJibConfig),
		},
	}
	buildResult, err := m.executeStages(ctx, source.Directory(module), module, stages)
	if err != nil {
		return nil, err
	}
	buildResult.ImageUrl = moduleJibConfig.getImageUrl()
	return buildResult, nil
}

// executeStages Execute pipeline stages stage by stage
func (m *Maven) executeStages(
	ctx context.Context,
	source *dagger.Directory,
	module string,
	stages []PipelineStage) (*ModuleBuildResult, error) {
	stageContainer := m.Container()
	result := &ModuleBuildResult{}
	for i, stage := range stages {
		buildResultStage, err := m.executeStage(ctx, source, module, i, stage, stageContainer)
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

func (m *Maven) executeStage(
	ctx context.Context,
	source *dagger.Directory,
	module string,
	stageNumber int,
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

func (m *Maven) buildJibOptions(config *JibConfig) []string {
	if config == nil {
		return nil
	}

	var options []string

	imageUrl := config.getImageUrl()

	if imageUrl != "" {
		options = append(options, fmt.Sprintf("-Djib.to.image=%s", imageUrl))
	}

	if config.Username != "" {
		options = append(options, fmt.Sprintf("-Djib.to.auth.username=%s", config.Username))
	}

	if config.Password != "" {
		options = append(options, fmt.Sprintf("-Djib.to.auth.password=%s", config.Password))
	}

	if config.Options != nil {
		options = append(options, config.Options...)
	}

	return options
}

func (j *JibConfig) getImageUrl() string {
	imageUrl := ""
	if j.Image != "" {
		imageUrl = fmt.Sprintf("%s/%s/%s", j.Registry, j.Group, j.Image)
	} else {
		imageUrl = fmt.Sprintf("%s/%s", j.Registry, j.Group)
	}
	tag := j.Tag
	if j.Tag == "" {
		tag = "latest"
	}
	return fmt.Sprintf("%s:%s", imageUrl, tag)
}
