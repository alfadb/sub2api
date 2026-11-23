//go:build unit

package service

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// zhipu 账号出站 ZCode Desktop 指纹注入（applyZCodeIdentityHeaders）的单元回归。

func zcodeIdentityTestAccount(platform string) *Account {
	return &Account{
		ID:          4105,
		Name:        "cn-identity",
		Platform:    platform,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"},
	}
}

// claudeCLISampleHeaders 模拟 claude-cli 入站残留的典型身份/协议头组合。
func claudeCLISampleHeaders() http.Header {
	h := http.Header{}
	h.Set("User-Agent", "claude-cli/2.1.81 (external, cli)")
	h.Set("x-app", "cli")
	h.Set("X-Stainless-Lang", "js")
	h.Set("X-Stainless-Runtime", "node")
	h.Set("Accept-Language", "en-US,en;q=0.9")
	h.Set("Sec-Fetch-Mode", "cors")
	h.Set("anthropic-version", "2023-06-01")
	h.Set("Authorization", "Bearer sk-test")
	return h
}

func TestApplyZCodeIdentityHeaders_NonZhipuNoop(t *testing.T) {
	// 非 zhipu（kimi）账号：header 必须完全不变。
	h := claudeCLISampleHeaders()
	before := h.Clone()
	applyZCodeIdentityHeaders(h, zcodeIdentityTestAccount(PlatformKimi), zcodeIdentityAnthropic, "conv-1")
	require.Equal(t, before, h)
}

func TestApplyZCodeIdentityHeaders_ZhipuOverwritesClientFingerprint(t *testing.T) {
	h := claudeCLISampleHeaders()
	applyZCodeIdentityHeaders(h, zcodeIdentityTestAccount(PlatformZhipu), zcodeIdentityAnthropic, "conv-1")

	// claude-cli 身份被剥离 / 覆盖，UA 以 ZCode 版本开头。
	require.True(t, strings.HasPrefix(getHeaderRaw(h, "user-agent"), "ZCode/"+zcodeAppVersion))
	require.Empty(t, getHeaderRaw(h, "x-app"))
	require.Empty(t, getHeaderRaw(h, "x-stainless-lang"))
	require.Empty(t, getHeaderRaw(h, "x-stainless-runtime"))
	// 浏览器身份残留同样被剥掉。
	require.Empty(t, getHeaderRaw(h, "accept-language"))
	require.Empty(t, getHeaderRaw(h, "sec-fetch-mode"))

	// 固定指纹头。
	require.Equal(t, zcodeAppVersion, getHeaderRaw(h, "X-Zcode-App-Version"))
	require.Equal(t, "glm", getHeaderRaw(h, "X-Zcode-Agent"))
	require.Equal(t, "Z Code@electron", getHeaderRaw(h, "X-Title"))

	// Referer 必须落在字面 key HTTP-Referer 上（Header.Set 会写成 Http-Referer）。
	require.Equal(t, []string{"https://zcode.z.ai"}, h["HTTP-Referer"])
	require.NotContains(t, h, "Http-Referer")

	// ZCode v4.0.0 新增的 5 个指纹头，字面取值与大小写都要精确。
	require.Equal(t, "production", getHeaderRaw(h, "X-Release-Channel"))
	require.Equal(t, "zh-CN", getHeaderRaw(h, "X-Client-Language"))
	require.Equal(t, "Asia/Shanghai", getHeaderRaw(h, "X-Client-Timezone"))
	require.Equal(t, []string{"main"}, h["x-zcode-session-type"])
	require.Equal(t, []string{"main_turn"}, h["x-zcode-query-source"])

	// 不动鉴权与协议头。
	require.Equal(t, "Bearer sk-test", h.Get("Authorization"))
	require.Equal(t, "2023-06-01", getHeaderRaw(h, "anthropic-version"))

	// UUID 均为合法形态。
	for _, key := range []string{"X-Zcode-Trace-Id", "X-Request-Id", "X-Query-Id", "X-Session-Id"} {
		_, err := uuid.Parse(getHeaderRaw(h, key))
		require.NoError(t, err, key)
	}
}

