package service

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// zhipu 账号出站客户端身份：发往 open.bigmodel.cn / api.z.ai 的推理请求统一
// 模拟为 ZCode Desktop 客户端指纹。指纹依据 ZCode v4.0.0 源码（zai-org/ZCode
// 仓库 tag v4.0.0：apps/zcode-cli/packages/bootstrap/src/model-config.ts、
// apps/zcode-cli/packages/adapters/src/model/runner-attribution.ts 与
// model-request-headers.ts）。上游按客户端身份做风控与降载，claude-cli /
// Codex / 浏览器身份会落在不利一侧，因此在全部智谱出站构造点、账号级
// ApplyHeaderOverrides 之前强制覆盖；header_overrides 仍是最后一步，可覆盖
// 注入值。非 zhipu 账号一律 no-op。
//
// 当前覆盖路径（都在 ApplyHeaderOverrides 之前）：
//   - 推理：sendCCUpstreamRequest、buildNativeAnthropicUpstreamRequest，
//     以及 generic Anthropic 链 buildUpstreamRequest；
//   - 账号连接测试三处探针（account_test_service.go、
//     account_test_service_cn_adaptive.go ×2）；
//   - 其他出站：额度探测、模型列表、渠道监控。
//
// MCP 透传是唯一例外：真实 ZCode 的 MCP 客户端（@modelcontextprotocol/client
// 的 StreamableHTTPClientTransport）根本不带 ZCode 身份头，见
// ApplyZCodeMCPTransportHeaders。
//
// 会话键来自 zcodeConversationKey：有入站请求时按客户端会话头 / body 会话
// 信号派生；没有入站请求的路径传固定哨兵字符串（"probe"、"quota"、"models"、
// "channel-monitor"），让这些旁路探测仍按同一账号派生稳定的 X-Session-Id。
//
// 版本号不写死：UA 的 "ZCode/<version>" 与 X-Zcode-App-Version 都取自
// zcodeAppVersion()（见 zcode_release.go，从官方更新清单懒加载）。
//
// 已知无法对齐的传输层差异（真实 ZCode v4.0.0 客户端 vs 本网关）：
//   - 真实客户端走 Node 内置 fetch（undici）的 HTTP/1.1，头名全小写且按字母序
//     输出（经 SDK 的 Headers 往返后顺序稳定）；网关用 Go net/http，头名大小写
//     （canonical 与 setHeaderRaw 的字面形态）和发送顺序都无法与真实客户端逐
//     字节一致。
//   - 真实客户端自动带 Accept-Encoding: br, gzip, deflate 并自行解压；Go 的
//     transport 不会解 brotli，所以这里删除出站 Accept-Encoding，让 Go 自己
//     协商压缩并自动解压（取值必然与真实客户端不同）。
//   - 版本号来自 zcode_release.go 的官方清单懒加载，进程首次出站时可能仍是
//     内置默认值。

// zcodeIdentityKind 区分出站协议面：ZCode Desktop 的 OpenAI（ai-sdk/
// openai-compatible）与 Anthropic（ai-sdk/anthropic）两条 SDK 栈 UA 不同。
type zcodeIdentityKind int

const (
	zcodeIdentityOpenAI zcodeIdentityKind = iota
	zcodeIdentityAnthropic
)

// zcodeDarwinReleases 真实 macOS 的 Darwin 内核号（24.x = macOS 15、
// 25.x = macOS 26、27.x = macOS 27）。按账号 ID 对表长度取模稳定选取：避免所有
// 账号看起来像同一台机器，同时单账号的 X-Os-Version 长期不变。表内顺序让现有
// 线上账号 159 继续落在原先的 25.5.0 上（保持设备指纹连续）。
var zcodeDarwinReleases = []string{"25.1.0", "25.3.0", "24.6.0", "25.5.0", "27.0.0", "24.5.0"}

// zcodeOsVersionForAccount 按账号 ID 稳定选取 Darwin 内核号（负数取绝对值）。
func zcodeOsVersionForAccount(accountID int64) string {
	index := accountID % int64(len(zcodeDarwinReleases))
	if index < 0 {
		index = -index
	}
	return zcodeDarwinReleases[index]
}

// zcodeSessionIDNamespace X-Session-Id 派生用的固定 UUIDv5 命名空间（值本身
// 任意，仅用于让同一账号同一会话恒定派生同一会话 ID）。
var zcodeSessionIDNamespace = uuid.MustParse("5f3d9a1c-7b2e-4c48-9d6a-1e0f2b3c4d5e")

