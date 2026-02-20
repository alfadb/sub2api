package service

import (
	"net/http"
	"net/url"
	"strings"
)

const (
	githubCopilotDefaultIntegrationID = "vscode-chat"
	githubCopilotClientIdentifier     = "OpenCode/1.0"
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

func githubCopilotDefaultUserAgent() string {
	return githubCopilotClientIdentifier
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

func applyGitHubCopilotHeaders(req *http.Request) {
	if req == nil {
		return
	}
	req.Header.Set("Editor-Version", githubCopilotClientIdentifier)
	req.Header.Set("Editor-Plugin-Version", githubCopilotClientIdentifier)
	req.Header.Set("Copilot-Integration-Id", githubCopilotDefaultIntegrationID)
}

func applyGitHubCopilotTokenExchangeHeaders(req *http.Request, githubToken string) {
	if req == nil {
		return
	}
	gh := strings.TrimSpace(githubToken)
	if gh != "" {
		req.Header.Set("Authorization", "Token "+gh)
	}
	req.Header.Set("User-Agent", githubCopilotClientIdentifier)
}
