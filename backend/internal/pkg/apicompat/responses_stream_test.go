// Package apicompat provides unit tests for Responses stream processing.
package apicompat

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// ResponsesStreamState - NewResponsesStreamState
// ---------------------------------------------------------------------------

func TestNewResponsesStreamState(t *testing.T) {
	state := NewResponsesStreamState()

	assert.Equal(t, "in_progress", state.Status, "default status should be in_progress")
	assert.NotZero(t, state.Created, "Created timestamp should be set")
	assert.NotNil(t, state.ToolCalls, "ToolCalls slice should be initialized")
	assert.NotNil(t, state.ReasoningBlocks, "ReasoningBlocks slice should be initialized")
	assert.Empty(t, state.ToolCalls, "ToolCalls should start empty")
	assert.Empty(t, state.ReasoningBlocks, "ReasoningBlocks should start empty")
}

// ---------------------------------------------------------------------------
// ResponsesStreamState - handleResponseCreated
// ---------------------------------------------------------------------------

func TestResponsesStreamState_HandleResponseCreated(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantID     string
		wantModel  string
		wantStatus string
	}{
		{
			name: "parses response.created with full metadata",
			input: `{
				"response": {
					"id": "resp_abc123",
					"model": "gpt-5.2-codex",
					"created_at": 1234567890
				}
			}`,
			wantID:     "resp_abc123",
			wantModel:  "gpt-5.2-codex",
			wantStatus: "in_progress",
		},
		{
			name: "handles missing created_at",
			input: `{
				"response": {
					"id": "resp_xyz",
					"model": "claude-sonnet-4.6"
				}
			}`,
			wantID:     "resp_xyz",
			wantModel:  "claude-sonnet-4.6",
			wantStatus: "in_progress",
		},
		{
			name: "handles minimal response",
			input: `{
				"response": {
					"id": "minimal"
				}
			}`,
			wantID:     "minimal",
			wantModel:  "",
			wantStatus: "in_progress",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := NewResponsesStreamState()
			var parsed map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(tt.input), &parsed))

			state.handleResponseCreated(parsed)

			assert.Equal(t, tt.wantID, state.ID)
			assert.Equal(t, tt.wantModel, state.Model)
			assert.Equal(t, tt.wantStatus, state.Status)
		})
	}
}

// ---------------------------------------------------------------------------
// ResponsesStreamState - handleOutputItemAdded
// ---------------------------------------------------------------------------

