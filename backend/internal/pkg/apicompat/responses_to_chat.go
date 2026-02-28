package apicompat

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Non-streaming: ResponsesResponse → ChatResponse
// ---------------------------------------------------------------------------

// ResponsesToChat converts a Responses API response into a Chat Completions
// response. Reasoning output items are ignored; only message and function_call
// output items are mapped.
func ResponsesToChat(resp *ResponsesResponse) *ChatResponse {
	out := &ChatResponse{
		ID:      resp.ID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   resp.Model,
	}

	msg := ChatMessage{Role: "assistant"}
	finishReason := ResponsesStatusToChat(resp.Status, resp.IncompleteDetails)

	// Collect text content and tool calls from output items.
	var textParts []string
	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" && part.Text != "" {
					textParts = append(textParts, part.Text)
				}
			}
		case "function_call":
			msg.ToolCalls = append(msg.ToolCalls, ChatToolCall{
				ID:   item.CallID,
				Type: "function",
				Function: ChatFunctionCall{
					Name:      item.Name,
					Arguments: item.Arguments,
				},
			})
			// case "reasoning": ignored
		}
	}

	if len(textParts) > 0 {
		text := strings.Join(textParts, "")
		content, _ := json.Marshal(text)
		msg.Content = content
	}

	// If tool_calls present and no explicit stop, use "tool_calls" finish reason.
	if len(msg.ToolCalls) > 0 && finishReason == "stop" {
		finishReason = "tool_calls"
	}

	out.Choices = []ChatChoice{{
		Index:        0,
		Message:      msg,
		FinishReason: finishReason,
	}}

	if resp.Usage != nil {
		out.Usage = &ChatUsage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		}
	}

	return out
}

// ---------------------------------------------------------------------------
// Streaming: ResponsesStreamEvent → []ChatStreamChunk (stateful converter)
// ---------------------------------------------------------------------------

// ResponsesToChatStreamState tracks state for converting a sequence of
// Responses SSE events into Chat Completions SSE chunks.
type ResponsesToChatStreamState struct {
	ResponseID string
	Model      string
	Created    int64

	// toolCallIndex is the next tool_calls array index to assign.
	toolCallIndex int
	// outputIndexToToolIdx maps Responses output_index → Chat tool_calls index.
	outputIndexToToolIdx map[int]int
}

// NewResponsesToChatStreamState returns an initialised stream state.
func NewResponsesToChatStreamState() *ResponsesToChatStreamState {
	return &ResponsesToChatStreamState{
		Created:              time.Now().Unix(),
		outputIndexToToolIdx: make(map[int]int),
	}
}

// ResponsesEventToChatChunks converts a single Responses SSE event into zero
// or more Chat Completions SSE chunks, updating state as it goes.
func ResponsesEventToChatChunks(
	evt *ResponsesStreamEvent,
	state *ResponsesToChatStreamState,
) []ChatStreamChunk {
	switch evt.Type {
	case "response.created":
		return handleResponseCreated(evt, state)
	case "response.output_text.delta":
		return handleOutputTextDelta(evt, state)
	case "response.output_item.added":
		return handleOutputItemAdded(evt, state)
	case "response.function_call_arguments.delta":
		return handleFuncCallArgsDelta(evt, state)
	case "response.output_item.done":
		return handleOutputItemDone(evt, state)
	case "response.completed", "response.incomplete":
		return handleResponseCompleted(evt, state)
	default:
		// response.output_item.added (reasoning), response.output_text.done,
		// response.function_call_arguments.done, etc. — ignored or no-op.
		return nil
	}
}

// --- handler: response.created ---

func handleResponseCreated(evt *ResponsesStreamEvent, state *ResponsesToChatStreamState) []ChatStreamChunk {
	if evt.Response != nil {
		state.ResponseID = evt.Response.ID
		state.Model = evt.Response.Model
	}
	return []ChatStreamChunk{state.makeChunk(ChatStreamChoice{
		Index: 0,
		Delta: ChatStreamDelta{Role: "assistant"},
	}, nil)}
}

// --- handler: response.output_text.delta ---

