package apicompat

import (
	"testing"
)

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

// ---------------------------------------------------------------------------
// ChatChunkToAnthropicEvents
// ---------------------------------------------------------------------------

func TestChatChunkToAnthropicEvents(t *testing.T) {
	t.Run("first chunk with role emits message_start and content_block_start", func(t *testing.T) {
		state := NewChatToAnthropicStreamState()
		chunk := &ChatStreamChunk{
			ID:    "chatcmpl-1",
			Model: "gpt-4",
			Choices: []ChatStreamChoice{{
				Index: 0,
				Delta: ChatStreamDelta{Role: "assistant", Content: strPtr("Hi")},
			}},
		}

		events := ChatChunkToAnthropicEvents(chunk, state)

		if !state.MessageStartSent {
			t.Errorf("MessageStartSent should be true")
		}

		// Expect: message_start, content_block_start (text), content_block_delta (text_delta)
		assertEventType(t, events, 0, "message_start")
		if events[0].Message == nil {
			t.Fatalf("message_start should have Message")
		}
		if events[0].Message.ID != "chatcmpl-1" {
			t.Errorf("message ID = %q, want %q", events[0].Message.ID, "chatcmpl-1")
		}
		if events[0].Message.Role != "assistant" {
			t.Errorf("message role = %q, want %q", events[0].Message.Role, "assistant")
		}

		assertEventType(t, events, 1, "content_block_start")
		if events[1].ContentBlock == nil || events[1].ContentBlock.Type != "text" {
			t.Errorf("content_block_start should be type=text")
		}

		assertEventType(t, events, 2, "content_block_delta")
		if events[2].Delta == nil || events[2].Delta.Type != "text_delta" || events[2].Delta.Text != "Hi" {
			t.Errorf("content_block_delta should have text_delta with text=Hi")
		}
	})

	t.Run("content delta emits content_block_delta with text_delta", func(t *testing.T) {
		state := NewChatToAnthropicStreamState()
		state.MessageStartSent = true
		state.ContentBlockOpen = true
		state.ContentBlockIndex = 0

		chunk := &ChatStreamChunk{
			Choices: []ChatStreamChoice{{
				Index: 0,
				Delta: ChatStreamDelta{Content: strPtr("world")},
			}},
		}

		events := ChatChunkToAnthropicEvents(chunk, state)

		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		assertEventType(t, events, 0, "content_block_delta")
		if events[0].Delta == nil || events[0].Delta.Type != "text_delta" {
			t.Errorf("expected text_delta type")
		}
		if events[0].Delta.Text != "world" {
			t.Errorf("delta text = %q, want %q", events[0].Delta.Text, "world")
		}
	})

	t.Run("tool call delta emits content_block_start and content_block_delta", func(t *testing.T) {
		state := NewChatToAnthropicStreamState()
		state.MessageStartSent = true

		chunk := &ChatStreamChunk{
			Choices: []ChatStreamChoice{{
				Index: 0,
				Delta: ChatStreamDelta{
					ToolCalls: []ChatToolCall{{
						Index: intPtr(0),
						ID:    "call_abc",
						Type:  "function",
						Function: ChatFunctionCall{
							Name:      "get_weather",
							Arguments: `{"loc`,
						},
					}},
				},
			}},
		}

		events := ChatChunkToAnthropicEvents(chunk, state)

		// Should have content_block_start (tool_use) + content_block_delta (input_json_delta)
		foundStart := false
		foundDelta := false
		for _, e := range events {
			if e.Type == "content_block_start" && e.ContentBlock != nil && e.ContentBlock.Type == "tool_use" {
				foundStart = true
				if e.ContentBlock.ID != "call_abc" {
					t.Errorf("tool_use ID = %q, want %q", e.ContentBlock.ID, "call_abc")
				}
				if e.ContentBlock.Name != "get_weather" {
					t.Errorf("tool_use name = %q, want %q", e.ContentBlock.Name, "get_weather")
				}
			}
			if e.Type == "content_block_delta" && e.Delta != nil && e.Delta.Type == "input_json_delta" {
				foundDelta = true
				if e.Delta.PartialJSON != `{"loc` {
					t.Errorf("partial_json = %q, want %q", e.Delta.PartialJSON, `{"loc`)
				}
			}
		}
		if !foundStart {
			t.Errorf("missing content_block_start for tool_use")
		}
		if !foundDelta {
			t.Errorf("missing content_block_delta for input_json_delta")
		}
	})
	t.Run("finish reason stop emits content_block_stop + message_delta + message_stop", func(t *testing.T) {
		state := NewChatToAnthropicStreamState()
		state.MessageStartSent = true
		state.ContentBlockOpen = true
		state.ContentBlockIndex = 0

		fr := "stop"
		chunk := &ChatStreamChunk{
			Choices: []ChatStreamChoice{{
				Index:        0,
				Delta:        ChatStreamDelta{},
				FinishReason: &fr,
			}},
			Usage: &ChatUsage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
		}

		events := ChatChunkToAnthropicEvents(chunk, state)

		types := eventTypes(events)
		assertContains(t, types, "content_block_stop")
		assertContains(t, types, "message_delta")
		assertContains(t, types, "message_stop")

		if !state.MessageStopSent {
			t.Errorf("MessageStopSent should be true")
		}

		// Check message_delta has stop_reason=end_turn
		for _, e := range events {
			if e.Type == "message_delta" {
				if e.Delta == nil || e.Delta.StopReason != "end_turn" {
					t.Errorf("message_delta stop_reason = %q, want %q", e.Delta.StopReason, "end_turn")
				}
				if e.Usage == nil || e.Usage.OutputTokens != 20 {
					t.Errorf("message_delta usage output_tokens = %d, want 20", e.Usage.OutputTokens)
				}
			}
		}
	})

	t.Run("finish reason tool_calls maps to stop_reason tool_use", func(t *testing.T) {
		state := NewChatToAnthropicStreamState()
		state.MessageStartSent = true
		// Simulate an open tool block
		state.ContentBlockOpen = true
		state.ContentBlockIndex = 0
		state.ToolCalls[0] = trackedToolCall{ID: "call_1", Name: "fn", AnthropicBlockIdx: 0}

		fr := "tool_calls"
		chunk := &ChatStreamChunk{
			Choices: []ChatStreamChoice{{
				Index:        0,
				Delta:        ChatStreamDelta{},
				FinishReason: &fr,
			}},
		}

		events := ChatChunkToAnthropicEvents(chunk, state)

		for _, e := range events {
			if e.Type == "message_delta" && e.Delta != nil {
				if e.Delta.StopReason != "tool_use" {
					t.Errorf("stop_reason = %q, want %q", e.Delta.StopReason, "tool_use")
				}
			}
		}
	})

	t.Run("state tracks MessageStartSent and ContentBlockIndex correctly", func(t *testing.T) {
		state := NewChatToAnthropicStreamState()

		if state.MessageStartSent {
			t.Errorf("MessageStartSent should be false initially")
		}
		if state.ContentBlockIndex != 0 {
			t.Errorf("ContentBlockIndex should be 0 initially")
		}

		// Send first chunk with content
		chunk := &ChatStreamChunk{
			ID:    "chatcmpl-2",
			Model: "gpt-4",
			Choices: []ChatStreamChoice{{
				Index: 0,
				Delta: ChatStreamDelta{Role: "assistant", Content: strPtr("A")},
			}},
		}
		ChatChunkToAnthropicEvents(chunk, state)

		if !state.MessageStartSent {
			t.Errorf("MessageStartSent should be true after first chunk")
		}
		if state.ContentBlockIndex != 0 {
			t.Errorf("ContentBlockIndex should be 0 while text block is open, got %d", state.ContentBlockIndex)
		}

		// Finish the stream — block closes, index advances
		fr := "stop"
		finishChunk := &ChatStreamChunk{
			Choices: []ChatStreamChoice{{
				Index:        0,
				Delta:        ChatStreamDelta{},
				FinishReason: &fr,
			}},
		}
		ChatChunkToAnthropicEvents(finishChunk, state)

		if state.ContentBlockIndex != 1 {
			t.Errorf("ContentBlockIndex should be 1 after block closed, got %d", state.ContentBlockIndex)
		}
	})

	t.Run("empty choices returns nil", func(t *testing.T) {
		state := NewChatToAnthropicStreamState()
		chunk := &ChatStreamChunk{Choices: nil}
		events := ChatChunkToAnthropicEvents(chunk, state)
		if events != nil {
			t.Errorf("expected nil for empty choices, got %d events", len(events))
		}
	})
}