func TestResponsesStreamState_HandleOutputItemAdded(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		wantItemType   string
		wantItemID     string
		wantToolCalls  int
		wantReasonings int
	}{
		{
			name: "message item initializes current text",
			input: `{
				"output_index": 0,
				"item": {
					"id": "msg_1",
					"type": "message",
					"role": "assistant"
				}
			}`,
			wantItemType:   "message",
			wantItemID:     "msg_1",
			wantToolCalls:  0,
			wantReasonings: 0,
		},
		{
			name: "function_call item creates tool call state",
			input: `{
				"output_index": 1,
				"item": {
					"id": "call_1",
					"type": "function_call",
					"call_id": "fc_abc",
					"name": "search"
				}
			}`,
			wantItemType:   "function_call",
			wantItemID:     "call_1",
			wantToolCalls:  1,
			wantReasonings: 0,
		},
		{
			name: "reasoning item creates reasoning state",
			input: `{
				"output_index": 0,
				"item": {
					"id": "reason_1",
					"type": "reasoning",
					"status": "in_progress"
				}
			}`,
			wantItemType:   "reasoning",
			wantItemID:     "reason_1",
			wantToolCalls:  0,
			wantReasonings: 1,
		},
		{
			name: "reasoning item with encrypted_content extracts it",
			input: `{
				"output_index": 0,
				"item": {
					"id": "reason_encrypted",
					"type": "reasoning",
					"encrypted_content": "encrypted_reasoning_data_here"
				}
			}`,
			wantItemType:   "reasoning",
			wantItemID:     "reason_encrypted",
			wantToolCalls:  0,
			wantReasonings: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := NewResponsesStreamState()
			var parsed map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(tt.input), &parsed))

			state.handleOutputItemAdded(parsed)

			assert.Equal(t, tt.wantItemID, state.CurrentItemID)
			assert.Equal(t, len(state.ToolCalls), tt.wantToolCalls)
			assert.Equal(t, len(state.ReasoningBlocks), tt.wantReasonings)

			if tt.wantItemType == "function_call" && tt.wantToolCalls > 0 {
				assert.NotNil(t, state.CurrentToolCall)
				assert.Equal(t, tt.wantItemID, state.CurrentToolCall.ItemID)
				assert.Equal(t, "in_progress", state.CurrentToolCall.Status)
			}

			if tt.wantItemType == "reasoning" && tt.wantReasonings > 0 {
				assert.NotNil(t, state.CurrentReasoning)
				assert.Equal(t, tt.wantItemID, state.CurrentReasoning.ItemID)
				assert.Equal(t, "in_progress", state.CurrentReasoning.Status)

				// Check encrypted_content extraction
				if strings.Contains(tt.input, "encrypted_content") {
					assert.Equal(t, "encrypted_reasoning_data_here", state.CurrentReasoning.SummaryText)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ResponsesStreamState - handleOutputItemDone
// ---------------------------------------------------------------------------

func TestResponsesStreamState_HandleOutputItemDone(t *testing.T) {
	tests := []struct {
		name         string
		initialState func(*ResponsesStreamState)
		event        string
		verifyState  func(t *testing.T, state *ResponsesStreamState)
	}{
		{
			name: "marks message item as done",
			event: `{
				"output_index": 0,
				"item": {
					"id": "msg_1",
					"type": "message",
					"status": "done"
				}
			}`,
			initialState: func(s *ResponsesStreamState) {
				s.CurrentItemID = "msg_1"
				s.CurrentText = "Hello world"
			},
			verifyState: func(t *testing.T, state *ResponsesStreamState) {
				assert.Equal(t, "", state.CurrentText, "current text should be cleared")
			},
		},
		{
			name: "marks function_call as complete",
			event: `{
				"output_index": 1,
				"item": {
					"id": "call_1",
					"type": "function_call",
					"status": "completed"
				}
			}`,
			initialState: func(s *ResponsesStreamState) {
				s.ToolCalls = []ResponsesToolCallState{
					{Index: 0, ItemID: "call_0", Status: "completed", IsComplete: true},
					{Index: 1, ItemID: "call_1", Status: "in_progress", IsComplete: false},
					{Index: 2, ItemID: "call_2", Status: "in_progress", IsComplete: false},
				}
			},
			verifyState: func(t *testing.T, state *ResponsesStreamState) {
				assert.Equal(t, "completed", state.ToolCalls[1].Status)
				assert.True(t, state.ToolCalls[1].IsComplete)
				// Other tool calls should be unchanged
				assert.False(t, state.ToolCalls[2].IsComplete)
			},
		},
		{
			name: "marks reasoning block as complete",
			event: `{
				"output_index": 0,
				"item": {
					"id": "reason_1",
					"type": "reasoning",
					"status": "completed"
				}
			}`,
			initialState: func(s *ResponsesStreamState) {
				s.ReasoningBlocks = []ResponsesReasoningState{
					{ItemID: "reason_1", Status: "in_progress", IsComplete: false},
					{ItemID: "reason_2", Status: "in_progress", IsComplete: false},
				}
			},
			verifyState: func(t *testing.T, state *ResponsesStreamState) {
				assert.Equal(t, "completed", state.ReasoningBlocks[0].Status)
				assert.True(t, state.ReasoningBlocks[0].IsComplete)
				assert.False(t, state.ReasoningBlocks[1].IsComplete)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := NewResponsesStreamState()
			if tt.initialState != nil {
				tt.initialState(state)
			}
			var parsed map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(tt.event), &parsed))

			state.handleOutputItemDone(parsed)

			if tt.verifyState != nil {
				tt.verifyState(t, state)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ResponsesStreamState - handleOutputTextDelta
// ---------------------------------------------------------------------------

func TestResponsesStreamState_HandleOutputTextDelta(t *testing.T) {
	tests := []struct {
		name        string
		initialText string
		delta       string
		wantText    string
	}{
		{
			name:        "appends delta to current text",
			initialText: "Hello",
			delta:       " world",
			wantText:    "Hello world",
		},
		{
			name:        "handles empty initial text",
			initialText: "",
			delta:       "First words",
			wantText:    "First words",
		},
		{
			name:        "handles multi-byte unicode",
			initialText: "Hello",
			delta:       " 世界",
			wantText:    "Hello 世界",
		},
		{
			name:        "handles special characters",
			initialText: "Line 1",
			delta:       "\nLine 2\n",
			wantText:    "Line 1\nLine 2\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := NewResponsesStreamState()
			state.CurrentText = tt.initialText

			event := `{"delta":` + jsonEscape(tt.delta) + `}`
			var parsed map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(event), &parsed))

			state.handleOutputTextDelta(parsed)

			assert.Equal(t, tt.wantText, state.CurrentText)
		})
	}
}

// ---------------------------------------------------------------------------
// ResponsesStreamState - handleOutputTextDone
// ---------------------------------------------------------------------------

func TestResponsesStreamState_HandleOutputTextDone(t *testing.T) {
	state := NewResponsesStreamState()
	state.CurrentText = "Some text"

	event := `{"item_id": "msg_1"}`
	var parsed map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(event), &parsed))

	state.handleOutputTextDone(parsed)

	assert.Equal(t, "", state.CurrentText, "current text should be cleared")
}

