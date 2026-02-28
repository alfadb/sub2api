// Package apicompat provides stream processing for the OpenAI Responses API.
// This module handles SSE event streaming from GitHub Copilot's /responses endpoint,
// including Stream ID synchronization and reasoning event handling.
package apicompat

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// StreamEventHandler is a callback that receives processed SSE events.
// The eventType is the SSE event type (e.g., "response.output_text.delta").
// The data is the JSON payload for the event.
type StreamEventHandler func(eventType string, data string) error

// ProcessResponsesStream reads SSE events from an upstream Responses API stream,
// applies Stream ID synchronization, handles reasoning events, and forwards
// all events to the provided handler callback.
//
// This is the core streaming processor for the /responses endpoint. It:
// - Tracks response metadata (id, model, created_at)
// - Tracks active reasoning blocks with encrypted_content
// - Handles text delta/done events
// - Handles function_call delta/done events
// - Properly flushes remaining events on stream completion
//
// Parameters:
//   - reader: The upstream response body to read SSE events from
//   - handler: Callback to receive each processed event
//
// Returns:
//   - *ResponsesUsageDetail: Token usage from the response (if available)
//   - error: Any error encountered during stream processing
func ProcessResponsesStream(reader io.Reader, handler StreamEventHandler) (*ResponsesUsageDetail, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	state := NewResponsesStreamState()
	idTracker := NewStreamIdTracker()

	for scanner.Scan() {
		line := scanner.Text()

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, ":") {
			// Forward comments and empty lines as-is
			if line != "" {
				_ = handler("", line)
			}
			continue
		}

		// Parse SSE format: "event: type" or "data: json"
		if strings.HasPrefix(line, "event: ") {
			eventType := strings.TrimSpace(line[7:])
			state.currentEventType = eventType
			continue
		}

		if strings.HasPrefix(line, "data: ") {
			data := line[6:]

			// Handle stream termination
			if data == "[DONE]" {
				if err := flushRemainingEvents(state, handler); err != nil {
					return nil, err
				}
				return state.finalUsage(), nil
			}

			// Process the SSE data event
			processedData, err := processSSEEvent(state, idTracker, state.currentEventType, data)
			if err != nil {
				// Log but continue processing on parse errors
				continue
			}

			// Forward the processed event to the handler
			if state.currentEventType != "" {
				if err := handler(state.currentEventType, processedData); err != nil {
					return nil, err
				}
			} else {
				// No explicit event type, infer from data
				_ = handler("", processedData)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("stream read error: %w", err)
	}

	// Flush any remaining events on clean exit
	if err := flushRemainingEvents(state, handler); err != nil {
		return nil, err
	}

	return state.finalUsage(), nil
}

// processSSEEvent processes a single SSE event, updating state and applying
// Stream ID fixes. Returns the processed JSON data string.
func processSSEEvent(state *ResponsesStreamState, idTracker *StreamIdTracker, eventType, data string) (string, error) {
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		return "", fmt.Errorf("failed to parse event JSON: %w", err)
	}

	// Extract the type field from the JSON payload
	var typeStr string
	if typeBytes, ok := parsed["type"]; ok {
		_ = json.Unmarshal(typeBytes, &typeStr)
	}

	// Use JSON type if SSE event type is not set
	if eventType == "" && typeStr != "" {
		eventType = typeStr
	}

	// Apply Stream ID synchronization
	fixedData, err := idTracker.FixStreamIds(data, eventType)
	if err != nil {
		// If ID fix fails, use original data
		fixedData = data
	}

	// Update state based on event type
	updateStreamState(state, eventType, parsed)

	return fixedData, nil
}

