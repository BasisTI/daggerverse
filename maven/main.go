// Dagger module to build maven Projects
package main

import (
	"context"
	"dagger/maven/internal/dagger"
	"fmt"
)

// FullBuildModules orchestrates build, test, Sonar analysis, and image publishing for each module in order.
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

// FullBuild executes the three-stage pipeline (build/test, Sonar, Jib) for a single Maven module.
func (m *Maven) FullBuild(ctx context.Context, source *dagger.Directory, module string, sonarConfig *SonarConfig, jibConfig *JibConfig) (*ModuleBuildResult, error) {

	moduleSonarConfig := *sonarConfig
	moduleJibConfig := *jibConfig
	moduleSonarConfig.ProjectKey = module
	moduleJibConfig.Image = module
	moduleJibConfig.Tag = m.GetVersionOrDefault(ctx, source, "lastest")
	sonarOptions, err := m.buildSonarOptions(ctx, &moduleSonarConfig)
	if err != nil {
		return nil, err
	}
	jibOptions, err := m.buildJibOptions(ctx, &moduleJibConfig)
	if err != nil {
		return nil, err
	}
	stages := []PipelineStage{
		{
			DisplayName: "Build and Test",
			Goals:       []string{"clean", "verify"},
		},
		{
			DisplayName: "SonarQube Analysis",
			Goals:       []string{"sonar:sonar"},
			Options:     sonarOptions,
		},
		{
			DisplayName: "Docker Build and Push with Jib",
			Goals:       []string{"jib:build"},
			Options:     jibOptions,
		},
	}
	buildResult, err := m.executeStages(ctx, source.Directory(module), module, stages)
	if err != nil {
		return nil, err
	}
	buildResult.ImageUrl = moduleJibConfig.getImageUrl()
	return buildResult, nil
}

// executeStages runs the provided pipeline stages sequentially using a shared container.
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

// executeStage mounts the module source, runs the Maven goals for a stage, and captures outputs.
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

// buildJibOptions materializes the Maven command-line arguments needed to run the Jib plugin.
func (m *Maven) buildJibOptions(ctx context.Context, config *JibConfig) ([]string, error) {
	if config == nil {
		return nil, nil
	}

	var options []string

	imageUrl := config.getImageUrl()

	if imageUrl != "" {
		options = append(options, fmt.Sprintf("-Djib.to.image=%s", imageUrl))
	}

	if config.Username != "" {
		options = append(options, fmt.Sprintf("-Djib.to.auth.username=%s", config.Username))
	}

	if config.PasswordSecret != nil {
		password, err := config.PasswordSecret.Plaintext(ctx)
		if err != nil {
			return nil, fmt.Errorf("get jib password: %w", err)
		}
		options = append(options, fmt.Sprintf("-Djib.to.auth.password=%s", password))
	}

	if config.Options != nil {
		options = append(options, config.Options...)
	}

	return options, nil
}

// getImageUrl resolves the fully-qualified image reference, applying the default tag when needed.
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