// ---------------------------------------------------------------------------
// ResponsesStreamState - handleFunctionCallDelta
// ---------------------------------------------------------------------------

func TestResponsesStreamState_HandleFunctionCallDelta(t *testing.T) {
	tests := []struct {
		name         string
		toolCalls    []ResponsesToolCallState
		outputIndex  int
		delta        string
		wantArgIndex int
		wantArgs     string
	}{
		{
			name: "appends delta to first tool call",
			toolCalls: []ResponsesToolCallState{
				{Index: 0, Arguments: `{"q":"`},
			},
			outputIndex:  0,
			delta:        `test"}`,
			wantArgIndex: 0,
			wantArgs:     `{"q":"test"}`,
		},
		{
			name: "appends delta to second tool call",
			toolCalls: []ResponsesToolCallState{
				{Index: 0, Arguments: `{}`},
				{Index: 1, Arguments: `{"pa`},
			},
			outputIndex:  1,
			delta:        `ram":"value"}`,
			wantArgIndex: 1,
			wantArgs:     `{"param":"value"}`,
		},
		{
			name: "handles json fragment delta",
			toolCalls: []ResponsesToolCallState{
				{Index: 0, Arguments: `{"results":[`},
			},
			outputIndex:  0,
			delta:        `{"id":1}]}`,
			wantArgIndex: 0,
			wantArgs:     `{"results":[{"id":1}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := NewResponsesStreamState()
			state.ToolCalls = tt.toolCalls

			event := `{"output_index":` + itoa(tt.outputIndex) + `,"delta":` + jsonEscape(tt.delta) + `}`
			var parsed map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(event), &parsed))

			state.handleFunctionCallDelta(parsed)

			assert.Equal(t, tt.wantArgs, state.ToolCalls[tt.wantArgIndex].Arguments)
		})
	}
}

// ---------------------------------------------------------------------------
// ResponsesStreamState - handleFunctionCallDone
// ---------------------------------------------------------------------------

func TestResponsesStreamState_HandleFunctionCallDone(t *testing.T) {
	tests := []struct {
		name       string
		toolCalls  []ResponsesToolCallState
		event      string
		wantCallID string
		wantArgs   string
	}{
		{
			name: "sets final arguments by call_id",
			toolCalls: []ResponsesToolCallState{
				{CallID: "fc_123", Arguments: `{"partial":true}`},
				{CallID: "fc_456", Arguments: ""},
			},
			event: `{
				"call_id": "fc_123",
				"arguments": "{\"complete\":true}"
			}`,
			wantCallID: "fc_123",
			wantArgs:   `{"complete":true}`,
		},
		{
			name: "sets final arguments by item_id (call_id in event matches ItemID in tool call)",
			toolCalls: []ResponsesToolCallState{
				{ItemID: "item_1", CallID: "fc_1", Arguments: ""},
			},
			event: `{
				"call_id": "item_1",
				"arguments": "{\"result\":\"ok\"}"
			}`,
			wantCallID: "fc_1", // The tool call's CallID doesn't change
			wantArgs:   `{"result":"ok"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := NewResponsesStreamState()
			state.ToolCalls = tt.toolCalls

			var parsed map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(tt.event), &parsed))

			state.handleFunctionCallDone(parsed)

			found := false
			for _, tc := range state.ToolCalls {
				if tc.CallID == tt.wantCallID {
					assert.Equal(t, tt.wantArgs, tc.Arguments)
					found = true
					break
				}
			}
			assert.True(t, found, "should find tool call with call_id %s", tt.wantCallID)
		})
	}
}