// zcodeTraceIDNamespace X-Zcode-Trace-Id 派生用的独立命名空间，与 session ID
// 共享同一份 material，但派生值互不相同。
var zcodeTraceIDNamespace = uuid.MustParse("b7c2e4a9-1d3f-4e6b-8a05-2c9d7f41e6b8")

// zcodeConflictingIdentityHeaders 常见客户端残留、且与 ZCode 指纹冲突的身份头。
// 只剥身份头：authorization / x-api-key / content-type / accept /
// anthropic-version 等鉴权与协议头不在剥离范围（anthropic-beta 只过滤
// claude-code-* / oauth-* 令牌，见 zcodeFilterAnthropicBetaHeader）。
// accept-language / sec-fetch-mode 剥掉后按真实客户端（undici）的默认值
// "*" 与 "cors" 重写。
var zcodeConflictingIdentityHeaders = []string{
	"user-agent",
	"x-app",
	"x-claude-code-session-id",
	"x-client-request-id",
	"anthropic-dangerous-direct-browser-access",
	"originator",
	"version",
	"session_id",
	"conversation_id",
	"accept-language",
	"sec-fetch-mode",
}

// applyZCodeIdentityHeaders 把出站请求头改写为 ZCode Desktop 指纹。
// sessionKey 是会话级派生键（由 zcodeConversationKey 得到），非空时
// X-Session-Id / X-Zcode-Trace-Id 按「账号 + 会话」稳定派生；为空时与旧版
// 纯账号派生逐字节一致。account 为 nil、非 zhipu 平台或 h 为 nil 时直接返回。
func applyZCodeIdentityHeaders(h http.Header, account *Account, kind zcodeIdentityKind, sessionKey string) {
	if h == nil || account == nil || !account.IsZhipu() {
		return
	}
	// 先剥离冲突身份头，再强制写入指纹，避免 passthrough 以不同 casing 写入
	// 的同名头与指纹并发出站。
	for _, key := range zcodeConflictingIdentityHeaders {
		deleteHeaderAllForms(h, key)
	}
	deleteZCodePrefixedHeaders(h, "x-codex-")
	deleteZCodePrefixedHeaders(h, "x-stainless-")
	zcodeFilterAnthropicBetaHeader(h)
	// 真实客户端由 Node 内置 fetch（undici）发起，会自动带
	// Accept-Encoding: br, gzip, deflate 并自行解压。Go 的 transport 不会解
	// brotli，不能照搬该值：删除出站 Accept-Encoding（任意大小写），让 Go 的
	// 传输层自己协商压缩并自动解压（显式值会关闭 Go 的自动解压）。
	for key := range h {
		if strings.EqualFold(key, "accept-encoding") {
			delete(h, key)
		}
	}

	// undici 的两个默认头：值就是 "*" 与 "cors"（前面已剥离入站残留值）。
	setHeaderRaw(h, "accept-language", "*")
	setHeaderRaw(h, "sec-fetch-mode", "cors")

	// 版本号从官方更新清单懒加载，同一次改写内只取一次，保证 UA 与
	// X-Zcode-App-Version 自洽。
	version := zcodeAppVersion()

	// setHeaderRaw 写字面 casing（与 header_util.go 惯例一致）；HTTP-Referer
	// 尤其必须如此，Header.Set 会把它规范化成 Http-Referer，与真实客户端不符。
	setHeaderRaw(h, "User-Agent", zcodeUserAgent(kind, version))
	setHeaderRaw(h, "X-Zcode-App-Version", version)
	setHeaderRaw(h, "X-Zcode-Agent", "glm")
	setHeaderRaw(h, "X-Title", "Z Code@electron")
	setHeaderRaw(h, "HTTP-Referer", "https://zcode.z.ai")
	setHeaderRaw(h, "X-Platform", "darwin-arm64")
	setHeaderRaw(h, "X-Os-Category", "macos")
	setHeaderRaw(h, "X-Os-Version", zcodeOsVersionForAccount(account.ID))
	setHeaderRaw(h, "X-Release-Channel", "production")
	setHeaderRaw(h, "X-Client-Language", "zh-CN")
	setHeaderRaw(h, "X-Client-Timezone", "Asia/Shanghai")
	setHeaderRaw(h, "x-zcode-session-type", "main")
	setHeaderRaw(h, "x-zcode-query-source", "main_turn")
	// 逐请求随机 UUID。
	setHeaderRaw(h, "X-Request-Id", uuid.NewString())
	setHeaderRaw(h, "X-Query-Id", uuid.NewString())
	// 会话级稳定派生：同一账号同一会话恒定，换会话即换 ID。
	setHeaderRaw(h, "X-Session-Id", zcodeSessionIDForAccount(account.ID, sessionKey))
	setHeaderRaw(h, "X-Zcode-Trace-Id", zcodeTraceIDForAccount(account.ID, sessionKey))

	// Anthropic 面：真实 ZCode 同时发 Authorization 与 X-Api-Key 两套鉴权。
	if kind == zcodeIdentityAnthropic {
		zcodeEnsureAnthropicDualAuth(h)
	}
}

