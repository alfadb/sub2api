//go:build unit

package service

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

// TestMain 让 service 包的单测不访问真实更新清单：否则第一次调用
// zcodeAppVersion() 会起一次后台刷新，既依赖网络，也可能让 UA 断言在版本刷新
// 前后抖动。zcode_release_test 的各用例自行替换取数器。
func TestMain(m *testing.M) {
	zcodeReleaseFetch = func(context.Context) ([]byte, error) {
		return []byte("version: " + zcodeDefaultAppVersion + "\n"), nil
	}
	os.Exit(m.Run())
}

// resetZCodeReleaseCacheForTest 把版本与调度状态复位到初始值。仅测试使用，
// 因此放在测试文件里：不带 unit tag 的构建不会出现未使用的包级函数。
func resetZCodeReleaseCacheForTest() {
	zcodeReleaseMu.Lock()
	zcodeReleaseRefreshing = false
	zcodeReleaseLastOK = ""
	zcodeReleaseLastOKAt = time.Time{}
	zcodeReleaseNextTryAt = time.Time{}
	zcodeReleaseVersion.Store(zcodeDefaultAppVersion)
	zcodeReleaseMu.Unlock()
}

// stubZCodeReleaseFetch 替换清单取数器，用例结束后复位。
func stubZCodeReleaseFetch(t *testing.T, fn func(ctx context.Context) ([]byte, error)) {
	t.Helper()
	previous := zcodeReleaseFetch
	zcodeReleaseFetch = fn
	t.Cleanup(func() { zcodeReleaseFetch = previous })
}

// freshZCodeReleaseState 把版本与调度状态复位到初值，并在用例结束时再复位一次。
func freshZCodeReleaseState(t *testing.T) {
	t.Helper()
	resetZCodeReleaseCacheForTest()
	t.Cleanup(resetZCodeReleaseCacheForTest)
}

// (a) 首次调用在刷新完成前立即返回内置默认值，且不阻塞调用方。
func TestZCodeAppVersion_ReturnsDefaultBeforeFirstRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		freshZCodeReleaseState(t)
		block := make(chan struct{})
		stubZCodeReleaseFetch(t, func(context.Context) ([]byte, error) {
			<-block
			return []byte("version: 4.1.0\n"), nil
		})

		require.Equal(t, zcodeDefaultAppVersion, zcodeAppVersion(), "刷新在飞时立即返回内置默认值")

		close(block)
		synctest.Wait()
		require.Equal(t, "4.1.0", zcodeAppVersion())
	})
}

// (b) 清单 version: 4.1.0 → 刷新完成后生效。
func TestZCodeAppVersion_RefreshesFromManifest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		freshZCodeReleaseState(t)
		stubZCodeReleaseFetch(t, func(context.Context) ([]byte, error) {
			return []byte("version: 4.1.0\nplatforms:\n  - darwin-aarch64\n"), nil
		})

		require.Equal(t, zcodeDefaultAppVersion, zcodeAppVersion())
		synctest.Wait()
		require.Equal(t, "4.1.0", zcodeAppVersion())
	})
}

// (c) 接口出错时 HTTP 仍是 200 但 body 是 JSON → 忽略，保留旧值。
func TestZCodeAppVersion_IgnoresJSONErrorBody(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		freshZCodeReleaseState(t)
		stubZCodeReleaseFetch(t, func(context.Context) ([]byte, error) {
			return []byte(`{"code":1000,"message":"invalid channel"}`), nil
		})

		require.Equal(t, zcodeDefaultAppVersion, zcodeAppVersion())
		synctest.Wait()
		require.Equal(t, zcodeDefaultAppVersion, zcodeAppVersion())
	})
}

// (d) 清单给出比基线更低的版本 → 忽略。
func TestZCodeAppVersion_IgnoresLowerVersion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		freshZCodeReleaseState(t)
		stubZCodeReleaseFetch(t, func(context.Context) ([]byte, error) {
			return []byte("version: 3.9.0\n"), nil
		})

		require.Equal(t, zcodeDefaultAppVersion, zcodeAppVersion())
		synctest.Wait()
		require.Equal(t, zcodeDefaultAppVersion, zcodeAppVersion())
	})
}

// (e) 预览渠道清单 → 忽略。
func TestZCodeAppVersion_IgnoresPreviewChannel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		freshZCodeReleaseState(t)
		stubZCodeReleaseFetch(t, func(context.Context) ([]byte, error) {
			return []byte("version: 4.1.0\nzcodeReleaseChannel: preview\n"), nil
		})

		require.Equal(t, zcodeDefaultAppVersion, zcodeAppVersion())
		synctest.Wait()
		require.Equal(t, zcodeDefaultAppVersion, zcodeAppVersion())
	})
}