func handleOutputTextDelta(evt *ResponsesStreamEvent, state *ResponsesToChatStreamState) []ChatStreamChunk {
	if evt.Delta == "" {
		return nil
	}
	return []ChatStreamChunk{state.makeChunk(ChatStreamChoice{
		Index: 0,
		Delta: ChatStreamDelta{Content: evt.Delta},
	}, nil)}
}

// --- handler: response.output_item.added (function_call only) ---

func handleOutputItemAdded(evt *ResponsesStreamEvent, state *ResponsesToChatStreamState) []ChatStreamChunk {
	if evt.Item == nil || evt.Item.Type != "function_call" {
		return nil
	}
	// Register this output_index → tool call index mapping.
	idx := state.toolCallIndex
	state.outputIndexToToolIdx[evt.OutputIndex] = idx
	state.toolCallIndex++

	return []ChatStreamChunk{state.makeChunk(ChatStreamChoice{
		Index: 0,
		Delta: ChatStreamDelta{
			ToolCalls: []ChatToolCall{{
				Index: idx,
				ID:    evt.Item.CallID,
				Type:  "function",
				Function: ChatFunctionCall{
					Name: evt.Item.Name,
				},
			}},
		},
	}, nil)}
}

// --- handler: response.function_call_arguments.delta ---

func handleFuncCallArgsDelta(evt *ResponsesStreamEvent, state *ResponsesToChatStreamState) []ChatStreamChunk {
	if evt.Delta == "" {
		return nil
	}
	idx, ok := state.outputIndexToToolIdx[evt.OutputIndex]
	if !ok {
		return nil
	}
	return []ChatStreamChunk{state.makeChunk(ChatStreamChoice{
		Index: 0,
		Delta: ChatStreamDelta{
			ToolCalls: []ChatToolCall{{
				Index: idx,
				Function: ChatFunctionCall{
					Arguments: evt.Delta,
				},
			}},
		},
	}, nil)}
}

// --- handler: response.output_item.done ---

func handleOutputItemDone(evt *ResponsesStreamEvent, state *ResponsesToChatStreamState) []ChatStreamChunk {
	if evt.Item == nil {
		return nil
	}
	// Only emit finish_reason for message items (not reasoning).
	if evt.Item.Type == "message" {
		fr := "stop"
		return []ChatStreamChunk{state.makeChunk(ChatStreamChoice{
			Index:        0,
			Delta:        ChatStreamDelta{},
			FinishReason: &fr,
		}, nil)}
	}
	return nil
}

// --- handler: response.completed / response.incomplete ---

func handleResponseCompleted(evt *ResponsesStreamEvent, state *ResponsesToChatStreamState) []ChatStreamChunk {
	var usage *ChatUsage
	if evt.Response != nil && evt.Response.Usage != nil {
		u := evt.Response.Usage
		usage = &ChatUsage{
			PromptTokens:     u.InputTokens,
			CompletionTokens: u.OutputTokens,
			TotalTokens:      u.TotalTokens,
		}
	}

	// If there were tool calls but no message item done event, emit finish.
	var chunks []ChatStreamChunk
	if state.toolCallIndex > 0 {
		fr := "tool_calls"
		chunks = append(chunks, state.makeChunk(ChatStreamChoice{
			Index:        0,
			Delta:        ChatStreamDelta{},
			FinishReason: &fr,
		}, nil))
	}

	// Final chunk with usage (empty choices).
	if usage != nil {
		chunks = append(chunks, state.makeChunk(ChatStreamChoice{}, usage))
	}
	return chunks
}

// --- helpers ---

func (s *ResponsesToChatStreamState) makeChunk(choice ChatStreamChoice, usage *ChatUsage) ChatStreamChunk {
	return ChatStreamChunk{
		ID:      s.ResponseID,
		Object:  "chat.completion.chunk",
		Created: s.Created,
		Model:   s.Model,
		Choices: []ChatStreamChoice{choice},
		Usage:   usage,
	}
}

// ResponsesStreamEventToSSE formats a ChatStreamChunk as an SSE data line.
func ResponsesStreamEventToSSE(chunk ChatStreamChunk) (string, error) {
	data, err := json.Marshal(chunk)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("data: %s\n\n", data), nil
}
