package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BasisTI/daggerverse/gitlabci"
)

func setStatus(client *gitlabci.Client, sha string, state gitlabci.State, name string) {
	if client == nil {
		return
	}
	if err := client.SetCommitStatus(sha, state, name, ""); err != nil {
		fmt.Printf("⚠️  [GitLab Status] falha ao reportar %s para %s: %v\n", state, name, err)
	}
}

// PublishAll constrói e publica as imagens de todos os projetos alterados.
func PublishAll[Dir any, Secret any](
	ctx context.Context,
	ops DaggerOps[Dir, Secret],
	buildTargets map[string]BuildTarget[Dir, Secret],
	source Dir,
	baseBranch, commitSha, version, registry, registryUser string,
	registryPassword Secret,
) (string, error) {
	projectCfg := make(ProjectConfig, len(buildTargets))
	for name, target := range buildTargets {
		projectCfg[name] = target.SourcePath(name)
	}

	pathsJson, err := json.Marshal(projectCfg)
	if err != nil {
		return "", err
	}

	targets, err := ops.GetChangedProjects(ctx, source, baseBranch, string(pathsJson))
	if err != nil {
		return "", err
	}

	if len(targets) == 0 {
		fmt.Println("✅ Nenhuma mudança detectada. Finalizando Pipeline.")
		return "", nil
	}

	fmt.Printf("🎯 Targets para build: %v\n", targets)

	var sb strings.Builder
	for _, targetName := range targets {
		target, exists := buildTargets[targetName]
		if !exists {
			return "", fmt.Errorf("serviço '%s' alterado mas sem estratégia de build definida", targetName)
		}
		statusName := targetName + ": Build"
		setStatus(ops.GitLabClient, commitSha, gitlabci.StateRunning, statusName)
		fmt.Printf("🚀 [Build] Iniciando: %s (v%s)\n", targetName, version)
		dir := target.SourcePath(targetName)
		img, err := target.Build(ctx, ops.GetSubDirectory(source, dir), targetName, commitSha, version, registry, registryUser, registryPassword)
		if err != nil {
			setStatus(ops.GitLabClient, commitSha, gitlabci.StateFailed, statusName)
			return "", fmt.Errorf("falha no build de %s: %w", targetName, err)
		}
		setStatus(ops.GitLabClient, commitSha, gitlabci.StateSuccess, statusName)
		sb.WriteString(img + "\n")
	}

	published := sb.String()
	if published != "" {
		if err := ops.TagWithSha(ctx, published, commitSha, registryUser, registryPassword); err != nil {
			return "", fmt.Errorf("falha ao adicionar tag SHA: %w", err)
		}
	}

	return published, nil
}

// CheckQuality roda testes e análise estática (Sonar) nos projetos alterados.
func CheckQuality[Dir any, Secret any](
	ctx context.Context,
	ops DaggerOps[Dir, Secret],
	qualityTargets map[string]QualityTarget[Dir, Secret],
	source Dir,
	baseBranch, commitSha, sonarHost string,
	sonarToken Secret,
	stopOnFirstFail bool,
) error {
	projectCfg := make(ProjectConfig, len(qualityTargets))
	for name, target := range qualityTargets {
		projectCfg[name] = target.SourcePath(name)
	}

	pathsJson, err := json.Marshal(projectCfg)
	if err != nil {
		return err
	}

	targets, err := ops.GetChangedProjects(ctx, source, baseBranch, string(pathsJson))
	if err != nil {
		return err
	}

	if len(targets) == 0 {
		fmt.Println("✅ Nenhuma mudança detectada. Nenhum check de qualidade necessário.")
		return nil
	}

	fmt.Printf("🎯 Targets para quality check: %v\n", targets)

	type qualityFailure struct {
		name string
		err  error
	}
	var failures []qualityFailure

	for _, targetName := range targets {
		target := qualityTargets[targetName]
		dir := target.SourcePath(targetName)
		statusName := targetName + ": Quality"
		setStatus(ops.GitLabClient, commitSha, gitlabci.StateRunning, statusName)
		fmt.Printf("🔍 [Quality] Verificando: %s\n", targetName)
		if checkErr := target.Check(ctx, ops.GetSubDirectory(source, dir), targetName, sonarHost, sonarToken); checkErr != nil {
			setStatus(ops.GitLabClient, commitSha, gitlabci.StateFailed, statusName)
			if stopOnFirstFail {
				return fmt.Errorf("quality gate falhou para %s: %w", targetName, checkErr)
			}
			failures = append(failures, qualityFailure{name: targetName, err: checkErr})
			fmt.Printf("❌ [Quality] Falhou: %s: %v\n", targetName, checkErr)
		} else {
			setStatus(ops.GitLabClient, commitSha, gitlabci.StateSuccess, statusName)
			fmt.Printf("✅ [Quality] OK: %s\n", targetName)
		}
	}

	if len(failures) > 0 {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Quality gate failed for %d service(s):\n", len(failures)))
		for _, f := range failures {
			sb.WriteString(fmt.Sprintf("  - %s: %v\n", f.name, f.err))
		}
		return fmt.Errorf("%s", sb.String())
	}

	return nil
}
