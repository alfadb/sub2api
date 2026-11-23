//go:build unit

package service

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 智谱官方域判断必须只接受 bigmodel.cn / z.ai 及真正的子域，拒绝伪装域名。
func TestIsZhipuOfficialHost(t *testing.T) {
	for _, tc := range []struct {
		name   string
		rawURL string
		want   bool
	}{
		{"bigmodel_subdomain", "https://open.bigmodel.cn", true},
		{"bigmodel_apex", "https://bigmodel.cn", true},
		{"zai_subdomain", "https://api.z.ai", true},
		{"zai_apex", "https://z.ai", true},
		{"bigmodel_explicit_port", "https://open.bigmodel.cn:443/x", true},
		{"unrelated_host", "https://evil.com", false},
		{"bigmodel_suffix_spoof", "https://bigmodel.cn.evil.com", false},
		{"bigmodel_prefix_spoof", "https://notbigmodel.cn", false},
		{"zai_suffix_spoof", "https://z.ai.evil.com", false},
		{"loopback", "http://127.0.0.1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.Parse(tc.rawURL)
			require.NoError(t, err)
			require.Equal(t, tc.want, IsZhipuOfficialHost(u))
		})
	}
	require.False(t, IsZhipuOfficialHost(nil), "nil URL 必须返回 false")
}

// 智谱官方域用的 H1 transport 不能协商 HTTP/2；HTTP/1.1 client 复用同一构造。
func TestNewSSRFSafeHTTP1TransportDisablesHTTP2(t *testing.T) {
	tr := newSSRFSafeTransport(false)
	require.False(t, tr.ForceAttemptHTTP2, "H1 transport 不得启用 ForceAttemptHTTP2")
	require.NotNil(t, tr.TLSNextProto, "H1 transport 必须显式给出 TLSNextProto")
	require.Len(t, tr.TLSNextProto, 0, "TLSNextProto 必须为空 map 以禁用 h2 协商")

	client := newSSRFSafeHTTP1Client(time.Second)
	require.Equal(t, time.Second, client.Timeout)
	require.NotNil(t, client.Transport)
}
