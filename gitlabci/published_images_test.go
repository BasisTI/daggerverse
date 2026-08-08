package gitlabci

import (
	"strings"
	"testing"
)

func TestPublishedImagesComment(t *testing.T) {
	published := "reg/grupo/snf:2026.08.07.5\nreg/grupo/frontend:2026.08.07.5\n"
	got := PublishedImagesComment(published, "2026.08.07.5")

	for _, want := range []string{
		"reg/grupo/snf:2026.08.07.5",
		"reg/grupo/frontend:2026.08.07.5",
		"2026.08.07.5",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("comentário não contém %q:\n%s", want, got)
		}
	}
	// As refs vão em bloco de código: são longas e o GitLab as quebraria ou
	// tentaria transformá-las em link.
	if strings.Count(got, "```") != 2 {
		t.Errorf("esperava um bloco de código fechado, obteve:\n%s", got)
	}
	// A quebra final de `published` não pode virar linha vazia dentro do bloco.
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if last := len(lines) - 1; lines[last] != "```" || lines[last-1] == "" {
		t.Errorf("bloco de código mal fechado:\n%s", got)
	}
}
