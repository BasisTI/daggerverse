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
}

// SetCommitStatus envia POST /api/v4/projects/:id/statuses/:sha
func (c *Client) SetCommitStatus(sha string, state State, name, description string) error {
	endpoint := fmt.Sprintf("%s/api/v4/projects/%s/statuses/%s",
		c.BaseURL, url.PathEscape(c.ProjectID), url.PathEscape(sha))

	params := url.Values{}
	params.Set("state", string(state))
	params.Set("name", name)
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
