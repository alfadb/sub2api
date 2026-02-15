package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func convertClaudeToolsToOpenAIChatTools(tools any) []any {
	arr, ok := tools.([]any)
	if !ok || len(arr) == 0 {
		return nil
	}
	out := make([]any, 0, len(arr))
	for _, t := range arr {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}

		toolType, _ := tm["type"].(string)
		toolType = strings.TrimSpace(toolType)

		name := ""
		desc := ""
		params := any(nil)
		if toolType == "custom" {
			name, _ = tm["name"].(string)
			if custom, ok := tm["custom"].(map[string]any); ok {
				desc, _ = custom["description"].(string)
				params = custom["input_schema"]
			}
		} else {
			name, _ = tm["name"].(string)
			desc, _ = tm["description"].(string)
			params = tm["input_schema"]
		}

		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}

		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        name,
				"description": strings.TrimSpace(desc),
				"parameters":  params,
			},
		})
	}
	return out
}

func convertClaudeMessagesToOpenAIChatCompletionsMessages(messages []any, system any) ([]any, error) {
	out := make([]any, 0, len(messages)+1)
	if systemText := extractClaudeSystemText(system); systemText != "" {
		out = append(out, map[string]any{"role": "system", "content": systemText})
	}

	flushMessage := func(role string, sb *strings.Builder, parts []any, usingParts bool) {
		role = strings.ToLower(strings.TrimSpace(role))
		if role == "" {
			role = "user"
		}

		if usingParts {
			if sb.Len() > 0 {
				parts = append(parts, map[string]any{"type": "text", "text": sb.String()})
				sb.Reset()
			}
			if len(parts) == 0 {
				return
			}
			out = append(out, map[string]any{"role": role, "content": parts})
			return
		}

		text := sb.String()
		sb.Reset()
		if strings.TrimSpace(text) == "" {
			return
		}
		out = append(out, map[string]any{"role": role, "content": text})
	}

	for _, m := range messages {
		mm, ok := m.(map[string]any)
		if !ok {
			continue
		}
		role, _ := mm["role"].(string)
		role = strings.ToLower(strings.TrimSpace(role))
		if role == "" {
			role = "user"
		}

		switch content := mm["content"].(type) {
		case string:
			if strings.TrimSpace(content) != "" {
				out = append(out, map[string]any{"role": role, "content": content})
			}
		case []any:
			var sb strings.Builder
			parts := make([]any, 0)
			usingParts := false

			appendText := func(text string) {
				if usingParts {
					parts = append(parts, map[string]any{"type": "text", "text": text})
					return
				}
				_, _ = sb.WriteString(text)
			}
			appendImageURL := func(url string) {
				if !usingParts {
					if sb.Len() > 0 {
						parts = append(parts, map[string]any{"type": "text", "text": sb.String()})
						sb.Reset()
					}
					usingParts = true
				}
				parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
			}

			for _, block := range content {
				bm, ok := block.(map[string]any)
				if !ok {
					continue
				}
				bt, _ := bm["type"].(string)
				bt = strings.ToLower(strings.TrimSpace(bt))

				switch bt {
				case "text":
					if text, ok := bm["text"].(string); ok {
						appendText(text)
					}
				case "thinking":
					if t, ok := bm["thinking"].(string); ok && strings.TrimSpace(t) != "" {
						appendText(t)
					}
				case "image":
					if src, ok := bm["source"].(map[string]any); ok {
						if srcType, _ := src["type"].(string); srcType == "base64" {
							mediaType, _ := src["media_type"].(string)
							data, _ := src["data"].(string)
							mediaType = strings.TrimSpace(mediaType)
							data = strings.TrimSpace(data)
							if mediaType != "" && data != "" {
								appendImageURL(fmt.Sprintf("data:%s;base64,%s", mediaType, data))
							}
						}
					}
				case "tool_use":
					flushMessage(role, &sb, parts, usingParts)
					sb.Reset()
					parts = make([]any, 0)
					usingParts = false

					id, _ := bm["id"].(string)
					name, _ := bm["name"].(string)
					id = strings.TrimSpace(id)
					name = strings.TrimSpace(name)
					if id == "" {
						id = "call_" + randomHex(12)
					}
					argsJSON, _ := json.Marshal(bm["input"])
					out = append(out, map[string]any{
						"role":    "assistant",
						"content": nil,
						"tool_calls": []any{
							map[string]any{
								"id":   id,
								"type": "function",
								"function": map[string]any{
									"name":      name,
									"arguments": string(argsJSON),
								},
							},
						},
					})
				case "tool_result":
					flushMessage(role, &sb, parts, usingParts)
					sb.Reset()
					parts = make([]any, 0)
					usingParts = false

					toolUseID, _ := bm["tool_use_id"].(string)
					toolUseID = strings.TrimSpace(toolUseID)
					output := extractClaudeContentText(bm["content"])
					out = append(out, map[string]any{
						"role":         "tool",
						"tool_call_id": toolUseID,
						"content":      output,
					})
				default:
					if b, err := json.Marshal(bm); err == nil {
						appendText(string(b))
					}
				}
			}
			flushMessage(role, &sb, parts, usingParts)
		default:
		}
	}

	return out, nil
}

