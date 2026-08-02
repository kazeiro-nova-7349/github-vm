package accounts

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// Account 4399 账号
type Account struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	Source    string `json:"source"`
	CreatedAt string `json:"created_at"`
}

// Manager 账号管理器
type Manager struct {
	mu       sync.Mutex
	accounts []Account
	filePath string
	nextID   int64
}

// NewManager 创建管理器
func NewManager(filePath string) *Manager {
	m := &Manager{
		accounts: []Account{},
		filePath: filePath,
		nextID:   1,
	}
	m.Load()
	return m
}

// Load 从文件加载
func (m *Manager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	f, err := os.Open(m.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	m.accounts = []Account{}
	m.nextID = 1

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			m.accounts = append(m.accounts, Account{
				ID:        m.nextID,
				Username:  parts[0],
				Password:  parts[1],
				Source:    "github-actions",
				CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
			})
			m.nextID++
		}
	}
	return scanner.Err()
}

// Save 保存到文件
func (m *Manager) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	f, err := os.Create(m.filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	for _, acc := range m.accounts {
		fmt.Fprintf(f, "%s:%s\n", acc.Username, acc.Password)
	}
	return nil
}

// GetAll 获取所有账号
func (m *Manager) GetAll() []Account {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]Account, len(m.accounts))
	copy(result, m.accounts)
	return result
}

// GetByID 获取单个账号
func (m *Manager) GetByID(id int64) (*Account, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.accounts {
		if m.accounts[i].ID == id {
			return &m.accounts[i], true
		}
	}
	return nil, false
}

// Add 添加账号
func (m *Manager) Add(username, password, source string) Account {
	m.mu.Lock()
	defer m.mu.Unlock()

	acc := Account{
		ID:        m.nextID,
		Username:  username,
		Password:  password,
		Source:    source,
		CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
	}
	m.nextID++
	m.accounts = append(m.accounts, acc)
	m.Save()
	return acc
}

// AddBatch 批量添加
func (m *Manager) AddBatch(lines []string) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	added := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			m.accounts = append(m.accounts, Account{
				ID:        m.nextID,
				Username:  parts[0],
				Password:  parts[1],
				Source:    "github-actions",
				CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
			})
			m.nextID++
			added++
		}
	}
	if added > 0 {
		m.Save()
	}
	return added
}

// Delete 删除单个
func (m *Manager) Delete(id int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.accounts {
		if m.accounts[i].ID == id {
			m.accounts = append(m.accounts[:i], m.accounts[i+1:]...)
			m.Save()
			return true
		}
	}
	return false
}

// DeleteBatch 批量删除
func (m *Manager) DeleteBatch(ids []int64) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	idSet := make(map[int64]bool)
	for _, id := range ids {
		idSet[id] = true
	}

	newAccounts := []Account{}
	deleted := 0
	for _, acc := range m.accounts {
		if idSet[acc.ID] {
			deleted++
		} else {
			newAccounts = append(newAccounts, acc)
		}
	}

	if deleted > 0 {
		m.accounts = newAccounts
		m.Save()
	}
	return deleted
}

// Clear 清空
func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.accounts = []Account{}
	m.Save()
}

// Count 数量
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.accounts)
}

// ExportText 导出文本
func (m *Manager) ExportText() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	var sb strings.Builder
	for _, acc := range m.accounts {
		fmt.Fprintf(&sb, "%s:%s\n", acc.Username, acc.Password)
	}
	return sb.String()
}

// ImportArtifact 从 GitHub artifact 导入
func (m *Manager) ImportArtifact(data []byte) int {
	lines := strings.Split(string(data), "\n")
	return m.AddBatch(lines)
}
