package service

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
)

func ConvertGeminiNativeRequestToClaudeMessages(model string, body []byte) ([]byte, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, errors.New("missing model")
	}

	var req antigravity.GeminiRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	claudeReq := map[string]any{
		"model":      model,
		"max_tokens": 1024,
		"stream":     false,
		"messages":   geminiContentsToClaudeMessages(req.Contents),
	}

	if req.GenerationConfig != nil {
		if req.GenerationConfig.MaxOutputTokens > 0 {
			claudeReq["max_tokens"] = req.GenerationConfig.MaxOutputTokens
		}
		if req.GenerationConfig.Temperature != nil {
			claudeReq["temperature"] = *req.GenerationConfig.Temperature
		}
		if req.GenerationConfig.TopP != nil {
			claudeReq["top_p"] = *req.GenerationConfig.TopP
		}
		if len(req.GenerationConfig.StopSequences) > 0 {
			stop := make([]any, 0, len(req.GenerationConfig.StopSequences))
			for _, s := range req.GenerationConfig.StopSequences {
				if strings.TrimSpace(s) != "" {
					stop = append(stop, s)
				}
			}
			if len(stop) > 0 {
				claudeReq["stop_sequences"] = stop
			}
		}
	}

	if sys := geminiSystemText(req.SystemInstruction); sys != "" {
		claudeReq["system"] = sys
	}

	if tools := geminiToolsToClaudeTools(req.Tools); len(tools) > 0 {
		claudeReq["tools"] = tools
	}

	return json.Marshal(claudeReq)
}

func ConvertClaudeMessageToGeminiResponse(claudeResp map[string]any, usage *ClaudeUsage) (map[string]any, error) {
	if claudeResp == nil {
		return nil, errors.New("empty response")
	}

	if usage == nil {
		usage = extractClaudeUsageFromResponse(claudeResp)
		if usage == nil {
			usage = &ClaudeUsage{}
		}
	}

	finishReason := "STOP"
	if sr, ok := claudeResp["stop_reason"].(string); ok {
		if strings.EqualFold(strings.TrimSpace(sr), "max_tokens") {
			finishReason = "MAX_TOKENS"
		}
	}

	parts := make([]any, 0)
	if content, ok := claudeResp["content"].([]any); ok {
		for _, b := range content {
			bm, ok := b.(map[string]any)
			if !ok {
				continue
			}
			bt, _ := bm["type"].(string)
			switch strings.ToLower(strings.TrimSpace(bt)) {
			case "text":
				if text, ok := bm["text"].(string); ok {
					parts = append(parts, map[string]any{"text": text})
				}
			case "tool_use":
				name, _ := bm["name"].(string)
				id, _ := bm["id"].(string)
				call := map[string]any{
					"name": strings.TrimSpace(name),
					"args": bm["input"],
				}
				if strings.TrimSpace(id) != "" {
					call["id"] = strings.TrimSpace(id)
				}
				parts = append(parts, map[string]any{"functionCall": call})
			}
		}
	} else if s, ok := claudeResp["content"].(string); ok {
		if strings.TrimSpace(s) != "" {
			parts = append(parts, map[string]any{"text": s})
		}
	}

	prompt := usage.InputTokens + usage.CacheReadInputTokens
	resp := map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"role":  "model",
					"parts": parts,
				},
				"finishReason": finishReason,
				"index":        0,
			},
		},
		"usageMetadata": map[string]any{
			"promptTokenCount":        prompt,
			"candidatesTokenCount":    usage.OutputTokens,
			"cachedContentTokenCount": usage.CacheReadInputTokens,
			"totalTokenCount":         prompt + usage.OutputTokens,
		},
	}
	return resp, nil
}

func geminiSystemText(sys *antigravity.GeminiContent) string {
	if sys == nil {
		return ""
	}
	parts := make([]string, 0, len(sys.Parts))
	for _, p := range sys.Parts {
		if s := strings.TrimSpace(p.Text); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n")
}

func geminiToolsToClaudeTools(tools []antigravity.GeminiToolDeclaration) []any {
	out := make([]any, 0)
	for _, td := range tools {
		for _, fd := range td.FunctionDeclarations {
			name := strings.TrimSpace(fd.Name)
			if name == "" {
				continue
			}
			params := any(fd.Parameters)
			if params == nil {
				params = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			out = append(out, map[string]any{
				"name":         name,
				"description":  strings.TrimSpace(fd.Description),
				"input_schema": params,
			})
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func geminiContentsToClaudeMessages(contents []antigravity.GeminiContent) []any {
	nameToCallID := make(map[string]string)
	out := make([]any, 0, len(contents))
	for _, c := range contents {
		role := strings.ToLower(strings.TrimSpace(c.Role))
		claudeRole := "user"
		if role == "model" {
			claudeRole = "assistant"
		}

		blocks := make([]any, 0)
		for _, p := range c.Parts {
			if s := strings.TrimSpace(p.Text); s != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": p.Text})
			}

			if p.InlineData != nil {
				mt := strings.TrimSpace(p.InlineData.MimeType)
				data := strings.TrimSpace(p.InlineData.Data)
				if mt != "" && data != "" {
					blocks = append(blocks, map[string]any{
						"type": "image",
						"source": map[string]any{
							"type":       "base64",
							"media_type": mt,
							"data":       data,
						},
					})
				}
			}

			if p.FunctionCall != nil {
				name := strings.TrimSpace(p.FunctionCall.Name)
				callID := strings.TrimSpace(p.FunctionCall.ID)
				if callID == "" {
					callID = nameToCallID[name]
				}
				if callID == "" {
					callID = "toolu_" + randomHex(12)
				}
				if name != "" {
					nameToCallID[name] = callID
				}
				blocks = append(blocks, map[string]any{
					"type":  "tool_use",
					"id":    callID,
					"name":  name,
					"input": p.FunctionCall.Args,
				})
			}

			if p.FunctionResponse != nil {
				name := strings.TrimSpace(p.FunctionResponse.Name)
				callID := strings.TrimSpace(p.FunctionResponse.ID)
				if callID == "" {
					callID = nameToCallID[name]
				}
				outText := ""
				if v, ok := p.FunctionResponse.Response["content"].(string); ok {
					outText = v
				} else if b, err := json.Marshal(p.FunctionResponse.Response); err == nil {
					outText = string(b)
				}
				if callID != "" {
					blocks = append(blocks, map[string]any{
						"type":        "tool_result",
						"tool_use_id": callID,
						"content":     outText,
					})
				} else if strings.TrimSpace(outText) != "" {
					blocks = append(blocks, map[string]any{"type": "text", "text": outText})
				}
			}
		}

		if len(blocks) == 0 {
			continue
		}
		out = append(out, map[string]any{
			"role":    claudeRole,
			"content": blocks,
		})
	}
	return out
}
