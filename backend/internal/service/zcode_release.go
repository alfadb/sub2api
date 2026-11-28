package service

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
)

// ZCode Desktop 版本号的动态来源。
//
// ZCode 客户端版本不是静态的：官方更新清单
// https://zcode.z.ai/api/v1/releases/electron/manifest?platform=darwin-aarch64&channel=1
// 返回 YAML，顶层 version 即当前发布版本（本次核实为 4.0.0）。UA 的
// "ZCode/<version>" 与 X-Zcode-App-Version 都取该值，写死会随官方发版逐渐落后，
// 而服务端可能按 X-Zcode-App-Version 做版本门控。
//
// 刷新策略与 model_protocol_catalog 同构：懒加载、后台单飞、失败保留旧值并退避。
// zcodeAppVersion 永不阻塞调用方：首次调用立即返回内置默认值，同时起一个后台
// goroutine 拉取清单；之后每 zcodeReleaseRefreshInterval 刷新一次，失败退避
// zcodeReleaseRetryBackoff。任何情况下当前生效版本都不会变小（官方回滚或清单
// 异常不应让我们比已知版本更旧）。
const (
	// zcodeDefaultAppVersion 内置默认版本，清单从未成功拉取时使用；也是接受
	// 新版本的基线（候选版本必须 >= 该值）。
	zcodeDefaultAppVersion = "4.0.0"
	// zcodeReleaseManifestURL 官方 Electron 客户端更新清单（darwin-aarch64，正式渠道）。
	zcodeReleaseManifestURL = "https://zcode.z.ai/api/v1/releases/electron/manifest?platform=darwin-aarch64&channel=1"
	// zcodeReleaseRefreshInterval 清单拉取成功后的刷新间隔。
	zcodeReleaseRefreshInterval = 6 * time.Hour
	// zcodeReleaseRetryBackoff 清单拉取失败后的重试退避；退避期内不再触发取数。
	zcodeReleaseRetryBackoff = time.Hour
	// zcodeReleaseFetchTimeout 单次清单请求的总超时。
	zcodeReleaseFetchTimeout = 5 * time.Second
	// zcodeReleaseMaxBodyBytes 清单响应体读取上限（清单只有几十行）。
	zcodeReleaseMaxBodyBytes = 64 << 10
)

// zcodeReleaseVersionPattern 匹配清单顶层的严格三段版本号，兼容 YAML 的引号写法。
var zcodeReleaseVersionPattern = regexp.MustCompile(`(?m)^version:\s*["']?([0-9]+\.[0-9]+\.[0-9]+)["']?\s*$`)

// zcodeReleasePreviewPattern 预览渠道标记：出现即拒绝该清单（我们只模仿正式渠道）。
var zcodeReleasePreviewPattern = regexp.MustCompile(`(?i)zcodeReleaseChannel:\s*["']?preview`)

// zcodeReleaseFetch 清单取数器，测试可替换。
var zcodeReleaseFetch = fetchZCodeReleaseManifest

// 包级状态：当前生效版本用 atomic 读取，保证 zcodeAppVersion 永不阻塞；
// 其余调度状态由 zcodeReleaseMu 保护。
var (
	zcodeReleaseVersion    atomic.Value // string
	zcodeReleaseMu         sync.Mutex
	zcodeReleaseRefreshing bool // 是否有一次刷新在飞（单飞）
	zcodeReleaseLastOK     string
	zcodeReleaseLastOKAt   time.Time
	zcodeReleaseNextTryAt  time.Time
)

func init() {
	zcodeReleaseVersion.Store(zcodeDefaultAppVersion)
}

// zcodeAppVersion 返回当前生效的 ZCode Desktop 版本号，永不阻塞调用方。
// 若已到允许尝试的时间且没有刷新在飞，则起一个后台 goroutine 刷新清单，
// 本次调用仍返回当前（旧）值。进程启动后的第一次调用即会触发一次刷新。
func zcodeAppVersion() string {
	version, _ := zcodeReleaseVersion.Load().(string)
	if version == "" {
		version = zcodeDefaultAppVersion
	}
	needRefresh := false
	zcodeReleaseMu.Lock()
	if !zcodeReleaseRefreshing && !time.Now().Before(zcodeReleaseNextTryAt) {
		zcodeReleaseRefreshing = true
		needRefresh = true
	}
	zcodeReleaseMu.Unlock()
	if needRefresh {
		go zcodeRefreshZCodeRelease()
	}
	return version
}

