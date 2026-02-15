package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type testHTTPUpstream struct {
	client *http.Client
}

func (t *testHTTPUpstream) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	if t.client == nil {
		t.client = http.DefaultClient
	}
	return t.client.Do(req)
}

func (t *testHTTPUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, enableTLSFingerprint bool) (*http.Response, error) {
	if t.client == nil {
		t.client = http.DefaultClient
	}
	return t.client.Do(req)
}

func TestConvertClaudeMessagesToOpenAIResponsesInput_ToolUseAndResult(t *testing.T) {
	messages := []any{
		map[string]any{
			"role": "assistant",
			"content": []any{
				map[string]any{
					"type":  "tool_use",
					"id":    "toolu_1",
					"name":  "get_weather",
					"input": map[string]any{"city": "SF"},
				},
			},
		},
		map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{
					"type":        "tool_result",
					"tool_use_id": "toolu_1",
					"content": []any{
						map[string]any{"type": "text", "text": "sunny"},
					},
				},
			},
		},
	}

	out, err := convertClaudeMessagesToOpenAIResponsesInput(messages)
	require.NoError(t, err)
	require.Len(t, out, 2)

	call, ok := out[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function_call", call["type"])
	require.Equal(t, "toolu_1", call["call_id"])
	require.Equal(t, "get_weather", call["name"])

	args, ok := call["arguments"].(string)
	require.True(t, ok)
	var parsedArgs map[string]any
	require.NoError(t, json.Unmarshal([]byte(args), &parsedArgs))
	require.Equal(t, "SF", parsedArgs["city"])

	callOut, ok := out[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function_call_output", callOut["type"])
	require.Equal(t, "toolu_1", callOut["call_id"])
	require.Equal(t, "get_weather", callOut["name"])
	require.Equal(t, "sunny", callOut["output"])
}

func TestConvertClaudeMessagesToOpenAIResponsesInput_ImageBlock(t *testing.T) {
	messages := []any{
		map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{
					"type": "image",
					"source": map[string]any{
						"type":       "base64",
						"media_type": "image/png",
						"data":       "aGVsbG8=",
					},
				},
			},
		},
	}

	out, err := convertClaudeMessagesToOpenAIResponsesInput(messages)
	require.NoError(t, err)
	require.Len(t, out, 1)

	msg, ok := out[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "message", msg["type"])
	require.Equal(t, "user", msg["role"])

	content, ok := msg["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 1)
	block, ok := content[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "input_image", block["type"])

	imageURL, ok := block["image_url"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "data:image/png;base64,aGVsbG8=", imageURL["url"])
}

func TestConvertOpenAIResponsesJSONToClaude_TextOnly(t *testing.T) {
	resp := map[string]any{
		"id": "resp_123",
		"output": []any{
			map[string]any{
				"type": "message",
				"content": []any{
					map[string]any{"type": "output_text", "text": "hi"},
				},
			},
		},
		"usage": map[string]any{
			"input_tokens":  5,
			"output_tokens": 7,
			"input_tokens_details": map[string]any{
				"cached_tokens": 2,
			},
		},
	}
	b, _ := json.Marshal(resp)

	claudeResp, usage, stopReason, err := convertOpenAIResponsesJSONToClaude(b, "gpt-5.2")
	require.NoError(t, err)
	require.Equal(t, "end_turn", stopReason)
	require.NotNil(t, usage)
	require.Equal(t, 5, usage.InputTokens)
	require.Equal(t, 7, usage.OutputTokens)
	require.Equal(t, 2, usage.CacheReadInputTokens)

	require.Equal(t, "message", claudeResp["type"])
	require.Equal(t, "assistant", claudeResp["role"])
	require.Equal(t, "gpt-5.2", claudeResp["model"])
	require.Equal(t, "end_turn", claudeResp["stop_reason"])

	content, ok := claudeResp["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 1)
	block, ok := content[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "text", block["type"])
	require.Equal(t, "hi", block["text"])
}

func TestConvertOpenAIResponsesJSONToClaude_ToolCall(t *testing.T) {
	resp := map[string]any{
		"id": "resp_456",
		"output": []any{
			map[string]any{
				"type":      "function_call",
				"call_id":   "call_1",
				"name":      "get_weather",
				"arguments": "{\"city\":\"SF\"}",
			},
		},
		"usage": map[string]any{
			"input_tokens":  1,
			"output_tokens": 2,
		},
	}
	b, _ := json.Marshal(resp)

	claudeResp, _, stopReason, err := convertOpenAIResponsesJSONToClaude(b, "gpt-5.2")
	require.NoError(t, err)
	require.Equal(t, "tool_use", stopReason)
	require.Equal(t, "tool_use", claudeResp["stop_reason"])

	content, ok := claudeResp["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 1)
	block, ok := content[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "tool_use", block["type"])
	require.Equal(t, "call_1", block["id"])
	require.Equal(t, "get_weather", block["name"])

	input, ok := block["input"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "SF", input["city"])
}

func TestExtractOpenAIResponsesInputTokens_OK(t *testing.T) {
	resp := map[string]any{
		"usage": map[string]any{
			"input_tokens": 42,
		},
	}
	b, _ := json.Marshal(resp)

	inputTokens, err := extractOpenAIResponsesInputTokens(b)
	require.NoError(t, err)
	require.Equal(t, 42, inputTokens)
}

func TestExtractOpenAIResponsesInputTokens_MissingUsage(t *testing.T) {
	resp := map[string]any{}
	b, _ := json.Marshal(resp)

	_, err := extractOpenAIResponsesInputTokens(b)
	require.Error(t, err)
}

func TestExtractOpenAIResponsesInputTokens_MissingInputTokens(t *testing.T) {
	resp := map[string]any{
		"usage": map[string]any{},
	}
	b, _ := json.Marshal(resp)

	_, err := extractOpenAIResponsesInputTokens(b)
	require.Error(t, err)
}

func TestOpenAIMessagesCompatService_ForwardCountTokens_JSONUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	type capturedReq struct {
		Path    string
		Headers http.Header
		Body    []byte
		JSON    map[string]any
	}
	capCh := make(chan capturedReq, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		capCh <- capturedReq{Path: r.URL.Path, Headers: r.Header.Clone(), Body: body, JSON: m}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"usage": map[string]any{"input_tokens": 123},
		})
	}))
	defer server.Close()

	cfg := &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false, AllowInsecureHTTP: true}}}
	upstream := &testHTTPUpstream{}
	openaiSvc := NewOpenAIGatewayService(nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, upstream, nil, nil, nil)
	compat := NewOpenAIMessagesCompatService(openaiSvc)

	account := &Account{
		ID:       1,
		Name:     "openai-test",
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":    "sk-test",
			"base_url":   server.URL,
			"user_agent": "sub2api-test",
		},
		Concurrency: 1,
	}

	claudeReq := map[string]any{
		"model":      "gpt-5.2",
		"max_tokens": 10,
		"system":     "You are helpful.",
		"messages": []any{
			map[string]any{"role": "user", "content": "hello"},
		},
	}
	body, err := json.Marshal(claudeReq)
	require.NoError(t, err)
	parsed, err := ParseGatewayRequest(body, "")
	require.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", bytes.NewReader(body))

	err = compat.ForwardCountTokens(context.Background(), c, account, parsed)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `{"input_tokens":123}`, w.Body.String())

	select {
	case cap := <-capCh:
		require.Equal(t, "/v1/responses", cap.Path)
		require.Equal(t, "Bearer sk-test", cap.Headers.Get("authorization"))
		require.Equal(t, false, cap.JSON["store"])
		require.Equal(t, false, cap.JSON["stream"])
		require.Equal(t, "You are helpful.", cap.JSON["instructions"])
		require.Equal(t, float64(1), cap.JSON["max_output_tokens"])
		require.NotNil(t, cap.JSON["input"])
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for upstream request")
	}
}

