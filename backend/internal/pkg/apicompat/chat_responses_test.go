package apicompat

import (
	"encoding/json"
	"testing"
)

// ---------------------------------------------------------------------------
// ChatToResponses
// ---------------------------------------------------------------------------

func TestChatToResponses(t *testing.T) {
	tests := []struct {
		name  string
		input ChatRequest
		check func(t *testing.T, got *ResponsesRequest)
	}{
		{
			name: "system message converts to system input item",
			input: ChatRequest{
				Model:    "gpt-4",
				Messages: []ChatMessage{{Role: "system", Content: json.RawMessage(`"You are helpful."`)}},
			},
			check: func(t *testing.T, got *ResponsesRequest) {
				items := unmarshalInputItems(t, got.Input)
				if len(items) != 1 {
					t.Fatalf("expected 1 item, got %d", len(items))
				}
				if items[0].Role != "system" {
					t.Errorf("role = %q, want %q", items[0].Role, "system")
				}
				text := unmarshalString(t, items[0].Content)
				if text != "You are helpful." {
					t.Errorf("content = %q, want %q", text, "You are helpful.")
				}
			},
		},
		{
			name: "user message converts to user input item",
			input: ChatRequest{
				Model:    "gpt-4",
				Messages: []ChatMessage{{Role: "user", Content: json.RawMessage(`"Hello"`)}},
			},
			check: func(t *testing.T, got *ResponsesRequest) {
				items := unmarshalInputItems(t, got.Input)
				if len(items) != 1 {
					t.Fatalf("expected 1 item, got %d", len(items))
				}
				if items[0].Role != "user" {
					t.Errorf("role = %q, want %q", items[0].Role, "user")
				}
				text := unmarshalString(t, items[0].Content)
				if text != "Hello" {
					t.Errorf("content = %q, want %q", text, "Hello")
				}
			},
		},
		{
			name: "assistant text message converts to assistant input item",
			input: ChatRequest{
				Model:    "gpt-4",
				Messages: []ChatMessage{{Role: "assistant", Content: json.RawMessage(`"Sure thing."`)}},
			},
			check: func(t *testing.T, got *ResponsesRequest) {
				items := unmarshalInputItems(t, got.Input)
				if len(items) != 1 {
					t.Fatalf("expected 1 item, got %d", len(items))
				}
				if items[0].Role != "assistant" {
					t.Errorf("role = %q, want %q", items[0].Role, "assistant")
				}
			},
		},
		{
			name: "assistant with tool_calls converts to function_call items",
			input: ChatRequest{
				Model: "gpt-4",
				Messages: []ChatMessage{{
					Role: "assistant",
					ToolCalls: []ChatToolCall{{
						ID:   "call_1",
						Type: "function",
						Function: ChatFunctionCall{
							Name:      "get_weather",
							Arguments: `{"city":"Tokyo"}`,
						},
					}},
				}},
			},
			check: func(t *testing.T, got *ResponsesRequest) {
				items := unmarshalInputItems(t, got.Input)
				if len(items) != 1 {
					t.Fatalf("expected 1 item, got %d", len(items))
				}
				if items[0].Type != "function_call" {
					t.Errorf("type = %q, want %q", items[0].Type, "function_call")
				}
				if items[0].CallID != "call_1" {
					t.Errorf("call_id = %q, want %q", items[0].CallID, "call_1")
				}
				if items[0].Name != "get_weather" {
					t.Errorf("name = %q, want %q", items[0].Name, "get_weather")
				}
				if items[0].Arguments != `{"city":"Tokyo"}` {
					t.Errorf("arguments = %q, want %q", items[0].Arguments, `{"city":"Tokyo"}`)
				}
			},
		},
		{
			name: "tool message converts to function_call_output item",
			input: ChatRequest{
				Model: "gpt-4",
				Messages: []ChatMessage{{
					Role:       "tool",
					Content:    json.RawMessage(`"sunny, 25C"`),
					ToolCallID: "call_1",
				}},
			},
			check: func(t *testing.T, got *ResponsesRequest) {
				items := unmarshalInputItems(t, got.Input)
				if len(items) != 1 {
					t.Fatalf("expected 1 item, got %d", len(items))
				}
				if items[0].Type != "function_call_output" {
					t.Errorf("type = %q, want %q", items[0].Type, "function_call_output")
				}
				if items[0].CallID != "call_1" {
					t.Errorf("call_id = %q, want %q", items[0].CallID, "call_1")
				}
				if items[0].Output != "sunny, 25C" {
					t.Errorf("output = %q, want %q", items[0].Output, "sunny, 25C")
				}
			},
		},
		{
			name: "max_tokens maps to max_output_tokens with floor of 128",
			input: ChatRequest{
				Model:     "gpt-4",
				Messages:  []ChatMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
				MaxTokens: intPtr(50),
			},
			check: func(t *testing.T, got *ResponsesRequest) {
				if got.MaxOutputTokens == nil {
					t.Fatal("max_output_tokens is nil")
				}
				if *got.MaxOutputTokens != 128 {
					t.Errorf("max_output_tokens = %d, want 128 (floor)", *got.MaxOutputTokens)
				}
			},
		},
		{
			name: "max_tokens above floor passes through",
			input: ChatRequest{
				Model:     "gpt-4",
				Messages:  []ChatMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
				MaxTokens: intPtr(4096),
			},
			check: func(t *testing.T, got *ResponsesRequest) {
				if got.MaxOutputTokens == nil {
					t.Fatal("max_output_tokens is nil")
				}
				if *got.MaxOutputTokens != 4096 {
					t.Errorf("max_output_tokens = %d, want 4096", *got.MaxOutputTokens)
				}
			},
		},
		{
			name: "tools passthrough function type only",
			input: ChatRequest{
				Model:    "gpt-4",
				Messages: []ChatMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
				Tools: []ChatTool{
					{Type: "function", Function: ChatFunction{Name: "fn1", Description: "desc1", Parameters: json.RawMessage(`{"type":"object"}`)}},
					{Type: "code_interpreter", Function: ChatFunction{Name: "ci"}},
				},
			},
			check: func(t *testing.T, got *ResponsesRequest) {
				if len(got.Tools) != 1 {
					t.Fatalf("tools len = %d, want 1", len(got.Tools))
				}
				if got.Tools[0].Name != "fn1" {
					t.Errorf("tool name = %q, want %q", got.Tools[0].Name, "fn1")
				}
				if got.Tools[0].Type != "function" {
					t.Errorf("tool type = %q, want %q", got.Tools[0].Type, "function")
				}
			},
		},
		{
			name: "stream flag is passed through",
			input: ChatRequest{
				Model:    "gpt-4",
				Messages: []ChatMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
				Stream:   true,
			},
			check: func(t *testing.T, got *ResponsesRequest) {
				if !got.Stream {
					t.Error("stream = false, want true")
				}
			},
		},
		{
			name: "include always contains reasoning.encrypted_content",
			input: ChatRequest{
				Model:    "gpt-4",
				Messages: []ChatMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
			},
			check: func(t *testing.T, got *ResponsesRequest) {
				if len(got.Include) != 1 || got.Include[0] != "reasoning.encrypted_content" {
					t.Errorf("include = %v, want [reasoning.encrypted_content]", got.Include)
				}
			},
		},
		{
			name: "store is always false",
			input: ChatRequest{
				Model:    "gpt-4",
				Messages: []ChatMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
			},
			check: func(t *testing.T, got *ResponsesRequest) {
				if got.Store == nil {
					t.Fatal("store is nil, want false")
				}
				if *got.Store {
					t.Error("store = true, want false")
				}
			},
		},
		{
			name: "empty messages",
			input: ChatRequest{
				Model:    "gpt-4",
				Messages: []ChatMessage{},
			},
			check: func(t *testing.T, got *ResponsesRequest) {
				items := unmarshalInputItems(t, got.Input)
				if len(items) != 0 {
					t.Errorf("expected 0 items, got %d", len(items))
				}
			},
		},
		{
			name: "unknown role defaults to user",
			input: ChatRequest{
				Model:    "gpt-4",
				Messages: []ChatMessage{{Role: "developer", Content: json.RawMessage(`"do stuff"`)}},
			},
			check: func(t *testing.T, got *ResponsesRequest) {
				items := unmarshalInputItems(t, got.Input)
				if len(items) != 1 {
					t.Fatalf("expected 1 item, got %d", len(items))
				}
				if items[0].Role != "user" {
					t.Errorf("role = %q, want %q (unknown defaults to user)", items[0].Role, "user")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ChatToResponses(&tt.input)
			if err != nil {
				t.Fatalf("ChatToResponses returned error: %v", err)
			}
			tt.check(t, got)
		})
	}
}

// ---------------------------------------------------------------------------
// ResponsesToChat
// ---------------------------------------------------------------------------

func TestResponsesToChat(t *testing.T) {
	tests := []struct {
		name  string
		input ResponsesResponse
		check func(t *testing.T, got *ChatResponse)
	}{
		{
			name: "message output with text converts to content",
			input: ResponsesResponse{
				ID:     "resp_1",
				Model:  "gpt-4",
				Status: "completed",
				Output: []ResponsesOutput{{
					Type: "message",
					Content: []ResponsesContentPart{
						{Type: "output_text", Text: "Hello world"},
					},
				}},
			},
			check: func(t *testing.T, got *ChatResponse) {
				if len(got.Choices) != 1 {
					t.Fatalf("choices len = %d, want 1", len(got.Choices))
				}
				text := unmarshalString(t, got.Choices[0].Message.Content)
				if text != "Hello world" {
					t.Errorf("content = %q, want %q", text, "Hello world")
				}
			},
		},
		{
			name: "function call output converts to tool_calls",
			input: ResponsesResponse{
				ID:     "resp_2",
				Model:  "gpt-4",
				Status: "completed",
				Output: []ResponsesOutput{{
					Type:      "function_call",
					CallID:    "call_abc",
					Name:      "search",
					Arguments: `{"q":"test"}`,
				}},
			},
			check: func(t *testing.T, got *ChatResponse) {
				msg := got.Choices[0].Message
				if len(msg.ToolCalls) != 1 {
					t.Fatalf("tool_calls len = %d, want 1", len(msg.ToolCalls))
				}
				tc := msg.ToolCalls[0]
				if tc.ID != "call_abc" {
					t.Errorf("tool_call id = %q, want %q", tc.ID, "call_abc")
				}
				if tc.Type != "function" {
					t.Errorf("tool_call type = %q, want %q", tc.Type, "function")
				}
				if tc.Function.Name != "search" {
					t.Errorf("function name = %q, want %q", tc.Function.Name, "search")
				}
				if tc.Function.Arguments != `{"q":"test"}` {
					t.Errorf("function args = %q, want %q", tc.Function.Arguments, `{"q":"test"}`)
				}
			},
		},
		{
			name: "reasoning output items are ignored",
			input: ResponsesResponse{
				ID:     "resp_3",
				Model:  "gpt-4",
				Status: "completed",
				Output: []ResponsesOutput{
					{Type: "reasoning", EncryptedContent: "secret"},
					{Type: "message", Content: []ResponsesContentPart{{Type: "output_text", Text: "visible"}}},
				},
			},
			check: func(t *testing.T, got *ChatResponse) {
				text := unmarshalString(t, got.Choices[0].Message.Content)
				if text != "visible" {
					t.Errorf("content = %q, want %q", text, "visible")
				}
			},
		},
		{
			name: "status completed maps to finish_reason stop",
			input: ResponsesResponse{
				ID:     "resp_4",
				Model:  "gpt-4",
				Status: "completed",
				Output: []ResponsesOutput{{
					Type:    "message",
					Content: []ResponsesContentPart{{Type: "output_text", Text: "done"}},
				}},
			},
			check: func(t *testing.T, got *ChatResponse) {
				if got.Choices[0].FinishReason != "stop" {
					t.Errorf("finish_reason = %q, want %q", got.Choices[0].FinishReason, "stop")
				}
			},
		},
		{
			name: "status incomplete with max_output_tokens maps to finish_reason length",
			input: ResponsesResponse{
				ID:                "resp_5",
				Model:             "gpt-4",
				Status:            "incomplete",
				IncompleteDetails: &ResponsesIncompleteDetails{Reason: "max_output_tokens"},
				Output: []ResponsesOutput{{
					Type:    "message",
					Content: []ResponsesContentPart{{Type: "output_text", Text: "truncated"}},
				}},
			},
			check: func(t *testing.T, got *ChatResponse) {
				if got.Choices[0].FinishReason != "length" {
					t.Errorf("finish_reason = %q, want %q", got.Choices[0].FinishReason, "length")
				}
			},
		},
		{
			name: "usage mapping input_tokens to prompt_tokens",
			input: ResponsesResponse{
				ID:     "resp_6",
				Model:  "gpt-4",
				Status: "completed",
				Output: []ResponsesOutput{{
					Type:    "message",
					Content: []ResponsesContentPart{{Type: "output_text", Text: "ok"}},
				}},
				Usage: &ResponsesUsage{
					InputTokens:  100,
					OutputTokens: 50,
					TotalTokens:  150,
				},
			},
			check: func(t *testing.T, got *ChatResponse) {
				if got.Usage == nil {
					t.Fatal("usage is nil")
				}
				if got.Usage.PromptTokens != 100 {
					t.Errorf("prompt_tokens = %d, want 100", got.Usage.PromptTokens)
				}
				if got.Usage.CompletionTokens != 50 {
					t.Errorf("completion_tokens = %d, want 50", got.Usage.CompletionTokens)
				}
				if got.Usage.TotalTokens != 150 {
					t.Errorf("total_tokens = %d, want 150", got.Usage.TotalTokens)
				}
			},
		},
		{
			name: "multiple text parts are joined",
			input: ResponsesResponse{
				ID:     "resp_7",
				Model:  "gpt-4",
				Status: "completed",
				Output: []ResponsesOutput{{
					Type: "message",
					Content: []ResponsesContentPart{
						{Type: "output_text", Text: "Hello "},
						{Type: "output_text", Text: "world"},
					},
				}},
			},
			check: func(t *testing.T, got *ChatResponse) {
				text := unmarshalString(t, got.Choices[0].Message.Content)
				if text != "Hello world" {
					t.Errorf("content = %q, want %q", text, "Hello world")
				}
			},
		},
		{
			name: "tool calls present overrides stop to tool_calls",
			input: ResponsesResponse{
				ID:     "resp_8",
				Model:  "gpt-4",
				Status: "completed",
				Output: []ResponsesOutput{
					{Type: "message", Content: []ResponsesContentPart{{Type: "output_text", Text: "let me check"}}},
					{Type: "function_call", CallID: "call_x", Name: "lookup", Arguments: `{}`},
				},
			},
			check: func(t *testing.T, got *ChatResponse) {
				if got.Choices[0].FinishReason != "tool_calls" {
					t.Errorf("finish_reason = %q, want %q", got.Choices[0].FinishReason, "tool_calls")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResponsesToChat(&tt.input)
			if got == nil {
				t.Fatal("ResponsesToChat returned nil")
			}
			tt.check(t, got)
		})
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func intPtr(v int) *int { return &v }

func unmarshalInputItems(t *testing.T, raw json.RawMessage) []ResponsesInputItem {
	t.Helper()
	var items []ResponsesInputItem
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatalf("failed to unmarshal input items: %v", err)
	}
	return items
}

func unmarshalString(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("failed to unmarshal string: %v (raw=%s)", err, raw)
	}
	return s
}
