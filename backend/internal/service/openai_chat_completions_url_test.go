//go:build unit

package service

import "testing"

func TestOpenAIChatCompletionsURLFromBaseURL(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		isCopilot   bool
		expectedURL string
	}{
		{
			name:        "openai root",
			baseURL:     "https://api.openai.com",
			isCopilot:   false,
			expectedURL: "https://api.openai.com/v1/chat/completions",
		},
		{
			name:        "openai root trailing slash",
			baseURL:     "https://api.openai.com/",
			isCopilot:   false,
			expectedURL: "https://api.openai.com/v1/chat/completions",
		},
		{
			name:        "openai v1",
			baseURL:     "https://api.openai.com/v1",
			isCopilot:   false,
			expectedURL: "https://api.openai.com/v1/chat/completions",
		},
		{
			name:        "openai v1 trailing slash",
			baseURL:     "https://api.openai.com/v1/",
			isCopilot:   false,
			expectedURL: "https://api.openai.com/v1/chat/completions",
		},
		{
			name:        "openai chat completions endpoint",
			baseURL:     "https://api.openai.com/v1/chat/completions",
			isCopilot:   false,
			expectedURL: "https://api.openai.com/v1/chat/completions",
		},
		{
			name:        "openai path prefix",
			baseURL:     "https://proxy.example.com/openai",
			isCopilot:   false,
			expectedURL: "https://proxy.example.com/openai/v1/chat/completions",
		},
		{
			name:        "openai path prefix v1",
			baseURL:     "https://proxy.example.com/openai/v1",
			isCopilot:   false,
			expectedURL: "https://proxy.example.com/openai/v1/chat/completions",
		},

		{
			name:        "github copilot root",
			baseURL:     "https://api.githubcopilot.com",
			isCopilot:   true,
			expectedURL: "https://api.githubcopilot.com/chat/completions",
		},
		{
			name:        "github copilot root trailing slash",
			baseURL:     "https://api.githubcopilot.com/",
			isCopilot:   true,
			expectedURL: "https://api.githubcopilot.com/chat/completions",
		},
		{
			name:        "github copilot v1",
			baseURL:     "https://api.githubcopilot.com/v1",
			isCopilot:   true,
			expectedURL: "https://api.githubcopilot.com/chat/completions",
		},
		{
			name:        "github copilot v1 trailing slash",
			baseURL:     "https://api.githubcopilot.com/v1/",
			isCopilot:   true,
			expectedURL: "https://api.githubcopilot.com/chat/completions",
		},
		{
			name:        "github copilot chat completions endpoint",
			baseURL:     "https://api.githubcopilot.com/chat/completions",
			isCopilot:   true,
			expectedURL: "https://api.githubcopilot.com/chat/completions",
		},
		{
			name:        "github copilot v1 chat completions endpoint",
			baseURL:     "https://api.githubcopilot.com/v1/chat/completions",
			isCopilot:   true,
			expectedURL: "https://api.githubcopilot.com/chat/completions",
		},
		{
			name:        "github copilot enterprise subdomain",
			baseURL:     "https://api.business.githubcopilot.com",
			isCopilot:   true,
			expectedURL: "https://api.business.githubcopilot.com/chat/completions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := openaiChatCompletionsURLFromBaseURL(tt.baseURL, tt.isCopilot)
			if got != tt.expectedURL {
				t.Fatalf("openaiChatCompletionsURLFromBaseURL(%q, %v) = %q, want %q", tt.baseURL, tt.isCopilot, got, tt.expectedURL)
			}
		})
	}
}
