//go:build unit

package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type pricingRemoteClientStatic struct {
	body []byte
}

func (c pricingRemoteClientStatic) FetchPricingJSON(ctx context.Context, url string) ([]byte, error) {
	return append([]byte(nil), c.body...), nil
}

func (c pricingRemoteClientStatic) FetchHashText(ctx context.Context, url string) (string, error) {
	return "", nil
}

func TestPricingService_GetModelInfo_PrefersFallbackTokenLimits(t *testing.T) {
	fallbackFile := filepath.Join(t.TempDir(), "fallback_token_limits.json")
	require.NoError(t, os.WriteFile(fallbackFile, []byte(`{
		"gpt-5.2": {
			"max_input_tokens": 400000,
			"max_output_tokens": 128000,
			"litellm_provider": "openai",
			"mode": "chat"
		},
		"github_copilot/text-embedding-3-small-inference": {
			"litellm_provider": "github_copilot",
			"max_input_tokens": 8191,
			"max_tokens": 8191,
			"mode": "embedding"
		}
	}`), 0644))

	cfg := &config.Config{
		RunMode: config.RunModeStandard,
		Pricing: config.PricingConfig{
			RemoteURL:                "https://example.com/model_prices_and_context_window.json",
			HashURL:                  "",
			DataDir:                  t.TempDir(),
			FallbackFile:             fallbackFile,
			UpdateIntervalHours:      24,
			HashCheckIntervalMinutes: 10,
		},
		Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{
				Enabled:           false,
				AllowInsecureHTTP: false,
			},
		},
	}

	remoteBody := []byte(`{
		"gpt-5.2": {
			"input_cost_per_token": 0.00000175,
			"output_cost_per_token": 0.000014,
			"litellm_provider": "openai",
			"max_input_tokens": 272000,
			"max_output_tokens": 128000,
			"mode": "chat"
		}
	}`)

	pricingSvc := NewPricingService(cfg, pricingRemoteClientStatic{body: remoteBody})
	require.NoError(t, pricingSvc.Initialize())
	t.Cleanup(pricingSvc.Stop)

	info := pricingSvc.GetModelInfo("openai", "gpt-5.2")
	require.NotNil(t, info)
	require.Equal(t, 400000, info.ContextWindow)
	require.Equal(t, 128000, info.MaxOutputTokens)

	info = pricingSvc.GetModelInfo("copilot", "text-embedding-3-small-inference")
	require.NotNil(t, info)
	require.Equal(t, 8191, info.ContextWindow)
	require.Equal(t, 8191, info.MaxOutputTokens)
}

func TestPricingService_GetModelInfo_UsesEmbeddedFallbackWhenFallbackFileMissing(t *testing.T) {
	missingFallback := filepath.Join(t.TempDir(), "missing_fallback.json")

	cfg := &config.Config{
		RunMode: config.RunModeStandard,
		Pricing: config.PricingConfig{
			RemoteURL:                "https://example.com/model_prices_and_context_window.json",
			HashURL:                  "",
			DataDir:                  t.TempDir(),
			FallbackFile:             missingFallback,
			UpdateIntervalHours:      24,
			HashCheckIntervalMinutes: 10,
		},
		Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{
				Enabled:           false,
				AllowInsecureHTTP: false,
			},
		},
	}

	remoteBody := []byte(`{
		"gpt-5.2": {
			"input_cost_per_token": 0.00000175,
			"output_cost_per_token": 0.000014,
			"litellm_provider": "openai",
			"max_input_tokens": 272000,
			"max_output_tokens": 128000,
			"mode": "chat"
		}
	}`)

	pricingSvc := NewPricingService(cfg, pricingRemoteClientStatic{body: remoteBody})
	require.NoError(t, pricingSvc.Initialize())
	t.Cleanup(pricingSvc.Stop)

	info := pricingSvc.GetModelInfo("copilot", "text-embedding-3-small-inference")
	require.NotNil(t, info)
	require.Equal(t, 8191, info.ContextWindow)
	require.Equal(t, 8191, info.MaxOutputTokens)
}

