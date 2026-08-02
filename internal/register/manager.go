package register

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Config 注册配置
type Config struct {
	TempRamToken    string `json:"tempram_token"`
	Prefix          string `json:"prefix"`
	Count           int    `json:"count"`
	OutputFile      string `json:"output_file"`
	CaptchaKey      string `json:"captcha_key"`
	CaptchaType     string `json:"captcha_type"` // "2captcha" / "capsolver"
	Mode            string `json:"mode"`         // "cdp" / "stealth"
	AutoStartChrome bool   `json:"auto_start_chrome"`
	ChromePort      int    `json:"chrome_port"`
	Proxy           string `json:"proxy"`
}

// DefaultConfig 默认配置
func DefaultConfig() Config {
	return Config{
		Prefix:          "usr",
		Count:           1,
		OutputFile:      "github_accounts.txt",
		CaptchaType:     "2captcha",
		Mode:            "stealth",
		AutoStartChrome: false,
		ChromePort:      9222,
	}
}

// Status 运行状态
type Status struct {
	Running    bool     `json:"running"`
	StartedAt  string   `json:"started_at,omitempty"`
	FinishedAt string   `json:"finished_at,omitempty"`
	Total      int      `json:"total"`
	Success    int      `json:"success"`
	Fail       int      `json:"fail"`
	Log        []string `json:"log"`
	PID        int      `json:"pid,omitempty"`
}

// Manager 注册管理器
type Manager struct {
	mu           sync.Mutex
	status       Status
	cancel       context.CancelFunc
	chrome       *ChromeManager
	chromeInited bool
}

func New() *Manager {
	return &Manager{
		status: Status{
			Running: false,
			Log:     []string{},
			Total:   0,
			Success: 0,
			Fail:    0,
		},
	}
}

// Start 启动注册子进程
func (m *Manager) Start(cfg Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.status.Running {
		return fmt.Errorf("注册任务已在运行")
	}

	if cfg.TempRamToken == "" {
		return fmt.Errorf("缺少 tempram_token")
	}
	cfg.TempRamToken = normalizeTempRamToken(cfg.TempRamToken)
	if !strings.HasPrefix(cfg.TempRamToken, "tm_") {
		return fmt.Errorf("TempRam Token 格式错误：需要 tm_ 开头的 Temporam API Key，当前不是 TempRam Key")
	}
	defaults := DefaultConfig()
	if cfg.Prefix == "" {
		cfg.Prefix = defaults.Prefix
	}
	if cfg.OutputFile == "" {
		cfg.OutputFile = defaults.OutputFile
	}
	if cfg.CaptchaType == "" {
		cfg.CaptchaType = defaults.CaptchaType
	}
	if cfg.Mode == "" {
		cfg.Mode = defaults.Mode
	}
	if cfg.Count <= 0 {
		cfg.Count = defaults.Count
	}
	if cfg.ChromePort == 0 {
		cfg.ChromePort = defaults.ChromePort
	}
	if _, err := exec.LookPath("node"); err != nil {
		return fmt.Errorf("未找到 node，请先安装 Node.js 或确认 node 在 PATH 中")
	}

	// 初始化日志
	logBuf := []string{}

	// 根据模式选择脚本
	var scriptPath string
	if cfg.Mode == "cdp" {
		// CDP 模式：连接用户手动启动的 Chrome
		scriptPath = m.findScript("register_cdp.js")
		if cfg.AutoStartChrome && !m.chromeInited {
			m.chrome = NewChromeManager(cfg.ChromePort)
			logBuf = append(logBuf, "[Chrome] 自动启动 Chrome...")
			if err := m.chrome.Start(); err != nil {
				logBuf = append(logBuf, "[Chrome] 启动失败: "+err.Error())
			} else {
				m.chromeInited = true
				logBuf = append(logBuf, "[Chrome] 等待就绪...")
				go func() {
					if err := m.chrome.WaitReady(30 * time.Second); err != nil {
						m.appendLog("[Chrome] 就绪超时: " + err.Error())
					} else {
						m.appendLog("[Chrome] 已就绪")
					}
				}()
			}
		}
	} else {
		// Stealth 模式：全自动
		scriptPath = m.findScript("stealth_register.js")
		logBuf = append(logBuf, "[模式] Stealth 全自动")
	}

	logBuf = append(logBuf, "[配置] mode="+cfg.Mode+", count="+fmt.Sprintf("%d", cfg.Count)+", output="+cfg.OutputFile)
	logBuf = append(logBuf, "[检查] Node.js 已找到，正在检查注册脚本")
	if scriptPath != "" {
		logBuf = append(logBuf, "[检查] 注册脚本: "+scriptPath)
	}

	if scriptPath == "" {
		return fmt.Errorf("register script not found")
	}

	// 清空输出文件
	os.Remove(cfg.OutputFile)

	m.status = Status{
		Running:   true,
		StartedAt: time.Now().Format("2006-01-02 15:04:05"),
		Total:     cfg.Count,
		Success:   0,
		Fail:      0,
		Log:       logBuf,
	}

	go m.run(scriptPath, cfg)

	return nil
}

