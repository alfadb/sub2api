package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIGatewayService_Forward_NormalizesOutputTokenCaps(t *testing.T) {
	gin.SetMode(gin.TestMode)

	platforms := []string{PlatformOpenAI, PlatformCopilot, PlatformAggregator}
	scenarios := []struct {
		name       string
		extraBody  map[string]any
		wantMaxOut float64
	}{
		{
			name:       "max_output_tokens_keeps_and_drops_legacy",
			extraBody:  map[string]any{"max_output_tokens": 77, "max_tokens": 88, "max_completion_tokens": 99},
			wantMaxOut: 77,
		},
		{
			name:       "max_tokens_maps_to_max_output_tokens",
			extraBody:  map[string]any{"max_tokens": 55},
			wantMaxOut: 55,
		},
		{
			name:       "max_completion_tokens_maps_to_max_output_tokens",
			extraBody:  map[string]any{"max_completion_tokens": 66},
			wantMaxOut: 66,
		},
	}

	for _, platform := range platforms {
		for _, scenario := range scenarios {
			t.Run(platform+"_"+scenario.name, func(t *testing.T) {
				type capturedReq struct {
					Path string
					JSON map[string]any
				}
				capCh := make(chan capturedReq, 1)

				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, _ := io.ReadAll(r.Body)
					var m map[string]any
					_ = json.Unmarshal(body, &m)
					capCh <- capturedReq{Path: r.URL.Path, JSON: m}

					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{
						"usage": map[string]any{"input_tokens": 1, "output_tokens": 1},
					})
				}))
				defer server.Close()

				cfg := &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false, AllowInsecureHTTP: true}}}
				upstream := &testHTTPUpstream{}
				svc := NewOpenAIGatewayService(nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, upstream, nil, nil, nil)

				account := &Account{
					ID:       1,
					Name:     "openai-test",
					Platform: platform,
					Type:     AccountTypeAPIKey,
					Credentials: map[string]any{
						"api_key":  "sk-test",
						"base_url": server.URL,
					},
					Concurrency: 1,
				}

				reqBody := map[string]any{
					"model":  "gpt-5.2",
					"stream": false,
				}
				for k, v := range scenario.extraBody {
					reqBody[k] = v
				}
				body, err := json.Marshal(reqBody)
				require.NoError(t, err)

				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", bytes.NewReader(body))

				result, err := svc.Forward(context.Background(), c, account, body)
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, http.StatusOK, w.Code)

				select {
				case cap := <-capCh:
					require.NotEmpty(t, cap.Path)
					require.Equal(t, scenario.wantMaxOut, cap.JSON["max_output_tokens"])
					require.NotContains(t, cap.JSON, "max_tokens")
					require.NotContains(t, cap.JSON, "max_completion_tokens")
				case <-time.After(2 * time.Second):
					t.Fatal("timeout waiting for upstream request")
				}
			})
		}
	}
}
