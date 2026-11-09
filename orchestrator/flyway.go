package main

import (
	"context"
	"dagger/orchestrator/internal/dagger"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// flywayMigrationRe reconhece uma migração versionada do Flyway: V<versão>__<descrição>.sql.
//
// A versão é feita de dígitos separados por `.` ou `_` (o Flyway trata os dois como separador).
// Migração repetível (R__) e de desfazer (U__) não têm ordem de versão e ficam de fora.
var flywayMigrationRe = regexp.MustCompile(`^V(\d+(?:[._]\d+)*)__.+\.sql$`)

// flywayDirMarker identifica o diretório de migrações: o histórico principal fica em
// `db/migration` e o de cada módulo do Spring Modulith em `db/migration/<módulo>`.
const flywayDirMarker = "db/migration"

// flywayMigration é uma migração versionada já separada em histórico, versão e arquivo.
type flywayMigration struct {
	path    string
	dir     string
	version string
	parts   []string
}

// parseFlywayMigration devolve a migração versionada do path, ou false se o arquivo não é uma.
// Só vale o que está sob um diretório `db/migration`.
func parseFlywayMigration(p string) (flywayMigration, bool) {
	dir := path.Dir(p)
	if !strings.Contains("/"+dir+"/", "/"+flywayDirMarker+"/") {
		return flywayMigration{}, false
	}
	m := flywayMigrationRe.FindStringSubmatch(path.Base(p))
	if m == nil {
		return flywayMigration{}, false
	}
	return flywayMigration{
		path:    p,
		dir:     dir,
		version: m[1],
		parts:   strings.Split(strings.ReplaceAll(m[1], "_", "."), "."),
	}, true
}

// compareFlywayParts compara duas versões como o Flyway: parte a parte, numericamente, e a
// versão com mais partes vence quando as anteriores são iguais (1.1 > 1). Zeros à esquerda não
// pesam, e a comparação por texto (sem converter para inteiro) não estoura com 12 ou 14 dígitos.
func compareFlywayParts(a, b []string) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y string
		if i < len(a) {
			x = strings.TrimLeft(a[i], "0")
		}
		if i < len(b) {
			y = strings.TrimLeft(b[i], "0")
		}
		switch {
		case len(x) != len(y):
			if len(x) < len(y) {
				return -1
			}
			return 1
		case x != y:
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// checkFlywayMigrations devolve as violações das migrações novas da branch.
//
// existing são os paths do diretório de trabalho da branch-alvo; added são os arquivos que a
// branch acrescenta a ela. Cada diretório de migração é um histórico à parte. Uma migração nova
// reprova se a versão já existe no histórico (na branch-alvo ou em outra migração nova) ou se é
// menor que a última da branch-alvo: o Flyway recusa a primeira em qualquer ambiente e, na
// segunda, a ignora ou aborta conforme `outOfOrder`.
func checkFlywayMigrations(existing, added []string) []string {
	last := map[string]flywayMigration{}
	seen := map[string][]flywayMigration{}
	for _, p := range existing {
		if m, ok := parseFlywayMigration(p); ok {
			seen[m.dir] = append(seen[m.dir], m)
			if cur, has := last[m.dir]; !has || compareFlywayParts(m.parts, cur.parts) > 0 {
				last[m.dir] = m
			}
		}
	}

	var news []flywayMigration
	for _, p := range added {
		if m, ok := parseFlywayMigration(p); ok {
			news = append(news, m)
		}
	}
	sort.Slice(news, func(i, j int) bool { return news[i].path < news[j].path })

	var out []string
	for _, n := range news {
		var dup *flywayMigration
		for i, o := range seen[n.dir] {
			if o.path != n.path && compareFlywayParts(o.parts, n.parts) == 0 {
				dup = &seen[n.dir][i]
				break
			}
		}
		l, hasLast := last[n.dir]
		switch {
		case dup != nil:
			msg := fmt.Sprintf("%s: versão %s repetida (já usada por %s)", n.path, n.version, dup.path)
			if hasLast {
				msg += fmt.Sprintf("; última versão da branch-alvo: %s (%s)", l.version, l.path)
			}
			out = append(out, msg)
		case hasLast && compareFlywayParts(n.parts, l.parts) < 0:
			out = append(out, fmt.Sprintf("%s: versão %s menor que a última da branch-alvo, %s (%s)",
				n.path, n.version, l.version, l.path))
		}
		seen[n.dir] = append(seen[n.dir], n)
	}
	return out
}

// CheckFlywayMigrations reprova a merge request que traz migração Flyway nova com versão repetida
// ou menor que a última da branch-alvo.
//
// O sufixo da US na versão evita a colisão de números, mas não a ordem de merge: duas branches
// podem escolher versões distintas e a que entra por último ficar atrás da outra. A comparação é
// entre os arquivos acrescentados pela branch (`git diff --diff-filter=A`) e os da branch-alvo,
// por diretório de migração -- o histórico principal e o de cada módulo do Spring Modulith são
// históricos à parte. Projeto sem migração versionada não é afetado.
//
// Não faz chamada de rede e não depende do pipeline.toml.
func (o *Orchestrator) CheckFlywayMigrations(
	ctx context.Context,
	// Branch-alvo da comparação (ex: "origin/develop").
	// +optional
	// +default="origin/develop"
	baseBranch string,
) (string, error) {
	ctr := dag.Container().From("alpine/git:v2.54.0").
		WithWorkdir("/src").WithDirectory("/src", o.Source).
		WithExec([]string{"git", "config", "--global", "--add", "safe.directory", "/src"})

	existing, err := gitLines(ctx, ctr, "ls-tree", "-r", "--name-only", baseBranch)
	if err != nil {
		return "", fmt.Errorf("não foi possível listar os arquivos de %s: %w", baseBranch, err)
	}
	added, err := gitLines(ctx, ctr, "diff", "--no-renames", "--name-only", "--diff-filter=A", baseBranch+"...HEAD")
	if err != nil {
		return "", fmt.Errorf("não foi possível listar os arquivos novos da branch: %w", err)
	}

	violations := checkFlywayMigrations(existing, added)
	if len(violations) > 0 {
		return "", fmt.Errorf("migração Flyway fora de ordem:\n  %s", strings.Join(violations, "\n  "))
	}
	return "Migrações Flyway em ordem.", nil
}

// gitLines roda um comando git no container e devolve as linhas não vazias da saída.
func gitLines(ctx context.Context, ctr *dagger.Container, args ...string) ([]string, error) {
	out, err := ctr.WithExec(append([]string{"git"}, args...)).Stdout(ctx)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}