// ApplyZCodeMCPTransportHeaders 把 MCP 透传出站头改写为真实 ZCode 客户端的
// 传输层形态，供 handler 包调用。
//
// 依据 ZCode v4.0.0 源码：MCP 走 @modelcontextprotocol/client 的
// StreamableHTTPClientTransport，未包装任何 ZCode 身份头——请求里没有
// X-Zcode-* / X-Platform 等指纹，UA 是 Node 内置 fetch（undici）的默认值
// "node"，并带 undici 默认头 accept-language: * 与 sec-fetch-mode: cors。
//
// 所以这里做的是「去身份化」而非「加身份」：先删除所有名字（不区分大小写）
// 含 "zcode" 的头，再写入上述三个传输层默认头。h 为 nil 时直接返回。
func ApplyZCodeMCPTransportHeaders(h http.Header) {
	if h == nil {
		return
	}
	for key := range h {
		if strings.Contains(strings.ToLower(key), "zcode") {
			delete(h, key)
		}
	}
	setHeaderRaw(h, "User-Agent", "node")
	setHeaderRaw(h, "accept-language", "*")
	setHeaderRaw(h, "sec-fetch-mode", "cors")
}

// zcodeUserAgent 按 kind 返回对应 SDK 栈的 User-Agent（取自 ZCode v4.0.0
// 源码：各 provider 包 withUserAgentSuffix 追加自身版本，provider-utils 再追加
// 自身版本与 runtime/node.js/<major>）。version 为本次请求取到的 ZCode 版本。
func zcodeUserAgent(kind zcodeIdentityKind, version string) string {
	if kind == zcodeIdentityAnthropic {
		return "ZCode/" + version + " ai-sdk/anthropic/3.0.81 ai-sdk/provider-utils/4.0.27 runtime/node.js/24"
	}
	return "ZCode/" + version + " ai-sdk/openai-compatible/2.0.60 ai-sdk/provider-utils/4.0.39 runtime/node.js/24"
}

// zcodeSessionMaterial 拼出会话级 ID 派生的输入：账号 ID 的十进制字符串；
// sessionKey 非空时再追加 "|" + sessionKey，从而同账号跨会话也能区分。
func zcodeSessionMaterial(accountID int64, sessionKey string) []byte {
	material := strconv.FormatInt(accountID, 10)
	if sessionKey != "" {
		material += "|" + sessionKey
	}
	return []byte(material)
}

// zcodeSessionIDForAccount 用固定命名空间对 material 做 UUIDv5 派生，
// 同一账号同一会话恒定得到同一 X-Session-Id。
func zcodeSessionIDForAccount(accountID int64, sessionKey string) string {
	return uuid.NewSHA1(zcodeSessionIDNamespace, zcodeSessionMaterial(accountID, sessionKey)).String()
}

// zcodeTraceIDForAccount 用独立命名空间对同一份 material 派生
// X-Zcode-Trace-Id：与 session ID 同源稳定，但取值不同。
func zcodeTraceIDForAccount(accountID int64, sessionKey string) string {
	return uuid.NewSHA1(zcodeTraceIDNamespace, zcodeSessionMaterial(accountID, sessionKey)).String()
}

// zcodeFindHeaderValue 返回第一个名字匹配（不区分大小写）的头名与原值；
// 找不到时都返回空字符串。
func zcodeFindHeaderValue(h http.Header, name string) (string, string) {
	for key, values := range h {
		if len(values) == 0 || !strings.EqualFold(key, name) {
			continue
		}
		return key, values[0]
	}
	return "", ""
}

