package github

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// AutoProvider 定义自动获取 Token 的接口
type AutoProvider interface {
	Name() string
	Description() string
	Detect(ctx context.Context) ([]Account, error)
}

// ===== 1. GitHub CLI 自动识别（已有逻辑封装）=====

type CLIAutoProvider struct{}

func (p *CLIAutoProvider) Name() string        { return "gh-cli" }
func (p *CLIAutoProvider) Description() string { return "通过本机 gh CLI 自动获取已登录账号" }

func (p *CLIAutoProvider) Detect(ctx context.Context) ([]Account, error) {
	result, err := AutoDetect(ctx, DetectRequest{WorkflowID: "register.yml"})
	if err != nil {
		return nil, err
	}
	return []Account{result.Account}, nil
}

// ===== 2. OAuth App 授权 URL 生成 =====

type OAuthAutoProvider struct {
	ClientID string
	Scope    string
}

func (p *OAuthAutoProvider) Name() string        { return "oauth-app" }
func (p *OAuthAutoProvider) Description() string { return "生成 GitHub OAuth 授权链接，用户浏览器授权后回调获取 token" }

func (p *OAuthAutoProvider) Detect(ctx context.Context) ([]Account, error) {
	if p.ClientID == "" {
		return nil, fmt.Errorf("未配置 github_oauth_client_id")
	}
	return nil, fmt.Errorf("请使用 /api/github/device/start 进行 Device Code 授权")
}

// AuthURL 生成 GitHub OAuth 授权链接
func (p *OAuthAutoProvider) AuthURL(state string) string {
	scope := p.Scope
	if scope == "" {
		scope = "repo workflow"
	}
	u := url.Values{}
	u.Set("client_id", p.ClientID)
	u.Set("scope", scope)
	u.Set("state", state)
	return "https://github.com/login/oauth/authorize?" + u.Encode()
}

// ===== 3. PAT 页面抓取（需要用户提供 cookie）=====

type PATAutoProvider struct {
	// GitHub 登录后的 cookie，用于访问 /settings/tokens/new
	SessionCookie string
	// Token 权限范围
	Scopes []string
}

func (p *PATAutoProvider) Name() string        { return "pat-page" }
func (p *PATAutoProvider) Description() string { return "通过 GitHub 登录 cookie 自动创建 PAT（需 session cookie）" }

func (p *PATAutoProvider) Detect(ctx context.Context) ([]Account, error) {
	if p.SessionCookie == "" {
		return nil, fmt.Errorf("未提供 GitHub session cookie")
	}
	token, err := p.createToken(ctx)
	if err != nil {
		return nil, err
	}
	acc := Account{Token: token, Enabled: true, WorkflowID: "register.yml"}
	login, err := NewClient(acc).Viewer(ctx)
	if err != nil {
		return nil, fmt.Errorf("token 验证失败: %w", err)
	}
	acc.Name = login
	acc.Owner = login
	acc.Repo = "4399AccountRegister-main"
	return []Account{acc}, nil
}

// createToken 通过 GitHub 网页表单创建 PAT
func (p *PATAutoProvider) createToken(ctx context.Context) (string, error) {
	// 1. 获取 new token 页面，提取 authenticity_token
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://github.com/settings/tokens/new", nil)
	req.Header.Set("Cookie", p.SessionCookie)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("访问 token 页面失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// 提取 authenticity_token
	re := regexp.MustCompile(`name="authenticity_token" value="([^"]+)"`)
	m := re.FindStringSubmatch(string(body))
	if len(m) < 2 {
		return "", fmt.Errorf("无法提取 authenticity_token，可能 cookie 已过期")
	}
	authToken := m[1]

	// 2. 提交创建 token 表单
	form := url.Values{}
	form.Set("authenticity_token", authToken)
	form.Set("oauth_access[description]", "auto-generated-"+time.Now().Format("20060102"))
	form.Set("oauth_access[scopes][]", "repo")
	form.Set("oauth_access[scopes][]", "workflow")

	req2, _ := http.NewRequestWithContext(ctx, "POST", "https://github.com/settings/tokens", strings.NewReader(form.Encode()))
	req2.Header.Set("Cookie", p.SessionCookie)
	req2.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.Header.Set("Referer", "https://github.com/settings/tokens/new")

	resp2, err := client.Do(req2)
	if err != nil {
		return "", fmt.Errorf("提交 token 创建失败: %w", err)
	}
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)

	// 提取创建的 token
	re2 := regexp.MustCompile(`id="oauth_access_token"[^>]*value="([^"]+)"`)
	m2 := re2.FindStringSubmatch(string(body2))
	if len(m2) < 2 {
		// 也可能在 flash message 中
		re3 := regexp.MustCompile(`([ghp|gho]_[A-Za-z0-9]{36,})`)
		m3 := re3.FindStringSubmatch(string(body2))
		if len(m3) >= 2 {
			return m3[1], nil
		}
		return "", fmt.Errorf("无法提取创建的 token，请检查页面响应")
	}
	return m2[1], nil
}

// ===== 4. 环境变量自动识别 =====

type EnvAutoProvider struct{}

func (p *EnvAutoProvider) Name() string        { return "env" }
func (p *EnvAutoProvider) Description() string { return "从环境变量 GITHUB_TOKEN / GITHUB_PAT 自动读取" }

func (p *EnvAutoProvider) Detect(ctx context.Context) ([]Account, error) {
	var accounts []Account
	envVars := []struct {
		Token string
		Name  string
		Owner string
		Repo  string
	}{
		{getEnv("GITHUB_TOKEN"), getEnvDefault("GITHUB_NAME", "env-token"), getEnv("GITHUB_OWNER"), getEnv("GITHUB_REPO")},
		{getEnv("GITHUB_PAT"), getEnvDefault("GITHUB_PAT_NAME", "env-pat"), getEnv("GITHUB_PAT_OWNER"), getEnv("GITHUB_PAT_REPO")},
	}
	for _, v := range envVars {
		if v.Token == "" {
			continue
		}
		acc := Account{Token: v.Token, Name: v.Name, Owner: v.Owner, Repo: v.Repo, Enabled: true, WorkflowID: "register.yml"}
		if acc.Owner == "" {
			login, err := NewClient(acc).Viewer(ctx)
			if err != nil {
				continue
			}
			acc.Owner = login
			acc.Name = login
		}
		if acc.Repo == "" {
			acc.Repo = "4399AccountRegister-main"
		}
		accounts = append(accounts, acc)
	}
	if len(accounts) == 0 {
		return nil, fmt.Errorf("未找到 GITHUB_TOKEN 或 GITHUB_PAT 环境变量")
	}
	return accounts, nil
}

// ===== Provider 注册表 =====

var defaultProviders = []AutoProvider{
	&CLIAutoProvider{},
	&EnvAutoProvider{},
}

// AutoDetectAll 尝试所有自动获取方式
func AutoDetectAll(ctx context.Context) ([]AutoResult, error) {
	results := []AutoResult{}
	for _, p := range defaultProviders {
		accounts, err := p.Detect(ctx)
		results = append(results, AutoResult{
			Provider:  p.Name(),
			Desc:      p.Description(),
			Accounts:  accounts,
			Error:     errToString(err),
		})
	}
	return results, nil
}

type AutoResult struct {
	Provider string   `json:"provider"`
	Desc     string   `json:"description"`
	Accounts []Account `json:"accounts,omitempty"`
	Error    string   `json:"error,omitempty"`
}

func errToString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func getEnv(key string) string {
	return os.Getenv(key)
}

func getEnvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
