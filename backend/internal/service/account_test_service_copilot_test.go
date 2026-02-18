//go:build unit

package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type httpUpstreamStubCopilotTest struct {
	doCalls        []string
	doWithTLSCalls []string
}

func (s *httpUpstreamStubCopilotTest) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	s.doCalls = append(s.doCalls, req.URL.String())
	if req.URL.String() == githubCopilotTokenExchangeURL {
		body := `{"token":"copilot-access-token","refresh_in":600}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusInternalServerError,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("unexpected Do url")),
	}, nil
}

func (s *httpUpstreamStubCopilotTest) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, enableTLSFingerprint bool) (*http.Response, error) {
	s.doWithTLSCalls = append(s.doWithTLSCalls, req.URL.String())
	if strings.HasSuffix(req.URL.String(), "/responses") {
		body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n" +
			"data: {\"type\":\"response.completed\"}\n"
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusInternalServerError,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("unexpected DoWithTLS url")),
	}, nil
}

func TestAccountTestService_CopilotTokenErrorIsSurfaced(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)

	svc := &AccountTestService{
		githubCopilotTokenProvider: NewGitHubCopilotTokenProvider(nil, nil),
	}

	account := &Account{
		ID:          1,
		Platform:    PlatformCopilot,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Credentials: map[string]any{},
	}

	err := svc.testOpenAIAccountConnection(c, account, "gpt-5.2")
	require.Error(t, err)

	body := rec.Body.String()
	require.Contains(t, body, "Failed to get Copilot access token")
	require.Contains(t, body, "github_token not found")
	require.NotContains(t, body, "No GitHub token or Copilot bearer token available")
}

func TestAccountTestService_CopilotEarlyAuthErrorsUseSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)

	svc := &AccountTestService{}

	account := &Account{
		ID:       1,
		Platform: PlatformCopilot,
		Type:     AccountTypeAPIKey,
		Status:   StatusActive,
		Credentials: map[string]any{
			"base_url":     "https://api.githubcopilot.com",
			"github_token": "dummy",
		},
	}

	err := svc.testOpenAIAccountConnection(c, account, "gpt-5")
	require.Error(t, err)
	require.Contains(t, rec.Body.String(), "data: ")
	require.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
}

func TestAccountTestService_CopilotNilTokenProviderStillUsesTokenExchange(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)

	upstream := &httpUpstreamStubCopilotTest{}

	svc := &AccountTestService{
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false, AllowInsecureHTTP: false}}},
		httpUpstream: upstream,
	}

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

	err := svc.testOpenAIAccountConnection(c, account, "gpt-5")
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, "test_complete")
	require.NotContains(t, body, "No GitHub token or Copilot bearer token available")
	require.GreaterOrEqual(t, len(upstream.doCalls), 1)
	require.Equal(t, githubCopilotTokenExchangeURL, upstream.doCalls[0])
}
