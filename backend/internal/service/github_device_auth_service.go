package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	gitHubDeviceCodeURL   = "https://github.com/login/device/code"
	gitHubAccessTokenURL  = "https://github.com/login/oauth/access_token"
	gitHubDeviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"

	gitHubCopilotDefaultDeviceClientID = "Iv1.b507a08c87ecfe98"
	gitHubCopilotDefaultDeviceScope    = "read:user"
	gitHubDeviceAuthSlowDownIncrement  = 5 * time.Second

	gitHubDeviceAuthMaxBodyLen = 2 << 20
)

type GitHubDeviceAuthService struct {
	httpUpstream HTTPUpstream
	store        GitHubDeviceSessionStore
}

func NewGitHubDeviceAuthService(store GitHubDeviceSessionStore, httpUpstream HTTPUpstream) *GitHubDeviceAuthService {
	if store == nil {
		store = NewInMemoryGitHubDeviceSessionStore()
	}
	return &GitHubDeviceAuthService{
		httpUpstream: httpUpstream,
		store:        store,
	}
}

type GitHubDeviceAuthStartResult struct {
	SessionID               string `json:"session_id"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete,omitempty"`
	ExpiresIn               int64  `json:"expires_in"`
	IntervalSeconds         int64  `json:"interval"`
}

type GitHubDeviceAuthPollResult struct {
	Status          string `json:"status"`
	IntervalSeconds int64  `json:"interval,omitempty"`
	AccessToken     string `json:"access_token,omitempty"`
	TokenType       string `json:"token_type,omitempty"`
	Scope           string `json:"scope,omitempty"`
	Error           string `json:"error,omitempty"`
	ErrorDesc       string `json:"error_description,omitempty"`
}

func (s *GitHubDeviceAuthService) Start(ctx context.Context, account *Account, clientID string, scope string) (*GitHubDeviceAuthStartResult, error) {
	if s == nil || s.httpUpstream == nil {
		return nil, errors.New("github device auth service not configured")
	}
	if account == nil {
		return nil, errors.New("account is nil")
	}

	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		clientID = gitHubCopilotDefaultDeviceClientID
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = gitHubCopilotDefaultDeviceScope
	}

	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("scope", scope)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gitHubDeviceCodeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	req.Header.Set("accept", "application/json")
	if strings.TrimSpace(req.Header.Get("user-agent")) == "" {
		req.Header.Set("user-agent", githubCopilotDefaultUserAgent())
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return nil, fmt.Errorf("github device code request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, gitHubDeviceAuthMaxBodyLen))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("github device code request failed: status=%d body=%s", resp.StatusCode, sanitizeUpstreamErrorMessage(string(body)))
	}

	var parsed struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int64  `json:"expires_in"`
		Interval                int64  `json:"interval"`
		Error                   string `json:"error"`
		ErrorDescription        string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse github device code response: %w", err)
	}
	if strings.TrimSpace(parsed.Error) != "" {
		msg := strings.TrimSpace(parsed.ErrorDescription)
		if msg == "" {
			msg = parsed.Error
		}
		return nil, fmt.Errorf("github device code request failed: %s", sanitizeUpstreamErrorMessage(msg))
	}
	if strings.TrimSpace(parsed.DeviceCode) == "" || strings.TrimSpace(parsed.UserCode) == "" || strings.TrimSpace(parsed.VerificationURI) == "" {
		return nil, errors.New("github device code response is incomplete")
	}
	if parsed.ExpiresIn <= 0 {
		parsed.ExpiresIn = 900
	}
	if parsed.Interval <= 0 {
		parsed.Interval = 5
	}

	sessionID, err := newSessionID()
	if err != nil {
		return nil, err
	}
	createdAt := time.Now()
	expiresAt := createdAt.Add(time.Duration(parsed.ExpiresIn) * time.Second)
	if err := s.store.Set(ctx, sessionID, &GitHubDeviceSession{
		AccountID:          account.ID,
		AccountConcurrency: account.Concurrency,
		ProxyURL:           proxyURL,
		ClientID:           clientID,
		Scope:              scope,
		DeviceCode:         parsed.DeviceCode,
		ExpiresAtUnix:      expiresAt.Unix(),
		IntervalSeconds:    parsed.Interval,
		CreatedAtUnix:      createdAt.Unix(),
	}, time.Until(expiresAt)); err != nil {
		return nil, fmt.Errorf("persist github device session failed: %w", err)
	}

	return &GitHubDeviceAuthStartResult{
		SessionID:               sessionID,
		UserCode:                parsed.UserCode,
		VerificationURI:         parsed.VerificationURI,
		VerificationURIComplete: strings.TrimSpace(parsed.VerificationURIComplete),
		ExpiresIn:               parsed.ExpiresIn,
		IntervalSeconds:         parsed.Interval,
	}, nil
}

