//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	mw "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	svc "github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type accountRepoStubForModels struct {
	accountRepoStubForCountTokens
	listByGroupIDsAccounts []svc.Account
}

func (s *accountRepoStubForModels) ListSchedulableByGroupIDs(ctx context.Context, groupIDs []int64) ([]svc.Account, error) {
	return append([]svc.Account(nil), s.listByGroupIDsAccounts...), nil
}

func TestGatewayHandler_Models_ReturnsNamespacedModelsWithPricing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(7)
	user := &svc.User{ID: 1, AllowedGroups: []int64{groupID}}
	apiKey := &svc.APIKey{ID: 10, User: user}

	fallbackFile := filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json")
	cfg := &config.Config{
		RunMode: config.RunModeStandard,
		Pricing: config.PricingConfig{
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

	pricingSvc := svc.NewPricingService(cfg, nil)
	require.NoError(t, pricingSvc.Initialize())
	t.Cleanup(pricingSvc.Stop)

	group := &svc.Group{ID: groupID, Platform: svc.PlatformOpenAI, Status: svc.StatusActive}
	groupRepo := &groupRepoStubForCountTokens{group: group}

	accounts := []svc.Account{
		{
			ID:          1,
			Platform:    svc.PlatformOpenAI,
			Type:        svc.AccountTypeUpstream,
			Status:      svc.StatusActive,
			Schedulable: true,
			Credentials: map[string]any{
				"base_url": "https://example-resource.openai.azure.com",
				"model_mapping": map[string]any{
					"gpt-5-chat": "gpt-5-chat",
				},
			},
		},
		{
			ID:          2,
			Platform:    svc.PlatformCopilot,
			Type:        svc.AccountTypeOAuth,
			Status:      svc.StatusActive,
			Schedulable: true,
			Credentials: map[string]any{
				"model_mapping": map[string]any{
					"gpt-5.2": "gpt-5.2",
				},
			},
		},
		{
			ID:          3,
			Platform:    svc.PlatformAggregator,
			Type:        svc.AccountTypeUpstream,
			Status:      svc.StatusActive,
			Schedulable: true,
			Credentials: map[string]any{
				"model_mapping": map[string]any{
					"gpt-5.2": "gpt-5.2",
				},
			},
		},
		{
			ID:          4,
			Platform:    svc.PlatformOpenAI,
			Type:        svc.AccountTypeUpstream,
			Status:      svc.StatusActive,
			Schedulable: true,
			Credentials: map[string]any{
				"model_mapping": map[string]any{
					"gpt-5.2": "gpt-5.2",
				},
			},
		},
	}
	accountRepo := &accountRepoStubForModels{listByGroupIDsAccounts: accounts}

	gatewaySvc := svc.NewGatewayService(
		accountRepo,
		groupRepo,
		nil,
		nil,
		nil,
		nil,
		nil,
		cfg,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	h := &GatewayHandler{gatewayService: gatewaySvc, pricingService: pricingSvc}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(mw.ContextKeyAPIKey), apiKey)

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Object string          `json:"object"`
		Data   []svc.ModelInfo `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "list", resp.Object)

	byID := make(map[string]svc.ModelInfo, len(resp.Data))
	for _, m := range resp.Data {
		byID[m.ID] = m
	}

	azure := byID["azure/gpt-5-chat"]
	require.Equal(t, 272000, azure.ContextWindow)
	require.Equal(t, 128000, azure.MaxOutputTokens)
	require.Equal(t, "https://azure.microsoft.com/en-us/blog/gpt-5-in-azure-ai-foundry-the-future-of-ai-apps-and-agents-starts-here/", azure.Source)

	openai := byID["openai/gpt-5.2"]
	require.Equal(t, 400000, openai.ContextWindow)
	require.Equal(t, 128000, openai.MaxOutputTokens)

	copilot := byID["copilot/gpt-5.2"]
	require.Equal(t, 400000, copilot.ContextWindow)
	require.Equal(t, 128000, copilot.MaxOutputTokens)

	aggregator := byID["aggregator/gpt-5.2"]
	require.Equal(t, 400000, aggregator.ContextWindow)
	require.Equal(t, 128000, aggregator.MaxOutputTokens)
}