// updateStreamState updates the streaming state based on the event type.
func updateStreamState(state *ResponsesStreamState, eventType string, parsed map[string]json.RawMessage) {
	switch eventType {
	case "response.created":
		state.handleResponseCreated(parsed)

	case "response.output_item.added":
		state.handleOutputItemAdded(parsed)

	case "response.output_item.done":
		state.handleOutputItemDone(parsed)

	case "response.output_text.delta":
		state.handleOutputTextDelta(parsed)

	case "response.output_text.done":
		state.handleOutputTextDone(parsed)

	case "response.function_call_arguments.delta":
		state.handleFunctionCallDelta(parsed)

	case "response.function_call_arguments.done":
		state.handleFunctionCallDone(parsed)

	case "response.reasoning_summary_part.added":
		state.handleReasoningSummaryPartAdded(parsed)

	case "response.reasoning_summary_text.delta":
		state.handleReasoningSummaryTextDelta(parsed)

	case "response.reasoning_summary_text.done":
		state.handleReasoningSummaryTextDone(parsed)

	case "response.completed", "response.incomplete":
		state.handleResponseCompleted(parsed)
	}
}

// NewResponsesStreamState creates a new stream state tracker.
func NewResponsesStreamState() *ResponsesStreamState {
	return &ResponsesStreamState{
		Created:         time.Now().Unix(),
		ToolCalls:       make([]ResponsesToolCallState, 0),
		ReasoningBlocks: make([]ResponsesReasoningState, 0),
		Status:          "in_progress",
	}
}

// handleResponseCreated processes the response.created event.
func (s *ResponsesStreamState) handleResponseCreated(parsed map[string]json.RawMessage) {
	if responseBytes, ok := parsed["response"]; ok {
		var response struct {
			ID        string `json:"id"`
			Model     string `json:"model"`
			CreatedAt int64  `json:"created_at"`
		}
		if err := json.Unmarshal(responseBytes, &response); err == nil {
			s.ID = response.ID
			s.Model = response.Model
			if response.CreatedAt > 0 {
				s.Created = response.CreatedAt
			}
		}
	}
	s.Status = "in_progress"
}

// handleOutputItemAdded processes the response.output_item.added event.
func (s *ResponsesStreamState) handleOutputItemAdded(parsed map[string]json.RawMessage) {
	var outputIndex int
	if idxBytes, ok := parsed["output_index"]; ok {
		_ = json.Unmarshal(idxBytes, &outputIndex)
	}

	if itemBytes, ok := parsed["item"]; ok {
		var item struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal(itemBytes, &item); err == nil {
			s.CurrentItemID = item.ID
			s.ContentIndex = outputIndex

			switch item.Type {
			case "message":
				s.CurrentText = ""
				s.PendingText = nil

			case "function_call":
				newToolCall := ResponsesToolCallState{
					Index:  len(s.ToolCalls),
					ItemID: item.ID,
					Status: "in_progress",
				}
				s.ToolCalls = append(s.ToolCalls, newToolCall)
				s.CurrentToolCall = &s.ToolCalls[len(s.ToolCalls)-1]

			case "reasoning":
				newReasoning := ResponsesReasoningState{
					ItemID:       item.ID,
					SummaryIndex: 0,
					Status:       "in_progress",
				}
				s.ReasoningBlocks = append(s.ReasoningBlocks, newReasoning)
				s.CurrentReasoning = &s.ReasoningBlocks[len(s.ReasoningBlocks)-1]

				// Extract encrypted_content if present
				if encryptedBytes, ok := parsed["item"]; ok {
					var reasoningItem struct {
						EncryptedContent string `json:"encrypted_content"`
					}
					if err := json.Unmarshal(encryptedBytes, &reasoningItem); err == nil {
						s.CurrentReasoning.SummaryText = reasoningItem.EncryptedContent
					}
				}
			}
		}
	}
}