// zcodeRefreshZCodeRelease 拉取一次清单并更新包级状态。
// 失败时保留当前版本、只把下次允许尝试时间推到退避之后，绝不打断服务。
func zcodeRefreshZCodeRelease() {
	ctx, cancel := context.WithTimeout(context.Background(), zcodeReleaseFetchTimeout)
	defer cancel()

	body, err := zcodeReleaseFetch(ctx)
	version := ""
	if err == nil {
		var ok bool
		version, ok = parseZCodeReleaseVersion(body)
		if !ok {
			err = fmt.Errorf("zcode release manifest contains no valid version")
		}
	}

	now := time.Now()
	zcodeReleaseMu.Lock()
	defer zcodeReleaseMu.Unlock()
	zcodeReleaseRefreshing = false

	if err == nil {
		floor := zcodeDefaultAppVersion
		if zcodeReleaseLastOK != "" && CompareVersions(zcodeReleaseLastOK, floor) > 0 {
			floor = zcodeReleaseLastOK
		}
		// 任何情况下当前版本都不能变小：把生效值也纳入下限。
		if current, _ := zcodeReleaseVersion.Load().(string); current != "" && CompareVersions(current, floor) > 0 {
			floor = current
		}
		if CompareVersions(version, floor) < 0 {
			err = fmt.Errorf("zcode release manifest version %s is older than %s", version, floor)
		}
	}
	if err != nil {
		zcodeReleaseNextTryAt = now.Add(zcodeReleaseRetryBackoff)
		// 只记错误/状态码，不记 body（清单内容与请求无关，避免日志噪音）。
		slog.Warn("zcode_release_refresh_failed", "error", err)
		return
	}

	previous, _ := zcodeReleaseVersion.Load().(string)
	previousOKAt := zcodeReleaseLastOKAt
	zcodeReleaseVersion.Store(version)
	zcodeReleaseLastOK = version
	zcodeReleaseLastOKAt = now
	zcodeReleaseNextTryAt = now.Add(zcodeReleaseRefreshInterval)
	if previous != version {
		slog.Info("zcode_release_version_updated",
			"from", previous,
			"to", version,
			"previous_success_at", previousOKAt,
		)
	}
}

// fetchZCodeReleaseManifest 默认取数器：GET 官方更新清单（带 5 秒超时），
// 非 2xx 视为失败，响应体最多读 zcodeReleaseMaxBodyBytes。
func fetchZCodeReleaseManifest(ctx context.Context) ([]byte, error) {
	client, err := httpclient.GetClient(httpclient.Options{Timeout: zcodeReleaseFetchTimeout})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, zcodeReleaseManifestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/x-yaml,text/yaml,text/plain,*/*")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("zcode release manifest returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, zcodeReleaseMaxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > zcodeReleaseMaxBodyBytes {
		return nil, fmt.Errorf("zcode release manifest exceeds %d bytes", zcodeReleaseMaxBodyBytes)
	}
	return body, nil
}

// parseZCodeReleaseVersion 从更新清单 body 里解析顶层 version。
//
// 该接口出错时 HTTP 仍是 200，但 body 是 JSON（如 {"code":1000,...}）：
// body 去掉首尾空白后以 '{' 或 '[' 开头一律视为失败。只接受严格三段数字版本
// （4.1.0-beta.1 之类预发布串不匹配）；出现 zcodeReleaseChannel: preview
// （不区分大小写）说明是预览渠道清单，同样拒绝。
func parseZCodeReleaseVersion(body []byte) (string, bool) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return "", false
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return "", false
	}
	if zcodeReleasePreviewPattern.MatchString(trimmed) {
		return "", false
	}
	match := zcodeReleaseVersionPattern.FindStringSubmatch(trimmed)
	if len(match) < 2 {
		return "", false
	}
	return match[1], true
}
