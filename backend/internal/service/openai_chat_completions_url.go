package service

import "strings"

func openaiChatCompletionsURLFromBaseURL(normalizedBaseURL string, isGitHubCopilot bool) string {
	base := strings.TrimRight(strings.TrimSpace(normalizedBaseURL), "/")
	if strings.HasSuffix(base, "/chat/completions") {
		if isGitHubCopilot && strings.HasSuffix(base, "/v1/chat/completions") {
			base = strings.TrimSuffix(base, "/v1/chat/completions")
			base = strings.TrimRight(base, "/")
			return base + "/chat/completions"
		}
		return base
	}
	if isGitHubCopilot {
		base = strings.TrimSuffix(base, "/v1")
		return base + "/chat/completions"
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/chat/completions"
	}
	return base + "/v1/chat/completions"
}
