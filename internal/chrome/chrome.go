package chrome

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/playwright-community/playwright-go"
)

const (
	defaultUserDataDir = "./chrome-profile"
)

// Launcher 启动真实 Chrome（有头）
type Launcher struct {
	ExecutablePath string
	UserDataDir    string
}

// FindChrome 自动查找 Chrome 路径
func FindChrome() string {
	candidates := []string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("chrome"); err == nil {
		return p
	}
	return ""
}

// Launch 启动带 profile 的有头 Chrome
func (l *Launcher) Launch() error {
	if l.ExecutablePath == "" {
		l.ExecutablePath = FindChrome()
	}
	if l.UserDataDir == "" {
		l.UserDataDir = defaultUserDataDir
	}
	abs, _ := filepath.Abs(l.UserDataDir)
	cmd := exec.Command(l.ExecutablePath,
		"--remote-debugging-port=9222",
		"--user-data-dir="+abs,
		"--no-first-run",
		"--no-default-browser-check",
		"https://github.com/login",
	)
	return cmd.Start()
}

// ConnectPW 通过 playwright 连接已启动的 Chrome
func ConnectPW() (playwright.Browser, *playwright.Playwright, error) {
	pw, err := playwright.Run()
	if err != nil {
		return nil, nil, fmt.Errorf("start playwright: %w", err)
	}
	browser, err := pw.Chromium.ConnectOverCDP("http://127.0.0.1:9222")
	if err != nil {
		pw.Stop()
		return nil, nil, fmt.Errorf("connect CDP: %w, please start Chrome first", err)
	}
	return browser, pw, nil
}

// Wait 辅助等待
func Wait(d time.Duration) {
	time.Sleep(d)
}
