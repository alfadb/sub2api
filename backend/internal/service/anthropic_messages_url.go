package service

import "strings"

func anthropicMessagesURLFromBaseURL(normalizedBaseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(normalizedBaseURL), "/")
	if strings.HasSuffix(base, "/v1/messages") {
		return base
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/messages"
	}
	return base + "/v1/messages"
}

func anthropicCountTokensURLFromBaseURL(normalizedBaseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(normalizedBaseURL), "/")
	if strings.HasSuffix(base, "/v1/messages/count_tokens") {
		return base
	}
	if strings.HasSuffix(base, "/v1/messages") {
		return base + "/count_tokens"
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/messages/count_tokens"
	}
	return base + "/v1/messages/count_tokens"
}
