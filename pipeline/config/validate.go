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

// validQualityTypes é o conjunto de tipos aceitos em `quality-type`: apenas os
// que têm build system capaz de rodar testes e análise estática. `dockerfile` e
// `custom` ficam de fora por definição — declarar um deles seria o mesmo que não
// declarar nada.
var validQualityTypes = map[TargetType]bool{
	TypeMaven: true,
	TypeNpm:   true,
	TypeUv:    true,
}

// Validate checa as invariantes do schema v1.
//
// Regras:
//   - schema-version deve ser 1;
//   - project.group não pode ser vazio;
//   - deve haver ao menos um target;
//   - todo target precisa de um type válido;
//   - quality-type, quando presente, precisa ser maven, npm ou uv, e exige sonar = true;
//   - sonar = true exige um tipo efetivo de quality com build system (ver EffectiveQualityType);
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

		if t.QualityType != "" {
			if !validQualityTypes[t.QualityType] {
				errs = append(errs, fmt.Sprintf(
					"target %q: quality-type %q inválido (válidos: maven, npm, uv) — "+
						"quality-type nomeia o build system que roda os testes e a análise estática do código",
					name, t.QualityType))
			}
			if !t.Sonar {
				errs = append(errs, fmt.Sprintf(
					"target %q: quality-type só faz sentido com sonar = true — "+
						"remova o campo ou ligue o sonar", name))
			}
		}

		if t.Sonar && c.EffectiveQualityType(name) == TypeDockerfile {
			errs = append(errs, fmt.Sprintf(
				"target %q: sonar = true em type = \"dockerfile\" exige quality-type — "+
					"o Dockerfile diz como a imagem é construída, não com que build system o código é "+
					"analisado; declare quality-type = \"maven\" | \"npm\" | \"uv\"", name))
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
