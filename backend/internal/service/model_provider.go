package service

import (
	"net/url"
	"strings"
)

func inferProviderFromAccount(acc *Account) string {
	if acc == nil {
		return ""
	}
	if isGitHubCopilotAccount(acc) {
		return ProviderCopilot
	}

	platform := strings.ToLower(strings.TrimSpace(acc.Platform))
	switch platform {
	case PlatformCopilot:
		return ProviderCopilot
	case PlatformAggregator:
		return ProviderAggregator
	case PlatformAntigravity:
		return ProviderAntigravity
	case PlatformAnthropic:
		return ProviderAnthropic
	case PlatformGemini:
		return ProviderGemini
	}

	host := normalizedHostname(acc.GetCredential("base_url"))
	if host != "" {
		if strings.HasSuffix(host, ".openai.azure.com") {
			return ProviderAzure
		}
		if host == "openrouter.ai" || strings.HasSuffix(host, ".openrouter.ai") {
			return ProviderOpenRouter
		}
	}

	return ProviderOpenAI
}

func normalizedHostname(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	candidate := trimmed
	if !strings.Contains(candidate, "://") {
		candidate = "https://" + candidate
	}
	parsed, err := url.Parse(candidate)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(parsed.Hostname()))
}