func TestApplyZCodeIdentityHeaders_SessionStablePerSessionKey(t *testing.T) {
	account := zcodeIdentityTestAccount(PlatformZhipu)

	h1, h2 := http.Header{}, http.Header{}
	applyZCodeIdentityHeaders(h1, account, zcodeIdentityOpenAI, "conv-a")
	applyZCodeIdentityHeaders(h2, account, zcodeIdentityOpenAI, "conv-a")

	// 同一账号同一会话：session-id 与 trace-id 恒定；request/query-id 逐请求变化。
	require.Equal(t, getHeaderRaw(h1, "X-Session-Id"), getHeaderRaw(h2, "X-Session-Id"))
	require.Equal(t, getHeaderRaw(h1, "X-Zcode-Trace-Id"), getHeaderRaw(h2, "X-Zcode-Trace-Id"))
	require.NotEqual(t, getHeaderRaw(h1, "X-Request-Id"), getHeaderRaw(h2, "X-Request-Id"))
	require.NotEqual(t, getHeaderRaw(h1, "X-Query-Id"), getHeaderRaw(h2, "X-Query-Id"))
	require.NotEqual(t, getHeaderRaw(h1, "X-Session-Id"), getHeaderRaw(h1, "X-Zcode-Trace-Id"))

	// 不同会话：session-id 与 trace-id 都必须变化。
	h3 := http.Header{}
	applyZCodeIdentityHeaders(h3, account, zcodeIdentityOpenAI, "conv-b")
	require.NotEqual(t, getHeaderRaw(h1, "X-Session-Id"), getHeaderRaw(h3, "X-Session-Id"))
	require.NotEqual(t, getHeaderRaw(h1, "X-Zcode-Trace-Id"), getHeaderRaw(h3, "X-Zcode-Trace-Id"))

	// sessionKey 为空：X-Session-Id 与改造前逐字节一致（纯账号派生）。
	h4 := http.Header{}
	applyZCodeIdentityHeaders(h4, account, zcodeIdentityAnthropic, "")
	wantLegacy := uuid.NewSHA1(zcodeSessionIDNamespace, []byte(strconv.FormatInt(account.ID, 10))).String()
	require.Equal(t, wantLegacy, getHeaderRaw(h4, "X-Session-Id"))
	require.NotEqual(t, wantLegacy, getHeaderRaw(h4, "X-Zcode-Trace-Id"))
}

func TestApplyZCodeIdentityHeaders_FiltersAnthropicBetaIdentityTokens(t *testing.T) {
	account := zcodeIdentityTestAccount(PlatformZhipu)

	t.Run("drops identity tokens and keeps the rest", func(t *testing.T) {
		h := http.Header{}
		h.Set("Anthropic-Beta", "claude-code-20250219,oauth-2025-04-20,context-1m-2025-08-07")
		applyZCodeIdentityHeaders(h, account, zcodeIdentityAnthropic, "conv-1")
		require.Equal(t, "context-1m-2025-08-07", getHeaderRaw(h, "anthropic-beta"))
		// 写回保持原来的头名形态（canonical casing）。
		require.Equal(t, []string{"context-1m-2025-08-07"}, h["Anthropic-Beta"])
	})

	t.Run("all identity tokens removes header", func(t *testing.T) {
		h := http.Header{}
		h.Set("anthropic-beta", "claude-code-20250219,oauth-2025-04-20")
		applyZCodeIdentityHeaders(h, account, zcodeIdentityAnthropic, "conv-1")
		require.Empty(t, getHeaderRaw(h, "anthropic-beta"))
		require.NotContains(t, h, "anthropic-beta")
		require.NotContains(t, h, "Anthropic-Beta")
	})

	t.Run("absent header is not added", func(t *testing.T) {
		h := http.Header{}
		applyZCodeIdentityHeaders(h, account, zcodeIdentityAnthropic, "conv-1")
		require.NotContains(t, h, "anthropic-beta")
		require.NotContains(t, h, "Anthropic-Beta")
	})
}

