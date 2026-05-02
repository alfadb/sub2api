// Package handler provides unit tests for the ResponsesHandler.
// These tests verify the integration between the handler and the apicompat
// streaming functionality, particularly for Stream ID synchronization and
// reasoning event handling.
package handler

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

// TestProcessResponsesStream_StreamIdSynchronization verifies that the
// ProcessResponsesStream correctly synchronizes stream IDs across the
// response lifecycle, fixing the known GitHub Copilot bug where added/done
// events return different item IDs.
func TestProcessResponsesStream_StreamIdSynchronization(t *testing.T) {
	// Simulate a stream where the 'done' event has a different ID than 'added'
	sseStream := `event: response.created
data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5.2"}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"id":"canonical_id_123","type":"message"}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","output_index":0,"delta":"Hello"}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{"id":"different_id_456","type":"message"}}

data: [DONE]
`

	// Track received events
	var events []string

	usage, err := apicompat.ProcessResponsesStream(strings.NewReader(sseStream), func(eventType, data string) error {
		events = append(events, eventType+":"+data)
		return nil
	})

	require.NoError(t, err)
	assert.NotNil(t, usage)

	// Verify we received all expected events
	assert.GreaterOrEqual(t, len(events), 3)

	// The key assertion: when 'done' event has different ID than 'added',
	// the StreamIdTracker should replace it with the canonical ID from 'added'
	var foundAddedEvent, foundDoneEvent bool
	var addedID, doneID string

	for _, evt := range events {
		if strings.Contains(evt, "response.output_item.added") {
			foundAddedEvent = true
			// Extract the item ID from the added event
			parts := strings.Split(evt, `"id":"`)
			if len(parts) > 1 {
				idPart := strings.Split(parts[1], `"`)[0]
				addedID = idPart
			}
		}
		if strings.Contains(evt, "response.output_item.done") {
			foundDoneEvent = true
			// Extract the item ID from the done event
			parts := strings.Split(evt, `"id":"`)
			if len(parts) > 1 {
				idPart := strings.Split(parts[1], `"`)[0]
				doneID = idPart
			}
		}
	}

	assert.True(t, foundAddedEvent, "should receive output_item.added event")
	assert.True(t, foundDoneEvent, "should receive output_item.done event")

	// The core assertion: both events should use the same canonical ID
	// (the one from the 'added' event)
	assert.Equal(t, "canonical_id_123", addedID, "added event should have the original ID")
	assert.Equal(t, "canonical_id_123", doneID, "done event should use the canonical ID from added, not the mismatched one")
}

// TestProcessResponsesStream_ReasoningEventHandling verifies that reasoning
// events are properly tracked through the streaming state machine.
func TestProcessResponsesStream_ReasoningEventHandling(t *testing.T) {
	sseStream := `event: response.created
data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5.2-codex"}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"id":"reason_1","type":"reasoning","encrypted_content":"opaque_encrypted_content"}}

event: response.reasoning_summary_part.added
data: {"type":"response.reasoning_summary_part.added","output_index":0,"summary_index":0}

event: response.reasoning_summary_text.delta
data: {"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"Thinking"}

event: response.reasoning_summary_text.delta
data: {"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":" step by step"}

event: response.reasoning_summary_text.done
data: {"type":"response.reasoning_summary_text.done","output_index":0,"summary_index":0}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{"id":"reason_1","type":"reasoning"}}

event: response.completed
data: {"type":"response.completed","response":{"status":"completed"}}

data: [DONE]
`

	var events []string
	usage, err := apicompat.ProcessResponsesStream(strings.NewReader(sseStream), func(eventType, data string) error {
		events = append(events, eventType+":"+data)
		return nil
	})

	require.NoError(t, err)
	assert.NotNil(t, usage)

	// Verify reasoning events were received
	foundReasoningAdded := false
	foundSummaryPartAdded := false
	foundSummaryDelta := false
	foundSummaryDone := false

	for _, evt := range events {
		if strings.Contains(evt, `"type":"reasoning"`) {
			foundReasoningAdded = true
		}
		if strings.Contains(evt, "response.reasoning_summary_part.added") {
			foundSummaryPartAdded = true
		}
		if strings.Contains(evt, "response.reasoning_summary_text.delta") {
			foundSummaryDelta = true
		}
		if strings.Contains(evt, "response.reasoning_summary_text.done") {
			foundSummaryDone = true
		}
	}

	assert.True(t, foundReasoningAdded, "should receive reasoning item added event")
	assert.True(t, foundSummaryPartAdded, "should receive summary_part.added event")
	assert.True(t, foundSummaryDelta, "should receive summary_text.delta events")
	assert.True(t, foundSummaryDone, "should receive summary_text.done event")
}

