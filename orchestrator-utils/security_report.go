package main

import (
	"context"
	"dagger/orchestrator-utils/internal/dagger"
	"encoding/json"
	"fmt"
	"html"
	"path"
	"sort"
	"strconv"
	"strings"
)

// dependencyCheckJSON é o nome do relatório JSON do OWASP Dependency-Check; o HTML tem o mesmo
// nome com extensão .html, no mesmo diretório.
const dependencyCheckJSON = "dependency-check-report.json"

// dependencyCheckJUnit é o relatório JUnit, gravado no mesmo diretório com nome próprio.
const dependencyCheckJUnit = "dependency-check-junit.xml"

// Severidades contadas no índice, da mais grave para a menos. O que não casa com nenhuma delas
// (UNKNOWN, INFO, vazio) vai para OTHER.
var severities = []string{"CRITICAL", "HIGH", "MEDIUM", "LOW"}

// SecurityReport é uma linha do índice: um relatório do Dependency-Check que não veio vazio.
type SecurityReport struct {
	// Primeiro componente do caminho: o target do pipeline.toml.
	Target string `json:"target"`
	// projectInfo.name do relatório.
	Module string `json:"module"`
	// Caminho do HTML relativo à raiz do diretório.
	Report string `json:"report"`
	// Dependências analisadas.
	Dependencies int `json:"dependencies"`
	// Dependências com pelo menos uma vulnerabilidade.
	VulnerableDependencies int `json:"vulnerableDependencies"`
	// Vulnerabilidades por severidade normalizada: CRITICAL, HIGH, MEDIUM, LOW, OTHER. As
	// chaves estão sempre presentes.
	Vulnerabilities map[string]int `json:"vulnerabilities"`
}

// SecurityTarget é o desfecho de um target, lido do <target>/exit-code.
type SecurityTarget struct {
	Name string `json:"name"`
	// -1 quando o target não tem exit-code.
	ExitCode int  `json:"exitCode"`
	Failed   bool `json:"failed"`
}

// SecuritySummary é o conteúdo do summary.json. O formato é contrato: mudar nome de campo
// quebra quem publica métricas a partir dele.
type SecuritySummary struct {
	Version int              `json:"version"`
	Failed  bool             `json:"failed"`
	Targets []SecurityTarget `json:"targets"`
	Reports []SecurityReport `json:"reports"`
	Totals  map[string]int   `json:"vulnerabilities"`
	Skipped []string         `json:"skippedEmptyReports"`
}

// SecurityReportIndex arruma o diretório devolvido pelo security-check: descarta os relatórios
// vazios e escreve na raiz um index.html, um summary.json e um badge.svg.
//
// Vive aqui, e não no orchestrator, para que o orchestrator local de um projeto
// (DAGGER_MODULE: ".") que implemente o mesmo contrato chame a mesma função em vez de
// duplicá-la: ele monta o diretório com <target>/exit-code e os relatórios e passa por aqui.
//
// Relatório vazio é o de um módulo sem dependências -- na prática o pom pai ou agregador, que
// o -am põe no reactor. Ele ficava em <target>/target/, o primeiro caminho que se vê ao
// navegar, e parecia dizer que o target não tinha nada. O HTML, o JSON e o JUnit dele saem do
// diretório -- o JUnit também porque viraria uma suíte vazia na aba Tests; o caminho dos demais, o exit-code e o FAILED ficam como estão.
func (u *OrchestratorUtils) SecurityReportIndex(
	ctx context.Context,
	// Diretório no formato do security-check: <target>/exit-code,
	// <target>/**/dependency-check-report.{html,json} e FAILED opcional.
	reports *dagger.Directory,
) (*dagger.Directory, error) {
	jsonPaths, err := reports.Glob(ctx, "**/"+dependencyCheckJSON)
	if err != nil {
		return nil, fmt.Errorf("procurando relatórios: %w", err)
	}
	contents := make(map[string][]byte, len(jsonPaths))
	for _, p := range jsonPaths {
		c, err := reports.File(p).Contents(ctx)
		if err != nil {
			return nil, fmt.Errorf("lendo %s: %w", p, err)
		}
		contents[p] = []byte(c)
	}
	exitPaths, err := reports.Glob(ctx, "*/exit-code")
	if err != nil {
		return nil, fmt.Errorf("procurando exit-code: %w", err)
	}
	exitCodes := make(map[string]string, len(exitPaths))
	for _, p := range exitPaths {
		c, err := reports.File(p).Contents(ctx)
		if err != nil {
			return nil, fmt.Errorf("lendo %s: %w", p, err)
		}
		exitCodes[path.Dir(p)] = c
	}

	summary, err := buildSecuritySummary(contents, exitCodes)
	if err != nil {
		return nil, err
	}
	drop := skippedReportFiles(summary.Skipped)
	summaryJSON, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return nil, err
	}
	return reports.WithoutFiles(drop).
		WithNewFile("summary.json", string(summaryJSON)+"\n").
		WithNewFile("index.html", renderSecurityIndex(summary)).
		WithNewFile("badge.svg", renderSecurityBadge(summary)), nil
}

