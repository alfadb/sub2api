package service

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func ConvertOpenAIResponsesRequestToClaudeMessages(req map[string]any) (map[string]any, error) {
	if req == nil {
		return nil, errors.New("empty request")
	}

	model, _ := req["model"].(string)
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, errors.New("model is required")
	}

	systemParts := make([]string, 0, 2)
	if instr, ok := req["instructions"].(string); ok {
		if s := strings.TrimSpace(instr); s != "" {
			systemParts = append(systemParts, s)
		}
	}

	msgs, sysFromInput, err := convertOpenAIResponsesInputToClaudeMessages(req["input"])
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(sysFromInput) != "" {
		systemParts = append(systemParts, sysFromInput)
	}

	maxTokens := 0
	if v, ok := parseIntegralNumber(req["max_output_tokens"]); ok {
		maxTokens = v
	} else if v, ok := parseIntegralNumber(req["max_tokens"]); ok {
		maxTokens = v
	}
	if maxTokens <= 0 {
		maxTokens = 1024
	}

	claudeReq := map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
		"stream":     false,
		"messages":   msgs,
	}
	if len(systemParts) > 0 {
		claudeReq["system"] = strings.Join(systemParts, "\n")
	}

	if tools := convertOpenAIResponsesToolsToClaudeTools(req["tools"]); len(tools) > 0 {
		claudeReq["tools"] = tools
	}

	if v, ok := req["temperature"].(float64); ok {
		claudeReq["temperature"] = v
	}
	if v, ok := req["top_p"].(float64); ok {
		claudeReq["top_p"] = v
	}
	if stop, ok := req["stop"].([]any); ok && len(stop) > 0 {
		claudeReq["stop_sequences"] = stop
	} else if stopStr, ok := req["stop"].(string); ok && strings.TrimSpace(stopStr) != "" {
		claudeReq["stop_sequences"] = []any{stopStr}
	}

	return claudeReq, nil
}

func ConvertClaudeMessageToOpenAIResponsesResponse(claudeResp map[string]any, usage *ClaudeUsage, requestedModel, responseID string) (map[string]any, error) {
	if claudeResp == nil {
		return nil, errors.New("empty claude response")
	}
	if strings.TrimSpace(responseID) == "" {
		responseID = "resp_" + randomHex(12)
	}
	createdAt := time.Now().Unix()

	if usage == nil {
		usage = extractClaudeUsageFromResponse(claudeResp)
		if usage == nil {
			usage = &ClaudeUsage{}
		}
	}

	items, err := convertClaudeResponseContentToOpenAIOutputItems(claudeResp["content"])
	if err != nil {
		return nil, err
	}

	openaiUsage := map[string]any{
		"input_tokens":  usage.InputTokens,
		"output_tokens": usage.OutputTokens,
		"total_tokens":  usage.InputTokens + usage.OutputTokens,
	}
	if usage.CacheReadInputTokens > 0 {
		openaiUsage["input_tokens_details"] = map[string]any{
			"cached_tokens": usage.CacheReadInputTokens,
		}
	}

	resp := map[string]any{
		"id":         responseID,
		"object":     "response",
		"created_at": createdAt,
		"model":      strings.TrimSpace(requestedModel),
		"status":     "completed",
		"output":     items,
		"usage":      openaiUsage,
	}

	return resp, nil
}

func extractClaudeUsageFromResponse(claudeResp map[string]any) *ClaudeUsage {
	if claudeResp == nil {
		return nil
	}
	u, ok := claudeResp["usage"].(map[string]any)
	if !ok || u == nil {
		return nil
	}
	in, _ := asInt(u["input_tokens"])
	out, _ := asInt(u["output_tokens"])
	cr, _ := asInt(u["cache_read_input_tokens"])
	cc, _ := asInt(u["cache_creation_input_tokens"])
	return &ClaudeUsage{
		InputTokens:              in,
		OutputTokens:             out,
		CacheReadInputTokens:     cr,
		CacheCreationInputTokens: cc,
	}
}

func convertClaudeResponseContentToOpenAIOutputItems(content any) ([]any, error) {
	blocks, ok := content.([]any)
	if !ok {
		if s, ok := content.(string); ok {
			s = strings.TrimSpace(s)
			if s == "" {
				return []any{}, nil
			}
			return []any{map[string]any{
				"type": "message",
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "output_text", "text": s},
				},
			}}, nil
		}
		return []any{}, nil
	}

	messageContent := make([]any, 0)
	items := make([]any, 0)

	for _, b := range blocks {
		bm, ok := b.(map[string]any)
		if !ok {
			continue
		}
		bt, _ := bm["type"].(string)
		bt = strings.ToLower(strings.TrimSpace(bt))
		switch bt {
		case "text":
			if text, ok := bm["text"].(string); ok && strings.TrimSpace(text) != "" {
				messageContent = append(messageContent, map[string]any{"type": "output_text", "text": text})
			}
		case "tool_use":
			callID, _ := bm["id"].(string)
			name, _ := bm["name"].(string)
			callID = strings.TrimSpace(callID)
			name = strings.TrimSpace(name)
			args := bm["input"]
			argsJSON, _ := json.Marshal(args)
			if callID == "" {
				callID = "call_" + randomHex(12)
			}
			items = append(items, map[string]any{
				"type":      "function_call",
				"id":        callID,
				"call_id":   callID,
				"name":      name,
				"arguments": string(argsJSON),
			})
		default:
		}
	}

	if len(messageContent) > 0 {
		items = append([]any{map[string]any{
			"type":    "message",
			"role":    "assistant",
			"content": messageContent,
		}}, items...)
	}

	return items, nil
}