// ---------------------------------------------------------------------------
// FinalizeAnthropicStream
// ---------------------------------------------------------------------------

func TestFinalizeAnthropicStream(t *testing.T) {
	t.Run("returns nil if MessageStartSent is false", func(t *testing.T) {
		state := NewChatToAnthropicStreamState()
		// Stream never started
		events := FinalizeAnthropicStream(state)
		if events != nil {
			t.Errorf("expected nil when stream never started, got %d events", len(events))
		}
	})

	t.Run("returns nil if MessageStopSent is true", func(t *testing.T) {
		state := NewChatToAnthropicStreamState()
		state.MessageStartSent = true
		state.MessageStopSent = true
		events := FinalizeAnthropicStream(state)
		if events != nil {
			t.Errorf("expected nil when stream already terminated, got %d events", len(events))
		}
	})

	t.Run("returns termination events if stream started but not stopped", func(t *testing.T) {
		state := NewChatToAnthropicStreamState()
		state.MessageStartSent = true
		state.MessageStopSent = false

		events := FinalizeAnthropicStream(state)

		if len(events) == 0 {
			t.Fatalf("expected termination events, got none")
		}

		types := eventTypes(events)
		assertContains(t, types, "message_delta")
		assertContains(t, types, "message_stop")

		// Check stop_reason is end_turn (from "stop" finish reason)
		for _, e := range events {
			if e.Type == "message_delta" && e.Delta != nil {
				if e.Delta.StopReason != "end_turn" {
					t.Errorf("stop_reason = %q, want %q", e.Delta.StopReason, "end_turn")
				}
			}
		}
	})

	t.Run("closes open content blocks before terminating", func(t *testing.T) {
		state := NewChatToAnthropicStreamState()
		state.MessageStartSent = true
		state.ContentBlockOpen = true
		state.ContentBlockIndex = 0

		events := FinalizeAnthropicStream(state)

		if len(events) == 0 {
			t.Fatalf("expected events, got none")
		}

		types := eventTypes(events)
		assertContains(t, types, "content_block_stop")
		assertContains(t, types, "message_delta")
		assertContains(t, types, "message_stop")

		if !state.MessageStopSent {
			t.Errorf("MessageStopSent should be true after finalize")
		}
	})
}

// Helpers (assertEventType, eventTypes, assertContains) are in anthropic_chat_test.go.