// TestProcessResponsesStream_FunctionCallIncrementalStreaming verifies that
// function call arguments are properly accumulated from incremental delta events.
func TestProcessResponsesStream_FunctionCallIncrementalStreaming(t *testing.T) {
	sseStream := `event: response.created
data: {"type":"response.created","response":{"id":"resp_1"}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"id":"call_1","type":"function_call","call_id":"fc_123","name":"search"}}

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"q\":"}

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"test"}

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"\",\"limit\":10}"}

event: response.function_call_arguments.done
data: {"type":"response.function_call_arguments.done","output_index":0,"call_id":"fc_123","name":"search","arguments":"{\"q\":\"test\",\"limit\":10}"}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{"id":"call_1","type":"function_call"}}

data: [DONE]
`

	var events []string
	usage, err := apicompat.ProcessResponsesStream(strings.NewReader(sseStream), func(eventType, data string) error {
		events = append(events, eventType+":"+data)
		return nil
	})

	require.NoError(t, err)
	assert.NotNil(t, usage)

	// Count delta events
	deltaCount := 0
	for _, evt := range events {
		if strings.Contains(evt, "response.function_call_arguments.delta") {
			deltaCount++
		}
	}

	assert.GreaterOrEqual(t, deltaCount, 3, "should receive multiple delta events")
}

// TestProcessResponsesStream_MultiItemStream verifies correct handling
// of streams with multiple output items (message + reasoning + function_call).
func TestProcessResponsesStream_MultiItemStream(t *testing.T) {
	sseStream := `event: response.created
data: {"type":"response.created","response":{"id":"resp_1"}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"id":"reason_1","type":"reasoning"}}

event: response.reasoning_summary_text.delta
data: {"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"Let me think"}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{"id":"reason_1","type":"reasoning"}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":1,"item":{"id":"msg_1","type":"message"}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","output_index":1,"delta":"Here is the answer"}

event: response.output_text.done
data: {"type":"response.output_text.done","output_index":1,"item_id":"msg_1"}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":2,"item":{"id":"call_1","type":"function_call","name":"search"}}

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","output_index":2,"delta":"{}"}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":2,"item":{"id":"call_1","type":"function_call"}}

event: response.completed
data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":50,"output_tokens":30}}}

data: [DONE]
`

	var events []string
	usage, err := apicompat.ProcessResponsesStream(strings.NewReader(sseStream), func(eventType, data string) error {
		events = append(events, eventType+":"+data)
		return nil
	})

	require.NoError(t, err)
	assert.NotNil(t, usage)

	// Verify usage was extracted
	assert.Equal(t, 50, usage.InputTokens)
	assert.Equal(t, 30, usage.OutputTokens)
	assert.Equal(t, 80, usage.TotalTokens)

	// Count items by type
	var reasoningItems, messageItems, functionCallItems int
	for _, evt := range events {
		if strings.Contains(evt, `"type":"reasoning"`) {
			reasoningItems++
		}
		if strings.Contains(evt, `"type":"message"`) && strings.Contains(evt, "output_item.added") {
			messageItems++
		}
		if strings.Contains(evt, `"type":"function_call"`) {
			functionCallItems++
		}
	}

	assert.GreaterOrEqual(t, reasoningItems, 1, "should have reasoning item")
	assert.GreaterOrEqual(t, messageItems, 1, "should have message item")
	assert.GreaterOrEqual(t, functionCallItems, 1, "should have function_call item")
}