func convertOpenAIResponsesToolsToClaudeTools(tools any) []any {
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
		if toolType != "function" {
			continue
		}

		name, _ := tm["name"].(string)
		desc, _ := tm["description"].(string)
		params := tm["parameters"]
		if fn, ok := tm["function"].(map[string]any); ok && fn != nil {
			if v, ok := fn["name"].(string); ok {
				name = v
			}
			if v, ok := fn["description"].(string); ok {
				desc = v
			}
			if v := fn["parameters"]; v != nil {
				params = v
			}
		}

		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, map[string]any{
			"name":         name,
			"description":  strings.TrimSpace(desc),
			"input_schema": params,
		})
	}
	return out
}

func convertOpenAIResponsesInputToClaudeMessages(input any) ([]any, string, error) {
	systemParts := make([]string, 0)
	messages := make([]any, 0)

	appendMessage := func(role string, content any) {
		role = strings.ToLower(strings.TrimSpace(role))
		if role == "" {
			role = "user"
		}
		messages = append(messages, map[string]any{"role": role, "content": content})
	}

	appendTextMessage := func(role, text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		appendMessage(role, text)
	}

	convertMessageContent := func(content any) any {
		switch v := content.(type) {
		case string:
			return v
		case []any:
			blocks := make([]any, 0, len(v))
			for _, it := range v {
				im, ok := it.(map[string]any)
				if !ok {
					continue
				}
				itType, _ := im["type"].(string)
				itType = strings.ToLower(strings.TrimSpace(itType))
				switch itType {
				case "input_text", "output_text", "text":
					if text, ok := im["text"].(string); ok {
						text = strings.TrimSpace(text)
						if text != "" {
							blocks = append(blocks, map[string]any{"type": "text", "text": text})
						}
					}
				case "input_image":
					if iu, ok := im["image_url"].(map[string]any); ok {
						if urlStr, ok := iu["url"].(string); ok {
							if mediaType, data, ok := parseDataURL(urlStr); ok {
								blocks = append(blocks, map[string]any{
									"type": "image",
									"source": map[string]any{
										"type":       "base64",
										"media_type": mediaType,
										"data":       data,
									},
								})
							} else {
								blocks = append(blocks, map[string]any{"type": "text", "text": urlStr})
							}
						}
					}
				}
			}
			if len(blocks) == 0 {
				return ""
			}
			return blocks
		default:
			return ""
		}
	}

	switch v := input.(type) {
	case string:
		appendTextMessage("user", v)
	case []any:
		for _, item := range v {
			im, ok := item.(map[string]any)
			if !ok {
				continue
			}
			itType, _ := im["type"].(string)
			itType = strings.ToLower(strings.TrimSpace(itType))
			switch itType {
			case "message":
				role, _ := im["role"].(string)
				role = strings.ToLower(strings.TrimSpace(role))
				contentAny := convertMessageContent(im["content"])
				if role == "system" {
					switch c := contentAny.(type) {
					case string:
						if s := strings.TrimSpace(c); s != "" {
							systemParts = append(systemParts, s)
						}
					case []any:
						if s := extractClaudeContentText(c); strings.TrimSpace(s) != "" {
							systemParts = append(systemParts, strings.TrimSpace(s))
						}
					}
					continue
				}
				if role == "" {
					role = "user"
				}
				if contentAny == "" {
					continue
				}
				appendMessage(role, contentAny)
			case "function_call", "tool_call":
				callID, _ := im["call_id"].(string)
				if strings.TrimSpace(callID) == "" {
					callID, _ = im["id"].(string)
				}
				callID = strings.TrimSpace(callID)
				name, _ := im["name"].(string)
				name = strings.TrimSpace(name)
				argsAny := im["arguments"]
				var toolInput any
				switch a := argsAny.(type) {
				case string:
					if strings.TrimSpace(a) != "" {
						var parsed any
						if json.Unmarshal([]byte(a), &parsed) == nil {
							toolInput = parsed
						} else {
							toolInput = a
						}
					}
				default:
					toolInput = a
				}
				if callID == "" {
					callID = "call_" + randomHex(12)
				}
				appendMessage("assistant", []any{map[string]any{
					"type":  "tool_use",
					"id":    callID,
					"name":  name,
					"input": toolInput,
				}})
			case "function_call_output":
				callID, _ := im["call_id"].(string)
				callID = strings.TrimSpace(callID)
				output, _ := im["output"].(string)
				output = strings.TrimSpace(output)
				if callID == "" {
					continue
				}
				appendMessage("user", []any{map[string]any{
					"type":        "tool_result",
					"tool_use_id": callID,
					"content":     output,
				}})
			case "input_text", "text":
				if text, ok := im["text"].(string); ok {
					appendTextMessage("user", text)
				}
			default:
			}
		}
	default:
	}

	return messages, strings.Join(systemParts, "\n"), nil
}

func parseDataURL(urlStr string) (mediaType string, data string, ok bool) {
	urlStr = strings.TrimSpace(urlStr)
	if !strings.HasPrefix(urlStr, "data:") {
		return "", "", false
	}
	comma := strings.Index(urlStr, ",")
	if comma < 0 {
		return "", "", false
	}
	header := urlStr[:comma]
	payload := urlStr[comma+1:]
	if !strings.Contains(header, ";base64") {
		return "", "", false
	}
	mt := strings.TrimPrefix(header, "data:")
	mt = strings.TrimSuffix(mt, ";base64")
	mt = strings.TrimSpace(mt)
	if mt == "" || strings.TrimSpace(payload) == "" {
		return "", "", false
	}
	return mt, strings.TrimSpace(payload), true
}