// ---------------------------------------------------------------------------
// ResponsesStreamState - handleReasoningSummaryPartAdded
// ---------------------------------------------------------------------------

func TestResponsesStreamState_HandleReasoningSummaryPartAdded(t *testing.T) {
	state := NewResponsesStreamState()
	state.ReasoningBlocks = []ResponsesReasoningState{
		{ItemID: "reason_1", SummaryIndex: 0, SummaryText: "previous"},
	}
	state.CurrentReasoning = &state.ReasoningBlocks[0]

	event := `{"summary_index":1}`
	var parsed map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(event), &parsed))

	state.handleReasoningSummaryPartAdded(parsed)

	assert.Equal(t, 1, state.CurrentReasoning.SummaryIndex)
	assert.Equal(t, "", state.CurrentReasoning.SummaryText, "summary text should be reset")
}

// ---------------------------------------------------------------------------
// ResponsesStreamState - handleReasoningSummaryTextDelta
// ---------------------------------------------------------------------------

func TestResponsesStreamState_HandleReasoningSummaryTextDelta(t *testing.T) {
	tests := []struct {
		name             string
		reasonings       []ResponsesReasoningState
		summaryIndex     int
		delta            string
		wantSummaryIndex int
		wantSummary      string
	}{
		{
			name: "appends delta to first reasoning block",
			reasonings: []ResponsesReasoningState{
				{SummaryIndex: 0, SummaryText: "Thinking "},
			},
			summaryIndex:     0,
			delta:            "about it",
			wantSummaryIndex: 0,
			wantSummary:      "Thinking about it",
		},
		{
			name: "appends delta to second reasoning block",
			reasonings: []ResponsesReasoningState{
				{SummaryIndex: 0, SummaryText: "First"},
				{SummaryIndex: 1, SummaryText: "Second "},
			},
			summaryIndex:     1,
			delta:            "part",
			wantSummaryIndex: 1,
			wantSummary:      "Second part",
		},
		{
			name: "handles empty initial summary text",
			reasonings: []ResponsesReasoningState{
				{SummaryIndex: 0, SummaryText: ""},
			},
			summaryIndex:     0,
			delta:            "Starting reasoning",
			wantSummaryIndex: 0,
			wantSummary:      "Starting reasoning",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := NewResponsesStreamState()
			state.ReasoningBlocks = tt.reasonings

			event := `{"summary_index":` + itoa(tt.summaryIndex) + `,"delta":` + jsonEscape(tt.delta) + `}`
			var parsed map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(event), &parsed))

			state.handleReasoningSummaryTextDelta(parsed)

			assert.Equal(t, tt.wantSummary, state.ReasoningBlocks[tt.wantSummaryIndex].SummaryText)
		})
	}
}

// ---------------------------------------------------------------------------
// ResponsesStreamState - handleReasoningSummaryTextDone
// ---------------------------------------------------------------------------

