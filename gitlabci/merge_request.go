package gitlabci

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Marcadores das notas escritas por nós: comentários HTML invisíveis que identificam a nota.
//
// São o que permite EDITAR a nota existente em vez de empilhar uma nova a cada execução, do mesmo
// jeito que o SonarQube faz: reexecutar o job atualiza o mesmo comentário.
//
// Um marcador por ASSUNTO. Uma MR recebe notas de origens diferentes -- as imagens publicadas no
// merge, o relatório de qualidade a cada push -- e elas convivem: com marcador único a segunda
// sobrescreveria a primeira.
const (
	PublishedImagesMarker = "<!-- daggerverse:published-images -->"
	PromotedImagesMarker  = "<!-- daggerverse:promoted-images -->"
	QualityReportMarker   = "<!-- daggerverse:quality-report -->"
)

// MergeRequestsForCommit lista os IIDs das merge requests associadas a um commit.
//
// GET /projects/:id/repository/commits/:sha/merge_requests
//
// O job de publish roda num push para develop, já depois do merge, então não há
// CI_MERGE_REQUEST_IID no ambiente -- a MR precisa ser descoberta a partir do commit. O GitLab
// casa tanto pelo merge commit quanto pelo commit de squash. Um push direto na branch, sem MR
// nenhuma, devolve lista vazia, e aí não há onde comentar.
func (c *Client) MergeRequestsForCommit(sha string) ([]int, error) {
	endpoint := fmt.Sprintf("%s/api/v4/projects/%s/repository/commits/%s/merge_requests",
		c.BaseURL, url.PathEscape(c.ProjectID), url.PathEscape(sha))

	var payload []struct {
		IID   int    `json:"iid"`
		State string `json:"state"`
	}
	if err := c.doJSON("GET", endpoint, &payload); err != nil {
		return nil, err
	}

	iids := make([]int, 0, len(payload))
	for _, mr := range payload {
		iids = append(iids, mr.IID)
	}
	return iids, nil
}

// UpsertMergeRequestNote publica um comentário na MR, ou atualiza o que já escrevemos antes.
//
// O corpo recebe o marker prefixado; é por ele que a nota anterior é reconhecida numa reexecução.
// Sem isso, cada rerun do job deixaria mais um comentário na MR.
func (c *Client) UpsertMergeRequestNote(iid int, marker, body string) error {
	body = marker + "\n" + body

	noteID, err := c.findNote(iid, marker)
	if err != nil {
		return err
	}

	params := url.Values{}
	params.Set("body", body)

	method, endpoint := "POST", fmt.Sprintf("%s/api/v4/projects/%s/merge_requests/%d/notes",
		c.BaseURL, url.PathEscape(c.ProjectID), iid)
	if noteID != 0 {
		method, endpoint = "PUT", fmt.Sprintf("%s/api/v4/projects/%s/merge_requests/%d/notes/%d",
			c.BaseURL, url.PathEscape(c.ProjectID), iid, noteID)
	}

	return c.doJSON(method, endpoint+"?"+params.Encode(), nil)
}

// findNote devolve o ID da nota marcada com marker, ou 0 se ainda não existe.
func (c *Client) findNote(iid int, marker string) (int, error) {
	endpoint := fmt.Sprintf("%s/api/v4/projects/%s/merge_requests/%d/notes?per_page=100",
		c.BaseURL, url.PathEscape(c.ProjectID), iid)

	var notes []struct {
		ID   int    `json:"id"`
		Body string `json:"body"`
	}
	if err := c.doJSON("GET", endpoint, &notes); err != nil {
		return 0, err
	}

	for _, n := range notes {
		if strings.Contains(n.Body, marker) {
			return n.ID, nil
		}
	}
	return 0, nil
}

// doJSON executa a requisição autenticada e, se out for não-nil, decodifica a resposta nele.
func (c *Client) doJSON(method, endpoint string, out any) error {
	req, err := http.NewRequest(method, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("PRIVATE-TOKEN", c.Token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("gitlab API returned status %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
