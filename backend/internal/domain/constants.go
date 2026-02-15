package domain

// Status constants
const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
	StatusError    = "error"
	StatusUnused   = "unused"
	StatusUsed     = "used"
	StatusExpired  = "expired"
)

// Role constants
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// Platform constants (API protocol type)
const (
	PlatformAnthropic   = "anthropic"
	PlatformOpenAI      = "openai"
	PlatformCopilot     = "copilot"
	PlatformAggregator  = "aggregator"
	PlatformGemini      = "gemini"
	PlatformAntigravity = "antigravity"
)

// Provider constants (actual service source)
// Provider identifies the upstream service provider for a model,
// enabling differentiation of context windows and pricing for the same model name.
const (
	ProviderOpenAI      = "openai"      // OpenAI 官方 API
	ProviderAzure       = "azure"       // Azure OpenAI
	ProviderCopilot     = "copilot"     // GitHub Copilot
	ProviderAnthropic   = "anthropic"   // Anthropic 官方 API
	ProviderGemini      = "gemini"      // Google Gemini 官方 API
	ProviderVertexAI    = "vertex"      // Google Vertex AI
	ProviderAntigravity = "antigravity" // Antigravity 服务
	ProviderBedrock     = "bedrock"     // AWS Bedrock
	ProviderOpenRouter  = "openrouter"  // OpenRouter 聚合
	ProviderAggregator  = "aggregator"  // 通用聚合器
)

// ProviderToPlatform maps provider to the API protocol (platform) it uses.
// This enables automatic platform inference from provider namespace.
var ProviderToPlatform = map[string]string{
	ProviderOpenAI:      PlatformOpenAI,
	ProviderAzure:       PlatformOpenAI,
	ProviderCopilot:     PlatformOpenAI,
	ProviderAnthropic:   PlatformAnthropic,
	ProviderGemini:      PlatformGemini,
	ProviderVertexAI:    PlatformGemini,
	ProviderAntigravity: PlatformAntigravity,
	ProviderBedrock:     PlatformOpenAI, // Bedrock uses OpenAI-compatible format
	ProviderOpenRouter:  PlatformOpenAI,
	ProviderAggregator:  PlatformOpenAI,
}

// GetPlatformFromProvider returns the platform (API protocol) for a given provider.
// Returns empty string if provider is unknown.
func GetPlatformFromProvider(provider string) string {
	if platform, ok := ProviderToPlatform[provider]; ok {
		return platform
	}
	return ""
}

// Account type constants
const (
	AccountTypeOAuth      = "oauth"       // OAuth类型账号（full scope: profile + inference）
	AccountTypeSetupToken = "setup-token" // Setup Token类型账号（inference only scope）
	AccountTypeAPIKey     = "apikey"      // API Key类型账号
	AccountTypeUpstream   = "upstream"    // 上游透传类型账号（通过 Base URL + API Key 连接上游）
)

// Redeem type constants
const (
	RedeemTypeBalance      = "balance"
	RedeemTypeConcurrency  = "concurrency"
	RedeemTypeSubscription = "subscription"
	RedeemTypeInvitation   = "invitation"
)

// PromoCode status constants
const (
	PromoCodeStatusActive   = "active"
	PromoCodeStatusDisabled = "disabled"
)

// Admin adjustment type constants
const (
	AdjustmentTypeAdminBalance     = "admin_balance"     // 管理员调整余额
	AdjustmentTypeAdminConcurrency = "admin_concurrency" // 管理员调整并发数
)

// Group subscription type constants
const (
	SubscriptionTypeStandard     = "standard"     // 标准计费模式（按余额扣费）
	SubscriptionTypeSubscription = "subscription" // 订阅模式（按限额控制）
)

// Subscription status constants
const (
	SubscriptionStatusActive    = "active"
	SubscriptionStatusExpired   = "expired"
	SubscriptionStatusSuspended = "suspended"
)

// DefaultAntigravityModelMapping 是 Antigravity 平台的默认模型映射
// 当账号未配置 model_mapping 时使用此默认值
// 与前端 useModelWhitelist.ts 中的 antigravityDefaultMappings 保持一致
var DefaultAntigravityModelMapping = map[string]string{
	// Claude 白名单
	"claude-opus-4-6-thinking":   "claude-opus-4-6-thinking", // 官方模型
	"claude-opus-4-6":            "claude-opus-4-6-thinking", // 简称映射
	"claude-opus-4-5-thinking":   "claude-opus-4-6-thinking", // 迁移旧模型
	"claude-sonnet-4-5":          "claude-sonnet-4-5",
	"claude-sonnet-4-5-thinking": "claude-sonnet-4-5-thinking",
	// Claude 详细版本 ID 映射
	"claude-opus-4-5-20251101":   "claude-opus-4-6-thinking", // 迁移旧模型
	"claude-sonnet-4-5-20250929": "claude-sonnet-4-5",
	// Claude Haiku → Sonnet（无 Haiku 支持）
	"claude-haiku-4-5":          "claude-sonnet-4-5",
	"claude-haiku-4-5-20251001": "claude-sonnet-4-5",
	// Gemini 2.5 白名单
	"gemini-2.5-flash":          "gemini-2.5-flash",
	"gemini-2.5-flash-lite":     "gemini-2.5-flash-lite",
	"gemini-2.5-flash-thinking": "gemini-2.5-flash-thinking",
	"gemini-2.5-pro":            "gemini-2.5-pro",
	// Gemini 3 白名单
	"gemini-3-flash":     "gemini-3-flash",
	"gemini-3-pro-high":  "gemini-3-pro-high",
	"gemini-3-pro-low":   "gemini-3-pro-low",
	"gemini-3-pro-image": "gemini-3-pro-image",
	// Gemini 3 preview 映射
	"gemini-3-flash-preview":     "gemini-3-flash",
	"gemini-3-pro-preview":       "gemini-3-pro-high",
	"gemini-3-pro-image-preview": "gemini-3-pro-image",
	// 其他官方模型
	"gpt-oss-120b-medium":    "gpt-oss-120b-medium",
	"tab_flash_lite_preview": "tab_flash_lite_preview",
}
