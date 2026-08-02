package register

import (
	"fmt"
	"math/rand"
	"time"
)

// StealthConfig 反检测配置
type StealthConfig struct {
	UserAgent    string
	Lang         string
	Timezone     string
	ViewportW    int
	ViewportH    int
	Proxy        string // http://user:pass@ip:port
}

// DefaultStealth 默认配置
func DefaultStealth() StealthConfig {
	agents := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.6099.43 Safari/537.36",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.6167.85 Safari/537.36",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.6099.43 Safari/537.36",
	}
	return StealthConfig{
		UserAgent: agents[rand.Intn(len(agents))],
		Lang:      "en-US",
		Timezone:  "America/New_York",
		ViewportW: 1280 + rand.Intn(200),
		ViewportH: 720 + rand.Intn(200),
	}
}

// HumanDelay 模拟人类延迟
func HumanDelay(min, max time.Duration) {
	d := min + time.Duration(rand.Int63n(int64(max-min)))
	time.Sleep(d)
}

// TypeLikeHuman 逐字符延迟
func TypeLikeHuman() time.Duration {
	return time.Duration(50+rand.Intn(100)) * time.Millisecond
}

// RandomMouseMovement 模拟鼠标
func RandomMouseMovement() (x, y int) {
	return rand.Intn(800) + 100, rand.Intn(600) + 100
}

// ProxyFormat 代理格式
func ProxyFormat(host, port, user, pass string) string {
	if user != "" {
		return fmt.Sprintf("http://%s:%s@%s:%s", user, pass, host, port)
	}
	return fmt.Sprintf("http://%s:%s", host, port)
}
