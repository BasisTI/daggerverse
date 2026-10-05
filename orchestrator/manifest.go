package main

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

// ManifestName é o arquivo, na raiz do diretório de cada target, em que o publish grava a identidade
// e o inventário do que exportou. A análise o lê, confere e o tira antes de restaurar os arquivos.
const ManifestName = "sonar-reuse-manifest.json"

// ManifestSchema muda quando o formato do manifesto muda de um jeito que o validador antigo não entende.
const ManifestSchema = 1

// ModuleInventory conta o que o build produziu num módulo, com o caminho relativo ao módulo do target
// ("." é o próprio módulo; "child" é um filho).
//
// A contagem, e não só a presença, é o que distingue um projeto sem testes de relatórios perdidos no
// transporte: sem teste, TestClasses e TestReports são zero nos dois lados da comparação; se o
// transporte perdeu os relatórios, TestReports cai e a conferência acusa.
type ModuleInventory struct {
	Path        string `json:"path"`
	Classes     int    `json:"classes"`
	TestClasses int    `json:"testClasses"`
	TestReports int    `json:"testReports"`
	Coverage    int    `json:"coverageXml"`
	Exec        int    `json:"coverageExec"`
}

// Manifest descreve o que o publish exportou para um target.
type Manifest struct {
	Schema    int               `json:"schema"`
	Target    string            `json:"target"`
	CommitSha string            `json:"commitSha"`
	Modules   []ModuleInventory `json:"modules"`
}

// newManifest monta o manifesto de um target a partir dos caminhos exportados.
func newManifest(target, commitSha string, paths []string) Manifest {
	return Manifest{Schema: ManifestSchema, Target: target, CommitSha: commitSha, Modules: inventoryFromPaths(paths)}
}

func (m Manifest) marshal() (string, error) {
	out, err := json.MarshalIndent(m, "", "  ")
	return string(out) + "\n", err
}

// splitTargetPath separa um caminho exportado em (módulo, resto dentro do target/). ok é falso para
// o que não está num target/.
func splitTargetPath(p string) (module, rest string, ok bool) {
	if after, found := strings.CutPrefix(p, "target/"); found {
		return ".", after, true
	}
	if idx := strings.Index(p, "/target/"); idx >= 0 {
		return p[:idx], p[idx+len("/target/"):], true
	}
	return "", "", false
}

// inventoryFromPaths conta o que há por módulo. A classificação é pelo nome do arquivo, e não pelo
// diretório, porque o pom pode mandar relatórios para qualquer lugar do target/.
func inventoryFromPaths(paths []string) []ModuleInventory {
	byModule := map[string]*ModuleInventory{}
	for _, p := range paths {
		if strings.HasSuffix(p, "/") || p == ManifestName {
			continue
		}
		module, rest, ok := splitTargetPath(p)
		if !ok {
			continue
		}
		base := path.Base(rest)
		inv := byModule[module]
		if inv == nil {
			inv = &ModuleInventory{Path: module}
			byModule[module] = inv
		}
		switch {
		case strings.HasPrefix(rest, "classes/") && strings.HasSuffix(base, ".class"):
			inv.Classes++
		case strings.HasPrefix(rest, "test-classes/") && strings.HasSuffix(base, ".class"):
			inv.TestClasses++
		case strings.HasPrefix(base, "TEST-") && strings.HasSuffix(base, ".xml"):
			inv.TestReports++
		case strings.HasPrefix(base, "jacoco") && strings.HasSuffix(base, ".xml"):
			inv.Coverage++
		case strings.HasPrefix(base, "jacoco") && strings.HasSuffix(base, ".exec"):
			inv.Exec++
		}
	}
	out := make([]ModuleInventory, 0, len(byModule))
	for _, inv := range byModule {
		out = append(out, *inv)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// reuseProblem diz por que o que foi exportado NÃO pode ser analisado no lugar do build, ou devolve
// "" quando pode. Nunca analisa com dado velho ou parcial: na dúvida, o motivo manda o target para o
// check completo.
//
// Confere, nesta ordem: o formato e o target do manifesto; o commit (relatório de outra revisão
// esconde uma falha do código atual); o inventário recontado sobre o que chegou (arquivo perdido
// no transporte); e a coerência do próprio build (há classes e nenhum relatório de teste).
func reuseProblem(m Manifest, target, commitSha string, current []ModuleInventory) string {
	if m.Schema != ManifestSchema {
		return fmt.Sprintf("manifesto com schema %d, esperado %d", m.Schema, ManifestSchema)
	}
	if m.Target != target {
		return fmt.Sprintf("manifesto é do target %q, não de %q", m.Target, target)
	}
	if commitSha == "" || m.CommitSha != commitSha {
		return fmt.Sprintf("relatórios do commit %s, análise do commit %s", short(m.CommitSha), short(commitSha))
	}
	if diff := inventoryDiff(m.Modules, current); diff != "" {
		return "o que chegou difere do que o publish exportou: " + diff
	}
	classes := 0
	for _, inv := range m.Modules {
		classes += inv.Classes
		if inv.TestClasses > 0 && inv.TestReports == 0 {
			return fmt.Sprintf("o módulo %q tem %d classes de teste e nenhum relatório de teste: os testes não rodaram ou os relatórios não foram gerados", inv.Path, inv.TestClasses)
		}
	}
	if classes == 0 {
		return "o publish não produziu nenhuma classe compilada"
	}
	return ""
}

func short(sha string) string {
	if sha == "" {
		return "(vazio)"
	}
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// inventoryDiff lista as diferenças entre o inventário do manifesto e o recontado, ou "" se iguais.
func inventoryDiff(want, got []ModuleInventory) string {
	index := func(list []ModuleInventory) map[string]ModuleInventory {
		m := make(map[string]ModuleInventory, len(list))
		for _, inv := range list {
			m[inv.Path] = inv
		}
		return m
	}
	w, g := index(want), index(got)
	var parts []string
	for _, inv := range want {
		other, present := g[inv.Path]
		if !present {
			parts = append(parts, fmt.Sprintf("módulo %q ausente", inv.Path))
		} else if other != inv {
			parts = append(parts, fmt.Sprintf("módulo %q: esperado %s, recebido %s", inv.Path, describe(inv), describe(other)))
		}
	}
	for _, inv := range got {
		if _, present := w[inv.Path]; !present {
			parts = append(parts, fmt.Sprintf("módulo %q não consta do manifesto", inv.Path))
		}
	}
	return strings.Join(parts, "; ")
}

func describe(inv ModuleInventory) string {
	return fmt.Sprintf("classes=%d testClasses=%d testReports=%d coverageXml=%d coverageExec=%d",
		inv.Classes, inv.TestClasses, inv.TestReports, inv.Coverage, inv.Exec)
}

// parseManifest lê o manifesto gravado pelo publish.
func parseManifest(raw string) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return Manifest{}, fmt.Errorf("manifesto ilegível: %w", err)
	}
	return m, nil
}