// skippedReportFiles lista os arquivos a remover para cada relatório vazio: o JSON, o HTML e o
// JUnit do mesmo diretório.
func skippedReportFiles(skipped []string) []string {
	var drop []string
	for _, p := range skipped {
		drop = append(drop, p, htmlReportPath(p), path.Join(path.Dir(p), dependencyCheckJUnit))
	}
	return drop
}

func htmlReportPath(jsonPath string) string {
	return strings.TrimSuffix(jsonPath, ".json") + ".html"
}

// dependencyCheckReport é o pedaço do JSON do Dependency-Check que o índice lê.
type dependencyCheckReport struct {
	ProjectInfo struct {
		Name string `json:"name"`
	} `json:"projectInfo"`
	Dependencies []struct {
		Vulnerabilities []struct {
			Severity string `json:"severity"`
		} `json:"vulnerabilities"`
	} `json:"dependencies"`
}

// normalizeSeverity junta as grafias que o JSON traz: o NVD escreve HIGH, o OSS Index e o
// GitHub Advisory escrevem high e moderate.
func normalizeSeverity(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "MODERATE" {
		s = "MEDIUM"
	}
	for _, known := range severities {
		if s == known {
			return s
		}
	}
	return "OTHER"
}

func emptyCounts() map[string]int {
	m := map[string]int{"OTHER": 0}
	for _, s := range severities {
		m[s] = 0
	}
	return m
}

// buildSecuritySummary monta o resumo a partir dos JSONs (caminho -> conteúdo) e dos exit-code
// (target -> conteúdo). Relatório sem dependências vai para Skipped.
func buildSecuritySummary(reports map[string][]byte, exitCodes map[string]string) (*SecuritySummary, error) {
	s := &SecuritySummary{Version: 1, Totals: emptyCounts(), Targets: []SecurityTarget{}, Reports: []SecurityReport{}, Skipped: []string{}}
	targetNames := map[string]bool{}
	for t := range exitCodes {
		targetNames[t] = true
	}
	for p, content := range reports {
		var r dependencyCheckReport
		if err := json.Unmarshal(content, &r); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if len(r.Dependencies) == 0 {
			s.Skipped = append(s.Skipped, p)
			continue
		}
		target, _, _ := strings.Cut(p, "/")
		targetNames[target] = true
		row := SecurityReport{Target: target, Module: r.ProjectInfo.Name, Report: htmlReportPath(p),
			Dependencies: len(r.Dependencies), Vulnerabilities: emptyCounts()}
		for _, d := range r.Dependencies {
			if len(d.Vulnerabilities) > 0 {
				row.VulnerableDependencies++
			}
			for _, v := range d.Vulnerabilities {
				sev := normalizeSeverity(v.Severity)
				row.Vulnerabilities[sev]++
				s.Totals[sev]++
			}
		}
		s.Reports = append(s.Reports, row)
	}
	for name := range targetNames {
		t := SecurityTarget{Name: name, ExitCode: -1}
		if c, ok := exitCodes[name]; ok {
			code, err := strconv.Atoi(strings.TrimSpace(c))
			if err != nil {
				return nil, fmt.Errorf("%s/exit-code: %w", name, err)
			}
			t.ExitCode = code
			t.Failed = code != 0
		}
		s.Failed = s.Failed || t.Failed
		s.Targets = append(s.Targets, t)
	}
	sort.Slice(s.Targets, func(i, j int) bool { return s.Targets[i].Name < s.Targets[j].Name })
	sort.Slice(s.Reports, func(i, j int) bool {
		if s.Reports[i].Target != s.Reports[j].Target {
			return s.Reports[i].Target < s.Reports[j].Target
		}
		return s.Reports[i].Report < s.Reports[j].Report
	})
	sort.Strings(s.Skipped)
	return s, nil
}

