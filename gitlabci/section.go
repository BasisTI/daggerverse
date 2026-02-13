package gitlabci

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var invalidChars = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// SanitizeID converte partes em um ID válido para seção GitLab CI.
// Ex: SanitizeID("portal-web", "Install Dependencies") → "portal-web_install_dependencies"
func SanitizeID(parts ...string) string {
	joined := strings.Join(parts, "_")
	joined = strings.ReplaceAll(joined, " ", "_")
	joined = invalidChars.ReplaceAllString(joined, "")
	return strings.ToLower(joined)
}

// SectionStart imprime o marcador de início de seção.
func SectionStart(id, title string) {
	ts := time.Now().Unix()
	fmt.Printf("\033[0Ksection_start:%d:%s\r\033[0K%s\n", ts, id, title)
}

// SectionEnd imprime o marcador de fim de seção.
func SectionEnd(id string) {
	ts := time.Now().Unix()
	fmt.Printf("\033[0Ksection_end:%d:%s\r\033[0K\n", ts, id)
}
