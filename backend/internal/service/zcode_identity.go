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
// Codex / 浏览器身份会落在不利一侧，因此在两条共享上游构造器
// （sendCCUpstreamRequest / buildNativeAnthropicUpstreamRequest）与账号探测
// 路径中、ApplyHeaderOverrides 之前强制覆盖；账号级 header_overrides 仍是
// 最后一步，可覆盖注入值。非 zhipu 账号一律 no-op。zhipu 无原生 Responses，
// 全部推理流量只经这两条共享构造器出站。

// zcodeIdentityKind 区分出站协议面：ZCode Desktop 的 OpenAI（ai-sdk/
// openai-compatible）与 Anthropic（ai-sdk/anthropic）两条 SDK 栈 UA 不同。
type zcodeIdentityKind int

const (
	zcodeIdentityOpenAI zcodeIdentityKind = iota
	zcodeIdentityAnthropic
)

// zcodeAppVersion ZCode Desktop 版本，UA 与 X-Zcode-App-Version 同源。
const zcodeAppVersion = "4.0.0"

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

	// setHeaderRaw 写字面 casing（与 header_util.go 惯例一致）；HTTP-Referer
	// 尤其必须如此，Header.Set 会把它规范化成 Http-Referer，与真实客户端不符。
	setHeaderRaw(h, "User-Agent", zcodeUserAgent(kind))
	setHeaderRaw(h, "X-Zcode-App-Version", zcodeAppVersion)
	setHeaderRaw(h, "X-Zcode-Agent", "glm")
	setHeaderRaw(h, "X-Title", "Z Code@electron")
	setHeaderRaw(h, "HTTP-Referer", "https://zcode.z.ai")
	setHeaderRaw(h, "X-Platform", "darwin-arm64")
	setHeaderRaw(h, "X-Os-Category", "macos")
	setHeaderRaw(h, "X-Os-Version", "25.5.0")
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
}

// zcodeUserAgent 按 kind 返回对应 SDK 栈的 User-Agent（取自 ZCode v4.0.0
// 源码：各 provider 包 withUserAgentSuffix 追加自身版本，provider-utils 再追加
// 自身版本与 runtime/node.js/<major>）。
func zcodeUserAgent(kind zcodeIdentityKind) string {
	if kind == zcodeIdentityAnthropic {
		return "ZCode/" + zcodeAppVersion + " ai-sdk/anthropic/3.0.81 ai-sdk/provider-utils/4.0.27 runtime/node.js/24"
	}
	return "ZCode/" + zcodeAppVersion + " ai-sdk/openai-compatible/2.0.60 ai-sdk/provider-utils/4.0.39 runtime/node.js/24"
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