func TestPricingService_GetModelInfo_UsesRemoteTokenLimitsWithoutPricing(t *testing.T) {
	fallbackFile := filepath.Join(t.TempDir(), "fallback_token_limits.json")
	require.NoError(t, os.WriteFile(fallbackFile, []byte(`{
		"gpt-5.2": {
			"max_input_tokens": 400000,
			"max_output_tokens": 128000,
			"litellm_provider": "openai",
			"mode": "chat"
		}
	}`), 0644))

	cfg := &config.Config{
		RunMode: config.RunModeStandard,
		Pricing: config.PricingConfig{
			RemoteURL:                "https://example.com/model_prices_and_context_window.json",
			HashURL:                  "",
			DataDir:                  t.TempDir(),
			FallbackFile:             fallbackFile,
			UpdateIntervalHours:      24,
			HashCheckIntervalMinutes: 10,
		},
		Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{
				Enabled:           false,
				AllowInsecureHTTP: false,
			},
		},
	}

	remoteBody := []byte(`{
		"gpt-5.2": {
			"input_cost_per_token": 0.00000175,
			"output_cost_per_token": 0.000014,
			"litellm_provider": "openai",
			"max_input_tokens": 272000,
			"max_output_tokens": 128000,
			"mode": "chat"
		},
		"oswe-vscode-prime": {
			"litellm_provider": "github_copilot",
			"max_input_tokens": 12345,
			"max_tokens": 6789,
			"mode": "chat"
		}
	}`)

	pricingSvc := NewPricingService(cfg, pricingRemoteClientStatic{body: remoteBody})
	require.NoError(t, pricingSvc.Initialize())
	t.Cleanup(pricingSvc.Stop)

	info := pricingSvc.GetModelInfo("copilot", "oswe-vscode-prime")
	require.NotNil(t, info)
	require.Equal(t, 12345, info.ContextWindow)
	require.Equal(t, 6789, info.MaxOutputTokens)
}

func TestPricingService_GetModelInfo_SuffixFallbackFindsTokenLimits(t *testing.T) {
	fallbackFile := filepath.Join(t.TempDir(), "fallback_token_limits.json")
	require.NoError(t, os.WriteFile(fallbackFile, []byte(`{
		"gpt-5.2": {
			"max_input_tokens": 400000,
			"max_output_tokens": 128000,
			"litellm_provider": "openai",
			"mode": "chat"
		}
	}`), 0644))

	cfg := &config.Config{
		RunMode: config.RunModeStandard,
		Pricing: config.PricingConfig{
			RemoteURL:                "https://example.com/model_prices_and_context_window.json",
			HashURL:                  "",
			DataDir:                  t.TempDir(),
			FallbackFile:             fallbackFile,
			UpdateIntervalHours:      24,
			HashCheckIntervalMinutes: 10,
		},
		Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{
				Enabled:           false,
				AllowInsecureHTTP: false,
			},
		},
	}

	remoteBody := []byte(`{
		"gpt-5.2": {
			"input_cost_per_token": 0.00000175,
			"output_cost_per_token": 0.000014,
			"litellm_provider": "openai",
			"max_input_tokens": 272000,
			"max_output_tokens": 128000,
			"mode": "chat"
		},
		"xai/grok-code-fast-1": {
			"litellm_provider": "xai",
			"max_input_tokens": 200,
			"max_output_tokens": 200,
			"mode": "chat"
		},
		"azure_ai/grok-code-fast-1": {
			"litellm_provider": "azure_ai",
			"max_input_tokens": 100,
			"max_output_tokens": 100,
			"mode": "chat"
		}
	}`)

	pricingSvc := NewPricingService(cfg, pricingRemoteClientStatic{body: remoteBody})
	require.NoError(t, pricingSvc.Initialize())
	t.Cleanup(pricingSvc.Stop)

	info := pricingSvc.GetModelInfo("copilot", "grok-code-fast-1")
	require.NotNil(t, info)
	require.Equal(t, 100, info.ContextWindow)
	require.Equal(t, 100, info.MaxOutputTokens)
}