// zcodeSiblingHeaderName 让补充头沿用已有鉴权头的大小写形态：兄弟头是 Go
// canonical 形态时用 canonical，否则跟随其小写写法（与真实客户端两个头同一种
// 写法一致）。
func zcodeSiblingHeaderName(canonical, sibling string) string {
	if sibling != "" && sibling != http.CanonicalHeaderKey(sibling) {
		return strings.ToLower(canonical)
	}
	return canonical
}

// zcodeEnsureAnthropicDualAuth 补全 Anthropic 面的双鉴权：真实 ZCode 同时发
// Authorization: Bearer 与 X-Api-Key。只在缺失一侧时补，不覆盖已有值；不写
// 任何日志（避免密钥值泄漏）。
func zcodeEnsureAnthropicDualAuth(h http.Header) {
	authKey, authValue := zcodeFindHeaderValue(h, "authorization")
	apiKeyKey, apiKeyValue := zcodeFindHeaderValue(h, "x-api-key")

	const bearerPrefix = "bearer "
	if apiKeyValue == "" && len(authValue) > len(bearerPrefix) && strings.EqualFold(authValue[:len(bearerPrefix)], bearerPrefix) {
		if token := strings.TrimSpace(authValue[len(bearerPrefix):]); token != "" {
			setHeaderRaw(h, zcodeSiblingHeaderName("X-Api-Key", authKey), token)
		}
	}
	if authValue == "" && apiKeyValue != "" {
		setHeaderRaw(h, zcodeSiblingHeaderName("Authorization", apiKeyKey), "Bearer "+apiKeyValue)
	}
}

// zcodeFilterAnthropicBetaHeader 从 anthropic-beta 头里剥掉 Claude Code / OAuth
// 身份令牌（claude-code-* / oauth-*），其余按原顺序写回原头名形态；过滤后
// 一个不剩就删除该头；原本没有该头则不动。
func zcodeFilterAnthropicBetaHeader(h http.Header) {
	rawKey := ""
	for key := range h {
		if strings.EqualFold(key, "anthropic-beta") {
			rawKey = key
			break
		}
	}
	if rawKey == "" {
		return
	}
	tokens := parseAnthropicBetaHeader(getHeaderRaw(h, rawKey))
	kept := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if strings.HasPrefix(token, "claude-code-") || strings.HasPrefix(token, "oauth-") {
			continue
		}
		kept = append(kept, token)
	}
	if len(kept) == 0 {
		deleteHeaderAllForms(h, "anthropic-beta")
		return
	}
	setHeaderRaw(h, rawKey, strings.Join(kept, ","))
}

// zcodeConversationKey 解析会话级 ZCode ID 的派生键，按优先级取第一个非空：
//  1. 显式会话头（ExtractClientSessionID）；
//  2. Anthropic 形状 body 的 metadata.user_id.session_id；
//  3. body 的 prompt_cache_key；
//  4. 内容派生的稳定会话 seed；
//  5. context 里的 API key（"apikey:" + ID 十进制）。
//
// c / body 为 nil 安全；只读结构化字段，不记录 body 内容；都没有时返回 ""。
func zcodeConversationKey(c *gin.Context, body []byte) string {
	if key := ExtractClientSessionID(c); key != "" {
		return key
	}
	if len(body) > 0 {
		if raw := strings.TrimSpace(gjson.GetBytes(body, "metadata.user_id").String()); raw != "" {
			if parsed := ParseMetadataUserID(raw); parsed != nil && parsed.SessionID != "" {
				return parsed.SessionID
			}
		}
		if key := strings.TrimSpace(openAIRequestPayloadView(body).Get("prompt_cache_key").String()); key != "" {
			return key
		}
		if seed := deriveOpenAIContentSessionSeed(body); seed != "" {
			return seed
		}
	}
	if c != nil {
		if apiKey := getAPIKeyFromContext(c); apiKey != nil {
			return "apikey:" + strconv.FormatInt(apiKey.ID, 10)
		}
	}
	return ""
}

// deleteZCodePrefixedHeaders 删除全部匹配 lowercase 前缀的身份头（任意 casing
// 形态），覆盖 x-codex-* / x-stainless-* 这类客户端 SDK 指纹族。
func deleteZCodePrefixedHeaders(h http.Header, prefix string) {
	for key := range h {
		if strings.HasPrefix(strings.ToLower(key), prefix) {
			deleteHeaderAllForms(h, key)
		}
	}
}
