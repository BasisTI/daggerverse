package gitlabci

import (
	"fmt"
	"strings"
)

// PublishedImagesComment monta o corpo Markdown do comentário com as imagens publicadas.
//
// Uma linha por imagem, em bloco de código: as referências são longas e o GitLab quebraria a
// linha no meio de uma tag, além de tentar transformar parte delas em link.
func PublishedImagesComment(published, version string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### 📦 Imagens publicadas — `%s`\n\n```\n", version))
	for _, image := range strings.Split(strings.TrimSpace(published), "\n") {
		if image = strings.TrimSpace(image); image != "" {
			sb.WriteString(image + "\n")
		}
	}
	sb.WriteString("```\n")
	return sb.String()
}

// ReportPublishedImages comenta na MR de origem do commit as imagens que acabaram de subir.
//
// Só há o que fazer se o publish produziu imagens e se o commit veio de uma MR: um push direto na
// branch não tem onde comentar. Falhas aqui são reportadas e engolidas -- o relatório é
// conveniência, e derrubar um publish que já terminou com sucesso porque um comentário não subiu
// seria trocar um problema pequeno por um grande.
func (c *Client) ReportPublishedImages(commitSha, published, version string) {
	if c == nil || strings.TrimSpace(published) == "" || commitSha == "" {
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

	body := PublishedImagesComment(published, version)
	for _, iid := range iids {
		if err := c.UpsertMergeRequestNote(iid, PublishedImagesMarker, body); err != nil {
			fmt.Printf("⚠️  [Relatório] falha ao comentar na MR !%d: %v\n", iid, err)
			continue
		}
		fmt.Printf("💬 [Relatório] imagens publicadas comentadas na MR !%d\n", iid)
	}
}
