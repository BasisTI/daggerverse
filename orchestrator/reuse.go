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

// withReusedReports troca o check dos targets Maven que têm relatórios por um que só analisa.
//
// Os demais -- npm, uv, e Maven sem relatório (o publish não o construiu, ou o diretório não veio)
// -- ficam com o check original, que roda o build inteiro. Um check de análise que passasse a
// ignorar esses targets deixaria a branch sem análise em silêncio.
func withReusedReports(
	cfg *config.Config,
	targets map[string]qualityTarget,
	available map[string]bool,
	reports *dagger.Directory,
	sonarExtra []string,
	nvdApiKey *dagger.Secret,
) (reused, fallback []string, err error) {
	for _, name := range sortedKeys(targets) {
		target := targets[name]
		rt, resolveErr := cfg.Resolve(name)
		if resolveErr != nil {
			return nil, nil, resolveErr
		}
		if !available[name] || !collectsReports(rt) {
			fallback = append(fallback, name)
			continue
		}
		target.Check = checkMavenFromReports(rt, sonarExtra, nvdApiKey, reports.Directory(name))
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
