package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// O pom pai entra no reactor pelo -am e gera um relatório com zero dependências.
const emptyReport = `{"projectInfo":{"name":"triagem-parent"},"dependencies":[]}`

// Severidades nas três grafias que aparecem de verdade: NVD (HIGH), OSS Index/GHSA (high, moderate).
const mixedReport = `{"projectInfo":{"name":"triagem-core"},"dependencies":[
 {"fileName":"a.jar","vulnerabilities":[{"severity":"CRITICAL"},{"severity":"high"},{"severity":"moderate"}]},
 {"fileName":"b.jar","vulnerabilities":[{"severity":"HIGH"},{"severity":"Low"},{"severity":"UNKNOWN"}]},
 {"fileName":"c.jar"}
]}`

const cleanReport = `{"projectInfo":{"name":"api"},"dependencies":[{"fileName":"d.jar","vulnerabilities":[]}]}`

func sampleSummary(t *testing.T) *SecuritySummary {
	t.Helper()
	s, err := buildSecuritySummary(map[string][]byte{
		"triagem-core/target/dependency-check-report.json":              []byte(emptyReport),
		"triagem-core/triagem-core/target/dependency-check-report.json": []byte(mixedReport),
		"api/target/dependency-check-report.json":                       []byte(cleanReport),
	}, map[string]string{"triagem-core": "1\n", "api": "0\n", "web": "1\n"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSecuritySummarySkipsEmptyReports(t *testing.T) {
	s := sampleSummary(t)
	if len(s.Skipped) != 1 || s.Skipped[0] != "triagem-core/target/dependency-check-report.json" {
		t.Errorf("Skipped = %v", s.Skipped)
	}
	if len(s.Reports) != 2 {
		t.Fatalf("Reports = %+v", s.Reports)
	}
}

func TestSecuritySummaryCountsSeverities(t *testing.T) {
	s := sampleSummary(t)
	r := s.Reports[1]
	if r.Target != "triagem-core" || r.Module != "triagem-core" ||
		r.Report != "triagem-core/triagem-core/target/dependency-check-report.html" {
		t.Errorf("linha errada: %+v", r)
	}
	if r.Dependencies != 3 || r.VulnerableDependencies != 2 {
		t.Errorf("dependências = %d, vulneráveis = %d", r.Dependencies, r.VulnerableDependencies)
	}
	want := map[string]int{"CRITICAL": 1, "HIGH": 2, "MEDIUM": 1, "LOW": 1, "OTHER": 1}
	for k, v := range want {
		if r.Vulnerabilities[k] != v || s.Totals[k] != v {
			t.Errorf("%s: linha %d, total %d, esperado %d", k, r.Vulnerabilities[k], s.Totals[k], v)
		}
	}
	if clean := s.Reports[0]; clean.Target != "api" || clean.VulnerableDependencies != 0 || clean.Vulnerabilities["CRITICAL"] != 0 {
		t.Errorf("relatório limpo: %+v", clean)
	}
}

func TestSecuritySummaryTargetStatus(t *testing.T) {
	s := sampleSummary(t)
	if !s.Failed {
		t.Error("Failed deveria ser true com um exit-code 1")
	}
	got := map[string]SecurityTarget{}
	for _, tg := range s.Targets {
		got[tg.Name] = tg
	}
	if got["api"].Failed || got["api"].ExitCode != 0 {
		t.Errorf("api: %+v", got["api"])
	}
	if !got["triagem-core"].Failed || got["triagem-core"].ExitCode != 1 {
		t.Errorf("triagem-core: %+v", got["triagem-core"])
	}
	if !got["web"].Failed {
		t.Errorf("web, sem relatório, precisa aparecer como falho: %+v", got["web"])
	}
}

// O summary.json alimenta métricas: os nomes dos campos são contrato.
func TestSecuritySummaryJSONShape(t *testing.T) {
	b, err := json.Marshal(sampleSummary(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"version":1`, `"failed":true`, `"targets"`, `"reports"`, `"vulnerabilities"`,
		`"skippedEmptyReports"`, `"vulnerableDependencies"`, `"exitCode"`, `"CRITICAL"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("summary.json sem %s: %s", key, b)
		}
	}
}

func TestSecurityIndexHTML(t *testing.T) {
	h := renderSecurityIndex(sampleSummary(t))
	for _, want := range []string{
		`href="triagem-core/triagem-core/target/dependency-check-report.html"`,
		`href="api/target/dependency-check-report.html"`,
		"falhou (exit 1)", "sem relatório", "1 relatório(s) sem dependências",
	} {
		if !strings.Contains(h, want) {
			t.Errorf("index.html sem %q", want)
		}
	}
	if strings.Contains(h, `href="triagem-core/target/`) {
		t.Error("index.html não deveria linkar o relatório vazio")
	}
	for _, external := range []string{"<script", "http://", "https://", "<link"} {
		if strings.Contains(h, external) {
			t.Errorf("index.html deveria ser autocontido, tem %q", external)
		}
	}
}

func TestSecuritySummaryRejectsBadJSON(t *testing.T) {
	if _, err := buildSecuritySummary(map[string][]byte{"x/target/dependency-check-report.json": []byte("{")}, nil); err == nil {
		t.Error("JSON inválido deveria dar erro")
	}
}

func TestSkippedReportFilesIncludesJUnit(t *testing.T) {
	got := skippedReportFiles([]string{"triagem-core/target/dependency-check-report.json"})
	want := []string{
		"triagem-core/target/dependency-check-report.json",
		"triagem-core/target/dependency-check-report.html",
		"triagem-core/target/dependency-check-junit.xml",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("= %v, esperado %v", got, want)
	}
}
