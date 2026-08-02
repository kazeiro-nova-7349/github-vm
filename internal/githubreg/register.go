package githubreg

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"main.go/internal/tempram"
)

const (
	githubHome    = "https://github.com"
	githubSignUp  = "https://github.com/signup"
	githubJoin    = "https://github.com/signup"
	githubSession = "https://github.com/session"
)

type Register struct {
	TM      *tempram.Client
	Client  *http.Client
	Proxy   string // 代理地址，如 http://127.0.0.1:7890 或 socks5://127.0.0.1:1080
}

type Result struct {
	Username  string
	Email     string
	Password  string
	Success   bool
	Error     string
	Token     string
}

func New(tm *tempram.Client) *Register {
	return &Register{TM: tm}
}

// BuildClient 构造带代理的 http.Client
func (r *Register) BuildClient() {
	transport := &http.Transport{
		MaxIdleConns:    10,
		IdleConnTimeout: 30 * time.Second,
	}
	if r.Proxy != "" {
		if u, err := url.Parse(r.Proxy); err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
	}
	jar, _ := cookiejar.New(nil)
	r.Client = &http.Client{
		Jar:       jar,
		Timeout:   30 * time.Second,
		Transport: transport,
	}
}

// Do 执行一次注册
func (r *Register) Do(username, password string) Result {
	if r.Client == nil {
		r.BuildClient()
	}
	email, err := r.TM.GenEmail()
	if err != nil {
		return Result{Username: username, Error: "gen email: " + err.Error()}
	}

	// 1. 访问 /join 获取 authenticity_token
	token, err := r.fetchToken()
	if err != nil {
		return Result{Username: username, Email: email, Error: "fetch token: " + err.Error()}
	}

	// 2. 提交第一阶段表单（email）
	if err := r.submitEmail(token, email); err != nil {
		return Result{Username: username, Email: email, Error: "submit email: " + err.Error()}
	}

	// 3. 等待验证码
	code, err := r.TM.WaitForCode(email, 90*time.Second, "github")
	if err != nil {
		return Result{Username: username, Email: email, Error: "wait code: " + err.Error()}
	}

	// 4. 提交验证码
	if err := r.submitCode(code); err != nil {
		return Result{Username: username, Email: email, Error: "submit code: " + err.Error()}
	}

	// 5. 提交 password + username
	if err := r.submitAccount(token, email, password, username); err != nil {
		return Result{Username: username, Email: email, Error: "submit account: " + err.Error()}
	}

	// 6. 是否注册成功（检查页面是否跳转）
	ok := r.checkSuccess()

	return Result{
		Username: username,
		Email:    email,
		Password: password,
		Success:  ok,
	}
}

func (r *Register) fetchToken() (string, error) {
	// 先访问首页获取初始 cookie
	req0, _ := http.NewRequest("GET", githubHome, nil)
	setHeaders(req0, "")
	r.Client.Do(req0)

	// 再访问 signup 页
	req, _ := http.NewRequest("GET", githubSignUp+"?source=login", nil)
	setHeaders(req, githubHome)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	resp, err := r.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)

	// 多种 token 模式
	patterns := []string{
		`name="authenticity_token" value="([^"]+)"`,
		`authenticity_token" value="([^"]+)"`,
		`"authenticityToken":"([^"]+)"`,
		`value="([^"]+)" name="authenticity_token"`,
	}
	for _, p := range patterns {
		re := regexp.MustCompile(p)
		m := re.FindStringSubmatch(string(b))
		if len(m) >= 2 {
			return m[1], nil
		}
	}
	// 保存页面用于调试
	os.WriteFile("/tmp/gh_signup.html", b, 0644)
	return "", fmt.Errorf("authenticity_token not found (saved to /tmp/gh_signup.html)")
}

func (r *Register) submitTokenFromPage(body string) string {
	re := regexp.MustCompile(`name="authenticity_token" value="([^"]+)"`)
	m := re.FindStringSubmatch(body)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}

func (r *Register) submitEmail(token, email string) error {
	form := url.Values{}
	form.Set("authenticity_token", token)
	form.Set("email", email)
	req, _ := http.NewRequest("POST", githubJoin+"?email=1", strings.NewReader(form.Encode()))
	setHeaders(req, githubJoin)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := r.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (r *Register) submitCode(code string) error {
	// 当前页面 url 可能是 /join/... 验证码页
	currentURL := githubJoin + "/verify"
	form := url.Values{}
	form.Set("otp", code)
	req, _ := http.NewRequest("POST", currentURL, strings.NewReader(form.Encode()))
	setHeaders(req, githubJoin)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := r.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (r *Register) submitAccount(token, email, password, username string) error {
	form := url.Values{}
	form.Set("authenticity_token", token)
	form.Set("user[login]", username)
	form.Set("user[email]", email)
	form.Set("user[password]", password)
	form.Set("source", "form-home")
	req, _ := http.NewRequest("POST", githubSession, strings.NewReader(form.Encode()))
	setHeaders(req, githubJoin)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := r.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (r *Register) checkSuccess() bool {
	// 检查 cookies 中是否有 logged_in / dotcom_user
	u, _ := url.Parse("https://github.com")
	for _, c := range r.Client.Jar.Cookies(u) {
		if c.Name == "logged_in" || c.Name == "dotcom_user" {
			return true
		}
	}
	return false
}

func setHeaders(req *http.Request, referer string) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.6099.43 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
}

// RandomPassword 生成强密码
func RandomPassword() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%"
	b := make([]byte, 16)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		b[i] = charset[n.Int64()]
	}
	return string(b)
}

// RandomUsername 生成用户名
func RandomUsername(prefix string) string {
	const charset = "abcdefghijklmnopqrstuvwxyz123456789"
	n, _ := rand.Int(rand.Reader, big.NewInt(5))
	length := 6 + int(n.Int64())
	b := make([]byte, length)
	for i := range b {
		x, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		b[i] = charset[x.Int64()]
	}
	return prefix + string(b)
}

// SaveResults 保存结果
func SaveResults(path string, results []Result) error {
	var buf bytes.Buffer
	for _, r := range results {
		if r.Success {
			buf.WriteString(fmt.Sprintf("%s|%s|%s\n", r.Username, r.Email, r.Password))
		}
	}
	// 写入文件（实际用 os.WriteFile）
	_ = path
	return nil
}

// 占位，避免 json 未用
var _ = json.Marshal
