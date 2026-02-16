//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPricingService_ParsePricingData_PreservesSource(t *testing.T) {
	s := &PricingService{}
	body := []byte(`{
		"gpt-5.2": {
			"input_cost_per_token": 0.000001,
			"output_cost_per_token": 0.000002,
			"litellm_provider": "openai",
			"mode": "chat",
			"supports_prompt_caching": true,
			"max_input_tokens": 10,
			"max_output_tokens": 20,
			"source": "https://example.com/pricing"
		}
	}`)

	data, err := s.parsePricingData(body)
	require.NoError(t, err)
	require.NotNil(t, data["gpt-5.2"])
	require.Equal(t, "https://example.com/pricing", data["gpt-5.2"].Source)
}