func (s *GitHubDeviceAuthService) Poll(ctx context.Context, accountID int64, sessionID string) (*GitHubDeviceAuthPollResult, error) {
	if s == nil || s.httpUpstream == nil {
		return nil, errors.New("github device auth service not configured")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, errors.New("session_id is required")
	}

	sess, ok, err := s.store.Get(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load device auth session failed: %w", err)
	}
	if !ok || sess == nil {
		return nil, errors.New("device auth session not found")
	}
	if sess.AccountID != accountID {
		return nil, errors.New("device auth session does not belong to this account")
	}
	now := time.Now()
	expiresAt := time.Unix(sess.ExpiresAtUnix, 0)
	if now.After(expiresAt) {
		_ = s.store.Delete(ctx, sessionID)
		return &GitHubDeviceAuthPollResult{Status: "error", Error: "expired_token", ErrorDesc: "device code expired"}, nil
	}

	form := url.Values{}
	form.Set("client_id", sess.ClientID)
	form.Set("device_code", sess.DeviceCode)
	form.Set("grant_type", gitHubDeviceGrantType)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gitHubAccessTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	req.Header.Set("accept", "application/json")
	if strings.TrimSpace(req.Header.Get("user-agent")) == "" {
		req.Header.Set("user-agent", githubCopilotDefaultUserAgent())
	}

	resp, err := s.httpUpstream.Do(req, sess.ProxyURL, sess.AccountID, sess.AccountConcurrency)
	if err != nil {
		return nil, fmt.Errorf("github device token poll failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, gitHubDeviceAuthMaxBodyLen))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("github device token poll failed: status=%d body=%s", resp.StatusCode, sanitizeUpstreamErrorMessage(string(body)))
	}

	var parsed struct {
		AccessToken      string `json:"access_token"`
		TokenType        string `json:"token_type"`
		Scope            string `json:"scope"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
		Interval         int64  `json:"interval"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse github device token response: %w", err)
	}

	if strings.TrimSpace(parsed.AccessToken) != "" {
		_ = s.store.Delete(ctx, sessionID)
		return &GitHubDeviceAuthPollResult{
			Status:      "success",
			AccessToken: strings.TrimSpace(parsed.AccessToken),
			TokenType:   strings.TrimSpace(parsed.TokenType),
			Scope:       strings.TrimSpace(parsed.Scope),
		}, nil
	}

	errCode := strings.TrimSpace(parsed.Error)
	if errCode == "" {
		return &GitHubDeviceAuthPollResult{Status: "error", Error: "unknown_error", ErrorDesc: "unexpected response"}, nil
	}

	if errCode == "authorization_pending" {
		return &GitHubDeviceAuthPollResult{Status: "pending", IntervalSeconds: sess.IntervalSeconds}, nil
	}
	if errCode == "slow_down" {
		sess.IntervalSeconds = sess.IntervalSeconds + int64(gitHubDeviceAuthSlowDownIncrement/time.Second)
		if parsed.Interval > 0 {
			sess.IntervalSeconds = parsed.Interval
		}
		if err := s.store.Set(ctx, sessionID, sess, time.Until(expiresAt)); err != nil {
			return nil, fmt.Errorf("persist slow_down interval failed: %w", err)
		}
		return &GitHubDeviceAuthPollResult{Status: "pending", IntervalSeconds: sess.IntervalSeconds, Error: errCode, ErrorDesc: strings.TrimSpace(parsed.ErrorDescription)}, nil
	}
	if errCode == "expired_token" || errCode == "access_denied" {
		_ = s.store.Delete(ctx, sessionID)
		return &GitHubDeviceAuthPollResult{Status: "error", Error: errCode, ErrorDesc: strings.TrimSpace(parsed.ErrorDescription)}, nil
	}

	return &GitHubDeviceAuthPollResult{Status: "error", Error: errCode, ErrorDesc: strings.TrimSpace(parsed.ErrorDescription)}, nil
}

func (s *GitHubDeviceAuthService) Cancel(ctx context.Context, accountID int64, sessionID string) bool {
	if s == nil {
		return false
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	sess, ok, err := s.store.Get(ctx, sessionID)
	if err != nil {
		return false
	}
	if !ok || sess == nil {
		return false
	}
	now := time.Now()
	expiresAt := time.Unix(sess.ExpiresAtUnix, 0)
	if now.After(expiresAt) {
		_ = s.store.Delete(ctx, sessionID)
		return false
	}
	if sess.AccountID != accountID {
		return false
	}
	if err := s.store.Delete(ctx, sessionID); err != nil {
		return false
	}
	return true
}

func newSessionID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