func TestResponsesStreamState_HandleReasoningSummaryTextDone(t *testing.T) {
	tests := []struct {
		name         string
		reasonings   []ResponsesReasoningState
		summaryIndex int
		wantComplete bool
	}{
		{
			name: "marks first reasoning block as complete",
			reasonings: []ResponsesReasoningState{
				{SummaryIndex: 0, IsComplete: false},
				{SummaryIndex: 1, IsComplete: false},
			},
			summaryIndex: 0,
			wantComplete: true,
		},
		{
			name: "marks second reasoning block as complete",
			reasonings: []ResponsesReasoningState{
				{SummaryIndex: 0, IsComplete: true},
				{SummaryIndex: 1, IsComplete: false},
			},
			summaryIndex: 1,
			wantComplete: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := NewResponsesStreamState()
			state.ReasoningBlocks = tt.reasonings

			event := `{"summary_index":` + itoa(tt.summaryIndex) + `}`
			var parsed map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(event), &parsed))

			state.handleReasoningSummaryTextDone(parsed)

			assert.Equal(t, tt.wantComplete, state.ReasoningBlocks[tt.summaryIndex].IsComplete)
		})
	}
}

// ---------------------------------------------------------------------------
// ResponsesStreamState - handleResponseCompleted
// ---------------------------------------------------------------------------

func TestResponsesStreamState_HandleResponseCompleted(t *testing.T) {
	tests := []struct {
		name       string
		event      string
		wantStatus string
		wantInput  int
		wantOutput int
		wantTotal  int
	}{
		{
			name: "parses completed response with usage",
			event: `{
				"response": {
					"status": "completed",
					"usage": {
						"input_tokens": 100,
						"output_tokens": 50,
						"total_tokens": 150
					}
				}
			}`,
			wantStatus: "completed",
			wantInput:  100,
			wantOutput: 50,
			wantTotal:  150,
		},
		{
			name: "handles incomplete response",
			event: `{
				"response": {
					"status": "incomplete",
					"usage": {
						"input_tokens": 10,
						"output_tokens": 5
					}
				}
			}`,
			wantStatus: "incomplete",
			wantInput:  10,
			wantOutput: 5,
		},
		{
			name: "handles response without usage",
			event: `{
				"response": {
					"status": "completed"
				}
			}`,
			wantStatus: "completed",
			wantInput:  0,
			wantOutput: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := NewResponsesStreamState()
			var parsed map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(tt.event), &parsed))

			state.handleResponseCompleted(parsed)

			assert.Equal(t, tt.wantStatus, state.Status)
			assert.Equal(t, tt.wantInput, state.InputTokens)
			assert.Equal(t, tt.wantOutput, state.OutputTokens)
		})
	}
}

// ---------------------------------------------------------------------------
// ResponsesStreamState - finalUsage
// ---------------------------------------------------------------------------

func TestResponsesStreamState_FinalUsage(t *testing.T) {
	state := NewResponsesStreamState()
	state.InputTokens = 100
	state.OutputTokens = 50

	usage := state.finalUsage()

	assert.Equal(t, 100, usage.InputTokens)
	assert.Equal(t, 50, usage.OutputTokens)
	assert.Equal(t, 150, usage.TotalTokens)
}

// ---------------------------------------------------------------------------
// ProcessResponsesStream - integration tests
// ---------------------------------------------------------------------------

func TestProcessResponsesStream_BasicStream(t *testing.T) {
	stream := `event: response.created
data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5.2"}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message"}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","output_index":0,"delta":"Hello"}

event: response.output_text.done
data: {"type":"response.output_text.done","output_index":0,"item_id":"msg_1"}

event: response.completed
data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":10,"output_tokens":5}}}

data: [DONE]
`

	var events []string
	usage, err := ProcessResponsesStream(strings.NewReader(stream), func(eventType, data string) error {
		events = append(events, eventType+":"+data)
		return nil
	})

	require.NoError(t, err)
	assert.NotNil(t, usage)
	assert.Equal(t, 10, usage.InputTokens)
	assert.Equal(t, 5, usage.OutputTokens)
	assert.Greater(t, len(events), 0)
}

func TestProcessResponsesStream_WithReasoning(t *testing.T) {
	stream := `event: response.created
data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5.2"}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"id":"reason_1","type":"reasoning","encrypted_content":"encrypted_data"}}

event: response.reasoning_summary_part.added
data: {"type":"response.reasoning_summary_part.added","output_index":0,"summary_index":0}

event: response.reasoning_summary_text.delta
data: {"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"Thinking"}

event: response.reasoning_summary_text.done
data: {"type":"response.reasoning_summary_text.done","output_index":0,"summary_index":0}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{"id":"reason_1","type":"reasoning"}}

data: [DONE]
`

	var events []string
	usage, err := ProcessResponsesStream(strings.NewReader(stream), func(eventType, data string) error {
		events = append(events, eventType+":"+data)
		return nil
	})

	require.NoError(t, err)
	assert.NotNil(t, usage)
	assert.Greater(t, len(events), 0)
}

