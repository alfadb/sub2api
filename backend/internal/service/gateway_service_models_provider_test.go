//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGatewayService_CollectModelsFromAccounts_ProviderNamespaced(t *testing.T) {
	svc := &GatewayService{}
	accounts := []Account{
		{
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"base_url":      "https://api.openai.com",
				"model_mapping": map[string]any{"gpt-5.2": "gpt-5.2"},
			},
		},
		{
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"base_url":      "https://foo.openai.azure.com",
				"model_mapping": map[string]any{"gpt-5.2": "gpt-5.2"},
			},
		},
		{
			Platform: PlatformCopilot,
			Type:     AccountTypeAPIKey,
			Extra: map[string]any{
				AccountExtraKeyAvailableModels: []any{"gpt-5.2"},
			},
		},
	}

	got := svc.collectModelsFromAccounts(accounts, "")
	require.Contains(t, got, "openai/gpt-5.2")
	require.Contains(t, got, "azure/gpt-5.2")
	require.Contains(t, got, "copilot/gpt-5.2")
}

func TestGatewayService_CollectModelsFromAccounts_OpenAIDefaultModels(t *testing.T) {
	svc := &GatewayService{}

	accounts := []Account{
		{
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Credentials: map[string]any{"base_url": "https://api.openai.com"},
		},
		{
			Platform:    PlatformOpenAI,
			Type:        AccountTypeOAuth,
			Credentials: map[string]any{},
		},
		{
			Platform:    PlatformAggregator,
			Type:        AccountTypeAPIKey,
			Credentials: map[string]any{},
		},
	}

	got := svc.collectModelsFromAccounts(accounts, "")
	require.NotEmpty(t, got, "should return default models for OpenAI accounts without model_mapping")
	require.Contains(t, got, "openai/gpt-5.1-codex", "should contain default OpenAI model")
	require.Contains(t, got, "openai/gpt-5.2", "should contain default OpenAI model")
	require.Contains(t, got, "aggregator/gpt-5.1-codex", "should contain default model with aggregator prefix")
}

func TestGatewayService_CollectModelsFromAccounts_MixedMapping(t *testing.T) {
	svc := &GatewayService{}

	accounts := []Account{
		{
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"model_mapping": map[string]any{"gpt-4o": "gpt-4o"},
			},
		},
		{
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Credentials: map[string]any{},
		},
	}

	got := svc.collectModelsFromAccounts(accounts, "")
	require.Contains(t, got, "openai/gpt-4o", "should contain model from mapping")
	require.Contains(t, got, "openai/gpt-5.1-codex", "should contain default model for account without mapping")
}