// (f) 取数器报错 → 保留旧值，退避期内不再取数，退避到期后重试。
func TestZCodeAppVersion_FailureRetainsValueAndBacksOff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		freshZCodeReleaseState(t)
		var calls atomic.Int32
		stubZCodeReleaseFetch(t, func(context.Context) ([]byte, error) {
			calls.Add(1)
			return nil, errors.New("manifest unavailable")
		})

		require.Equal(t, zcodeDefaultAppVersion, zcodeAppVersion())
		synctest.Wait()
		require.Equal(t, int32(1), calls.Load())

		require.Equal(t, zcodeDefaultAppVersion, zcodeAppVersion(), "失败保留旧值")
		require.Equal(t, int32(1), calls.Load(), "退避期内不再触发取数")

		time.Sleep(zcodeReleaseRetryBackoff) // 虚拟时间推进到退避之后。
		zcodeAppVersion()
		synctest.Wait()
		require.Equal(t, int32(2), calls.Load(), "退避到期后允许重试")
	})
}

// (g) 过期时多个并发调用只触发一次取数。
func TestZCodeAppVersion_ConcurrentCallsShareOneRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		freshZCodeReleaseState(t)
		var calls atomic.Int32
		var enterOnce sync.Once
		entered := make(chan struct{})
		release := make(chan struct{})
		stubZCodeReleaseFetch(t, func(context.Context) ([]byte, error) {
			calls.Add(1)
			enterOnce.Do(func() { close(entered) })
			<-release
			return []byte("version: 4.1.0\n"), nil
		})

		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				zcodeAppVersion()
			}()
		}
		wg.Wait()
		<-entered
		require.Equal(t, int32(1), calls.Load(), "并发调用只允许一次取数")

		close(release)
		synctest.Wait()
		require.Equal(t, "4.1.0", zcodeAppVersion())
	})
}

// (h) 带引号的 version 也接受。
func TestZCodeAppVersion_AcceptsQuotedVersion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		freshZCodeReleaseState(t)
		stubZCodeReleaseFetch(t, func(context.Context) ([]byte, error) {
			return []byte("version: \"4.2.0\"\n"), nil
		})

		require.Equal(t, zcodeDefaultAppVersion, zcodeAppVersion())
		synctest.Wait()
		require.Equal(t, "4.2.0", zcodeAppVersion())
	})
}

// (i) 非严格三段版本（预发布串）忽略。
func TestZCodeAppVersion_IgnoresNonStrictVersion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		freshZCodeReleaseState(t)
		stubZCodeReleaseFetch(t, func(context.Context) ([]byte, error) {
			return []byte("version: 4.1.0-beta.1\n"), nil
		})

		require.Equal(t, zcodeDefaultAppVersion, zcodeAppVersion())
		synctest.Wait()
		require.Equal(t, zcodeDefaultAppVersion, zcodeAppVersion())
	})
}

// 已接受的版本不会因为后续清单回退而变小。
func TestZCodeAppVersion_NeverGoesBackwards(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		freshZCodeReleaseState(t)
		var mu sync.Mutex
		bodies := [][]byte{
			[]byte("version: 4.2.0\n"),
			[]byte("version: 4.1.0\n"),
		}
		next := 0
		stubZCodeReleaseFetch(t, func(context.Context) ([]byte, error) {
			mu.Lock()
			defer mu.Unlock()
			body := bodies[next]
			if next < len(bodies)-1 {
				next++
			}
			return body, nil
		})

		require.Equal(t, zcodeDefaultAppVersion, zcodeAppVersion())
		synctest.Wait()
		require.Equal(t, "4.2.0", zcodeAppVersion())

		time.Sleep(zcodeReleaseRefreshInterval) // 进入下一次允许刷新窗口。
		zcodeAppVersion()
		synctest.Wait()
		require.Equal(t, "4.2.0", zcodeAppVersion(), "清单回退不得让生效版本变小")
	})
}

func TestParseZCodeReleaseVersion(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
		ok   bool
	}{
		{name: "yaml", body: "version: 4.0.0\nfoo: bar\n", want: "4.0.0", ok: true},
		{name: "quoted", body: `version: "4.2.0"`, want: "4.2.0", ok: true},
		{name: "single quoted", body: "version: '4.3.1'\n", want: "4.3.1", ok: true},
		{name: "json error body", body: `{"code":1000}`, ok: false},
		{name: "json array", body: `[{"version":"4.0.0"}]`, ok: false},
		{name: "preview channel", body: "version: 4.1.0\nZcodeReleaseChannel: Preview\n", ok: false},
		{name: "prerelease", body: "version: 4.1.0-beta.1\n", ok: false},
		{name: "empty", body: " \n", ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseZCodeReleaseVersion([]byte(tc.body))
			require.Equal(t, tc.ok, ok)
			require.Equal(t, tc.want, got)
		})
	}
}
