package appconfig

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	gh "main.go/internal/github"
)

type Config struct {
	WebAddr             string       `json:"web_addr"`
	GitHubAccounts      []gh.Account `json:"github_accounts"`
	GitHubOAuthClientID string       `json:"github_oauth_client_id"`
	DefaultRepo         string       `json:"default_repo"`
	GitHubWorkflowID    string       `json:"github_workflow_id"`
	Deploy              DeployConfig `json:"deploy"`
	Job                 JobConfig    `json:"job"`
}

type DeployConfig struct {
	RepoPath string `json:"repo_path"`
	Private  bool   `json:"private"`
	Remote   string `json:"remote"`
}

type JobConfig struct {
	MaxRetries       int `json:"max_retries"`
	PollIntervalSecs int `json:"poll_interval_secs"`
}

func Load() (Config, error) {
	cfg := Defaults()
	for _, path := range []string{"web_config.local.json", "web_config.json"} {
		b, err := os.ReadFile(path)
		if err == nil {
			if err := json.Unmarshal(b, &cfg); err != nil {
				return cfg, err
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return cfg, err
		}
	}
	applyEnv(&cfg)
	fillDefaults(&cfg)
	return cfg, nil
}

func Defaults() Config {
	return Config{
		WebAddr:          "127.0.0.1:8080",
		DefaultRepo:      "4399AccountRegister-main",
		GitHubWorkflowID: "register.yml",
		Deploy:           DeployConfig{RepoPath: ".", Remote: "auto-vm"},
		Job:              JobConfig{MaxRetries: 3, PollIntervalSecs: 20},
	}
}

func SaveLocal(cfg Config) error {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile("web_config.local.json", b, 0600)
}

func ParseAccounts(text string) ([]gh.Account, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("导入内容为空")
	}
	if strings.Contains(text, ",") {
		return parseCSVAccounts(text)
	}
	var accounts []gh.Account
	for idx, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 4 {
			return nil, fmt.Errorf("第 %d 行格式错误，需要 token|owner|repo|workflow_id", idx+1)
		}
		accounts = append(accounts, gh.Account{
			Name:       fmt.Sprintf("vm%02d", len(accounts)+1),
			Token:      strings.TrimSpace(parts[0]),
			Owner:      strings.TrimSpace(parts[1]),
			Repo:       strings.TrimSpace(parts[2]),
			WorkflowID: strings.TrimSpace(parts[3]),
			Enabled:    true,
		})
	}
	return accounts, nil
}

func MergeAccounts(existing, incoming []gh.Account) []gh.Account {
	out := append([]gh.Account(nil), existing...)
	for _, account := range incoming {
		matched := false
		for i := range out {
			if out[i].Name == account.Name && account.Name != "" {
				out[i] = account
				matched = true
				break
			}
		}
		if !matched {
			out = append(out, account)
		}
	}
	return out
}

func parseCSVAccounts(text string) ([]gh.Account, error) {
	r := csv.NewReader(strings.NewReader(text))
	r.TrimLeadingSpace = true
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errors.New("CSV 为空")
	}
	start := 0
	if len(rows[0]) > 0 && strings.EqualFold(strings.TrimSpace(rows[0][0]), "name") {
		start = 1
	}
	var accounts []gh.Account
	for i := start; i < len(rows); i++ {
		row := rows[i]
		if len(row) < 5 {
			return nil, fmt.Errorf("CSV 第 %d 行字段不足", i+1)
		}
		enabled := true
		if len(row) >= 6 {
			enabled = strings.EqualFold(strings.TrimSpace(row[5]), "true") || strings.TrimSpace(row[5]) == "1"
		}
		accounts = append(accounts, gh.Account{
			Name:       strings.TrimSpace(row[0]),
			Token:      strings.TrimSpace(row[1]),
			Owner:      strings.TrimSpace(row[2]),
			Repo:       strings.TrimSpace(row[3]),
			WorkflowID: strings.TrimSpace(row[4]),
			Enabled:    enabled,
		})
	}
	return accounts, nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("WEB_ADDR"); v != "" {
		cfg.WebAddr = v
	}
	if v := os.Getenv("GITHUB_OAUTH_CLIENT_ID"); v != "" {
		cfg.GitHubOAuthClientID = v
	}
	if v := os.Getenv("GITHUB_DEFAULT_REPO"); v != "" {
		cfg.DefaultRepo = v
	}
	if v := os.Getenv("GITHUB_WORKFLOW_ID"); v != "" {
		cfg.GitHubWorkflowID = v
	}
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		cfg.GitHubAccounts = append(cfg.GitHubAccounts, gh.Account{
			Name:       getenv("GITHUB_NAME", "env"),
			Token:      token,
			Owner:      os.Getenv("GITHUB_OWNER"),
			Repo:       os.Getenv("GITHUB_REPO"),
			WorkflowID: getenv("GITHUB_WORKFLOW_ID", "register.yml"),
			Enabled:    true,
		})
	}
}

func fillDefaults(cfg *Config) {
	if cfg.WebAddr == "" {
		cfg.WebAddr = "127.0.0.1:8080"
	}
	if cfg.DefaultRepo == "" {
		cfg.DefaultRepo = "4399AccountRegister-main"
	}
	if cfg.GitHubWorkflowID == "" {
		cfg.GitHubWorkflowID = "register.yml"
	}
	if cfg.Deploy.RepoPath == "" {
		cfg.Deploy.RepoPath = "."
	}
	if cfg.Deploy.Remote == "" {
		cfg.Deploy.Remote = "auto-vm"
	}
	if cfg.Job.MaxRetries == 0 {
		cfg.Job.MaxRetries = 3
	}
	if cfg.Job.PollIntervalSecs == 0 {
		cfg.Job.PollIntervalSecs = 20
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
