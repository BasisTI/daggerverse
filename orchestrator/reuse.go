package main

import (
	"context"
	"dagger/orchestrator/internal/dagger"
	"fmt"
	"sort"
	"strings"

	"github.com/BasisTI/daggerverse/pipeline/config"
)

// PublishedFile é o arquivo, na raiz do diretório devolvido por publish-all-with-reports, com as
// imagens publicadas, uma por linha -- o mesmo texto que publish-all devolve.
const PublishedFile = "published.txt"

// reportsExcludeDir é o diretório onde o job de publish exporta o que a análise reaproveita.
// Fica dentro do checkout, porque o GitLab só guarda como artefato o que está no workspace, e por
// isso precisa ficar de fora do que sobe ao engine: não é código do projeto.
const reportsExcludeDir = ".sonar-reuse/"

// reportCollector junta, por target, o que o build deixou para a análise do Sonar.
//
// A estratégia de build devolve só a referência da imagem, e a assinatura é compartilhada com
// npm, uv e dockerfile; o coletor é o canal lateral para o que só o Maven tem a dizer.
type reportCollector struct {
	byTarget map[string]*dagger.Directory
	order    []string
}

func newReportCollector() *reportCollector {
	return &reportCollector{byTarget: map[string]*dagger.Directory{}}
}

func (c *reportCollector) add(target string, inputs *dagger.Directory) {
	if _, seen := c.byTarget[target]; !seen {
		c.order = append(c.order, target)
	}
	c.byTarget[target] = inputs
}

// directory monta o diretório de saída: published.txt e um subdiretório por target.
func (c *reportCollector) directory(published string) *dagger.Directory {
	out := dag.Directory().WithNewFile(PublishedFile, published)
	for _, name := range c.order {
		out = out.WithDirectory(name, c.byTarget[name])
	}
	return out
}

// collectsReports diz se o build do target deixa algo para a análise reaproveitar: só o Maven que
// também é analisado como Maven. O resto continua sendo analisado pelo caminho de sempre.
func collectsReports(rt config.ResolvedTarget) bool {
	return rt.Sonar && rt.Type == config.TypeMaven && rt.QualityType == config.TypeMaven
}

// listFiles devolve os caminhos de tudo o que há no diretório.
func listFiles(ctx context.Context, dir *dagger.Directory) ([]string, error) {
	return dir.Glob(ctx, "**/*")
}

// stampInputs grava no diretório exportado do target o manifesto: o commit, o target e o inventário
// do que o build produziu. É o que a análise confere antes de reaproveitar.
func stampInputs(ctx context.Context, inputs *dagger.Directory, target, commitSha string) (*dagger.Directory, error) {
	paths, err := listFiles(ctx, inputs)
	if err != nil {
		return nil, fmt.Errorf("inventariar os relatórios de %s: %w", target, err)
	}
	manifest, err := newManifest(target, commitSha, paths).marshal()
	if err != nil {
		return nil, err
	}
	return inputs.WithNewFile(ManifestName, manifest), nil
}

// reusableTargets devolve os nomes de target presentes no diretório de relatórios.
//
// Entries devolve os diretórios com `/` no fim.
func reusableTargets(ctx context.Context, reports *dagger.Directory) (map[string]bool, error) {
	if reports == nil {
		return nil, nil
	}
	entries, err := reports.Entries(ctx)
	if err != nil {
		return nil, fmt.Errorf("listar os relatórios reaproveitados: %w", err)
	}
	names := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if name, isDir := strings.CutSuffix(entry, "/"); isDir {
			names[name] = true
		}
	}
	return names, nil
}

// reuseValidator confere o que o publish exportou para um target contra o commit analisado e devolve
// o motivo de não poder reaproveitar, ou "" quando pode.
type reuseValidator func(ctx context.Context, target string) string

// manifestValidator lê o manifesto do target no diretório de relatórios e recontra o inventário.
func manifestValidator(reports *dagger.Directory, commitSha string) reuseValidator {
	return func(ctx context.Context, target string) string {
		dir := reports.Directory(target)
		raw, err := dir.File(ManifestName).Contents(ctx)
		if err != nil {
			return "sem manifesto do publish (" + ManifestName + ")"
		}
		manifest, err := parseManifest(raw)
		if err != nil {
			return err.Error()
		}
		paths, err := listFiles(ctx, dir)
		if err != nil {
			return fmt.Sprintf("não foi possível listar os relatórios: %v", err)
		}
		return reuseProblem(manifest, target, commitSha, inventoryFromPaths(paths))
	}
}

// fallbackTarget é um target que roda o check completo, com o motivo.
type fallbackTarget struct {
	name, reason string
}

// withReusedReports troca o check dos targets Maven cujos relatórios passam na conferência por um
// que só analisa.
//
// Os demais -- npm, uv, Maven sem relatório, e Maven cujo manifesto não confere com o commit nem com
// o inventário -- ficam com o check original, que roda o build inteiro, e o motivo volta para o log.
// Um check de análise que passasse a ignorar esses targets, ou que analisasse dado velho ou parcial,
// deixaria a branch com medidas erradas sem erro nenhum.
func withReusedReports(
	ctx context.Context,
	cfg *config.Config,
	targets map[string]qualityTarget,
	available map[string]bool,
	reports *dagger.Directory,
	validate reuseValidator,
	sonarExtra []string,
	nvdApiKey *dagger.Secret,
) (reused []string, fallback []fallbackTarget, err error) {
	for _, name := range sortedKeys(targets) {
		target := targets[name]
		rt, resolveErr := cfg.Resolve(name)
		if resolveErr != nil {
			return nil, nil, resolveErr
		}
		switch {
		case !collectsReports(rt):
			fallback = append(fallback, fallbackTarget{name, "o publish não exporta relatórios para este tipo de target"})
			continue
		case !available[name]:
			fallback = append(fallback, fallbackTarget{name, "o publish não exportou este target"})
			continue
		}
		if reason := validate(ctx, name); reason != "" {
			fallback = append(fallback, fallbackTarget{name, reason})
			continue
		}
		target.Check = checkMavenFromReports(rt, sonarExtra, nvdApiKey, reports.Directory(name).WithoutFile(ManifestName))
		targets[name] = target
		reused = append(reused, name)
	}
	return reused, fallback, nil
}

func sortedKeys(targets map[string]qualityTarget) []string {
	names := make([]string, 0, len(targets))
	for name := range targets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
