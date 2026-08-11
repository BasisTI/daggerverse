package gitlabci

import (
	"strings"
	"testing"
)

func TestPromotedImagesComment(t *testing.T) {
	promoted := "reg/grupo/lightdash-content:production-2026.08.10.351\nreg/grupo/beneficios:production-2026.08.10.351\n"
	got := PromotedImagesComment(promoted, 3)

	for _, want := range []string{
		"reg/grupo/lightdash-content:production-2026.08.10.351",
		"reg/grupo/beneficios:production-2026.08.10.351",
		"3 imagens já estavam em produção",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("comentário não contém %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "```") != 2 {
		t.Errorf("esperava um bloco de código fechado, obteve:\n%s", got)
	}
	// A quebra final de `promoted` não pode virar linha vazia dentro do bloco.
	block := strings.Split(got, "```")[1]
	for _, line := range strings.Split(strings.Trim(block, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			t.Errorf("linha vazia dentro do bloco de código:\n%s", got)
		}
	}
}

// Uma promoção em que nada mudou não pode render um bloco de código vazio: o
// comentário existe para dizer o que entrou em produção, e "``` ```" não diz nada.
func TestPromotedImagesCommentSemImagensNovas(t *testing.T) {
	got := PromotedImagesComment("", 4)

	if strings.Contains(got, "```") {
		t.Errorf("não deveria abrir bloco de código sem imagens:\n%s", got)
	}
	if !strings.Contains(got, "4 imagens já estavam em produção") {
		t.Errorf("comentário deveria dizer quantas já estavam em produção:\n%s", got)
	}
}

func TestPromotedImagesCommentSingular(t *testing.T) {
	got := PromotedImagesComment("reg/grupo/app:production-1", 1)

	if !strings.Contains(got, "1 imagem já estava em produção") {
		t.Errorf("esperava o rodapé no singular, obteve:\n%s", got)
	}
}

// Sem MR não há onde comentar, e um cliente nulo é o caso normal de um projeto
// que não configurou o token do GitLab. Nenhum dos dois pode entrar em pânico.
func TestReportPromotedImagesSemClienteOuCommit(t *testing.T) {
	var nilClient *Client
	nilClient.ReportPromotedImages("abc123", "reg/grupo/app:production-1", 0)

	client := &Client{BaseURL: "http://invalido.local", Token: "x", ProjectID: "1"}
	client.ReportPromotedImages("", "reg/grupo/app:production-1", 0)
	client.ReportPromotedImages("abc123", "", 0)
}
