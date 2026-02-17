//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	openaiPkg "github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	mw "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	svc "github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type accountRepoStubForModels struct {
	accountRepoStubForCountTokens
	listByGroupIDsAccounts []svc.Account
}

type pricingRemoteClientAlwaysFail struct{}

func (p pricingRemoteClientAlwaysFail) FetchPricingJSON(ctx context.Context, url string) ([]byte, error) {
	return nil, errors.New("remote pricing fetch disabled in unit test")
}

func (p pricingRemoteClientAlwaysFail) FetchHashText(ctx context.Context, url string) (string, error) {
	return "", errors.New("remote pricing hash fetch disabled in unit test")
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

	pricingSvc := svc.NewPricingService(cfg, pricingRemoteClientAlwaysFail{})
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
					"gpt-5.2":                             "gpt-5.2",
					"claude-opus-4.5":                     "claude-opus-4.5",
					"gemini-2.5-pro":                      "gemini-2.5-pro",
					"openrouter/bytedance/ui-tars-1.5-7b": "openrouter/bytedance/ui-tars-1.5-7b",
				},
			},
		},
		{
			ID:          5,
			Platform:    svc.PlatformOpenAI,
			Type:        svc.AccountTypeUpstream,
			Status:      svc.StatusActive,
			Schedulable: true,
			Credentials: map[string]any{
				"base_url": "https://openrouter.ai/api/v1",
				"model_mapping": map[string]any{
					"openrouter/bytedance/ui-tars-1.5-7b": "openrouter/bytedance/ui-tars-1.5-7b",
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
		Object string            `json:"object"`
		Data   []openaiPkg.Model `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "list", resp.Object)

	byID := make(map[string]openaiPkg.Model, len(resp.Data))
	for _, m := range resp.Data {
		byID[m.ID] = m
	}

	azure := byID["azure/gpt-5-chat"]
	require.Equal(t, 272000, azure.ContextWindow)
	require.Equal(t, 128000, azure.MaxOutputTokens)
	require.Equal(t, "https://azure.microsoft.com/en-us/blog/gpt-5-in-azure-ai-foundry-the-future-of-ai-apps-and-agents-starts-here/", azure.Source)
	require.Equal(t, "azure", azure.OwnedBy)

	openai := byID["openai/gpt-5.2"]
	require.Equal(t, 400000, openai.ContextWindow)
	require.Equal(t, 128000, openai.MaxOutputTokens)
	require.Equal(t, "openai", openai.OwnedBy)

	copilot := byID["copilot/gpt-5.2"]
	require.Equal(t, 128000, copilot.ContextWindow)
	require.Equal(t, 64000, copilot.MaxOutputTokens)
	require.Equal(t, "copilot", copilot.OwnedBy)

	aggregator := byID["aggregator/gpt-5.2"]
	require.Equal(t, 400000, aggregator.ContextWindow)
	require.Equal(t, 128000, aggregator.MaxOutputTokens)
	require.Equal(t, "aggregator", aggregator.OwnedBy)

	copilotClaude := byID["copilot/claude-opus-4.5"]
	require.Equal(t, 128000, copilotClaude.ContextWindow)
	require.Equal(t, 16000, copilotClaude.MaxOutputTokens)
	require.Equal(t, "copilot", copilotClaude.OwnedBy)

	copilotGemini := byID["copilot/gemini-2.5-pro"]
	require.Equal(t, 128000, copilotGemini.ContextWindow)
	require.Equal(t, 64000, copilotGemini.MaxOutputTokens)
	require.Equal(t, "copilot", copilotGemini.OwnedBy)

	or := byID["openrouter/bytedance/ui-tars-1.5-7b"]
	require.Equal(t, 131072, or.ContextWindow)
	require.Equal(t, 2048, or.MaxOutputTokens)
	require.Equal(t, "openrouter", or.OwnedBy)
}
