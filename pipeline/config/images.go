package config

import "encoding/json"

// ProjectImages é a REGRA CENTRAL ANTI-DRIFT do schema.
//
// Retorna o mapa `"<group>/<image efetiva>" -> "<paths que disparam o build>"`
// derivado de TODOS os targets — inclusive os type = "custom", que não são
// buildados pelo orchestrator genérico mas continuam existindo no registry.
//
// É exatamente esse mapa que CheckImages/Promote consomem, então é impossível
// divergir do conjunto de imagens realmente produzido pelo build.
//
// O valor é a lista COMPLETA de trigger paths, e não só o path primário: quem
// consome resolve a imagem pelo último commit que a reconstruiu, e um target
// com extra-trigger-paths é reconstruído por commits que não tocam o próprio
// path. Ver TriggerPaths.
func (c *Config) ProjectImages() map[string][]string {
	images := make(map[string][]string, len(c.Targets))
	for _, name := range c.TargetNames() {
		images[c.ImagePath(name)] = c.TriggerPaths(name)
	}
	return images
}

// TriggerPaths retorna todos os paths que disparam o build do target: o path
// efetivo seguido dos extra-trigger-paths, sem repetição.
//
// Manter os dois juntos é o que impede check-images e promote de resolverem a
// imagem por um commit velho. `lightdash-content` é o caso que motivou isto:
// path = "lightdash", mas a imagem embute `rh-dp/dbtrh` e
// `financeiro/dbt_financeiro`, então um commit em qualquer projeto dbt a
// reconstrói. Olhando só para "lightdash", o promote encontrava a tag `sha-`
// de um build anterior e promovia uma versão defasada.
func (c *Config) TriggerPaths(name string) []string {
	paths := []string{c.EffectivePath(name)}
	seen := map[string]bool{paths[0]: true}
	for _, extra := range c.Targets[name].ExtraTriggerPaths {
		if extra == "" || seen[extra] {
			continue
		}
		seen[extra] = true
		paths = append(paths, extra)
	}
	return paths
}

// ImagePath retorna "<group>/<image efetiva>" de um target — o caminho da
// imagem dentro do registry, sem host e sem tag.
func (c *Config) ImagePath(name string) string {
	return c.Project.Group + "/" + c.EffectiveImage(name)
}

// ProjectImagesJSON serializa ProjectImages no formato aceito pelo parâmetro
// projectImagesJson do orchestrator-utils.
func (c *Config) ProjectImagesJSON() (string, error) {
	data, err := json.Marshal(c.ProjectImages())
	if err != nil {
		return "", err
	}
	return string(data), nil
}
