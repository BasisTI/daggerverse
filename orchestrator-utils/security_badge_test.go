package main

import (
	"encoding/xml"
	"strings"
	"testing"
)

func summaryWith(failed bool, reports int, totals map[string]int) *SecuritySummary {
	s := &SecuritySummary{Failed: failed, Totals: emptyCounts()}
	for i := 0; i < reports; i++ {
		s.Reports = append(s.Reports, SecurityReport{})
	}
	for k, v := range totals {
		s.Totals[k] = v
	}
	return s
}

func TestSecurityBadge(t *testing.T) {
	cases := []struct {
		name       string
		s          *SecuritySummary
		msg, color string
	}{
		{"crítica", summaryWith(true, 2, map[string]int{"CRITICAL": 40, "HIGH": 102, "MEDIUM": 3}), "40 críticas · 102 altas", badgeRed},
		{"só alta", summaryWith(false, 1, map[string]int{"HIGH": 1, "LOW": 5}), "1 alta", badgeOrange},
		{"sem achados", summaryWith(false, 1, nil), "sem achados", badgeGreen},
		{"falhou sem relatório", summaryWith(true, 0, nil), "falhou", badgeGrey},
		{"só média e baixa", summaryWith(false, 1, map[string]int{"MEDIUM": 2, "LOW": 1}), "2 médias · 1 baixa", badgeYellow},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg, color := securityBadge(c.s)
			if msg != c.msg || color != c.color {
				t.Errorf("= %q %s, esperado %q %s", msg, color, c.msg, c.color)
			}
			svg := renderSecurityBadge(c.s)
			if err := xml.Unmarshal([]byte(svg), new(struct{})); err != nil {
				t.Fatalf("SVG inválido: %v\n%s", err, svg)
			}
			for _, want := range []string{">CVE<", ">" + c.msg + "<", `fill="` + c.color + `"`} {
				if !strings.Contains(svg, want) {
					t.Errorf("SVG sem %q", want)
				}
			}
			if strings.Contains(svg, "@font-face") || strings.Contains(svg, "href=") {
				t.Errorf("SVG referencia recurso externo")
			}
		})
	}
}
