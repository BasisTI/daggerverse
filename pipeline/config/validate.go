package config

import (
	"fmt"
	"strings"
)

// validTypes é o conjunto de tipos de target aceitos.
var validTypes = map[TargetType]bool{
	TypeMaven:      true,
	TypeNpm:        true,
	TypeUv:         true,
	TypeDockerfile: true,
	TypeCustom:     true,
}

// Validate checa as invariantes do schema v1.
//
// Regras:
//   - schema-version deve ser 1;
//   - project.group não pode ser vazio;
//   - deve haver ao menos um target;
//   - todo target precisa de um type válido;
//   - reactor = true exige module não vazio;
//   - os nomes de imagem efetivos devem ser únicos entre os targets.
func (c *Config) Validate() error {
	var errs []string

	if c.SchemaVersion != SchemaVersion {
		errs = append(errs, fmt.Sprintf("schema-version inválido: %d (esperado %d)", c.SchemaVersion, SchemaVersion))
	}

	if strings.TrimSpace(c.Project.Group) == "" {
		errs = append(errs, "project.group é obrigatório")
	}

	if len(c.Targets) == 0 {
		errs = append(errs, "é necessário ao menos um target em [targets.<nome>]")
	}

	seenImages := make(map[string]string, len(c.Targets))
	for _, name := range c.TargetNames() {
		t := c.Targets[name]

		if strings.TrimSpace(name) == "" {
			errs = append(errs, "nome de target vazio")
			continue
		}

		switch {
		case t.Type == "":
			errs = append(errs, fmt.Sprintf("target %q: type é obrigatório", name))
		case !validTypes[t.Type]:
			errs = append(errs, fmt.Sprintf("target %q: type %q inválido (válidos: maven, npm, uv, dockerfile, custom)", name, t.Type))
		}

		if t.Reactor && strings.TrimSpace(t.Module) == "" {
			errs = append(errs, fmt.Sprintf("target %q: reactor = true exige module não vazio", name))
		}

		img := c.EffectiveImage(name)
		if prev, dup := seenImages[img]; dup {
			errs = append(errs, fmt.Sprintf("imagem %q duplicada entre os targets %q e %q", img, prev, name))
			continue
		}
		seenImages[img] = name
	}

	if len(errs) > 0 {
		return fmt.Errorf("configuração de pipeline inválida:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}