// TestProcessResponsesStream_FlushOnIncompleteStream verifies that pending
// events are flushed when the stream ends prematurely.
func TestProcessResponsesStream_FlushOnIncompleteStream(t *testing.T) {
	sseStream := `event: response.created
data: {"type":"response.created","response":{"id":"resp_1"}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message"}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","output_index":0,"delta":"Incomplete text"}

event: response.completed
data: {"type":"response.completed","response":{"status":"incomplete"}}

data: [DONE]
`

	var events []string
	usage, err := apicompat.ProcessResponsesStream(strings.NewReader(sseStream), func(eventType, data string) error {
		events = append(events, eventType+":"+data)
		return nil
	})

	require.NoError(t, err)
	assert.NotNil(t, usage)

	// Should have a flush event for the incomplete text
	foundFlushedText := false
	for _, evt := range events {
		if strings.Contains(evt, "response.output_text.done") && strings.Contains(evt, "Incomplete text") {
			foundFlushedText = true
			break
		}
	}

	assert.True(t, foundFlushedText, "incomplete text should be flushed on stream end")
}

// TestProcessResponsesStream_EmptyAndCommentEvents verifies that SSE comments
// and empty lines are handled correctly.
func TestProcessResponsesStream_EmptyAndCommentEvents(t *testing.T) {
	sseStream := `: this is a comment

event: response.created
data: {"type":"response.created","response":{"id":"resp_1"}}

: another comment

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message"}}

data: [DONE]
`

	var comments []string
	var dataEvents []string

	usage, err := apicompat.ProcessResponsesStream(strings.NewReader(sseStream), func(eventType, data string) error {
		if eventType == "" && strings.HasPrefix(data, ":") {
			comments = append(comments, data)
		} else if eventType != "" {
			dataEvents = append(dataEvents, eventType)
		}
		return nil
	})

	require.NoError(t, err)
	assert.NotNil(t, usage)

	// Comments should be forwarded
	assert.GreaterOrEqual(t, len(comments), 2, "comments should be forwarded")

	// Events should be processed
	assert.Contains(t, dataEvents, "response.created")
	assert.Contains(t, dataEvents, "response.output_item.added")
}

// TestProcessResponsesStream_HandlerErrorPropagation verifies that errors
// from the handler callback are propagated correctly.
func TestProcessResponsesStream_HandlerErrorPropagation(t *testing.T) {
	sseStream := `event: response.created
data: {"type":"response.created","response":{"id":"resp_1"}}
data: [DONE]
`

	_, err := apicompat.ProcessResponsesStream(strings.NewReader(sseStream), func(eventType, data string) error {
		// Simulate handler error
		if strings.Contains(data, "resp_1") {
			return assert.AnError
		}
		return nil
	})

	assert.Error(t, err)
}

// TestFormatSSEEventHelpers verifies the SSE formatting helpers.
func TestFormatSSEEventHelpers(t *testing.T) {
	t.Run("FormatSSEEvent formats with type", func(t *testing.T) {
		result := apicompat.FormatSSEEvent("response.created", `{"id":"resp_1"}`)
		assert.Equal(t, "event: response.created\ndata: {\"id\":\"resp_1\"}\n\n", result)
	})

	t.Run("FormatSSEData formats without type", func(t *testing.T) {
		result := apicompat.FormatSSEData(": comment")
		assert.Equal(t, "data: : comment\n", result)
	})

	t.Run("FormatSSEDone returns sentinel", func(t *testing.T) {
		result := apicompat.FormatSSEDone()
		assert.Equal(t, "data: [DONE]\n\n", result)
	})
}

