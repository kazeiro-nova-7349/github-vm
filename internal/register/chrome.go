package register

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

var httpClient = &http.Client{Timeout: 3 * time.Second}

// ChromeManager 管理 Chrome 进程
type ChromeManager struct {
	ExecutablePath string
	UserDataDir    string
	Port           int
	Process        *os.Process
}

// NewChromeManager 创建 Chrome 管理器
func NewChromeManager(port int) *ChromeManager {
	abs, _ := filepath.Abs(filepath.Join("chrome-data", fmt.Sprintf("port-%d", port)))
	return &ChromeManager{
		Port:        port,
		UserDataDir: abs,
	}
}

// FindChrome 查找 Chrome 可执行文件
func (c *ChromeManager) FindChrome() {
	if c.ExecutablePath != "" {
		return
	}
	candidates := []string{}
	if runtime.GOOS == "linux" {
		candidates = []string{
			"/usr/bin/google-chrome",
			"/usr/bin/chromium-browser",
			"/usr/bin/chromium",
			"/snap/bin/chromium",
		}
	} else if runtime.GOOS == "windows" {
		candidates = []string{
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		}
	} else if runtime.GOOS == "darwin" {
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		}
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			c.ExecutablePath = p
			return
		}
	}
}

// Start 启动 Chrome
func (c *ChromeManager) Start() error {
	c.FindChrome()
	if c.ExecutablePath == "" {
		return fmt.Errorf("Chrome not found")
	}

	// 确保 user-data-dir 存在
	os.MkdirAll(c.UserDataDir, 0755)

	args := []string{
		fmt.Sprintf("--remote-debugging-port=%d", c.Port),
		fmt.Sprintf("--user-data-dir=%s", c.UserDataDir),
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-gpu",
		"--disable-dev-shm-usage",
		"--headless=new",
		"--disable-blink-features=AutomationControlled",
		"--no-sandbox",
	}

	// Linux 服务器用 xvfb 包装（可选）
	if runtime.GOOS == "linux" {
		if _, err := exec.LookPath("xvfb-run"); err == nil {
			args = append([]string{"-a", c.ExecutablePath}, args...)
			c.execCmd("xvfb-run", args...)
			return nil
		}
	}

	c.execCmd(c.ExecutablePath, args...)
	return nil
}

func (c *ChromeManager) execCmd(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return
	}
	c.Process = cmd.Process
}

// Stop 停止 Chrome
func (c *ChromeManager) Stop() error {
	if c.Process != nil {
		return c.Process.Kill()
	}
	return nil
}

// IsRunning 检查 Chrome 是否运行
func (c *ChromeManager) IsRunning() bool {
	if c.Process == nil {
		return false
	}
	// 简单检查进程是否存在
	return c.Process.Signal(nil) == nil
}

// WaitReady 等待 Chrome 就绪
func (c *ChromeManager) WaitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// 尝试连接 CDP
		if c.checkCDP() {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("Chrome not ready after %v", timeout)
}

func (c *ChromeManager) checkCDP() bool {
	// 简单 HTTP 探测
	resp, err := httpClient.Get(fmt.Sprintf("http://127.0.0.1:%d/json/version", c.Port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}
