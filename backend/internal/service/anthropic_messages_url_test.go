//go:build unit

package service

import "testing"

func TestAnthropicMessagesURLFromBaseURL(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		expected string
	}{
		{
			name:     "root",
			baseURL:  "https://api.anthropic.com",
			expected: "https://api.anthropic.com/v1/messages",
		},
		{
			name:     "root trailing slash",
			baseURL:  "https://api.anthropic.com/",
			expected: "https://api.anthropic.com/v1/messages",
		},
		{
			name:     "v1",
			baseURL:  "https://api.anthropic.com/v1",
			expected: "https://api.anthropic.com/v1/messages",
		},
		{
			name:     "v1 trailing slash",
			baseURL:  "https://api.anthropic.com/v1/",
			expected: "https://api.anthropic.com/v1/messages",
		},
		{
			name:     "messages endpoint",
			baseURL:  "https://api.anthropic.com/v1/messages",
			expected: "https://api.anthropic.com/v1/messages",
		},
		{
			name:     "messages endpoint trailing slash",
			baseURL:  "https://api.anthropic.com/v1/messages/",
			expected: "https://api.anthropic.com/v1/messages",
		},
		{
			name:     "path prefix",
			baseURL:  "https://proxy.example.com/anthropic",
			expected: "https://proxy.example.com/anthropic/v1/messages",
		},
		{
			name:     "github copilot root",
			baseURL:  "https://api.githubcopilot.com",
			expected: "https://api.githubcopilot.com/v1/messages",
		},
		{
			name:     "github copilot v1",
			baseURL:  "https://api.githubcopilot.com/v1",
			expected: "https://api.githubcopilot.com/v1/messages",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := anthropicMessagesURLFromBaseURL(tt.baseURL)
			if got != tt.expected {
				t.Fatalf("anthropicMessagesURLFromBaseURL(%q) = %q, want %q", tt.baseURL, got, tt.expected)
			}
		})
	}
}

func TestAnthropicCountTokensURLFromBaseURL(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		expected string
	}{
		{
			name:     "root",
			baseURL:  "https://api.anthropic.com",
			expected: "https://api.anthropic.com/v1/messages/count_tokens",
		},
		{
			name:     "root trailing slash",
			baseURL:  "https://api.anthropic.com/",
			expected: "https://api.anthropic.com/v1/messages/count_tokens",
		},
		{
			name:     "v1",
			baseURL:  "https://api.anthropic.com/v1",
			expected: "https://api.anthropic.com/v1/messages/count_tokens",
		},
		{
			name:     "v1 trailing slash",
			baseURL:  "https://api.anthropic.com/v1/",
			expected: "https://api.anthropic.com/v1/messages/count_tokens",
		},
		{
			name:     "messages endpoint",
			baseURL:  "https://api.anthropic.com/v1/messages",
			expected: "https://api.anthropic.com/v1/messages/count_tokens",
		},
		{
			name:     "count_tokens endpoint",
			baseURL:  "https://api.anthropic.com/v1/messages/count_tokens",
			expected: "https://api.anthropic.com/v1/messages/count_tokens",
		},
		{
			name:     "count_tokens endpoint trailing slash",
			baseURL:  "https://api.anthropic.com/v1/messages/count_tokens/",
			expected: "https://api.anthropic.com/v1/messages/count_tokens",
		},
		{
			name:     "path prefix",
			baseURL:  "https://proxy.example.com/anthropic",
			expected: "https://proxy.example.com/anthropic/v1/messages/count_tokens",
		},
		{
			name:     "github copilot root",
			baseURL:  "https://api.githubcopilot.com",
			expected: "https://api.githubcopilot.com/v1/messages/count_tokens",
		},
		{
			name:     "github copilot v1",
			baseURL:  "https://api.githubcopilot.com/v1",
			expected: "https://api.githubcopilot.com/v1/messages/count_tokens",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := anthropicCountTokensURLFromBaseURL(tt.baseURL)
			if got != tt.expected {
				t.Fatalf("anthropicCountTokensURLFromBaseURL(%q) = %q, want %q", tt.baseURL, got, tt.expected)
			}
		})
	}
}
