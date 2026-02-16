//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInferProviderFromAccount_OpenAIBaseURL_Azure(t *testing.T) {
	acc := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://foo.openai.azure.com"}}
	require.Equal(t, ProviderAzure, inferProviderFromAccount(acc))
}

func TestInferProviderFromAccount_OpenAIBaseURL_DefaultOpenAI(t *testing.T) {
	acc := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.openai.com"}}
	require.Equal(t, ProviderOpenAI, inferProviderFromAccount(acc))
}

func TestInferProviderFromAccount_CopilotPlatform(t *testing.T) {
	acc := &Account{Platform: PlatformCopilot, Type: AccountTypeAPIKey}
	require.Equal(t, ProviderCopilot, inferProviderFromAccount(acc))
}