func convertOpenAIChatCompletionsJSONToClaude(openaiResp []byte, originalModel string) (map[string]any, *ClaudeUsage, string, error) {
	var resp map[string]any
	if err := json.Unmarshal(openaiResp, &resp); err != nil {
		return nil, nil, "", err
	}

	usage := &ClaudeUsage{}
	if u, ok := resp["usage"].(map[string]any); ok {
		if in, ok := asInt(u["prompt_tokens"]); ok {
			usage.InputTokens = in
		}
		if out, ok := asInt(u["completion_tokens"]); ok {
			usage.OutputTokens = out
		}
	}

	content := make([]any, 0)
	stopReason := "end_turn"

	if choicesAny, ok := resp["choices"].([]any); ok && len(choicesAny) > 0 {
		choice, _ := choicesAny[0].(map[string]any)
		finishReason, _ := choice["finish_reason"].(string)
		switch strings.TrimSpace(finishReason) {
		case "length":
			stopReason = "max_tokens"
		case "tool_calls", "function_call":
			stopReason = "tool_use"
		}

		msg, _ := choice["message"].(map[string]any)
		if msg != nil {
			if text, _ := openAIChatMessageContentToText(msg["content"]); strings.TrimSpace(text) != "" {
				content = append(content, map[string]any{"type": "text", "text": text})
			}

			if toolCallsAny, ok := msg["tool_calls"].([]any); ok && len(toolCallsAny) > 0 {
				stopReason = "tool_use"
				for _, tc := range toolCallsAny {
					tcm, ok := tc.(map[string]any)
					if !ok {
						continue
					}
					callID, _ := tcm["id"].(string)
					callID = strings.TrimSpace(callID)
					if callID == "" {
						callID = "call_" + randomHex(12)
					}

					fn, _ := tcm["function"].(map[string]any)
					name, _ := fn["name"].(string)
					args, _ := fn["arguments"].(string)

					var input any
					if strings.TrimSpace(args) != "" {
						var parsed any
						if json.Unmarshal([]byte(args), &parsed) == nil {
							input = parsed
						} else {
							input = args
						}
					}

					content = append(content, map[string]any{
						"type":  "tool_use",
						"id":    callID,
						"name":  strings.TrimSpace(name),
						"input": input,
					})
				}
			} else if fc, ok := msg["function_call"].(map[string]any); ok {
				stopReason = "tool_use"
				name, _ := fc["name"].(string)
				args, _ := fc["arguments"].(string)
				callID := "call_" + randomHex(12)

				var input any
				if strings.TrimSpace(args) != "" {
					var parsed any
					if json.Unmarshal([]byte(args), &parsed) == nil {
						input = parsed
					} else {
						input = args
					}
				}
				content = append(content, map[string]any{
					"type":  "tool_use",
					"id":    callID,
					"name":  strings.TrimSpace(name),
					"input": input,
				})
			}
		}
	}

	if len(content) == 0 {
		content = append(content, map[string]any{"type": "text", "text": ""})
	}

	msgID, _ := resp["id"].(string)
	msgID = strings.TrimSpace(msgID)
	if msgID == "" {
		msgID = "msg_" + randomHex(12)
	}

	claudeResp := map[string]any{
		"id":            msgID,
		"type":          "message",
		"role":          "assistant",
		"model":         originalModel,
		"content":       content,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":                usage.InputTokens,
			"output_tokens":               usage.OutputTokens,
			"cache_creation_input_tokens": usage.CacheCreationInputTokens,
			"cache_read_input_tokens":     usage.CacheReadInputTokens,
		},
	}

	return claudeResp, usage, stopReason, nil
}

func extractOpenAIChatCompletionsInputTokens(openaiResp []byte) (int, error) {
	var resp map[string]any
	if err := json.Unmarshal(openaiResp, &resp); err != nil {
		return 0, err
	}
	usageAny, ok := resp["usage"].(map[string]any)
	if !ok {
		return 0, errors.New("missing usage")
	}
	promptTokens, ok := asInt(usageAny["prompt_tokens"])
	if !ok {
		return 0, errors.New("missing usage.prompt_tokens")
	}
	return promptTokens, nil
}

func openAIChatMessageContentToText(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case []any:
		var sb strings.Builder
		for _, part := range t {
			pm, ok := part.(map[string]any)
			if !ok {
				continue
			}
			pt, _ := pm["type"].(string)
			if strings.EqualFold(strings.TrimSpace(pt), "text") {
				if text, ok := pm["text"].(string); ok {
					_, _ = sb.WriteString(text)
				}
			}
		}
		return sb.String(), true
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return "", false
		}
		return string(b), true
	}
}
