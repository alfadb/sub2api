package apicompat

import (
	"encoding/json"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Test helpers used by streaming_test.go and this file
// ---------------------------------------------------------------------------

func assertEventType(t *testing.T, events []AnthropicStreamEvent, idx int, want string) {
	t.Helper()
	if idx >= len(events) {
		t.Fatalf("assertEventType: index %d out of range (len=%d)", idx, len(events))
	}
	if events[idx].Type != want {
		t.Errorf("events[%d].Type = %q, want %q", idx, events[idx].Type, want)
	}
}

func eventTypes(events []AnthropicStreamEvent) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Type
	}
	return out
}

func assertContains(t *testing.T, types []string, want string) {
	t.Helper()
	for _, s := range types {
		if strings.Contains(s, want) || s == want {
			return
		}
	}
	t.Errorf("expected %v to contain %q", types, want)
}

// ---------------------------------------------------------------------------
// AnthropicToChat
// ---------------------------------------------------------------------------

func TestAnthropicToChat(t *testing.T) {
	temp := 0.7
	topP := 0.9

	tests := []struct {
		name  string
		req   *AnthropicRequest
		check func(t *testing.T, out *ChatRequest)
	}{
		{
			name: "basic user and assistant messages",
			req: &AnthropicRequest{
				Model:     "claude-sonnet-4",
				MaxTokens: 1024,
				Messages: []AnthropicMessage{
					{Role: "user", Content: json.RawMessage(`"Hello"`)},
					{Role: "assistant", Content: json.RawMessage(`"Hi there"`)},
				},
			},
			check: func(t *testing.T, out *ChatRequest) {
				if out.Model != "claude-sonnet-4" {
					t.Errorf("model = %q, want %q", out.Model, "claude-sonnet-4")
				}
				if len(out.Messages) != 2 {
					t.Fatalf("messages len = %d, want 2", len(out.Messages))
				}
				if out.Messages[0].Role != "user" {
					t.Errorf("msg[0].role = %q, want %q", out.Messages[0].Role, "user")
				}
				if out.Messages[1].Role != "assistant" {
					t.Errorf("msg[1].role = %q, want %q", out.Messages[1].Role, "assistant")
				}
			},
		},
		{
			name: "system prompt string becomes system message",
			req: &AnthropicRequest{
				Model:     "claude-sonnet-4",
				MaxTokens: 1024,
				System:    json.RawMessage(`"You are helpful."`),
				Messages: []AnthropicMessage{
					{Role: "user", Content: json.RawMessage(`"Hi"`)},
				},
			},
			check: func(t *testing.T, out *ChatRequest) {
				if len(out.Messages) < 2 {
					t.Fatalf("messages len = %d, want >= 2", len(out.Messages))
				}
				if out.Messages[0].Role != "system" {
					t.Errorf("msg[0].role = %q, want %q", out.Messages[0].Role, "system")
				}
				var text string
				if err := json.Unmarshal(out.Messages[0].Content, &text); err != nil {
					t.Fatalf("unmarshal system content: %v", err)
				}
				if text != "You are helpful." {
					t.Errorf("system text = %q, want %q", text, "You are helpful.")
				}
			},
		},
		{
			name: "system prompt array of blocks becomes system message",
			req: &AnthropicRequest{
				Model:     "claude-sonnet-4",
				MaxTokens: 1024,
				System:    json.RawMessage(`[{"type":"text","text":"Block one"},{"type":"text","text":"Block two"}]`),
				Messages: []AnthropicMessage{
					{Role: "user", Content: json.RawMessage(`"Hi"`)},
				},
			},
			check: func(t *testing.T, out *ChatRequest) {
				if out.Messages[0].Role != "system" {
					t.Errorf("msg[0].role = %q, want %q", out.Messages[0].Role, "system")
				}
				var text string
				if err := json.Unmarshal(out.Messages[0].Content, &text); err != nil {
					t.Fatalf("unmarshal system content: %v", err)
				}
				want := "Block one\n\nBlock two"
				if text != want {
					t.Errorf("system text = %q, want %q", text, want)
				}
			},
		},
		{
			name: "tool use blocks convert to tool_calls",
			req: &AnthropicRequest{
				Model:     "claude-sonnet-4",
				MaxTokens: 1024,
				Messages: []AnthropicMessage{
					{Role: "assistant", Content: json.RawMessage(`[{"type":"text","text":"Let me check."},{"type":"tool_use","id":"tu_1","name":"get_weather","input":{"city":"NYC"}}]`)},
				},
			},
			check: func(t *testing.T, out *ChatRequest) {
				if len(out.Messages) != 1 {
					t.Fatalf("messages len = %d, want 1", len(out.Messages))
				}
				msg := out.Messages[0]
				if msg.Role != "assistant" {
					t.Errorf("role = %q, want %q", msg.Role, "assistant")
				}
				if len(msg.ToolCalls) != 1 {
					t.Fatalf("tool_calls len = %d, want 1", len(msg.ToolCalls))
				}
				tc := msg.ToolCalls[0]
				if tc.ID != "tu_1" {
					t.Errorf("tool_call id = %q, want %q", tc.ID, "tu_1")
				}
				if tc.Type != "function" {
					t.Errorf("tool_call type = %q, want %q", tc.Type, "function")
				}
				if tc.Function.Name != "get_weather" {
					t.Errorf("tool_call name = %q, want %q", tc.Function.Name, "get_weather")
				}
				if tc.Function.Arguments != `{"city":"NYC"}` {
					t.Errorf("tool_call args = %q, want %q", tc.Function.Arguments, `{"city":"NYC"}`)
				}
			},
		},
		{
			name: "tool result blocks convert to tool messages",
			req: &AnthropicRequest{
				Model:     "claude-sonnet-4",
				MaxTokens: 1024,
				Messages: []AnthropicMessage{
					{Role: "user", Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"tu_1","content":"Sunny, 72F"}]`)},
				},
			},
			check: func(t *testing.T, out *ChatRequest) {
				if len(out.Messages) != 1 {
					t.Fatalf("messages len = %d, want 1", len(out.Messages))
				}
				msg := out.Messages[0]
				if msg.Role != "tool" {
					t.Errorf("role = %q, want %q", msg.Role, "tool")
				}
				if msg.ToolCallID != "tu_1" {
					t.Errorf("tool_call_id = %q, want %q", msg.ToolCallID, "tu_1")
				}
			},
		},
		{
			name: "thinking blocks are ignored",
			req: &AnthropicRequest{
				Model:     "claude-sonnet-4",
				MaxTokens: 1024,
				Messages: []AnthropicMessage{
					{Role: "assistant", Content: json.RawMessage(`[{"type":"thinking","thinking":"internal reasoning"},{"type":"text","text":"Final answer"}]`)},
				},
			},
			check: func(t *testing.T, out *ChatRequest) {
				msg := out.Messages[0]
				var text string
				if err := json.Unmarshal(msg.Content, &text); err != nil {
					t.Fatalf("unmarshal content: %v", err)
				}
				if text != "Final answer" {
					t.Errorf("content = %q, want %q", text, "Final answer")
				}
				if len(msg.ToolCalls) != 0 {
					t.Errorf("tool_calls len = %d, want 0", len(msg.ToolCalls))
				}
			},
		},
		{
			name: "temperature top_p max_tokens passed through",
			req: &AnthropicRequest{
				Model:       "claude-sonnet-4",
				MaxTokens:   2048,
				Temperature: &temp,
				TopP:        &topP,
				Messages: []AnthropicMessage{
					{Role: "user", Content: json.RawMessage(`"Hi"`)},
				},
			},
			check: func(t *testing.T, out *ChatRequest) {
				if out.Temperature == nil || *out.Temperature != 0.7 {
					t.Errorf("temperature = %v, want 0.7", out.Temperature)
				}
				if out.TopP == nil || *out.TopP != 0.9 {
					t.Errorf("top_p = %v, want 0.9", out.TopP)
				}
				if out.MaxTokens == nil || *out.MaxTokens != 2048 {
					t.Errorf("max_tokens = %v, want 2048", out.MaxTokens)
				}
			},
		},
		{
			name: "model name is preserved as-is",
			req: &AnthropicRequest{
				Model:     "claude-sonnet-4-20250514",
				MaxTokens: 1024,
				Messages: []AnthropicMessage{
					{Role: "user", Content: json.RawMessage(`"Hi"`)},
				},
			},
			check: func(t *testing.T, out *ChatRequest) {
				// Model name is no longer normalized; model_mapping handles conversion
				if out.Model != "claude-sonnet-4-20250514" {
					t.Errorf("model = %q, want %q", out.Model, "claude-sonnet-4-20250514")
				}
			},
		},
		{
			name: "empty messages array",
			req: &AnthropicRequest{
				Model:     "claude-sonnet-4",
				MaxTokens: 1024,
				Messages:  []AnthropicMessage{},
			},
			check: func(t *testing.T, out *ChatRequest) {
				if len(out.Messages) != 0 {
					t.Errorf("messages len = %d, want 0", len(out.Messages))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := AnthropicToChat(tt.req)
			if err != nil {
				t.Fatalf("AnthropicToChat() error = %v", err)
			}
			tt.check(t, out)
		})
	}
}

// ---------------------------------------------------------------------------
// ChatToAnthropic
// ---------------------------------------------------------------------------

func TestChatToAnthropic(t *testing.T) {
	tests := []struct {
		name  string
		resp  *ChatResponse
		model string
		check func(t *testing.T, out *AnthropicResponse)
	}{
		{
			name: "basic text response converts to content blocks",
			resp: &ChatResponse{
				ID: "chatcmpl-1",
				Choices: []ChatChoice{
					{
						Message:      ChatMessage{Role: "assistant", Content: json.RawMessage(`"Hello!"`)},
						FinishReason: "stop",
					},
				},
			},
			model: "claude-sonnet-4",
			check: func(t *testing.T, out *AnthropicResponse) {
				if out.ID != "chatcmpl-1" {
					t.Errorf("id = %q, want %q", out.ID, "chatcmpl-1")
				}
				if out.Type != "message" {
					t.Errorf("type = %q, want %q", out.Type, "message")
				}
				if out.Role != "assistant" {
					t.Errorf("role = %q, want %q", out.Role, "assistant")
				}
				if out.Model != "claude-sonnet-4" {
					t.Errorf("model = %q, want %q", out.Model, "claude-sonnet-4")
				}
				found := false
				for _, b := range out.Content {
					if b.Type == "text" && b.Text == "Hello!" {
						found = true
					}
				}
				if !found {
					t.Errorf("expected text block with 'Hello!', got %+v", out.Content)
				}
			},
		},
		{
			name: "tool calls convert to tool_use blocks",
			resp: &ChatResponse{
				ID: "chatcmpl-2",
				Choices: []ChatChoice{
					{
						Message: ChatMessage{
							Role: "assistant",
							ToolCalls: []ChatToolCall{
								{
									ID:   "call_1",
									Type: "function",
									Function: ChatFunctionCall{
										Name:      "get_weather",
										Arguments: `{"city":"NYC"}`,
									},
								},
							},
						},
						FinishReason: "tool_calls",
					},
				},
			},
			model: "claude-sonnet-4",
			check: func(t *testing.T, out *AnthropicResponse) {
				var toolBlocks []AnthropicContentBlock
				for _, b := range out.Content {
					if b.Type == "tool_use" {
						toolBlocks = append(toolBlocks, b)
					}
				}
				if len(toolBlocks) != 1 {
					t.Fatalf("tool_use blocks = %d, want 1", len(toolBlocks))
				}
				tb := toolBlocks[0]
				if tb.ID != "call_1" {
					t.Errorf("tool_use id = %q, want %q", tb.ID, "call_1")
				}
				if tb.Name != "get_weather" {
					t.Errorf("tool_use name = %q, want %q", tb.Name, "get_weather")
				}
				if string(tb.Input) != `{"city":"NYC"}` {
					t.Errorf("tool_use input = %q, want %q", string(tb.Input), `{"city":"NYC"}`)
				}
			},
		},
		{
			name: "finish_reason stop maps to stop_reason end_turn",
			resp: &ChatResponse{
				ID: "chatcmpl-3",
				Choices: []ChatChoice{
					{
						Message:      ChatMessage{Role: "assistant", Content: json.RawMessage(`"Done"`)},
						FinishReason: "stop",
					},
				},
			},
			model: "claude-sonnet-4",
			check: func(t *testing.T, out *AnthropicResponse) {
				if out.StopReason != "end_turn" {
					t.Errorf("stop_reason = %q, want %q", out.StopReason, "end_turn")
				}
			},
		},
		{
			name: "finish_reason tool_calls maps to stop_reason tool_use",
			resp: &ChatResponse{
				ID: "chatcmpl-4",
				Choices: []ChatChoice{
					{
						Message:      ChatMessage{Role: "assistant"},
						FinishReason: "tool_calls",
					},
				},
			},
			model: "claude-sonnet-4",
			check: func(t *testing.T, out *AnthropicResponse) {
				if out.StopReason != "tool_use" {
					t.Errorf("stop_reason = %q, want %q", out.StopReason, "tool_use")
				}
			},
		},
		{
			name: "finish_reason length maps to stop_reason max_tokens",
			resp: &ChatResponse{
				ID: "chatcmpl-5",
				Choices: []ChatChoice{
					{
						Message:      ChatMessage{Role: "assistant", Content: json.RawMessage(`"truncated"`)},
						FinishReason: "length",
					},
				},
			},
			model: "claude-sonnet-4",
			check: func(t *testing.T, out *AnthropicResponse) {
				if out.StopReason != "max_tokens" {
					t.Errorf("stop_reason = %q, want %q", out.StopReason, "max_tokens")
				}
			},
		},
		{
			name: "usage mapping prompt_tokens to input_tokens",
			resp: &ChatResponse{
				ID: "chatcmpl-6",
				Choices: []ChatChoice{
					{
						Message:      ChatMessage{Role: "assistant", Content: json.RawMessage(`"ok"`)},
						FinishReason: "stop",
					},
				},
				Usage: &ChatUsage{
					PromptTokens:     100,
					CompletionTokens: 50,
					TotalTokens:      150,
				},
			},
			model: "claude-sonnet-4",
			check: func(t *testing.T, out *AnthropicResponse) {
				if out.Usage.InputTokens != 100 {
					t.Errorf("input_tokens = %d, want 100", out.Usage.InputTokens)
				}
				if out.Usage.OutputTokens != 50 {
					t.Errorf("output_tokens = %d, want 50", out.Usage.OutputTokens)
				}
			},
		},
		{
			name: "empty content produces fallback text block",
			resp: &ChatResponse{
				ID: "chatcmpl-7",
				Choices: []ChatChoice{
					{
						Message:      ChatMessage{Role: "assistant"},
						FinishReason: "stop",
					},
				},
			},
			model: "claude-sonnet-4",
			check: func(t *testing.T, out *AnthropicResponse) {
				if len(out.Content) == 0 {
					t.Fatal("content should not be empty")
				}
				if out.Content[0].Type != "text" {
					t.Errorf("content[0].type = %q, want %q", out.Content[0].Type, "text")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := ChatToAnthropic(tt.resp, tt.model)
			tt.check(t, out)
		})
	}
}
