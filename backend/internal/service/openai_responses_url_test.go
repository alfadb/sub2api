//go:build unit

package service

import "testing"

func TestOpenAIResponsesURLFromBaseURL(t *testing.T) {
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
			expectedURL: "https://api.openai.com/v1/responses",
		},
		{
			name:        "openai root trailing slash",
			baseURL:     "https://api.openai.com/",
			isCopilot:   false,
			expectedURL: "https://api.openai.com/v1/responses",
		},
		{
			name:        "openai v1",
			baseURL:     "https://api.openai.com/v1",
			isCopilot:   false,
			expectedURL: "https://api.openai.com/v1/responses",
		},
		{
			name:        "openai v1 trailing slash",
			baseURL:     "https://api.openai.com/v1/",
			isCopilot:   false,
			expectedURL: "https://api.openai.com/v1/responses",
		},
		{
			name:        "openai responses endpoint",
			baseURL:     "https://api.openai.com/v1/responses",
			isCopilot:   false,
			expectedURL: "https://api.openai.com/v1/responses",
		},
		{
			name:        "openai path prefix",
			baseURL:     "https://proxy.example.com/openai",
			isCopilot:   false,
			expectedURL: "https://proxy.example.com/openai/v1/responses",
		},
		{
			name:        "openai path prefix v1",
			baseURL:     "https://proxy.example.com/openai/v1",
			isCopilot:   false,
			expectedURL: "https://proxy.example.com/openai/v1/responses",
		},

		{
			name:        "github copilot root",
			baseURL:     "https://api.githubcopilot.com",
			isCopilot:   true,
			expectedURL: "https://api.githubcopilot.com/responses",
		},
		{
			name:        "github copilot root trailing slash",
			baseURL:     "https://api.githubcopilot.com/",
			isCopilot:   true,
			expectedURL: "https://api.githubcopilot.com/responses",
		},
		{
			name:        "github copilot v1",
			baseURL:     "https://api.githubcopilot.com/v1",
			isCopilot:   true,
			expectedURL: "https://api.githubcopilot.com/responses",
		},
		{
			name:        "github copilot v1 trailing slash",
			baseURL:     "https://api.githubcopilot.com/v1/",
			isCopilot:   true,
			expectedURL: "https://api.githubcopilot.com/responses",
		},
		{
			name:        "github copilot responses endpoint",
			baseURL:     "https://api.githubcopilot.com/responses",
			isCopilot:   true,
			expectedURL: "https://api.githubcopilot.com/responses",
		},
		{
			name:        "github copilot v1 responses endpoint",
			baseURL:     "https://api.githubcopilot.com/v1/responses",
			isCopilot:   true,
			expectedURL: "https://api.githubcopilot.com/responses",
		},
		{
			name:        "github copilot enterprise subdomain",
			baseURL:     "https://api.business.githubcopilot.com",
			isCopilot:   true,
			expectedURL: "https://api.business.githubcopilot.com/responses",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := openaiResponsesURLFromBaseURL(tt.baseURL, tt.isCopilot)
			if got != tt.expectedURL {
				t.Fatalf("openaiResponsesURLFromBaseURL(%q, %v) = %q, want %q", tt.baseURL, tt.isCopilot, got, tt.expectedURL)
			}
		})
	}
}