// handleOutputItemDone processes the response.output_item.done event.
func (s *ResponsesStreamState) handleOutputItemDone(parsed map[string]json.RawMessage) {
	var outputIndex int
	if idxBytes, ok := parsed["output_index"]; ok {
		_ = json.Unmarshal(idxBytes, &outputIndex)
	}

	if itemBytes, ok := parsed["item"]; ok {
		var item struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal(itemBytes, &item); err == nil {
			switch item.Type {
			case "message":
				s.CurrentText = ""

			case "function_call":
				for i := range s.ToolCalls {
					if s.ToolCalls[i].ItemID == item.ID || s.ToolCalls[i].Index == outputIndex {
						s.ToolCalls[i].Status = "completed"
						s.ToolCalls[i].IsComplete = true
						break
					}
				}

			case "reasoning":
				for i := range s.ReasoningBlocks {
					if s.ReasoningBlocks[i].ItemID == item.ID {
						s.ReasoningBlocks[i].Status = "completed"
						s.ReasoningBlocks[i].IsComplete = true
						break
					}
				}
			}
		}
	}
}

// handleOutputTextDelta processes the response.output_text.delta event.
func (s *ResponsesStreamState) handleOutputTextDelta(parsed map[string]json.RawMessage) {
	if deltaBytes, ok := parsed["delta"]; ok {
		var delta string
		if err := json.Unmarshal(deltaBytes, &delta); err == nil {
			s.CurrentText += delta
		}
	}
}

// handleOutputTextDone processes the response.output_text.done event.
func (s *ResponsesStreamState) handleOutputTextDone(parsed map[string]json.RawMessage) {
	s.CurrentText = ""
}

// handleFunctionCallDelta processes the response.function_call_arguments.delta event.
func (s *ResponsesStreamState) handleFunctionCallDelta(parsed map[string]json.RawMessage) {
	var outputIndex int
	if idxBytes, ok := parsed["output_index"]; ok {
		_ = json.Unmarshal(idxBytes, &outputIndex)
	}

	if deltaBytes, ok := parsed["delta"]; ok {
		var delta string
		if err := json.Unmarshal(deltaBytes, &delta); err == nil {
			for i := range s.ToolCalls {
				if s.ToolCalls[i].Index == outputIndex {
					s.ToolCalls[i].Arguments += delta
					break
				}
			}
		}
	}
}

// handleFunctionCallDone processes the response.function_call_arguments.done event.
func (s *ResponsesStreamState) handleFunctionCallDone(parsed map[string]json.RawMessage) {
	var callID string
	if idBytes, ok := parsed["call_id"]; ok {
		_ = json.Unmarshal(idBytes, &callID)
	}

	if argumentsBytes, ok := parsed["arguments"]; ok {
		var arguments string
		if err := json.Unmarshal(argumentsBytes, &arguments); err == nil {
			for i := range s.ToolCalls {
				if s.ToolCalls[i].CallID == callID || s.ToolCalls[i].ItemID == callID {
					s.ToolCalls[i].Arguments = arguments
					break
				}
			}
		}
	}
}

// handleReasoningSummaryPartAdded processes the response.reasoning_summary_part.added event.
func (s *ResponsesStreamState) handleReasoningSummaryPartAdded(parsed map[string]json.RawMessage) {
	var summaryIndex int
	if idxBytes, ok := parsed["summary_index"]; ok {
		_ = json.Unmarshal(idxBytes, &summaryIndex)
	}

	if s.CurrentReasoning != nil {
		s.CurrentReasoning.SummaryIndex = summaryIndex
		s.CurrentReasoning.SummaryText = ""
	}
}

// handleReasoningSummaryTextDelta processes the response.reasoning_summary_text.delta event.
func (s *ResponsesStreamState) handleReasoningSummaryTextDelta(parsed map[string]json.RawMessage) {
	var summaryIndex int
	if idxBytes, ok := parsed["summary_index"]; ok {
		_ = json.Unmarshal(idxBytes, &summaryIndex)
	}

	if deltaBytes, ok := parsed["delta"]; ok {
		var delta string
		if err := json.Unmarshal(deltaBytes, &delta); err == nil {
			for i := range s.ReasoningBlocks {
				if s.ReasoningBlocks[i].SummaryIndex == summaryIndex {
					s.ReasoningBlocks[i].SummaryText += delta
					break
				}
			}
		}
	}
}

