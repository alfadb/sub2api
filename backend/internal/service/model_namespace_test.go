package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/stretchr/testify/assert"
)

func TestParseModelNamespace(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected ModelNamespace
	}{
		{
			name:  "simple model name",
			input: "gpt-4o",
			expected: ModelNamespace{
				Provider: ProviderOpenAI,
				Platform: PlatformOpenAI,
				Model:    "gpt-4o",
			},
		},
		{
			name:  "claude model",
			input: "claude-sonnet-4-5",
			expected: ModelNamespace{
				Provider: ProviderAnthropic,
				Platform: PlatformAnthropic,
				Model:    "claude-sonnet-4-5",
			},
		},
		{
			name:  "gemini model",
			input: "gemini-2.5-flash",
			expected: ModelNamespace{
				Provider: ProviderGemini,
				Platform: PlatformGemini,
				Model:    "gemini-2.5-flash",
			},
		},
		{
			name:  "provider namespace - openai",
			input: "openai/gpt-5.2",
			expected: ModelNamespace{
				Provider:     ProviderOpenAI,
				Platform:     PlatformOpenAI,
				Model:        "gpt-5.2",
				HasNamespace: true,
			},
		},
		{
			name:  "provider namespace - copilot",
			input: "copilot/gpt-5.2",
			expected: ModelNamespace{
				Provider:     ProviderCopilot,
				Platform:     PlatformCopilot,
				Model:        "gpt-5.2",
				HasNamespace: true,
			},
		},
		{
			name:  "provider namespace - aggregator",
			input: "aggregator/gpt-5.2",
			expected: ModelNamespace{
				Provider:     ProviderAggregator,
				Platform:     PlatformAggregator,
				Model:        "gpt-5.2",
				HasNamespace: true,
			},
		},
		{
			name:  "provider namespace - azure",
			input: "azure/gpt-5.2",
			expected: ModelNamespace{
				Provider:     ProviderAzure,
				Platform:     PlatformOpenAI,
				Model:        "gpt-5.2",
				HasNamespace: true,
			},
		},
		{
			name:  "provider namespace - anthropic",
			input: "anthropic/claude-sonnet-4-5",
			expected: ModelNamespace{
				Provider:     ProviderAnthropic,
				Platform:     PlatformAnthropic,
				Model:        "claude-sonnet-4-5",
				HasNamespace: true,
			},
		},
		{
			name:  "provider alias - claude",
			input: "claude/claude-sonnet-4-5",
			expected: ModelNamespace{
				Provider:     ProviderAnthropic,
				Platform:     PlatformAnthropic,
				Model:        "claude-sonnet-4-5",
				HasNamespace: true,
			},
		},
		{
			name:  "provider alias - github",
			input: "github/gpt-4o",
			expected: ModelNamespace{
				Provider:     ProviderCopilot,
				Platform:     PlatformCopilot,
				Model:        "gpt-4o",
				HasNamespace: true,
			},
		},
		{
			name:  "empty string",
			input: "",
			expected: ModelNamespace{
				Provider: "",
				Platform: "",
				Model:    "",
			},
		},
		{
			name:  "whitespace only",
			input: "   ",
			expected: ModelNamespace{
				Provider: "",
				Platform: "",
				Model:    "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseModelNamespace(tt.input)
			assert.Equal(t, tt.expected.Provider, result.Provider)
			assert.Equal(t, tt.expected.Platform, result.Platform)
			assert.Equal(t, tt.expected.Model, result.Model)
			assert.Equal(t, tt.expected.HasNamespace, result.HasNamespace)
		})
	}
}

func TestModelNamespace_NamespacedModel(t *testing.T) {
	tests := []struct {
		name     string
		ns       ModelNamespace
		expected string
	}{
		{
			name: "with namespace",
			ns: ModelNamespace{
				Provider:     ProviderOpenAI,
				Model:        "gpt-4o",
				HasNamespace: true,
			},
			expected: "openai/gpt-4o",
		},
		{
			name: "without namespace",
			ns: ModelNamespace{
				Provider: ProviderOpenAI,
				Model:    "gpt-4o",
			},
			expected: "gpt-4o",
		},
		{
			name: "empty provider",
			ns: ModelNamespace{
				Model:        "gpt-4o",
				HasNamespace: true,
			},
			expected: "gpt-4o",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.ns.NamespacedModel())
		})
	}
}

func TestProviderToPlatform(t *testing.T) {
	tests := []struct {
		provider   string
		expectedOK bool
		platform   string
	}{
		{domain.ProviderOpenAI, true, domain.PlatformOpenAI},
		{domain.ProviderAzure, true, domain.PlatformOpenAI},
		{domain.ProviderCopilot, true, domain.PlatformCopilot},
		{domain.ProviderAggregator, true, domain.PlatformAggregator},
		{domain.ProviderAnthropic, true, domain.PlatformAnthropic},
		{domain.ProviderGemini, true, domain.PlatformGemini},
		{domain.ProviderVertexAI, true, domain.PlatformGemini},
		{domain.ProviderAntigravity, true, domain.PlatformAntigravity},
		{"unknown", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			platform := domain.GetPlatformFromProvider(tt.provider)
			if tt.expectedOK {
				assert.Equal(t, tt.platform, platform)
			} else {
				assert.Empty(t, platform)
			}
		})
	}
}
