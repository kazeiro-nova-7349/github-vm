package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Account struct {
	Name       string `json:"name"`
	Token      string `json:"token"`
	Owner      string `json:"owner"`
	Repo       string `json:"repo"`
	WorkflowID string `json:"workflow_id"`
	Enabled    bool   `json:"enabled"`
}

type Client struct {
	Account Account
	HTTP    *http.Client
}

type Repo struct {
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	HTMLURL  string `json:"html_url"`
	Private  bool   `json:"private"`
}

type Run struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HTMLURL    string `json:"html_url"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

type Artifact struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	SizeInBytes        int64  `json:"size_in_bytes"`
	ArchiveDownloadURL string `json:"archive_download_url"`
	Expired            bool   `json:"expired"`
}

type dispatchRequest struct {
	Ref    string            `json:"ref"`
	Inputs map[string]string `json:"inputs,omitempty"`
}

func NewClient(account Account) *Client {
	return &Client{Account: account, HTTP: &http.Client{Timeout: 20 * time.Second}}
}

func (c *Client) Viewer(ctx context.Context) (string, error) {
	var payload struct {
		Login string `json:"login"`
	}
	if err := c.JSON(ctx, http.MethodGet, "https://api.github.com/user", nil, &payload); err != nil {
		return "", err
	}
	return payload.Login, nil
}

func (c *Client) Repo(ctx context.Context) (*Repo, error) {
	var repo Repo
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s", c.Account.Owner, c.Account.Repo)
	if err := c.JSON(ctx, http.MethodGet, url, nil, &repo); err != nil {
		return nil, err
	}
	return &repo, nil
}

func (c *Client) EnsureRepo(ctx context.Context, private bool) error {
	if _, err := c.Repo(ctx); err == nil {
		return nil
	}
	body := map[string]any{"name": c.Account.Repo, "private": private, "auto_init": false}
	return c.JSON(ctx, http.MethodPost, "https://api.github.com/user/repos", body, nil)
}

func (c *Client) DispatchWorkflow(ctx context.Context, ref string, inputs map[string]string) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/workflows/%s/dispatches", c.Account.Owner, c.Account.Repo, c.Account.WorkflowID)
	return c.JSON(ctx, http.MethodPost, url, dispatchRequest{Ref: ref, Inputs: inputs}, nil)
}

func (c *Client) ListRuns(ctx context.Context, perPage int) ([]Run, error) {
	if perPage <= 0 {
		perPage = 20
	}
	var payload struct {
		WorkflowRuns []Run `json:"workflow_runs"`
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/workflows/%s/runs?per_page=%d", c.Account.Owner, c.Account.Repo, c.Account.WorkflowID, perPage)
	if err := c.JSON(ctx, http.MethodGet, url, nil, &payload); err != nil {
		return nil, err
	}
	return payload.WorkflowRuns, nil
}

func (c *Client) GetRun(ctx context.Context, runID int64) (*Run, error) {
	var run Run
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/runs/%d", c.Account.Owner, c.Account.Repo, runID)
	if err := c.JSON(ctx, http.MethodGet, url, nil, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

func (c *Client) ListArtifacts(ctx context.Context, runID int64) ([]Artifact, error) {
	var payload struct {
		Artifacts []Artifact `json:"artifacts"`
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/runs/%d/artifacts", c.Account.Owner, c.Account.Repo, runID)
	if err := c.JSON(ctx, http.MethodGet, url, nil, &payload); err != nil {
		return nil, err
	}
	return payload.Artifacts, nil
}

func (c *Client) DownloadArtifact(ctx context.Context, artifactID int64) ([]byte, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/artifacts/%d/zip", c.Account.Owner, c.Account.Repo, artifactID)
	return c.Bytes(ctx, http.MethodGet, url, nil)
}

func (c *Client) JSON(ctx context.Context, method, url string, in any, out any) error {
	b, err := c.Bytes(ctx, method, url, in)
	if err != nil {
		return err
	}
	if out != nil && len(b) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}

func (c *Client) Bytes(ctx context.Context, method, url string, in any) ([]byte, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Account.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GitHub API %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return b, nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func EnabledAccounts(accounts []Account) []Account {
	out := make([]Account, 0, len(accounts))
	for _, account := range accounts {
		if account.Enabled && account.Token != "" && account.Owner != "" && account.Repo != "" && account.WorkflowID != "" {
			out = append(out, account)
		}
	}
	return out
}

func MaskToken(token string) string {
	if token == "" {
		return ""
	}
	if len(token) <= 8 {
		return "***"
	}
	return token[:4] + "..." + token[len(token)-4:]
}

func SanitizeAccount(account Account) Account {
	account.Token = MaskToken(account.Token)
	return account
}

func SanitizeAccounts(accounts []Account) []Account {
	out := make([]Account, 0, len(accounts))
	for _, account := range accounts {
		out = append(out, SanitizeAccount(account))
	}
	return out
}
