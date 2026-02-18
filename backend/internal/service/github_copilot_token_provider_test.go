//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type httpUpstreamStubCopilotTokenExchange403 struct{}

func (s httpUpstreamStubCopilotTokenExchange403) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	if req == nil || req.URL == nil || req.URL.String() != githubCopilotTokenExchangeURL {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("unexpected request")),
		}, nil
	}
	body := `{"can_signup_for_limited":false,"error_details":{"url":"https://support.github.com?editor={EDITOR}","message":"Contact Support. You are currently logged in as alfadb.","title":"Contact Support","notification_id":"feature_flag_blocked"},"message":"Resource not accessible by integration."}`
	return &http.Response{
		StatusCode: http.StatusForbidden,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func (s httpUpstreamStubCopilotTokenExchange403) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, enableTLSFingerprint bool) (*http.Response, error) {
	return nil, errors.New("unexpected")
}

func TestGitHubCopilotTokenProvider_PrefersErrorDetailsMessage(t *testing.T) {
	p := NewGitHubCopilotTokenProvider(nil, httpUpstreamStubCopilotTokenExchange403{})

	account := &Account{
		ID:          1,
		Platform:    PlatformCopilot,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"base_url":     "https://api.githubcopilot.com",
			"github_token": "dummy",
		},
	}

	_, err := p.GetAccessToken(context.Background(), account)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Contact Support")
}