func TestApplyZCodeIdentityHeaders_UAVariesByKind(t *testing.T) {
	account := zcodeIdentityTestAccount(PlatformZhipu)
	hOpenAI, hAnthropic := http.Header{}, http.Header{}
	applyZCodeIdentityHeaders(hOpenAI, account, zcodeIdentityOpenAI, "conv-1")
	applyZCodeIdentityHeaders(hAnthropic, account, zcodeIdentityAnthropic, "conv-1")

	// ZCode v4.0.0 两条 SDK 栈的精确 UA。
	require.Equal(t,
		"ZCode/4.0.0 ai-sdk/openai-compatible/2.0.60 ai-sdk/provider-utils/4.0.39 runtime/node.js/24",
		getHeaderRaw(hOpenAI, "User-Agent"))
	require.Equal(t,
		"ZCode/4.0.0 ai-sdk/anthropic/3.0.81 ai-sdk/provider-utils/4.0.27 runtime/node.js/24",
		getHeaderRaw(hAnthropic, "User-Agent"))
	require.NotEqual(t, getHeaderRaw(hOpenAI, "User-Agent"), getHeaderRaw(hAnthropic, "User-Agent"))
}

func TestApplyZCodeIdentityHeaders_NilSafety(t *testing.T) {
	// h / account 任一为 nil 都必须安全 no-op。
	require.NotPanics(t, func() {
		applyZCodeIdentityHeaders(nil, zcodeIdentityTestAccount(PlatformZhipu), zcodeIdentityOpenAI, "conv-1")
		applyZCodeIdentityHeaders(http.Header{}, nil, zcodeIdentityOpenAI, "conv-1")
		applyZCodeIdentityHeaders(nil, nil, zcodeIdentityAnthropic, "conv-1")
	})
	h := http.Header{}
	applyZCodeIdentityHeaders(h, nil, zcodeIdentityOpenAI, "")
	require.Empty(t, h)
}

// zcodeTestGinContext 用 httptest 构造带指定请求头的 gin context。
func zcodeTestGinContext(t *testing.T, headers map[string]string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	return c
}

func TestZCodeConversationKey(t *testing.T) {
	anthropicBody := []byte(`{"metadata":{"user_id":"{\"device_id\":\"dev\",\"session_id\":\"sess-body\"}"},` +
		`"prompt_cache_key":"pc-key","messages":[{"role":"user","content":"hi"}]}`)

	t.Run("explicit session header wins", func(t *testing.T) {
		c := zcodeTestGinContext(t, map[string]string{"X-Claude-Code-Session-Id": "sess-explicit"})
		require.Equal(t, "sess-explicit", zcodeConversationKey(c, anthropicBody))
	})

	t.Run("metadata user id session when no header", func(t *testing.T) {
		c := zcodeTestGinContext(t, nil)
		require.Equal(t, "sess-body", zcodeConversationKey(c, anthropicBody))
	})

	t.Run("prompt_cache_key when no session signals", func(t *testing.T) {
		c := zcodeTestGinContext(t, nil)
		body := []byte(`{"prompt_cache_key":"pc-1","messages":[{"role":"user","content":"hi"}]}`)
		require.Equal(t, "pc-1", zcodeConversationKey(c, body))
	})

	t.Run("content seed fallback", func(t *testing.T) {
		c := zcodeTestGinContext(t, nil)
		body := []byte(`{"model":"glm-4.7","messages":[{"role":"user","content":"hi"}]}`)
		require.True(t, strings.HasPrefix(zcodeConversationKey(c, body), contentSessionSeedPrefix))
	})

	t.Run("api key fallback", func(t *testing.T) {
		c := zcodeTestGinContext(t, nil)
		c.Set("api_key", &APIKey{ID: 42})
		require.Equal(t, "apikey:42", zcodeConversationKey(c, nil))
	})

	t.Run("nil context and body are safe", func(t *testing.T) {
		require.Empty(t, zcodeConversationKey(nil, nil))
	})
}
