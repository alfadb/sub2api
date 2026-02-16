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
