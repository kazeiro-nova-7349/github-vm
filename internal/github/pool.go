package github

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// AccountPool 是 Web 控制端内置的 GitHub 账号池
type AccountPool struct {
	mu       sync.Mutex
	accounts []Account
	nextID   int64
}

// NewAccountPool 创建账号池
func NewAccountPool() *AccountPool {
	return &AccountPool{accounts: []Account{}, nextID: 1}
}

// AccountEntry 是带元数据的账号条目
type AccountEntry struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Owner      string `json:"repo_owner"`
	Repo       string `json:"repo"`
	WorkflowID string `json:"workflow_id"`
	TokenMask  string `json:"token_mask"`
	Enabled    bool   `json:"enabled"`
	Status     string `json:"status"`
	LastError  string `json:"error,omitempty"`
	TestedAt   string `json:"tested_at,omitempty"`
}

// ImportResult 批量导入结果
type ImportResult struct {
	Imported int            `json:"imported"`
	Skipped  int            `json:"skipped"`
	Errors   []string       `json:"errors,omitempty"`
	Entries  []AccountEntry `json:"entries"`
}

// ImportLines 批量导入 token|owner|repo|workflow 行
func (p *AccountPool) ImportLines(text string, defaultWorkflow string) ImportResult {
	text = strings.TrimSpace(text)
	if text == "" {
		return ImportResult{Errors: []string{"导入内容为空"}}
	}
	if defaultWorkflow == "" {
		defaultWorkflow = "register.yml"
	}
	lines := strings.Split(text, "\n")
	result := ImportResult{Errors: []string{}, Entries: []AccountEntry{}}
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 2 {
			result.Errors = append(result.Errors, fmt.Sprintf("第 %d 行: 至少需要 token|owner", i+1))
			continue
		}
		token := strings.TrimSpace(parts[0])
		owner := strings.TrimSpace(parts[1])
		repo := owner
		if len(parts) >= 3 {
			repo = strings.TrimSpace(parts[2])
		}
		workflow := defaultWorkflow
		if len(parts) >= 4 {
			workflow = strings.TrimSpace(parts[3])
		}
		name := owner
		if len(parts) >= 5 && strings.TrimSpace(parts[4]) != "" {
			name = strings.TrimSpace(parts[4])
		}
		entry := p.addAccount(Account{
			Name:       name,
			Token:      token,
			Owner:      owner,
			Repo:       repo,
			WorkflowID: workflow,
			Enabled:    true,
		})
		result.Imported++
		result.Entries = append(result.Entries, entry)
	}
	return result
}

// addAccount 添加账号到池
func (p *AccountPool) addAccount(acc Account) AccountEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry := AccountEntry{
		ID:         p.nextID,
		Name:       acc.Name,
		Owner:      acc.Owner,
		Repo:       acc.Repo,
		WorkflowID: acc.WorkflowID,
		TokenMask:  MaskToken(acc.Token),
		Enabled:    acc.Enabled,
		Status:     "untested",
	}
	p.nextID++
	p.accounts = append(p.accounts, acc)
	return entry
}

// List 返回所有条目（脱敏）
func (p *AccountPool) List() []AccountEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	entries := make([]AccountEntry, 0, len(p.accounts))
	for i, acc := range p.accounts {
		status := "untested"
		errMsg := ""
		testedAt := ""
		if i < len(p.accounts) && p.accounts[i].Token != "" {
			// 保留已有状态
		}
		entries = append(entries, AccountEntry{
			ID:         int64(i + 1),
			Name:       acc.Name,
			Owner:      acc.Owner,
			Repo:       acc.Repo,
			WorkflowID: acc.WorkflowID,
			TokenMask:  MaskToken(acc.Token),
			Enabled:    acc.Enabled,
			Status:     status,
			LastError:  errMsg,
			TestedAt:   testedAt,
		})
	}
	return entries
}

// Remove 删除账号
func (p *AccountPool) Remove(id int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	idx := int(id) - 1
	if idx < 0 || idx >= len(p.accounts) {
		return false
	}
	p.accounts = append(p.accounts[:idx], p.accounts[idx+1:]...)
	return true
}

// Rename 重命名账号
func (p *AccountPool) Rename(id int64, name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	idx := int(id) - 1
	if idx < 0 || idx >= len(p.accounts) || strings.TrimSpace(name) == "" {
		return false
	}
	p.accounts[idx].Name = strings.TrimSpace(name)
	return true
}

// Get 返回指定账号
func (p *AccountPool) Get(id int64) (Account, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	idx := int(id) - 1
	if idx < 0 || idx >= len(p.accounts) {
		return Account{}, false
	}
	return p.accounts[idx], true
}

// SetEnabled 启用/禁用
func (p *AccountPool) SetEnabled(id int64, enabled bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	idx := int(id) - 1
	if idx < 0 || idx >= len(p.accounts) {
		return false
	}
	p.accounts[idx].Enabled = enabled
	return true
}

// Clear 清空
func (p *AccountPool) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.accounts = []Account{}
	p.nextID = 1
}

// ToAccounts 导出为 gh.Account 切片
func (p *AccountPool) ToAccounts() []Account {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Account, len(p.accounts))
	copy(out, p.accounts)
	return out
}

// Enabled 返回启用的 gh.Account
func (p *AccountPool) Enabled() []Account {
	return EnabledAccounts(p.ToAccounts())
}

// Count 总数
func (p *AccountPool) Count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.accounts)
}

// TestResult 测试结果
type TestResult struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Login string `json:"login,omitempty"`
	Error string `json:"error,omitempty"`
}

// Test 测试单个账号
func Test(ctx context.Context, acc Account) TestResult {
	login, err := NewClient(acc).Viewer(ctx)
	if err != nil {
		return TestResult{Name: acc.Name, OK: false, Error: err.Error()}
	}
	return TestResult{Name: acc.Name, OK: true, Login: login}
}

// TestAll 批量测试（带时间戳）
func (p *AccountPool) TestAll(ctx context.Context) []TestResult {
	accounts := p.ToAccounts()
	results := make([]TestResult, 0, len(accounts))
	for i, acc := range accounts {
		r := Test(ctx, acc)
		r.ID = int64(i + 1)
		results = append(results, r)
	}
	return results
}

// Touch 更新时间戳
func Touch(t string) string {
	if t == "" {
		return time.Now().Format("2006-01-02 15:04:05")
	}
	return t
}