func (m *Manager) findScript(name string) string {
	candidates := []string{
		filepath.Join("cmd", "github-browser", name),
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, "cmd", "github-browser", name))
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func (m *Manager) run(scriptPath string, cfg Config) {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.cancel = cancel
	m.mu.Unlock()

	// 构建命令
	args := []string{scriptPath, cfg.TempRamToken, fmt.Sprintf("%d", cfg.Count)}
	if cfg.Proxy != "" {
		args = append(args, cfg.Proxy)
	}
	if cfg.CaptchaKey != "" {
		args = append(args, cfg.CaptchaKey)
	}

	cmd := exec.CommandContext(ctx, "node", args...)
	cmd.Dir = mustWorkDir()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		m.appendLog("[错误] stdout 管道创建失败: " + err.Error())
		m.finish()
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		m.appendLog("[错误] stderr 管道创建失败: " + err.Error())
		m.finish()
		return
	}

	m.appendLog("[启动] node " + scriptPath + " " + maskToken(cfg.TempRamToken) + " " + fmt.Sprintf("%d", cfg.Count))
	m.appendLog("[工作目录] " + cmd.Dir)

	if err := cmd.Start(); err != nil {
		m.appendLog("[错误] 启动失败: " + err.Error())
		m.appendLog("[提示] 请确认 Node.js 已安装，脚本路径存在，TempRam Token 有效")
		m.finish()
		return
	}

	m.mu.Lock()
	m.status.PID = cmd.Process.Pid
	m.mu.Unlock()

	m.appendLog("[运行中] PID=" + fmt.Sprintf("%d", cmd.Process.Pid))

	go m.pipeLog("stdout", stdout)
	go m.pipeLog("stderr", stderr)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	waitErr := error(nil)
	select {
	case waitErr = <-done:
	case <-time.After(2 * time.Minute):
		m.appendLog("[等待] 子进程已运行 2 分钟，仍未退出；通常表示浏览器、网络或验证码环节仍在等待")
		waitErr = <-done
	}

	m.mu.Lock()
	m.status.PID = 0
	m.mu.Unlock()

	if waitErr != nil {
		m.appendLog("[退出] 错误: " + waitErr.Error())
	}

	// 读取输出文件
	if data, err := os.ReadFile(cfg.OutputFile); err == nil {
		lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
		success := 0
		for _, line := range lines {
			if len(line) > 0 {
				success++
				m.appendLog("[成功] " + string(line))
			}
		}
		m.mu.Lock()
		m.status.Success = success
		m.status.Fail = m.status.Total - success
		m.mu.Unlock()
	}

	m.finish()
}

// Stop 停止注册
func (m *Manager) Stop() error {
	m.mu.Lock()
	if !m.status.Running {
		m.mu.Unlock()
		return fmt.Errorf("没有运行中的任务")
	}
	cancel := m.cancel
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	m.appendLog("[停止] 用户取消")
	return nil
}

// Status 获取状态
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	// 复制
	s := m.status
	s.Log = make([]string, len(m.status.Log))
	copy(s.Log, m.status.Log)
	return s
}

// Results 读取结果文件
func (m *Manager) Results(outputFile string) ([]map[string]string, error) {
	data, err := os.ReadFile(outputFile)
	if err != nil {
		return nil, err
	}
	var results []map[string]string
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		parts := bytes.Split(line, []byte("|"))
		if len(parts) >= 3 {
			results = append(results, map[string]string{
				"username": string(parts[0]),
				"email":    string(parts[1]),
				"password": string(parts[2]),
			})
		}
	}
	return results, nil
}

// 内部方法

func (m *Manager) pipeLog(name string, r io.Reader) {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		m.appendLog("[" + name + "] " + scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		m.appendLog("[" + name + "] 读取失败: " + err.Error())
	}
}

func (m *Manager) appendLog(msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := time.Now().Format("15:04:05")
	m.status.Log = append(m.status.Log, "["+t+"] "+msg)
	// 保留最近 200 条
	if len(m.status.Log) > 200 {
		m.status.Log = m.status.Log[len(m.status.Log)-200:]
	}
}

func (m *Manager) finish() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.Running = false
	m.status.FinishedAt = time.Now().Format("2006-01-02 15:04:05")
}

func normalizeTempRamToken(token string) string {
	token = strings.TrimSpace(token)
	if strings.HasPrefix(token, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(token, "Bearer "))
	}
	return token
}

func maskToken(t string) string {
	if len(t) <= 8 {
		return "***"
	}
	return t[:4] + "..." + t[len(t)-4:]
}

func mustWorkDir() string {
	if d, err := os.Getwd(); err == nil {
		return d
	}
	return "."
}
