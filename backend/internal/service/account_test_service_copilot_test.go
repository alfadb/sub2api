//go:build unit

package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

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
