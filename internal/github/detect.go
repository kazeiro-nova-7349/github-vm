package github

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

type DetectRequest struct {
	WorkflowID string `json:"workflow_id"`
	Repo       string `json:"repo"`
}

type DetectResult struct {
	Account    Account `json:"account"`
	LineMasked string  `json:"line_masked"`
}

func AutoDetect(ctx context.Context, req DetectRequest) (DetectResult, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return DetectResult{}, errors.New("未检测到 GitHub CLI，请先安装 gh 或手动导入 token")
	}
	if err := runStatus(ctx); err != nil {
		return DetectResult{}, fmt.Errorf("GitHub CLI 未登录，请先运行 gh auth login: %w", err)
	}
	token, err := commandOutput(ctx, "gh", "auth", "token")
	if err != nil {
		return DetectResult{}, err
	}
	login, err := commandOutput(ctx, "gh", "api", "user", "--jq", ".login")
	if err != nil {
		return DetectResult{}, err
	}
	owner, repo := login, strings.TrimSpace(req.Repo)
	if repo == "" {
		remote, err := commandOutput(ctx, "git", "remote", "get-url", "origin")
		if err == nil {
			if parsedOwner, parsedRepo, ok := ParseRemote(remote); ok {
				owner, repo = parsedOwner, parsedRepo
			}
		}
	}
	if repo == "" {
		repo = "4399AccountRegister-main"
	}
	workflowID := strings.TrimSpace(req.WorkflowID)
	if workflowID == "" {
		workflowID = "register.yml"
	}
	account := Account{Name: login, Token: token, Owner: owner, Repo: repo, WorkflowID: workflowID, Enabled: true}
	return DetectResult{Account: account, LineMasked: fmt.Sprintf("%s|%s|%s|%s", MaskToken(token), owner, repo, workflowID)}, nil
}

func ParseRemote(remote string) (owner, repo string, ok bool) {
	remote = strings.TrimSpace(remote)
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`^https://(?:[^@/]+@)?github\.com/([^/]+)/([^/]+?)(?:\.git)?/?$`),
		regexp.MustCompile(`^git@github\.com:([^/]+)/([^/]+?)(?:\.git)?$`),
	}
	for _, pattern := range patterns {
		m := pattern.FindStringSubmatch(remote)
		if len(m) == 3 {
			return m[1], strings.TrimSuffix(m[2], ".git"), true
		}
	}
	return "", "", false
}

func runStatus(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "gh", "auth", "status")
	return cmd.Run()
}

func commandOutput(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
