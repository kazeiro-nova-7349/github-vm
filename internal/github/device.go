package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type DeviceService struct {
	ClientID    string
	DefaultRepo string
	WorkflowID  string
	HTTP        *http.Client
	mu          sync.Mutex
	sessions    map[string]*DeviceSession
}

type DeviceSession struct {
	SessionID       string    `json:"session_id"`
	DeviceCode      string    `json:"-"`
	UserCode        string    `json:"user_code"`
	VerificationURI string    `json:"verification_uri"`
	ExpiresIn       int       `json:"expires_in"`
	Interval        int       `json:"interval"`
	CreatedAt       time.Time `json:"created_at"`
	Status          string    `json:"status"`
	Message         string    `json:"message,omitempty"`
	Account         Account   `json:"account,omitempty"`
	LineMasked      string    `json:"line_masked,omitempty"`
}

type deviceStartResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

func NewDeviceService(clientID, defaultRepo, workflowID string) *DeviceService {
	return &DeviceService{
		ClientID:    clientID,
		DefaultRepo: defaultString(defaultRepo, "4399AccountRegister-main"),
		WorkflowID:  defaultString(workflowID, "register.yml"),
		HTTP:        &http.Client{Timeout: 20 * time.Second},
		sessions:    map[string]*DeviceSession{},
	}
}

func (s *DeviceService) Start(ctx context.Context) (*DeviceSession, error) {
	if strings.TrimSpace(s.ClientID) == "" {
		return nil, errors.New("缺少 github_oauth_client_id，请先在 web_config.local.json 或环境变量 GITHUB_OAUTH_CLIENT_ID 中配置 GitHub OAuth App Client ID")
	}
	body := map[string]string{"client_id": s.ClientID, "scope": "repo workflow"}
	var payload deviceStartResponse
	if err := s.githubOAuthJSON(ctx, "https://github.com/login/device/code", body, &payload); err != nil {
		return nil, err
	}
	if payload.Interval <= 0 {
		payload.Interval = 5
	}
	session := &DeviceSession{
		SessionID:       fmt.Sprintf("dev_%d", time.Now().UnixNano()),
		DeviceCode:      payload.DeviceCode,
		UserCode:        payload.UserCode,
		VerificationURI: payload.VerificationURI,
		ExpiresIn:       payload.ExpiresIn,
		Interval:        payload.Interval,
		CreatedAt:       time.Now(),
		Status:          "pending",
		Message:         "等待 GitHub 授权",
	}
	s.mu.Lock()
	s.sessions[session.SessionID] = session
	s.mu.Unlock()
	return sanitizeDeviceSession(session), nil
}

func (s *DeviceService) Poll(ctx context.Context, sessionID string) (*DeviceSession, error) {
	s.mu.Lock()
	session := s.sessions[sessionID]
	s.mu.Unlock()
	if session == nil {
		return nil, errors.New("device session not found")
	}
	if session.Status == "connected" || session.Status == "expired" || session.Status == "failed" {
		return sanitizeDeviceSession(session), nil
	}
	if time.Since(session.CreatedAt) > time.Duration(session.ExpiresIn)*time.Second {
		s.updateSession(sessionID, func(ds *DeviceSession) { ds.Status = "expired"; ds.Message = "GitHub 授权码已过期" })
		return sanitizeDeviceSession(session), nil
	}
	var token tokenResponse
	err := s.githubOAuthJSON(ctx, "https://github.com/login/oauth/access_token", map[string]string{
		"client_id":   s.ClientID,
		"device_code": session.DeviceCode,
		"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
	}, &token)
	if err != nil {
		return nil, err
	}
	if token.Error != "" {
		msg := token.ErrorDesc
		if msg == "" {
			msg = token.Error
		}
		status := "pending"
		if token.Error == "expired_token" || token.Error == "access_denied" {
			status = "failed"
		}
		s.updateSession(sessionID, func(ds *DeviceSession) { ds.Status = status; ds.Message = msg })
		return sanitizeDeviceSession(session), nil
	}
	if token.AccessToken == "" {
		return sanitizeDeviceSession(session), nil
	}
	account, err := s.accountFromToken(ctx, token.AccessToken)
	if err != nil {
		s.updateSession(sessionID, func(ds *DeviceSession) { ds.Status = "failed"; ds.Message = err.Error() })
		return sanitizeDeviceSession(session), nil
	}
	s.updateSession(sessionID, func(ds *DeviceSession) {
		ds.Status = "connected"
		ds.Message = "GitHub 已授权"
		ds.Account = account
		ds.LineMasked = fmt.Sprintf("%s|%s|%s|%s", MaskToken(account.Token), account.Owner, account.Repo, account.WorkflowID)
	})
	s.mu.Lock()
	updated := s.sessions[sessionID]
	s.mu.Unlock()
	return sanitizeDeviceSession(updated), nil
}

func (s *DeviceService) RawAccount(sessionID string) (Account, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessions[sessionID]
	if session == nil || session.Status != "connected" || session.Account.Token == "" {
		return Account{}, false
	}
	return session.Account, true
}

func (s *DeviceService) accountFromToken(ctx context.Context, token string) (Account, error) {
	account := Account{Token: token, WorkflowID: s.WorkflowID, Repo: s.DefaultRepo, Enabled: true}
	login, err := NewClient(account).Viewer(ctx)
	if err != nil {
		return Account{}, err
	}
	account.Name = login
	account.Owner = login
	return account, nil
}

func (s *DeviceService) updateSession(sessionID string, fn func(*DeviceSession)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session := s.sessions[sessionID]; session != nil {
		fn(session)
	}
}

func (s *DeviceService) githubOAuthJSON(ctx context.Context, url string, in any, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GitHub OAuth %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	return json.Unmarshal(data, out)
}

func (s *DeviceService) httpClient() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return http.DefaultClient
}

func sanitizeDeviceSession(session *DeviceSession) *DeviceSession {
	if session == nil {
		return nil
	}
	copy := *session
	copy.DeviceCode = ""
	copy.Account = SanitizeAccount(copy.Account)
	return &copy
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