func TestPricingService_GetModelInfo_CopilotOSWEUsesDefaultTokenLimits(t *testing.T) {
	missingFallback := filepath.Join(t.TempDir(), "missing_fallback.json")

	cfg := &config.Config{
		RunMode: config.RunModeStandard,
		Pricing: config.PricingConfig{
			RemoteURL:                "https://example.com/model_prices_and_context_window.json",
			HashURL:                  "",
			DataDir:                  t.TempDir(),
			FallbackFile:             missingFallback,
			UpdateIntervalHours:      24,
			HashCheckIntervalMinutes: 10,
		},
		Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{
				Enabled:           false,
				AllowInsecureHTTP: false,
			},
		},
	}

	remoteBody := []byte(`{
		"gpt-5.2": {
			"input_cost_per_token": 0.00000175,
			"output_cost_per_token": 0.000014,
			"litellm_provider": "openai",
			"max_input_tokens": 272000,
			"max_output_tokens": 128000,
			"mode": "chat"
		}
	}`)

	pricingSvc := NewPricingService(cfg, pricingRemoteClientStatic{body: remoteBody})
	require.NoError(t, pricingSvc.Initialize())
	t.Cleanup(pricingSvc.Stop)

	info := pricingSvc.GetModelInfo("copilot", "oswe-vscode-prime")
	require.NotNil(t, info)
	require.Equal(t, 128000, info.ContextWindow)
	require.Equal(t, 16384, info.MaxOutputTokens)

	info = pricingSvc.GetModelInfo("copilot", "oswe-vscode-secondary")
	require.NotNil(t, info)
	require.Equal(t, 128000, info.ContextWindow)
	require.Equal(t, 16384, info.MaxOutputTokens)
}

func TestPricingService_GetModelInfo_ClaudeDotVersionResolvesDashTokenLimits(t *testing.T) {
	missingFallback := filepath.Join(t.TempDir(), "missing_fallback.json")

	cfg := &config.Config{
		RunMode: config.RunModeStandard,
		Pricing: config.PricingConfig{
			RemoteURL:                "https://example.com/model_prices_and_context_window.json",
			HashURL:                  "",
			DataDir:                  t.TempDir(),
			FallbackFile:             missingFallback,
			UpdateIntervalHours:      24,
			HashCheckIntervalMinutes: 10,
		},
		Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{
				Enabled:           false,
				AllowInsecureHTTP: false,
			},
		},
	}

	remoteBody := []byte(`{
		"claude-opus-4-5": {
			"input_cost_per_token": 0.000005,
			"output_cost_per_token": 0.000025,
			"litellm_provider": "anthropic",
			"max_input_tokens": 200000,
			"max_output_tokens": 64000,
			"mode": "chat"
		},
		"claude-sonnet-4-5": {
			"input_cost_per_token": 0.000003,
			"output_cost_per_token": 0.000015,
			"litellm_provider": "anthropic",
			"max_input_tokens": 200000,
			"max_output_tokens": 64000,
			"mode": "chat"
		}
	}`)

	pricingSvc := NewPricingService(cfg, pricingRemoteClientStatic{body: remoteBody})
	require.NoError(t, pricingSvc.Initialize())
	t.Cleanup(pricingSvc.Stop)

	info := pricingSvc.GetModelInfo("anthropic", "claude-opus-4.5")
	require.NotNil(t, info)
	require.Equal(t, 200000, info.ContextWindow)
	require.Equal(t, 64000, info.MaxOutputTokens)

	info = pricingSvc.GetModelInfo("anthropic", "claude-sonnet-4.5")
	require.NotNil(t, info)
	require.Equal(t, 200000, info.ContextWindow)
	require.Equal(t, 64000, info.MaxOutputTokens)
}
