package repository

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 智谱官方域的出站请求必须覆盖成仅 HTTP/1.1 的 zhipu_h1；其它上游保持 profile
// 解析出的原模式（无 profile -> default，OpenAI profile -> openai_h2）。
func TestEffectiveUpstreamProtocolMode(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			OpenAIHTTP2: config.GatewayOpenAIHTTP2Config{Enabled: true},
		},
	}
	svc, ok := NewHTTPUpstream(cfg).(*httpUpstreamService)
	require.True(t, ok, "expected *httpUpstreamService")

	profiles := []struct {
		name    string
		profile service.HTTPUpstreamProfile
		want    string
	}{
		{"no_profile", service.HTTPUpstreamProfileDefault, upstreamProtocolModeDefault},
		{"openai_profile", service.HTTPUpstreamProfileOpenAI, upstreamProtocolModeOpenAIH2},
	}
	targets := []struct {
		name   string
		rawURL string
		zhipu  bool
	}{
		{"bigmodel", "https://open.bigmodel.cn/api/paas/v4/chat/completions", true},
		{"zai", "https://api.z.ai/api/coding/paas/v4/chat/completions", true},
		{"bigmodel_coding", "https://open.bigmodel.cn/api/coding/paas/v4/chat/completions", true},
		{"openai", "https://api.openai.com/v1/chat/completions", false},
		{"anthropic", "https://api.anthropic.com/v1/messages", false},
		{"loopback", "http://127.0.0.1:1234/v1/chat/completions", false},
	}
	for _, p := range profiles {
		t.Run(p.name, func(t *testing.T) {
			resolved := svc.resolveProtocolMode(p.profile, "direct", nil)
			require.Equal(t, p.want, resolved, "profile 原模式")
			for _, tc := range targets {
				t.Run(tc.name, func(t *testing.T) {
					req, err := http.NewRequest(http.MethodPost, tc.rawURL, nil)
					require.NoError(t, err)
					want := resolved
					if tc.zhipu {
						want = upstreamProtocolModeZhipuH1
					}
					require.Equal(t, want, effectiveUpstreamProtocolMode(req, resolved))
				})
			}
		})
	}
}