func TestProcessResponsesStream_WithFunctionCall(t *testing.T) {
	stream := `event: response.created
data: {"type":"response.created","response":{"id":"resp_1"}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"id":"call_1","type":"function_call","call_id":"fc_123","name":"search"}}

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"q\":\"test"}

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"\"}"}

event: response.function_call_arguments.done
data: {"type":"response.function_call_arguments.done","output_index":0,"call_id":"fc_123","name":"search","arguments":"{\"q\":\"test\"}"}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{"id":"call_1","type":"function_call"}}

data: [DONE]
`

	var events []string
	usage, err := ProcessResponsesStream(strings.NewReader(stream), func(eventType, data string) error {
		events = append(events, eventType+":"+data)
		return nil
	})

	require.NoError(t, err)
	assert.NotNil(t, usage)
	assert.Greater(t, len(events), 0)
}

func TestProcessResponsesStream_CommentForwarding(t *testing.T) {
	stream := `: this is a comment
event: response.created
data: {"type":"response.created","response":{"id":"resp_1"}}

: another comment
data: [DONE]
`

	var comments []string
	_, err := ProcessResponsesStream(strings.NewReader(stream), func(eventType, data string) error {
		if eventType == "" && strings.HasPrefix(data, ":") {
			comments = append(comments, data)
		}
		return nil
	})

	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(comments), 2)
}

func TestProcessResponsesStream_HandlerError(t *testing.T) {
	stream := `event: response.created
data: {"type":"response.created","response":{"id":"resp_1"}}
data: [DONE]
`

	_, err := ProcessResponsesStream(strings.NewReader(stream), func(eventType, data string) error {
		return assert.AnError
	})

	assert.Error(t, err)
}

func TestProcessResponsesStream_EmptyStream(t *testing.T) {
	stream := ``

	usage, err := ProcessResponsesStream(strings.NewReader(stream), func(eventType, data string) error {
		return nil
	})

	require.NoError(t, err)
	assert.NotNil(t, usage)
}

func TestProcessResponsesStream_StreamWithFlushOnDone(t *testing.T) {
	// Tests that pending text is flushed when stream ends
	stream := `event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message"}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","output_index":0,"delta":"Incomplete"}

event: response.completed
data: {"type":"response.completed","response":{"status":"incomplete"}}

data: [DONE]
`

	var events []string
	_, err := ProcessResponsesStream(strings.NewReader(stream), func(eventType, data string) error {
		events = append(events, eventType+":"+data)
		return nil
	})

	require.NoError(t, err)

	// Should have a flush event with the incomplete text
	foundFlush := false
	for _, evt := range events {
		if strings.Contains(evt, "response.output_text.done") && strings.Contains(evt, "Incomplete") {
			foundFlush = true
			break
		}
	}
	assert.True(t, foundFlush, "pending text should be flushed on stream completion")
}

// ---------------------------------------------------------------------------
// FormatSSEEvent helpers
// ---------------------------------------------------------------------------

func TestFormatSSEEvent(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		data      string
		want      string
	}{
		{
			name:      "formats event with type",
			eventType: "response.created",
			data:      `{"id":"resp_1"}`,
			want:      "event: response.created\ndata: {\"id\":\"resp_1\"}\n\n",
		},
		{
			name:      "formats event without type",
			eventType: "",
			data:      `{"data":"value"}`,
			want:      "data: {\"data\":\"value\"}\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatSSEEvent(tt.eventType, tt.data)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormatSSEData(t *testing.T) {
	got := FormatSSEData("test data")
	assert.Equal(t, "data: test data\n", got)
}

func TestFormatSSEDone(t *testing.T) {
	got := FormatSSEDone()
	assert.Equal(t, "data: [DONE]\n\n", got)
}

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func itoa(i int) string {
	return strconv.Itoa(i)
}