func TestOpenAIMessagesCompatService_ForwardCountTokens_SSEUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	capCh := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capCh <- struct{}{}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":77}}}\n"))
		_, _ = w.Write([]byte("data: [DONE]\n"))
	}))
	defer server.Close()

	cfg := &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false, AllowInsecureHTTP: true}}}
	upstream := &testHTTPUpstream{}
	openaiSvc := NewOpenAIGatewayService(nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, upstream, nil, nil, nil)
	compat := NewOpenAIMessagesCompatService(openaiSvc)

	account := &Account{
		ID:       1,
		Name:     "openai-test",
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": server.URL,
		},
		Concurrency: 1,
	}

	claudeReq := map[string]any{
		"model":      "gpt-5.2",
		"max_tokens": 10,
		"messages": []any{
			map[string]any{"role": "user", "content": "hello"},
		},
	}
	body, err := json.Marshal(claudeReq)
	require.NoError(t, err)
	parsed, err := ParseGatewayRequest(body, "")
	require.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", bytes.NewReader(body))

	err = compat.ForwardCountTokens(context.Background(), c, account, parsed)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `{"input_tokens":77}`, w.Body.String())

	select {
	case <-capCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for upstream request")
	}
}

func TestOpenAIMessagesCompatService_Forward_ForwardsMaxTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)

	type capturedReq struct {
		Path    string
		Headers http.Header
		JSON    map[string]any
	}
	capCh := make(chan capturedReq, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		capCh <- capturedReq{Path: r.URL.Path, Headers: r.Header.Clone(), JSON: m}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "resp_1",
			"output": []any{
				map[string]any{
					"type": "message",
					"content": []any{
						map[string]any{"type": "output_text", "text": "hi"},
					},
				},
			},
			"usage": map[string]any{"input_tokens": 5, "output_tokens": 7},
		})
	}))
	defer server.Close()

	cfg := &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false, AllowInsecureHTTP: true}}}
	upstream := &testHTTPUpstream{}
	openaiSvc := NewOpenAIGatewayService(nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, upstream, nil, nil, nil)
	compat := NewOpenAIMessagesCompatService(openaiSvc)

	account := &Account{
		ID:       1,
		Name:     "openai-test",
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": server.URL,
		},
		Concurrency: 1,
	}

	claudeReq := map[string]any{
		"model":      "gpt-5.2",
		"max_tokens": 99,
		"system":     "You are helpful.",
		"messages": []any{
			map[string]any{"role": "user", "content": "hello"},
		},
	}
	body, err := json.Marshal(claudeReq)
	require.NoError(t, err)
	parsed, err := ParseGatewayRequest(body, "")
	require.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

	result, err := compat.Forward(context.Background(), c, account, parsed)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusOK, w.Code)

	select {
	case cap := <-capCh:
		require.Equal(t, "/v1/responses", cap.Path)
		require.Equal(t, "Bearer sk-test", cap.Headers.Get("authorization"))
		require.Equal(t, float64(99), cap.JSON["max_output_tokens"])
		require.Equal(t, "You are helpful.", cap.JSON["instructions"])
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for upstream request")
	}
}
