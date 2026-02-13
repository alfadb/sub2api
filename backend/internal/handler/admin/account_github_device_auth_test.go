package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type githubDeviceAuthAdminService struct {
	*stubAdminService
	account       service.Account
	updatedInput  *service.UpdateAccountInput
	updatedAtUnix int64
}

func (s *githubDeviceAuthAdminService) GetAccount(_ context.Context, id int64) (*service.Account, error) {
	acc := s.account
	acc.ID = id
	return &acc, nil
}

func (s *githubDeviceAuthAdminService) UpdateAccount(_ context.Context, id int64, input *service.UpdateAccountInput) (*service.Account, error) {
	s.updatedInput = input
	s.updatedAtUnix = time.Now().Unix()
	acc := s.account
	acc.ID = id
	if input != nil {
		if input.Credentials != nil {
			acc.Credentials = input.Credentials
		}
		if strings.TrimSpace(input.Name) != "" {
			acc.Name = input.Name
		}
	}
	return &acc, nil
}

type fakeGitHubHTTPUpstreamForAdminHandler struct{}

func (f *fakeGitHubHTTPUpstreamForAdminHandler) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	switch req.URL.String() {
	case "https://github.com/login/device/code":
		body := `{"device_code":"dc1","user_code":"uc1","verification_uri":"https://github.com/login/device","verification_uri_complete":"https://github.com/login/device?user_code=uc1","expires_in":900,"interval":5}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	case "https://github.com/login/oauth/access_token":
		body := `{"access_token":"gho_xxx","token_type":"bearer","scope":"read:user"}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	default:
		return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(`{"error":"not_found"}`))}, nil
	}
}

func (f *fakeGitHubHTTPUpstreamForAdminHandler) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ bool) (*http.Response, error) {
	return f.Do(req, proxyURL, accountID, accountConcurrency)
}

func setupAccountGitHubDeviceAuthRouter(t *testing.T) (*gin.Engine, *githubDeviceAuthAdminService) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	adminSvc := &githubDeviceAuthAdminService{
		stubAdminService: newStubAdminService(),
		account: service.Account{
			ID:     3,
			Name:   "copilot",
			Type:   service.AccountTypeAPIKey,
			Status: service.StatusActive,
			Credentials: map[string]any{
				"base_url": "https://api.githubcopilot.com",
				"api_key":  "sk-test",
			},
		},
	}

	store := service.NewInMemoryGitHubDeviceSessionStore()
	upstream := &fakeGitHubHTTPUpstreamForAdminHandler{}
	deviceAuth := service.NewGitHubDeviceAuthService(store, upstream)

	accountHandler := NewAccountHandler(
		adminSvc,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		deviceAuth,
		nil,
		nil,
		nil,
	)

	router.POST("/api/v1/admin/accounts/:id/github/device/start", accountHandler.StartGitHubDeviceAuth)
	router.POST("/api/v1/admin/accounts/:id/github/device/poll", accountHandler.PollGitHubDeviceAuth)
	router.POST("/api/v1/admin/accounts/:id/github/device/cancel", accountHandler.CancelGitHubDeviceAuth)

	return router, adminSvc
}

func TestAccountGitHubDeviceAuth_StartPollStoresToken(t *testing.T) {
	router, adminSvc := setupAccountGitHubDeviceAuthRouter(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/3/github/device/start", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var startEnv struct {
		Code int `json:"code"`
		Data struct {
			SessionID string `json:"session_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &startEnv))
	require.Equal(t, 0, startEnv.Code)
	require.NotEmpty(t, startEnv.Data.SessionID)
	pollBody, _ := json.Marshal(map[string]any{"session_id": startEnv.Data.SessionID})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/3/github/device/poll", bytes.NewReader(pollBody))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	require.NotNil(t, adminSvc.updatedInput)
	gh, ok := adminSvc.updatedInput.Credentials["github_token"].(string)
	require.True(t, ok)
	require.Equal(t, "gho_xxx", gh)
}

func TestAccountGitHubDeviceAuth_Cancel(t *testing.T) {
	router, _ := setupAccountGitHubDeviceAuthRouter(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/3/github/device/start", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var startEnv struct {
		Code int `json:"code"`
		Data struct {
			SessionID string `json:"session_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &startEnv))
	require.NotEmpty(t, startEnv.Data.SessionID)
	cancelBody, _ := json.Marshal(map[string]any{"session_id": startEnv.Data.SessionID})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/3/github/device/cancel", bytes.NewReader(cancelBody))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}