// TestProcessResponsesStream_CodexReasoningIntegration verifies the full
// integration of reasoning event handling for Codex models.
func TestProcessResponsesStream_CodexReasoningIntegration(t *testing.T) {
	// This simulates a realistic Codex response with reasoning
	sseStream := `event: response.created
data: {"type":"response.created","response":{"id":"resp_codex_1","model":"gpt-5.2-codex","status":"in_progress"}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"id":"reasoning_block_1","type":"reasoning","encrypted_content":"gAAAAABl..."}}

event: response.reasoning_summary_part.added
data: {"type":"response.reasoning_summary_part.added","output_index":0,"summary_index":0}

event: response.reasoning_summary_text.delta
data: {"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"I'll"}

event: response.reasoning_summary_text.delta
data: {"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":" analyze this code"}

event: response.reasoning_summary_text.done
data: {"type":"response.reasoning_summary_text.done","output_index":0,"summary_index":0}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{"id":"reasoning_block_1","type":"reasoning","status":"completed"}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":1,"item":{"id":"msg_1","type":"message","role":"assistant"}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","output_index":1,"delta":"Based on my analysis"}

event: response.output_text.done
data: {"type":"response.output_text.done","output_index":1,"item_id":"msg_1","content_index":0}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_codex_1","status":"completed","usage":{"input_tokens":100,"output_tokens":50}}}

data: [DONE]
`

	var events []string
	var foundEncryptedContent bool

	usage, err := apicompat.ProcessResponsesStream(strings.NewReader(sseStream), func(eventType, data string) error {
		events = append(events, eventType+":"+data)
		if strings.Contains(data, "encrypted_content") {
			foundEncryptedContent = true
		}
		return nil
	})

	require.NoError(t, err)
	assert.NotNil(t, usage)
	assert.True(t, foundEncryptedContent, "should preserve encrypted_content in reasoning events")
	assert.Equal(t, 100, usage.InputTokens)
	assert.Equal(t, 50, usage.OutputTokens)
}

// TestHTTPResponseWithSSE verifies that an HTTP response containing
// SSE events can be processed correctly.
func TestHTTPResponseWithSSE(t *testing.T) {
	// Create a mock HTTP response
	sseStream := `event: response.created
data: {"type":"response.created","response":{"id":"http_test_1"}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"id":"msg_http_1","type":"message"}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","output_index":0,"delta":"HTTP streaming"}

data: [DONE]
`

	mockResp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(sseStream)),
		Header:     make(http.Header),
	}
	mockResp.Header.Set("Content-Type", "text/event-stream")
	mockResp.Header.Set("Cache-Control", "no-cache")

	// Process the response body
	var events []string
	usage, err := apicompat.ProcessResponsesStream(mockResp.Body, func(eventType, data string) error {
		events = append(events, eventType+":"+data)
		return nil
	})

	require.NoError(t, err)
	assert.NotNil(t, usage)
	assert.GreaterOrEqual(t, len(events), 3)
}

// TestSSEFormattingRoundTrip verifies that SSE events can be formatted
// and then parsed back correctly.
func TestSSEFormattingRoundTrip(t *testing.T) {
	originalEvents := []struct {
		eventType string
		data      string
	}{
		{"response.created", `{"type":"response.created","response":{"id":"test_1"}}`},
		{"response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1"}}`},
		{"response.output_text.delta", `{"type":"response.output_text.delta","delta":"Hello"}`},
	}

	// Format events
	var formattedSSE string
	for _, evt := range originalEvents {
		formattedSSE += apicompat.FormatSSEEvent(evt.eventType, evt.data)
	}
	formattedSSE += apicompat.FormatSSEDone()

	// Parse back
	var parsedEvents []string
	_, err := apicompat.ProcessResponsesStream(strings.NewReader(formattedSSE), func(eventType, data string) error {
		// Skip empty event types (comments)
		if eventType != "" {
			parsedEvents = append(parsedEvents, eventType+":"+data)
		}
		return nil
	})

	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(parsedEvents), len(originalEvents), "should parse all formatted events")
}
