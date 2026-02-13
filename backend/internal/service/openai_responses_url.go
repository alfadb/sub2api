package service

import "strings"

func openaiResponsesURLFromBaseURL(normalizedBaseURL string, isGitHubCopilot bool) string {
	base := strings.TrimRight(strings.TrimSpace(normalizedBaseURL), "/")
	if strings.HasSuffix(base, "/responses") {
		// GitHub Copilot expects /responses without /v1.
		if isGitHubCopilot && strings.HasSuffix(base, "/v1/responses") {
			base = strings.TrimSuffix(base, "/v1/responses")
			base = strings.TrimRight(base, "/")
			return base + "/responses"
		}
		return base
	}
	if isGitHubCopilot {
		if strings.HasSuffix(base, "/v1") {
			base = strings.TrimSuffix(base, "/v1")
		}
		return base + "/responses"
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/responses"
	}
	return base + "/v1/responses"
}