// handleReasoningSummaryTextDone processes the response.reasoning_summary_text.done event.
func (s *ResponsesStreamState) handleReasoningSummaryTextDone(parsed map[string]json.RawMessage) {
	var summaryIndex int
	if idxBytes, ok := parsed["summary_index"]; ok {
		_ = json.Unmarshal(idxBytes, &summaryIndex)
	}

	for i := range s.ReasoningBlocks {
		if s.ReasoningBlocks[i].SummaryIndex == summaryIndex {
			s.ReasoningBlocks[i].IsComplete = true
			break
		}
	}
}

// handleResponseCompleted processes the response.completed or response.incomplete event.
func (s *ResponsesStreamState) handleResponseCompleted(parsed map[string]json.RawMessage) {
	if responseBytes, ok := parsed["response"]; ok {
		var response struct {
			Status string                `json:"status"`
			Usage  *ResponsesUsageDetail `json:"usage"`
		}
		if err := json.Unmarshal(responseBytes, &response); err == nil {
			s.Status = response.Status
			if response.Usage != nil {
				s.InputTokens = response.Usage.InputTokens
				s.OutputTokens = response.Usage.OutputTokens
			}
		}
	}
}

// finalUsage returns the usage summary from the stream state.
func (s *ResponsesStreamState) finalUsage() *ResponsesUsageDetail {
	return &ResponsesUsageDetail{
		InputTokens:  s.InputTokens,
		OutputTokens: s.OutputTokens,
		TotalTokens:  s.InputTokens + s.OutputTokens,
	}
}

// flushRemainingEvents ensures all pending events are flushed before stream completion.
// This handles cases where the upstream may have sent events without corresponding
// done events.
func flushRemainingEvents(state *ResponsesStreamState, handler StreamEventHandler) error {
	// Flush any pending text
	if state.CurrentText != "" {
		doneEvent := map[string]any{
			"type":         "response.output_text.done",
			"item_id":      state.CurrentItemID,
			"output_index": state.ContentIndex,
			"text":         state.CurrentText,
		}
		if data, err := json.Marshal(doneEvent); err == nil {
			_ = handler("response.output_text.done", string(data))
		}
		state.CurrentText = ""
	}

	// Mark all incomplete tool calls as done
	for i := range state.ToolCalls {
		if !state.ToolCalls[i].IsComplete && state.ToolCalls[i].Status == "in_progress" {
			doneEvent := map[string]any{
				"type":    "response.function_call_arguments.done",
				"call_id": state.ToolCalls[i].CallID,
				"name":    state.ToolCalls[i].Name,
			}
			if state.ToolCalls[i].Arguments != "" {
				doneEvent["arguments"] = state.ToolCalls[i].Arguments
			}
			if data, err := json.Marshal(doneEvent); err == nil {
				_ = handler("response.function_call_arguments.done", string(data))
			}
		}
	}

	// Mark all incomplete reasoning blocks as done
	for i := range state.ReasoningBlocks {
		if !state.ReasoningBlocks[i].IsComplete && state.ReasoningBlocks[i].Status == "in_progress" {
			doneEvent := map[string]any{
				"type":          "response.reasoning_summary_text.done",
				"item_id":       state.ReasoningBlocks[i].ItemID,
				"summary_index": state.ReasoningBlocks[i].SummaryIndex,
			}
			if data, err := json.Marshal(doneEvent); err == nil {
				_ = handler("response.reasoning_summary_text.done", string(data))
			}
		}
	}

	return nil
}

// FormatSSEEvent formats an event type and data as an SSE line.
func FormatSSEEvent(eventType, data string) string {
	if eventType != "" {
		return fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, data)
	}
	return fmt.Sprintf("data: %s\n\n", data)
}

// FormatSSEData formats data as an SSE data line (no event type).
func FormatSSEData(data string) string {
	return fmt.Sprintf("data: %s\n", data)
}

// FormatSSEDone returns the SSE [DONE] sentinel.
func FormatSSEDone() string {
	return "data: [DONE]\n\n"
}
