package register

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// CaptchaSolver 打码平台接口
type CaptchaSolver interface {
	Solve(imageBase64 string, captchaType string) (string, error)
}

// TwoCaptcha 2Captcha 打码
type TwoCaptcha struct {
	APIKey string
}

func (s *TwoCaptcha) Solve(imageBase64 string, captchaType string) (string, error) {
	// 提交任务
	form := map[string]string{
		"key":    s.APIKey,
		"method": "base64",
		"body":   imageBase64,
		"json":   "1",
	}
	if captchaType == "recaptcha" {
		form["method"] = "userrecaptcha"
		form["googlekey"] = "TARGET_SITEKEY"
		form["pageurl"] = "https://github.com/signup"
	}

	data, _ := json.Marshal(form)
	resp, err := http.Post("https://2captcha.com/in.php", "application/json", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)

	var result struct {
		Status  int    `json:"status"`
		Request string `json:"request"`
	}
	if err := json.Unmarshal(b, &result); err != nil {
		return "", err
	}
	if result.Status != 1 {
		return "", fmt.Errorf("2captcha submit failed: %s", string(b))
	}

	// 轮询结果
	for i := 0; i < 60; i++ {
		time.Sleep(5 * time.Second)
		r2, err := http.Get(fmt.Sprintf("https://2captcha.com/res.php?key=%s&action=get&id=%s&json=1", s.APIKey, result.Request))
		if err != nil {
			continue
		}
		b2, _ := io.ReadAll(r2.Body)
		r2.Body.Close()
		var res struct {
			Status  int    `json:"status"`
			Request string `json:"request"`
			Error   string `json:"error_text"`
		}
		json.Unmarshal(b2, &res)
		if res.Status == 1 {
			return res.Request, nil
		}
		if res.Request != "CAPCHA_NOT_READY" {
			return "", fmt.Errorf("2captcha error: %s", res.Error)
		}
	}
	return "", fmt.Errorf("2captcha timeout")
}

// CapSolver CapSolver 打码
type CapSolver struct {
	APIKey string
}

func (s *CapSolver) Solve(imageBase64 string, captchaType string) (string, error) {
	task := map[string]interface{}{
		"type":      "ImageToTextTask",
		"body":      imageBase64,
		"phrase":    false,
		"case":      true,
		"numeric":   0,
		"math":      false,
		"minLength": 6,
		"maxLength": 8,
	}
	payload := map[string]interface{}{
		"clientKey": s.APIKey,
		"task":      task,
	}
	data, _ := json.Marshal(payload)
	resp, err := http.Post("https://api.capsolver.com/createTask", "application/json", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	_ = b
	return "", fmt.Errorf("not implemented")
}

// ImageToBase64 图片转 base64
func ImageToBase64(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}
