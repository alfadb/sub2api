package apicompat

import (
	"encoding/json"
)

// minMaxOutputTokens is the floor for max_output_tokens in a Responses request.
const minMaxOutputTokens = 128

// ChatToResponses converts a Chat Completions request into a Responses API
// request. This is used when a client sends /chat/completions for a model that
// only supports the /responses endpoint (e.g. codex models).
func ChatToResponses(req *ChatRequest) (*ResponsesRequest, error) {
	input, err := convertChatMessages(req.Messages)
	if err != nil {
		return nil, err
	}

	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}

	out := &ResponsesRequest{
		Model:       req.Model,
		Input:       inputJSON,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      req.Stream,
		Include:     []string{"reasoning.encrypted_content"},
	}

	// store: false
	storeFalse := false
	out.Store = &storeFalse

	// max_tokens → max_output_tokens with floor
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		v := *req.MaxTokens
		if v < minMaxOutputTokens {
			v = minMaxOutputTokens
		}
		out.MaxOutputTokens = &v
	}

	// tools passthrough: ChatTool (function) → ResponsesTool (function)
	if len(req.Tools) > 0 {
		out.Tools = convertChatToolsToResponses(req.Tools)
	}

	return out, nil
}

// convertChatMessages translates the Chat Completions messages array into a
// Responses API input items array.
func convertChatMessages(msgs []ChatMessage) ([]ResponsesInputItem, error) {
	var out []ResponsesInputItem

	for _, m := range msgs {
		items, err := chatMsgToResponsesItem(m)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	return out, nil
}

// chatMsgToResponsesItem dispatches a single ChatMessage by role.
func chatMsgToResponsesItem(m ChatMessage) ([]ResponsesInputItem, error) {
	switch m.Role {
	case "system":
		return chatSystemToResponses(m)
	case "user":
		return chatUserToResponses(m)
	case "assistant":
		return chatAssistantToResponses(m)
	case "tool":
		return chatToolToResponses(m)
	default:
		// Unknown role — pass as user message to avoid data loss.
		return chatUserToResponses(m)
	}
}

// chatSystemToResponses maps a system ChatMessage to a Responses input message.
func chatSystemToResponses(m ChatMessage) ([]ResponsesInputItem, error) {
	text := extractChatContentText(m.Content)
	content, _ := json.Marshal(text)
	return []ResponsesInputItem{{
		Role:    "system",
		Content: content,
	}}, nil
}

// chatUserToResponses maps a user ChatMessage to a Responses input message.
func chatUserToResponses(m ChatMessage) ([]ResponsesInputItem, error) {
	text := extractChatContentText(m.Content)
	content, _ := json.Marshal(text)
	return []ResponsesInputItem{{
		Role:    "user",
		Content: content,
	}}, nil
}

// chatAssistantToResponses maps an assistant ChatMessage to one or more
// Responses input items: a message for text content, plus function_call items
// for each tool call.
func chatAssistantToResponses(m ChatMessage) ([]ResponsesInputItem, error) {
	var items []ResponsesInputItem

	// Text content → assistant message with output_text content part.
	text := extractChatContentText(m.Content)
	if text != "" {
		parts := []ResponsesContentPart{{Type: "output_text", Text: text}}
		partsJSON, err := json.Marshal(parts)
		if err != nil {
			return nil, err
		}
		items = append(items, ResponsesInputItem{
			Role:    "assistant",
			Content: partsJSON,
		})
	}

	// Tool calls → function_call items.
	for _, tc := range m.ToolCalls {
		items = append(items, ResponsesInputItem{
			Type:      "function_call",
			CallID:    tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}

	return items, nil
}

// chatToolToResponses maps a tool ChatMessage to a function_call_output item.
func chatToolToResponses(m ChatMessage) ([]ResponsesInputItem, error) {
	output := extractChatContentText(m.Content)
	return []ResponsesInputItem{{
		Type:   "function_call_output",
		CallID: m.ToolCallID,
		Output: output,
	}}, nil
}

// extractChatContentText extracts a plain text string from ChatMessage.Content,
// which may be a JSON string or an array of content parts.
func extractChatContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	// Try string first (most common).
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}

	// Try array of content parts.
	var parts []ChatContentPart
	if json.Unmarshal(raw, &parts) == nil {
		for _, p := range parts {
			if p.Type == "text" && p.Text != "" {
				return p.Text
			}
		}
	}

	return ""
}

// convertChatToolsToResponses maps Chat Completions function tools to
// Responses API function tools. The schema is passed through as-is.
func convertChatToolsToResponses(tools []ChatTool) []ResponsesTool {
	out := make([]ResponsesTool, 0, len(tools))
	for _, t := range tools {
		if t.Type != "function" {
			continue
		}
		out = append(out, ResponsesTool{
			Type:        "function",
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		})
	}
	return out
}
