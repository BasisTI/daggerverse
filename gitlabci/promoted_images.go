package gitlabci

import (
	"fmt"
	"strings"
)

// PromotedImagesComment monta o corpo Markdown do comentário com as imagens promovidas.
//
// Só as imagens que de fato foram copiadas entram na lista. As demais continuam apontando para a
// mesma imagem da promoção anterior, e listá-las junto afogaria o que realmente entrou nesta
// release -- numa promoção típica a maioria dos serviços não mudou. Elas viram uma linha de
// rodapé, que é o suficiente para o leitor saber que a promoção cobriu o projeto inteiro.
func PromotedImagesComment(promoted string, unchanged int) string {
	var sb strings.Builder
	sb.WriteString("### 🚀 Imagens promovidas para produção\n\n")

	// As refs vão em bloco de código: são longas e o GitLab quebraria a linha no meio de uma tag,
	// além de tentar transformar parte delas em link.
	refs := nonEmptyLines(promoted)
	if len(refs) > 0 {
		sb.WriteString("```\n")
		for _, ref := range refs {
			sb.WriteString(ref + "\n")
		}
		sb.WriteString("```\n")
	} else {
		sb.WriteString("Nenhuma imagem nova nesta promoção.\n")
	}

	if unchanged > 0 {
		sb.WriteString("\n" + unchangedNote(unchanged) + "\n")
	}
	return sb.String()
}

// unchangedNote redige o rodapé das imagens que já estavam em produção.
func unchangedNote(unchanged int) string {
	if unchanged == 1 {
		return "_1 imagem já estava em produção nesta versão._"
	}
	return fmt.Sprintf("_%d imagens já estavam em produção nesta versão._", unchanged)
}

// nonEmptyLines quebra o texto em linhas, descartando as vazias.
func nonEmptyLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// ReportPromotedImages comenta na MR de origem do commit as imagens que foram para produção.
//
// O job de promote roda no push para main, depois do merge, então não há CI_MERGE_REQUEST_IID no
// ambiente -- a MR de develop→main é descoberta a partir do commit de merge, do mesmo jeito que em
// ReportPublishedImages. Falhas aqui são reportadas e engolidas: as imagens já estão em produção, e
// derrubar o job porque um comentário não subiu deixaria a pipeline vermelha sem nada a corrigir.
func (c *Client) ReportPromotedImages(commitSha, promoted string, unchanged int) {
	if c == nil || commitSha == "" {
		return
	}
	if strings.TrimSpace(promoted) == "" && unchanged == 0 {
		return
	}

	iids, err := c.MergeRequestsForCommit(commitSha)
	if err != nil {
		fmt.Printf("⚠️  [Relatório] falha ao localizar a MR do commit %s: %v\n", commitSha, err)
		return
	}
	if len(iids) == 0 {
		fmt.Println("ℹ️  [Relatório] commit sem MR associada: nada a comentar.")
		return
	}

	body := PromotedImagesComment(promoted, unchanged)
	for _, iid := range iids {
		if err := c.UpsertMergeRequestNote(iid, PromotedImagesMarker, body); err != nil {
			fmt.Printf("⚠️  [Relatório] falha ao comentar na MR !%d: %v\n", iid, err)
			continue
		}
		fmt.Printf("💬 [Relatório] imagens promovidas comentadas na MR !%d\n", iid)
	}
}
