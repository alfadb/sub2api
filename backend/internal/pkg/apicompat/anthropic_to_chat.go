package apicompat

import (
	"encoding/json"
	"strings"
)

// AnthropicToChat converts an Anthropic Messages request into a Chat
// Completions request suitable for forwarding to the Copilot upstream.
func AnthropicToChat(req *AnthropicRequest) (*ChatRequest, error) {
	msgs, err := convertAnthropicMessages(req.System, req.Messages)
	if err != nil {
		return nil, err
	}

	out := &ChatRequest{
		Model:       req.Model,
		Messages:    msgs,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      req.Stream,
	}

	if req.MaxTokens > 0 {
		v := req.MaxTokens
		out.MaxTokens = &v
	}

	if len(req.StopSeqs) > 0 {
		b, err := json.Marshal(req.StopSeqs)
		if err != nil {
			return nil, err
		}
		out.Stop = b
	}

	if len(req.Tools) > 0 {
		out.Tools = convertAnthropicTools(req.Tools)
	}

	return out, nil
}

// convertAnthropicMessages builds the Chat Completions messages array from
// the Anthropic system field and message list.
func convertAnthropicMessages(system json.RawMessage, msgs []AnthropicMessage) ([]ChatMessage, error) {
	var out []ChatMessage

	// System prompt → single system message.
	if len(system) > 0 {
		sysMsg, err := parseSystemPrompt(system)
		if err != nil {
			return nil, err
		}
		if sysMsg != "" {
			raw, _ := json.Marshal(sysMsg)
			out = append(out, ChatMessage{Role: "system", Content: raw})
		}
	}

	for _, m := range msgs {
		converted, err := convertOneMessage(m)
		if err != nil {
			return nil, err
		}
		out = append(out, converted...)
	}
	return out, nil
}

// parseSystemPrompt handles the Anthropic system field which can be a plain
// string or an array of text blocks.
func parseSystemPrompt(raw json.RawMessage) (string, error) {
	// Try string first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	// Try array of content blocks.
	var blocks []AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", err
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

// convertOneMessage converts a single Anthropic message into one or more Chat
// messages. A user message containing tool_result blocks is split: tool
// results become role=tool messages, remaining blocks become a user message.
func convertOneMessage(m AnthropicMessage) ([]ChatMessage, error) {
	switch m.Role {
	case "user":
		return convertUserMessage(m.Content)
	case "assistant":
		return convertAssistantMessage(m.Content)
	default:
		// Pass through unknown roles as-is.
		return []ChatMessage{{Role: m.Role, Content: m.Content}}, nil
	}
}

// convertUserMessage handles an Anthropic user message. Content can be a
// plain string or an array of blocks. tool_result blocks are extracted into
// separate role=tool messages (placed before the remaining user content to
// maintain the tool_use → tool_result → user ordering).
func convertUserMessage(raw json.RawMessage) ([]ChatMessage, error) {
	// Try plain string.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		content, _ := json.Marshal(s)
		return []ChatMessage{{Role: "user", Content: content}}, nil
	}

	var blocks []AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, err
	}

	var out []ChatMessage

	// Extract tool_result blocks first.
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		text := extractToolResultText(b)
		content, _ := json.Marshal(text)
		out = append(out, ChatMessage{
			Role:       "tool",
			Content:    content,
			ToolCallID: b.ToolUseID,
		})
	}

	// Remaining blocks → user message.
	text := extractTextFromBlocks(blocks)
	if text != "" {
		content, _ := json.Marshal(text)
		out = append(out, ChatMessage{Role: "user", Content: content})
	}

	return out, nil
}

// extractToolResultText gets the text content from a tool_result block.
// The content field can be a string or an array of content blocks.
func extractToolResultText(b AnthropicContentBlock) string {
	if len(b.Content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(b.Content, &s); err == nil {
		return s
	}
	var inner []AnthropicContentBlock
	if err := json.Unmarshal(b.Content, &inner); err == nil {
		var parts []string
		for _, ib := range inner {
			if ib.Type == "text" && ib.Text != "" {
				parts = append(parts, ib.Text)
			}
		}
		return strings.Join(parts, "\n\n")
	}
	return ""
}

// convertAssistantMessage handles an Anthropic assistant message. Thinking
// blocks are ignored (Copilot chat doesn't support them). tool_use blocks
// become tool_calls on the assistant message.
func convertAssistantMessage(raw json.RawMessage) ([]ChatMessage, error) {
	// Try plain string.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		content, _ := json.Marshal(s)
		return []ChatMessage{{Role: "assistant", Content: content}}, nil
	}

	var blocks []AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, err
	}

	msg := ChatMessage{Role: "assistant"}

	// Collect text content (skip thinking blocks).
	text := extractTextFromBlocks(blocks)
	if text != "" {
		raw, _ := json.Marshal(text)
		msg.Content = raw
	}

	// Collect tool_use → tool_calls.
	for _, b := range blocks {
		if b.Type != "tool_use" {
			continue
		}
		args := "{}"
		if len(b.Input) > 0 {
			args = string(b.Input)
		}
		msg.ToolCalls = append(msg.ToolCalls, ChatToolCall{
			ID:   b.ID,
			Type: "function",
			Function: ChatFunctionCall{
				Name:      b.Name,
				Arguments: args,
			},
		})
	}

	return []ChatMessage{msg}, nil
}

// extractTextFromBlocks joins all text blocks, ignoring thinking/tool_use/
// tool_result blocks.
func extractTextFromBlocks(blocks []AnthropicContentBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// convertAnthropicTools maps Anthropic tool definitions to Chat Completions
// function tools. The key difference is input_schema → parameters.
func convertAnthropicTools(tools []AnthropicTool) []ChatTool {
	out := make([]ChatTool, len(tools))
	for i, t := range tools {
		out[i] = ChatTool{
			Type: "function",
			Function: ChatFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		}
	}
	return out
}
