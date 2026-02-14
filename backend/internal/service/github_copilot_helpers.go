package service

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

const (
	githubCopilotDefaultVSCodeVersion              = "1.109.2"
	githubCopilotDefaultCopilotChatVersion         = "0.37.4"
	githubCopilotDefaultGitHubAPIVersionHeaderDate = "2025-10-01"
	githubCopilotDefaultIntegrationID              = "vscode-chat"
	githubCopilotDefaultOpenAIIntent               = "conversation-agent"
	githubCopilotDefaultUserAgentLibraryVersion    = "electron-fetch"
)

func isGitHubCopilotBaseURL(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}
	candidate := trimmed
	if !strings.Contains(candidate, "://") {
		candidate = "https://" + candidate
	}
	parsed, err := url.Parse(candidate)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return false
	}
	if host == "api.githubcopilot.com" {
		return true
	}
	return strings.HasSuffix(host, ".githubcopilot.com")
}

func isGitHubCopilotAccount(account *Account) bool {
	if account == nil {
		return false
	}
	if account.Type != AccountTypeAPIKey {
		return false
	}
	if account.Platform == PlatformCopilot {
		return true
	}
	baseURL := strings.TrimSpace(account.GetCredential("base_url"))
	if baseURL == "" {
		return false
	}
	return isGitHubCopilotBaseURL(baseURL)
}

func githubCopilotDefaultEditorVersion() string {
	return "vscode/" + githubCopilotDefaultVSCodeVersion
}

func githubCopilotDefaultEditorPluginVersion() string {
	return "copilot-chat/" + githubCopilotDefaultCopilotChatVersion
}

func githubCopilotDefaultUserAgent() string {
	return "GitHubCopilotChat/" + githubCopilotDefaultCopilotChatVersion
}

func githubCopilotVisionEnabledFromResponsesPayload(reqBody map[string]any) bool {
	if reqBody == nil {
		return false
	}
	input, ok := reqBody["input"]
	if !ok || input == nil {
		return false
	}
	items, ok := input.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := m["type"].(string); t == "input_image" {
			return true
		}
		content, ok := m["content"]
		if !ok || content == nil {
			continue
		}
		blocks, ok := content.([]any)
		if !ok {
			continue
		}
		for _, block := range blocks {
			bm, ok := block.(map[string]any)
			if !ok {
				continue
			}
			bt, _ := bm["type"].(string)
			if bt == "input_image" || bt == "image_url" {
				return true
			}
		}
	}
	return false
}

func githubCopilotInitiatorFromResponsesPayload(reqBody map[string]any) string {
	if reqBody == nil {
		return "user"
	}
	input, ok := reqBody["input"]
	if !ok || input == nil {
		return "user"
	}
	items, ok := input.([]any)
	if !ok || len(items) == 0 {
		return "user"
	}
	last, ok := items[len(items)-1].(map[string]any)
	if !ok {
		return "user"
	}
	if t, _ := last["type"].(string); t != "" {
		switch t {
		case "function_call", "function_call_output":
			return "agent"
		case "message":
		}
	}
	role, _ := last["role"].(string)
	switch role {
	case "assistant", "tool":
		return "agent"
	default:
		return "user"
	}
}

func githubCopilotVisionEnabledFromClaudeMessagesPayload(messages []any) bool {
	if len(messages) == 0 {
		return false
	}
	for _, msg := range messages {
		m, ok := msg.(map[string]any)
		if !ok {
			continue
		}
		content, ok := m["content"]
		if !ok || content == nil {
			continue
		}
		blocks, ok := content.([]any)
		if !ok {
			continue
		}
		for _, block := range blocks {
			bm, ok := block.(map[string]any)
			if !ok {
				continue
			}
			bt, _ := bm["type"].(string)
			switch bt {
			case "image", "image_url", "input_image":
				return true
			}
		}
	}
	return false
}

func githubCopilotInitiatorFromClaudeMessagesPayload(messages []any) string {
	if len(messages) == 0 {
		return "user"
	}
	last, ok := messages[len(messages)-1].(map[string]any)
	if !ok {
		return "user"
	}
	role, _ := last["role"].(string)
	if role != "user" {
		return "agent"
	}
	content, ok := last["content"]
	if !ok || content == nil {
		return "user"
	}
	blocks, ok := content.([]any)
	if !ok {
		return "user"
	}
	for _, block := range blocks {
		bm, ok := block.(map[string]any)
		if !ok {
			return "user"
		}
		bt, _ := bm["type"].(string)
		if bt != "tool_result" {
			return "user"
		}
	}
	return "agent"
}

func IsGitHubCopilotBaseURL(raw string) bool {
	return isGitHubCopilotBaseURL(raw)
}

func IsGitHubCopilotAccount(account *Account) bool {
	return isGitHubCopilotAccount(account)
}

func githubCopilotModelsURLFromBaseURL(normalizedBaseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(normalizedBaseURL), "/")
	if strings.HasSuffix(base, "/models") {
		if strings.HasSuffix(base, "/v1/models") {
			base = strings.TrimSuffix(base, "/v1/models")
			base = strings.TrimRight(base, "/")
			return base + "/models"
		}
		return base
	}
	if strings.HasSuffix(base, "/v1") {
		base = strings.TrimSuffix(base, "/v1")
		base = strings.TrimRight(base, "/")
	}
	return base + "/models"
}

func applyGitHubCopilotHeaders(req *http.Request, vision bool, initiator string) {
	if req == nil {
		return
	}
	if initiator != "agent" {
		initiator = "user"
	}
	req.Header.Set("copilot-integration-id", githubCopilotDefaultIntegrationID)
	req.Header.Set("editor-version", githubCopilotDefaultEditorVersion())
	req.Header.Set("editor-plugin-version", githubCopilotDefaultEditorPluginVersion())
	req.Header.Set("user-agent", githubCopilotDefaultUserAgent())
	req.Header.Set("openai-intent", githubCopilotDefaultOpenAIIntent)
	req.Header.Set("x-github-api-version", githubCopilotDefaultGitHubAPIVersionHeaderDate)
	req.Header.Set("x-request-id", uuid.NewString())
	req.Header.Set("x-vscode-user-agent-library-version", githubCopilotDefaultUserAgentLibraryVersion)
	req.Header.Set("X-Initiator", initiator)
	if vision {
		req.Header.Set("copilot-vision-request", "true")
	}
}

func applyGitHubCopilotTokenExchangeHeaders(req *http.Request, githubToken string) {
	if req == nil {
		return
	}
	gh := strings.TrimSpace(githubToken)
	if gh != "" {
		req.Header.Set("authorization", "token "+gh)
	}
	if strings.TrimSpace(req.Header.Get("accept")) == "" {
		req.Header.Set("accept", "application/json")
	}
	req.Header.Set("copilot-integration-id", githubCopilotDefaultIntegrationID)
	req.Header.Set("editor-version", githubCopilotDefaultEditorVersion())
	req.Header.Set("editor-plugin-version", githubCopilotDefaultEditorPluginVersion())
	req.Header.Set("user-agent", githubCopilotDefaultUserAgent())
	req.Header.Set("x-github-api-version", githubCopilotDefaultGitHubAPIVersionHeaderDate)
	req.Header.Set("x-vscode-user-agent-library-version", githubCopilotDefaultUserAgentLibraryVersion)
}
