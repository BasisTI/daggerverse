package main

import (
	"dagger/orchestrator/internal/dagger"
	"fmt"
	"strings"

	"github.com/BasisTI/daggerverse/pipeline"
	"github.com/BasisTI/daggerverse/pipeline/config"
)

// buildTarget e qualityTarget são os tipos concretos da lib pipeline neste módulo.
type buildTarget = pipeline.BuildTarget[*dagger.Directory, *dagger.Secret]
type qualityTarget = pipeline.QualityTarget[*dagger.Directory, *dagger.Secret]

// buildTargets converte a configuração declarativa no mapa de BuildTargets
// consumido por pipeline.PublishAll.
//
// Targets custom não têm estratégia e são rejeitados antes daqui, por
// errCustomTargets.
func buildTargets(cfg *config.Config) (map[string]buildTarget, error) {
	targets := make(map[string]buildTarget, len(cfg.Targets))
	for _, rt := range cfg.ResolveAll() {
		if rt.Type == config.TypeCustom {
			continue
		}
		build, err := buildStrategy(rt, cfg.Project.Group)
		if err != nil {
			return nil, err
		}
		targets[rt.Name] = buildTarget{
			Build:             build,
			Path:              rt.Path,
			MountPath:         rt.SourcePath,
			VersionFile:       rt.VersionFile,
			RootVersionFile:   rt.RootVersionFile,
			ExtraTriggerPaths: rt.ExtraTriggerPaths,
		}
	}
	return targets, nil
}

// qualityTargets converte os targets com `sonar = true` no mapa de
// QualityTargets consumido por pipeline.CheckQuality.
func qualityTargets(cfg *config.Config, sonarExtra []string) (map[string]qualityTarget, error) {
	targets := make(map[string]qualityTarget, len(cfg.Targets))
	for _, name := range cfg.SonarTargetNames() {
		rt, err := cfg.Resolve(name)
		if err != nil {
			return nil, err
		}
		if rt.Type == config.TypeCustom {
			continue
		}
		check, err := qualityStrategy(rt, sonarExtra)
		if err != nil {
			return nil, err
		}
		targets[rt.Name] = qualityTarget{
			Check:             check,
			Path:              rt.Path,
			MountPath:         rt.SourcePath,
			ExtraTriggerPaths: rt.ExtraTriggerPaths,
		}
	}
	return targets, nil
}

// customTargetNames lista, em ordem alfabética, os targets type = "custom".
func customTargetNames(cfg *config.Config) []string {
	return cfg.TargetNamesByType(config.TypeCustom)
}

// errCustomTargets devolve um erro explicativo quando a configuração declara
// targets custom, que o orchestrator genérico não sabe construir.
//
// Só publish-all e check-quality falham: check-images e promote operam sobre o
// mapa de imagens completo e continuam válidos para targets custom.
func errCustomTargets(cfg *config.Config, function string) error {
	if !cfg.HasCustomTargets() {
		return nil
	}
	return fmt.Errorf(
		"%s não pode rodar: os targets %s são type = \"custom\", e o orchestrator genérico não sabe construí-los.\n"+
			"Targets custom exigem um módulo Dagger próprio no projeto — implemente as estratégias de build lá e\n"+
			"aponte DAGGER_MODULE para esse módulo no .gitlab-ci.yml. O arquivo declarativo continua sendo a fonte\n"+
			"de verdade: check-images e promote seguem funcionando pelo módulo genérico, inclusive para as imagens\n"+
			"desses targets custom.",
		function, strings.Join(customTargetNames(cfg), ", "))
}

// versionFilePath devolve o arquivo de versão do target relativo à raiz do
// repositório, exatamente como pipeline.PublishAll o calcula — é o que o
// relatório do Validate mostra como "seria bumpado".
func versionFilePath(rt config.ResolvedTarget) string {
	return rt.VersionFilePath()
}
