//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeHTTPUpstream struct {
	mu        sync.Mutex
	pollCount int
}

func (f *fakeHTTPUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	switch req.URL.String() {
	case gitHubDeviceCodeURL:
		body := `{"device_code":"dc1","user_code":"uc1","verification_uri":"https://github.com/login/device","verification_uri_complete":"https://github.com/login/device?user_code=uc1","expires_in":900,"interval":5}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	case gitHubAccessTokenURL:
		f.pollCount++
		if f.pollCount == 1 {
			body := `{"error":"authorization_pending","error_description":"pending"}`
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		}
		if f.pollCount == 2 {
			body := `{"error":"slow_down","error_description":"slow","interval":10}`
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		}
		body := `{"access_token":"gho_xxx","token_type":"bearer","scope":"read:user"}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	default:
		return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(`{"error":"not_found"}`))}, nil
	}
}

func (f *fakeHTTPUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ bool) (*http.Response, error) {
	return f.Do(req, proxyURL, accountID, accountConcurrency)
}

func TestGitHubDeviceAuthService_StartPollFlow(t *testing.T) {
	store := NewInMemoryGitHubDeviceSessionStore()
	upstream := &fakeHTTPUpstream{}
	svc := NewGitHubDeviceAuthService(store, upstream)
	account := &Account{ID: 123, Type: AccountTypeAPIKey, Concurrency: 3}

	start, err := svc.Start(context.Background(), account, "", "")
	require.NoError(t, err)
	require.NotNil(t, start)
	require.NotEmpty(t, start.SessionID)
	require.Equal(t, int64(900), start.ExpiresIn)
	require.Equal(t, int64(5), start.IntervalSeconds)
	require.Equal(t, "uc1", start.UserCode)

	res, err := svc.Poll(context.Background(), account.ID, start.SessionID)
	require.NoError(t, err)
	require.Equal(t, "pending", res.Status)
	require.Equal(t, int64(5), res.IntervalSeconds)

	res, err = svc.Poll(context.Background(), account.ID, start.SessionID)
	require.NoError(t, err)
	require.Equal(t, "pending", res.Status)
	require.Equal(t, int64(10), res.IntervalSeconds)
	require.Equal(t, "slow_down", res.Error)

	res, err = svc.Poll(context.Background(), account.ID, start.SessionID)
	require.NoError(t, err)
	require.Equal(t, "success", res.Status)
	require.Equal(t, "gho_xxx", res.AccessToken)

	_, ok, err := store.Get(context.Background(), start.SessionID)
	require.NoError(t, err)
	require.False(t, ok)
}
