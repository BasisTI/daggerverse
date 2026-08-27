package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/BasisTI/daggerverse/gitlabci"
)

// triggerSeparator separa o nome real de um target do sufixo sintético usado
// para registrar seus ExtraTriggerPaths no mapa de change detection.
const triggerSeparator = "##trigger"

func setStatus(client *gitlabci.Client, sha string, state gitlabci.State, name string) {
	if client == nil {
		return
	}
	if err := client.SetCommitStatus(sha, state, name, ""); err != nil {
		fmt.Printf("⚠️  [GitLab Status] falha ao reportar %s para %s: %v\n", state, name, err)
	}
}

// addTriggerPaths registra entradas sintéticas "<nome>##trigger<i>" -> path no
// ProjectConfig, uma por ExtraTriggerPath. Como GetChangedProjects trabalha com
// um mapa nome -> path, essa é a forma genérica de fazer um target ser disparado
// por mais de um path (trigger cruzado).
func addTriggerPaths(cfg ProjectConfig, name string, extraPaths []string) {
	for i, path := range extraPaths {
		if path == "" {
			continue
		}
		cfg[name+triggerSeparator+strconv.Itoa(i)] = path
	}
}

// normalizeTargetNames remove os sufixos sintéticos de trigger dos nomes
// devolvidos por GetChangedProjects e deduplica preservando a ordem original.
func normalizeTargetNames(names []string) []string {
	out := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if idx := strings.Index(name, triggerSeparator); idx >= 0 {
			name = name[:idx]
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// changedTargets monta o JSON de paths (com triggers sintéticos), consulta
// GetChangedProjects e normaliza o resultado de volta para os nomes reais.
func changedTargets[Dir any](
	ctx context.Context,
	getChangedProjects func(ctx context.Context, source Dir, baseBranch, pathsJson string) ([]string, error),
	projectCfg ProjectConfig,
	source Dir,
	baseBranch string,
) ([]string, error) {
	pathsJson, err := json.Marshal(projectCfg)
	if err != nil {
		return nil, err
	}

	raw, err := getChangedProjects(ctx, source, baseBranch, string(pathsJson))
	if err != nil {
		return nil, err
	}

	return normalizeTargetNames(raw), nil
}

// qualityTargetNames resolve quais targets o check de qualidade vai percorrer: todos, em ordem
// determinística, ou só os alterados em relação a baseBranch.
//
// A ordem importa no modo allTargets porque o mapa de targets não tem ordem: sem o sort, dois
// runs da mesma varredura produziriam logs em ordens diferentes e o relatório de falhas ficaria
// impossível de comparar entre execuções.
func qualityTargetNames[Dir any, Secret any](
	ctx context.Context,
	ops DaggerOps[Dir, Secret],
	qualityTargets map[string]QualityTarget[Dir, Secret],
	source Dir,
	baseBranch string,
	allTargets bool,
) ([]string, error) {
	if allTargets {
		names := make([]string, 0, len(qualityTargets))
		for name := range qualityTargets {
			names = append(names, name)
		}
		sort.Strings(names)
		return names, nil
	}

	projectCfg := make(ProjectConfig, len(qualityTargets))
	for name, target := range qualityTargets {
		projectCfg[name] = target.SourcePath(name)
		addTriggerPaths(projectCfg, name, target.ExtraTriggerPaths)
	}

	return changedTargets(ctx, ops.GetChangedProjects, projectCfg, source, baseBranch)
}

// PublishAll constrói e publica as imagens de todos os projetos alterados.
// Retorna um PublishResult com as imagens publicadas e os arquivos de versão dos projetos alterados.
//
// O diretório entregue a cada BuildStrategy é ops.GetSubDirectory(source, mountPath),
// onde mountPath é o MountPath efetivo do target. A implementação de
// GetSubDirectory DEVE tratar o valor RepoRoot (".") devolvendo o próprio source.
func PublishAll[Dir any, Secret any](
	ctx context.Context,
	ops DaggerOps[Dir, Secret],
	buildTargets map[string]BuildTarget[Dir, Secret],
	source Dir,
	baseBranch, commitSha, version, registry, registryUser string,
	registryPassword Secret,
) (PublishResult, error) {
	projectCfg := make(ProjectConfig, len(buildTargets))
	for name, target := range buildTargets {
		projectCfg[name] = target.SourcePath(name)
		addTriggerPaths(projectCfg, name, target.ExtraTriggerPaths)
	}

	targets, err := changedTargets(ctx, ops.GetChangedProjects, projectCfg, source, baseBranch)
	if err != nil {
		return PublishResult{}, err
	}

	if len(targets) == 0 {
		fmt.Println("✅ Nenhuma mudança detectada. Finalizando Pipeline.")
		return PublishResult{}, nil
	}

	fmt.Printf("🎯 Targets para build: %v\n", targets)

	var sb strings.Builder
	var versionFiles []string
	seenVersionFiles := make(map[string]bool)
	for _, targetName := range targets {
		target, exists := buildTargets[targetName]
		if !exists {
			return PublishResult{}, fmt.Errorf("serviço '%s' alterado mas sem estratégia de build definida", targetName)
		}
		statusName := targetName + ": Build"
		setStatus(ops.GitLabClient, commitSha, gitlabci.StateRunning, statusName)
		fmt.Printf("🚀 [Build] Iniciando: %s (v%s)\n", targetName, version)
		mount := target.EffectiveMountPath(targetName)
		img, err := target.Build(ctx, ops.GetSubDirectory(source, mount), targetName, commitSha, version, registry, registryUser, registryPassword)
		if err != nil {
			setStatus(ops.GitLabClient, commitSha, gitlabci.StateFailed, statusName)
			return PublishResult{}, fmt.Errorf("falha no build de %s: %w", targetName, err)
		}
		setStatus(ops.GitLabClient, commitSha, gitlabci.StateSuccess, statusName)
		sb.WriteString(img + "\n")

		// Vários targets reactor compartilham o pom.xml da raiz: deduplicamos
		// para que o bump de versão aconteça uma única vez por arquivo.
		if vf := target.VersionFilePath(targetName); vf != "" && !seenVersionFiles[vf] {
			seenVersionFiles[vf] = true
			versionFiles = append(versionFiles, vf)
		}
	}

	published := sb.String()
	if published != "" {
		if err := ops.TagWithSha(ctx, published, commitSha, registryUser, registryPassword); err != nil {
			return PublishResult{}, fmt.Errorf("falha ao adicionar tag SHA: %w", err)
		}
	}

	return PublishResult{
		Published:    published,
		VersionFiles: versionFiles,
	}, nil
}

// CheckQuality roda testes e análise estática (Sonar) nos projetos alterados.
//
// Ao contrário de PublishAll, o diretório entregue a cada QualityStrategy é a RAIZ DO
// REPOSITÓRIO, acompanhada do MountPath efetivo do target como `sourcePath`. O recorte do
// subdiretório fica a cargo da estratégia, que monta a árvore inteira para preservar o .git e os
// caminhos do índice do git -- sem isso não há blame, e sem blame não há análise de PR.
//
// Com allTargets, a detecção de mudanças é pulada e todos os targets são analisados. É o que a
// varredura completa de uma branch precisa: fora de uma merge request não existe base natural para
// o diff, e comparar contra qualquer coisa devolveria a lista vazia -- uma varredura que não
// varre nada e ainda assim termina verde.
func CheckQuality[Dir any, Secret any](
	ctx context.Context,
	ops DaggerOps[Dir, Secret],
	qualityTargets map[string]QualityTarget[Dir, Secret],
	source Dir,
	baseBranch, commitSha, sonarHost string,
	sonarToken Secret,
	stopOnFirstFail bool,
	allTargets bool,
) error {
	targets, err := qualityTargetNames(ctx, ops, qualityTargets, source, baseBranch, allTargets)
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
		target, exists := qualityTargets[targetName]
		if !exists {
			return fmt.Errorf("serviço '%s' alterado mas sem check de qualidade definido", targetName)
		}
		mount := target.EffectiveMountPath(targetName)
		statusName := targetName + ": Quality"
		setStatus(ops.GitLabClient, commitSha, gitlabci.StateRunning, statusName)
		fmt.Printf("🔍 [Quality] Verificando: %s\n", targetName)
		if checkErr := target.Check(ctx, source, targetName, mount, sonarHost, sonarToken); checkErr != nil {
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
