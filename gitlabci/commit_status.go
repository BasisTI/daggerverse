package gitlabci

import (
	"fmt"
	"net/http"
	"net/url"
)

type State string

const (
	StatePending  State = "pending"
	StateRunning  State = "running"
	StateSuccess  State = "success"
	StateFailed   State = "failed"
	StateCanceled State = "canceled"
)

// Client encapsula autenticação e URL base da API GitLab.
type Client struct {
	BaseURL   string // ex: "https://gitlab.com" (sem /api/v4)
	Token     string
	ProjectID string
	// Ref é a branch da pipeline que está reportando (ex: CI_COMMIT_REF_NAME).
	//
	// Sem ele o GitLab anexa cada status à pipeline MAIS RECENTE do SHA, e não à
	// que fez a chamada. Quando uma segunda pipeline nasce no mesmo commit no meio
	// de um build -- típico de um push em develop que também é head de uma MR --,
	// o status terminal vai para a pipeline nova e o `running` da original fica
	// órfão, sem ninguém para fechá-lo: a pipeline trava em "running" para sempre.
	// Informar o ref fixa os dois POSTs na mesma pipeline.
	Ref string
}

// SetCommitStatus envia POST /api/v4/projects/:id/statuses/:sha
func (c *Client) SetCommitStatus(sha string, state State, name, description string) error {
	endpoint := fmt.Sprintf("%s/api/v4/projects/%s/statuses/%s",
		c.BaseURL, url.PathEscape(c.ProjectID), url.PathEscape(sha))

	params := url.Values{}
	params.Set("state", string(state))
	params.Set("name", name)
	if c.Ref != "" {
		params.Set("ref", c.Ref)
	}
	if description != "" {
		params.Set("description", description)
	}

	req, err := http.NewRequest("POST", endpoint+"?"+params.Encode(), nil)
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
	return nil
}
