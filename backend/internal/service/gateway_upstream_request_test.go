//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Scenario: OAuth / setup-token 凭据只能走签发方协议，API Key 与其他凭据类型不受限
func TestAnthropicMessagesCredentialAllowed(t *testing.T) {
	cases := []struct {
		platform string
		typ      string
		allowed  bool
	}{
		{PlatformAnthropic, AccountTypeOAuth, true},
		{PlatformAnthropic, AccountTypeSetupToken, true},
		{PlatformAnthropic, AccountTypeServiceAccount, true},
		{PlatformAnthropic, AccountTypeBedrock, true},
		{PlatformTypeSafe, AccountTypeOAuth, true},
		{PlatformOpenAI, AccountTypeAPIKey, true},
		{PlatformDeepseek, AccountTypeAPIKey, true},
		{PlatformOpenAI, AccountTypeOAuth, false},
		{PlatformOpenAI, AccountTypeSetupToken, false},
		{PlatformGrok, AccountTypeOAuth, false},
		{PlatformGemini, AccountTypeOAuth, false},
		{PlatformAntigravity, AccountTypeOAuth, false},
	}
	for _, tc := range cases {
		allowed := anthropicMessagesCredentialAllowed(&Account{ID: 1, Platform: tc.platform, Type: tc.typ})
		require.Equal(t, tc.allowed, allowed, "%s/%s", tc.platform, tc.typ)
	}
}

// Scenario: 回归 —— OpenAI OAuth 账号误入 Anthropic 管线时，在构建请求前即被拒绝
// （ChatGPT token 不会发往 api.anthropic.com），且只让 handler 换号、不处罚账号
func TestGatewayBuildUpstreamRequestRejectsForeignOAuthWithoutPenalty(t *testing.T) {
	account := &Account{ID: 25, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	body := []byte(`{"model":"gpt-6-astra","messages":[]}`)

	req, _, err := (&GatewayService{}).buildUpstreamRequest(context.Background(), nil, account, body, "chatgpt-token", "oauth", "gpt-6-astra", true, false)
	require.Nil(t, req)

	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))
	require.True(t, failoverErr.ShouldRetryNextAccount())
	require.False(t, failoverErr.RetryableOnSameAccount, "same-account retry would end in temp-unschedulable")
	require.False(t, failoverErr.IsCredentialFailure())
	require.Equal(t, anthropicMessagesForeignOAuthReason, failoverErr.Reason)
}

// Scenario: generic Anthropic 链（buildUpstreamRequest）对 zhipu 账号也必须把
// 上游请求头改成 ZCode Desktop 指纹：最终 beta 已写入、session 头已同步后注入，
// 账号 header_overrides 仍是最后一步。
func TestBuildUpstreamRequest_ZhipuPresentsZCodeIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("User-Agent", "claude-cli/1.0.0 (external, cli)")
	req.Header.Set("x-app", "cli")
	req.Header.Set("anthropic-beta", "claude-code-20250219,context-1m-2025-08-07")
	req.Header.Set("accept-language", "en-US")
	req.Header.Set("x-claude-code-session-id", "abc")
	c.Request = req

	account := &Account{
		ID:       4105,
		Platform: PlatformZhipu,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://open.bigmodel.cn/api/anthropic",
		},
	}
	body := []byte(`{"model":"glm-4.7","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`)

	svc := &GatewayService{cfg: rawChatCompletionsTestConfig()}
	upstreamReq, _, err := svc.buildUpstreamRequest(
		context.Background(), c, account, body, "sk-test", "api_key",
		"glm-4.7", false, false,
	)
	require.NoError(t, err)

	require.Equal(t,
		"ZCode/"+zcodeAppVersion()+" ai-sdk/anthropic/3.0.81 ai-sdk/provider-utils/4.0.27 runtime/node.js/24",
		getHeaderRaw(upstreamReq.Header, "user-agent"))
	require.Empty(t, getHeaderRaw(upstreamReq.Header, "x-app"))
	// undici 默认头：客户端传 en-US，出站固定为 *。
	require.Equal(t, "*", getHeaderRaw(upstreamReq.Header, "accept-language"))
	require.Equal(t, "cors", getHeaderRaw(upstreamReq.Header, "sec-fetch-mode"))
	require.Equal(t, "context-1m-2025-08-07", getHeaderRaw(upstreamReq.Header, "anthropic-beta"))
	require.Equal(t, zcodeAppVersion(), getHeaderRaw(upstreamReq.Header, "X-Zcode-App-Version"))
	require.Equal(t, zcodeSessionIDForAccount(account.ID, "abc"),
		getHeaderRaw(upstreamReq.Header, "X-Session-Id"))
}

// Scenario: count_tokens 对不匹配凭据按「平台不支持」返回 404，不取 token、不出站
func TestForwardCountTokensRejectsForeignOAuthAsUnsupported(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)

	account := &Account{ID: 25, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	parsed := &ParsedRequest{Model: "gpt-6-astra", Body: NewRequestBodyRef([]byte(`{"model":"gpt-6-astra","messages":[]}`))}

	err := (&GatewayService{}).ForwardCountTokens(context.Background(), c, account, parsed)

	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, w.Code)
}
