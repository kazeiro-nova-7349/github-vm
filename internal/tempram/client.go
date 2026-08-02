package tempram

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	defaultBase = "https://api.temporam.com/v1"
)

type Client struct {
	APIBase string
	Token   string
	HTTP    *http.Client
}

type Domain struct {
	Domain    string `json:"domain"`
	Available bool   `json:"available"`
}

type Email struct {
	ID        string    `json:"id"`
	UUID      string    `json:"uuid"`
	ToEmail   string    `json:"to_email"`
	FromEmail string    `json:"from_email"`
	FromName  string    `json:"from_name"`
	Subject   string    `json:"subject"`
	Summary   string    `json:"summary"`
	CreatedAt time.Time `json:"created_at"`
}

type apiResponse struct {
	Code  int     `json:"code"`
	Error bool    `json:"error"`
	Data  []Email `json:"data"`
	Meta  struct {
		Remaining int `json:"remaining"`
	} `json:"meta"`
}

type domainResponse struct {
	Code  int      `json:"code"`
	Error bool     `json:"error"`
	Data  []Domain `json:"data"`
	Meta  struct {
		Remaining int `json:"remaining"`
	} `json:"meta"`
}

var codeRe = regexp.MustCompile(`\b\d{6,8}\b`)

func New(token string) *Client {
	return &Client{
		APIBase: defaultBase,
		Token:   token,
		HTTP:    &http.Client{Timeout: 15 * time.Second},
	}
}

// GetDomains 获取可用邮箱域名
func (c *Client) GetDomains() ([]string, error) {
	req, err := c.newReq("GET", "/domains", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("tempram %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var raw domainResponse
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parse domains: %w", err)
	}
	out := []string{}
	for _, d := range raw.Data {
		out = append(out, d.Domain)
	}
	return out, nil
}

// GenEmail 申请一个随机地址
func (c *Client) GenEmail() (string, error) {
	domains, err := c.GetDomains()
	if err != nil || len(domains) == 0 {
		return "", fmt.Errorf("no domain available: %w", err)
	}
	prefix := fmt.Sprintf("gh%06d", time.Now().UnixNano()%1000000)
	return prefix + "@" + domains[0], nil
}

// GetEmails 拉取某个地址的收件箱
func (c *Client) GetEmails(address string) ([]Email, error) {
	req, err := c.newReq("GET", "/emails?email="+address, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("tempram %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var raw apiResponse
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parse emails: %w", err)
	}
	return raw.Data, nil
}

// WaitForCode 轮询等待验证码邮件
func (c *Client) WaitForCode(address string, timeout time.Duration, keyword string) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		emails, err := c.GetEmails(address)
		if err == nil {
			for _, e := range emails {
				if keyword != "" && !containsAny(e.Subject+" "+e.Summary, keyword) {
					continue
				}
				code := extractCode(e.Summary)
				if code == "" {
					code = extractCode(e.Subject)
				}
				if code != "" {
					return code, nil
				}
			}
		}
		time.Sleep(5 * time.Second)
	}
	return "", fmt.Errorf("timeout waiting for code")
}

func (c *Client) newReq(method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, c.APIBase+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func containsAny(s string, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

func extractCode(s string) string {
	return codeRe.FindString(s)
}
