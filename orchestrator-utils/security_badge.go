package main

import (
	"fmt"
	"html"
	"strings"
	"unicode/utf8"
)

// Cores do badge, as mesmas do shields.io.
const (
	badgeRed    = "#e05d44"
	badgeOrange = "#fe7d37"
	badgeYellow = "#dfb317"
	badgeGreen  = "#4c1"
	badgeGrey   = "#9f9f9f"
)

// securityBadge decide o texto e a cor do badge a partir do resumo.
//
// Falha sem relatório nenhum é cinza: não se sabe o estado das dependências, e "sem achados"
// seria mentira. Com relatório, a cor segue a pior severidade encontrada, mesmo que algum target
// tenha falhado -- a falha do build já aparece no job; o badge fala das CVEs.
func securityBadge(s *SecuritySummary) (message, color string) {
	if len(s.Reports) == 0 && s.Failed {
		return "falhou", badgeGrey
	}
	t := s.Totals
	var parts []string
	add := func(n int, singular, plural string) {
		if n == 1 {
			parts = append(parts, "1 "+singular)
		} else if n > 1 {
			parts = append(parts, fmt.Sprintf("%d %s", n, plural))
		}
	}
	switch {
	case t["CRITICAL"] > 0:
		add(t["CRITICAL"], "crítica", "críticas")
		add(t["HIGH"], "alta", "altas")
		return strings.Join(parts, " · "), badgeRed
	case t["HIGH"] > 0:
		add(t["HIGH"], "alta", "altas")
		return strings.Join(parts, " · "), badgeOrange
	case t["MEDIUM"]+t["LOW"]+t["OTHER"] > 0:
		add(t["MEDIUM"], "média", "médias")
		add(t["LOW"], "baixa", "baixas")
		add(t["OTHER"], "outra", "outras")
		return strings.Join(parts, " · "), badgeYellow
	}
	return "sem achados", badgeGreen
}

// badgeTextWidth estima a largura do texto em Verdana 11px, a fonte do estilo flat do shields.
// Não há como medir sem fonte; a média de 7px por caractere, mais folga para as largas, basta.
func badgeTextWidth(s string) int {
	w := 0
	for _, r := range s {
		switch {
		case strings.ContainsRune("il.·|: ", r):
			w += 4
		case strings.ContainsRune("mwMW", r):
			w += 10
		default:
			w += 7
		}
	}
	return w + utf8.RuneCountInString(s)/10
}

// renderSecurityBadge escreve o badge.svg no estilo flat do shields.io: autocontido, sem fonte
// nem imagem externa (a família cai para o que o navegador tiver).
func renderSecurityBadge(s *SecuritySummary) string {
	const label = "CVE"
	message, color := securityBadge(s)
	lw := badgeTextWidth(label) + 10
	mw := badgeTextWidth(message) + 10
	total := lw + mw
	msg := html.EscapeString(message)
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%[1]d" height="20" role="img" aria-label="%[4]s: %[5]s">
<title>%[4]s: %[5]s</title>
<linearGradient id="s" x2="0" y2="100%%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></linearGradient>
<clipPath id="r"><rect width="%[1]d" height="20" rx="3" fill="#fff"/></clipPath>
<g clip-path="url(#r)"><rect width="%[2]d" height="20" fill="#555"/><rect x="%[2]d" width="%[3]d" height="20" fill="%[6]s"/><rect width="%[1]d" height="20" fill="url(#s)"/></g>
<g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" font-size="11">
<text x="%[7]d" y="15" fill="#010101" fill-opacity=".3">%[4]s</text><text x="%[7]d" y="14">%[4]s</text>
<text x="%[8]d" y="15" fill="#010101" fill-opacity=".3">%[5]s</text><text x="%[8]d" y="14">%[5]s</text>
</g></svg>
`, total, lw, mw, label, msg, color, lw/2, lw+mw/2)
}
