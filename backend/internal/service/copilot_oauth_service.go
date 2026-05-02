package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	copilotClientID         = "Ov23li8tweQw6odWQebz"
	copilotOAuthScope       = "read:user"
	copilotDeviceCodeURL    = "https://github.com/login/device/code"
	copilotAccessTokenURL   = "https://github.com/login/oauth/access_token"
	copilotOAuthHTTPTimeout = 30 * time.Second
)

// CopilotOAuthService handles GitHub Device Code Flow for Copilot accounts,
// precisely mimicking opencode's OAuth request behavior.
type CopilotOAuthService struct {
	httpClient     *http.Client
	versionService *OpenCodeVersionService
}

func NewCopilotOAuthService(versionService *OpenCodeVersionService) *CopilotOAuthService {
	return &CopilotOAuthService{
		httpClient:     &http.Client{Timeout: copilotOAuthHTTPTimeout},
		versionService: versionService,
	}
}

// CopilotDeviceCodeResponse is the response from GitHub's device code endpoint.
type CopilotDeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	Interval        int    `json:"interval"`
}

// CopilotTokenResponse is the response from GitHub's access token endpoint.
type CopilotTokenResponse struct {
	AccessToken string `json:"access_token,omitempty"`
	Error       string `json:"error,omitempty"`
	Interval    int    `json:"interval,omitempty"`
}

// InitiateDeviceCode starts the GitHub Device Code Flow.
// POST https://github.com/login/device/code with exact opencode headers.
func (s *CopilotOAuthService) InitiateDeviceCode(ctx context.Context) (*CopilotDeviceCodeResponse, error) {
	body, err := json.Marshal(map[string]string{
		"client_id": copilotClientID,
		"scope":     copilotOAuthScope,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal device code request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, copilotDeviceCodeURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create device code request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", s.versionService.UserAgent())

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("device code request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read device code response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device code request returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result CopilotDeviceCodeResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode device code response: %w", err)
	}
	return &result, nil
}

// PollAccessToken polls GitHub for an access token using the device code.
// POST https://github.com/login/oauth/access_token with exact opencode headers.
//
// Returns:
//   - (token_response, nil) on success or recognized polling states
//   - (nil, error) on transport/decode failures
//
// Caller should inspect CopilotTokenResponse.Error:
//   - "": success, AccessToken is populated
//   - "authorization_pending": user hasn't authorized yet, keep polling
//   - "slow_down": increase interval by CopilotTokenResponse.Interval
//   - other: terminal error, stop polling
func (s *CopilotOAuthService) PollAccessToken(ctx context.Context, deviceCode string) (*CopilotTokenResponse, error) {
	body, err := json.Marshal(map[string]string{
		"client_id":   copilotClientID,
		"device_code": deviceCode,
		"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
	})
	if err != nil {
		return nil, fmt.Errorf("marshal token request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, copilotAccessTokenURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", s.versionService.UserAgent())

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token request returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result CopilotTokenResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	return &result, nil
}