// renderSecurityIndex escreve o index.html: autocontido, sem script nem asset externo, para
// abrir pelo navegador de artefatos do GitLab.
func renderSecurityIndex(s *SecuritySummary) string {
	status := map[string]SecurityTarget{}
	for _, t := range s.Targets {
		status[t.Name] = t
	}
	var b strings.Builder
	b.WriteString(`<!doctype html>
<html lang="pt-BR"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Security check</title>
<style>
body{font-family:system-ui,sans-serif;margin:1.5rem;color:#1f2328}
table{border-collapse:collapse}
th,td{border:1px solid #d0d7de;padding:.35rem .6rem;text-align:left}
td.n{text-align:right;font-variant-numeric:tabular-nums}
th{background:#f6f8fa}
.fail{color:#cf222e;font-weight:600}.ok{color:#1a7f37}
.crit{background:#ffebe9;font-weight:600}.zero{color:#8c959f}
</style></head><body>
<h1>Security check</h1>
`)
	if s.Failed {
		b.WriteString(`<p class="fail">Algum target falhou.</p>` + "\n")
	} else {
		b.WriteString(`<p class="ok">Todos os targets passaram.</p>` + "\n")
	}
	b.WriteString("<table><thead><tr><th>Target</th><th>Módulo</th><th>Dependências</th><th>Vulneráveis</th>")
	for _, sev := range severities {
		b.WriteString("<th>" + sev + "</th>")
	}
	b.WriteString("<th>Status</th><th>Relatório</th></tr></thead><tbody>\n")
	cell := func(n int, sev string) string {
		class := "n"
		if n == 0 {
			class += " zero"
		} else if sev == "CRITICAL" || sev == "HIGH" {
			class += " crit"
		}
		return fmt.Sprintf(`<td class="%s">%d</td>`, class, n)
	}
	rowTargets := map[string]bool{}
	for _, r := range s.Reports {
		rowTargets[r.Target] = true
		b.WriteString("<tr><td>" + html.EscapeString(r.Target) + "</td><td>" + html.EscapeString(r.Module) + "</td>")
		b.WriteString(cell(r.Dependencies, "") + cell(r.VulnerableDependencies, ""))
		for _, sev := range severities {
			b.WriteString(cell(r.Vulnerabilities[sev], sev))
		}
		b.WriteString(statusCell(status[r.Target]))
		b.WriteString(`<td><a href="` + html.EscapeString(r.Report) + `">HTML</a></td></tr>` + "\n")
	}
	// Target sem relatório nenhum (o build quebrou antes do plugin, ou só havia o do pom pai)
	// ainda precisa aparecer, senão uma falha some do índice.
	for _, t := range s.Targets {
		if rowTargets[t.Name] {
			continue
		}
		fmt.Fprintf(&b, `<tr><td>%s</td><td colspan="%d" class="zero">sem relatório</td>%s<td></td></tr>`+"\n",
			html.EscapeString(t.Name), 2+len(severities)+1, statusCell(t))
	}
	b.WriteString("</tbody></table>\n")
	if len(s.Skipped) > 0 {
		fmt.Fprintf(&b, "<p class=\"zero\">%d relatório(s) sem dependências omitido(s) (pom pai ou agregador).</p>\n", len(s.Skipped))
	}
	b.WriteString(`<p><a href="summary.json">summary.json</a></p>` + "\n</body></html>\n")
	return b.String()
}

func statusCell(t SecurityTarget) string {
	switch {
	case t.ExitCode == -1:
		return `<td class="zero">sem exit-code</td>`
	case t.Failed:
		return fmt.Sprintf(`<td class="fail">falhou (exit %d)</td>`, t.ExitCode)
	default:
		return `<td class="ok">ok</td>`
	}
}
