package config

import "encoding/json"

// ProjectImages é a REGRA CENTRAL ANTI-DRIFT do schema.
//
// Retorna o mapa `"<group>/<image efetiva>" -> "<path efetivo>"` derivado de
// TODOS os targets — inclusive os type = "custom", que não são buildados pelo
// orchestrator genérico mas continuam existindo no registry.
//
// É exatamente esse mapa que CheckImages/Promote consomem, então é impossível
// divergir do conjunto de imagens realmente produzido pelo build.
func (c *Config) ProjectImages() map[string]string {
	images := make(map[string]string, len(c.Targets))
	for _, name := range c.TargetNames() {
		images[c.ImagePath(name)] = c.EffectivePath(name)
	}
	return images
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
