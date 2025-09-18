// Dagger module to build maven Projects
package main

import (
	"context"
	"dagger/maven/internal/dagger"
	"fmt"
)

// FullBuildModules Builds all stages: compile+test, sonar, docker push for a list of modules
func (m *Maven) FullBuildModules(
	ctx context.Context,
	source *dagger.Directory,
	modules []string,
	sonarConfig *SonarConfig,
	jibConfig *JibConfig) []*ModuleBuildResult {
	results := make([]*ModuleBuildResult, 0)
	for _, module := range modules {
		result := m.FullBuild(ctx, source, module, sonarConfig, jibConfig)
		results = append(results, result)
	}
	return results
}

// FullBuild Builds all stages: compile+test, sonar, docker push for a given maven module
func (m *Maven) FullBuild(
	ctx context.Context,
	source *dagger.Directory,
	module string,
	sonarConfig *SonarConfig,
	jibConfig *JibConfig) *ModuleBuildResult {

	moduleSonarConfig := *sonarConfig
	moduleJibConfig := *jibConfig
	moduleSonarConfig.ProjectKey = module
	jibConfig.Image = module
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
	return m.executeStages(ctx, source.Directory(module), module, stages)
}

func (m *Maven) executeStages(
	ctx context.Context,
	source *dagger.Directory,
	module string,
	stages []PipelineStage) *ModuleBuildResult {
	stageContainer := m.Container()
	result := &ModuleBuildResult{}
	for i, stage := range stages {
		buildResultStage, err := m.executeStage(ctx, source, module, i, stage, stageContainer)
		if err != nil {
			return nil
		}
		stageContainer = buildResultStage.Container
		result.Stdout = append(result.Stdout, buildResultStage.Stdout)
		result.Stderr = append(result.Stderr, buildResultStage.Stderr)
		result.ExecutedStages = append(result.ExecutedStages, stage.DisplayName)
		result.Artifacts = buildResultStage.Artifacts
	}
	return result
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

	if config.Registry != "" && config.Image != "" {
		imageUrl := fmt.Sprintf("%s/%s/%s", config.Registry, config.Group, config.Image)
		if config.Tag != "" {
			imageUrl = fmt.Sprintf("%s:%s", imageUrl, config.Tag)
		}
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

func (m *Maven) getImageUrl(config *JibConfig) (string, error) {
	imageUrl := ""
	var err error = nil
	if config.Registry != "" && config.Group != "" && config.Image != "" {
		imageUrl = fmt.Sprintf("%s/%s/%s", config.Registry, config.Group, config.Image)
		if config.Tag != "" {
			imageUrl = fmt.Sprintf("%s:%s", imageUrl, config.Tag)
		}
		return imageUrl, nil
	} else {
		err = fmt.Errorf("registry, group or image is empty in jib configuration")
	}
	return imageUrl, err
}
