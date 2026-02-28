package service

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

type UsageLogRepository interface {
	// Create creates a usage log and returns whether it was actually inserted.
	// inserted is false when the insert was skipped due to conflict (idempotent retries).
	Create(ctx context.Context, log *UsageLog) (inserted bool, err error)
	GetByID(ctx context.Context, id int64) (*UsageLog, error)
	Delete(ctx context.Context, id int64) error

	ListByUser(ctx context.Context, userID int64, params pagination.PaginationParams) ([]UsageLog, *pagination.PaginationResult, error)
	ListByAPIKey(ctx context.Context, apiKeyID int64, params pagination.PaginationParams) ([]UsageLog, *pagination.PaginationResult, error)
	ListByAccount(ctx context.Context, accountID int64, params pagination.PaginationParams) ([]UsageLog, *pagination.PaginationResult, error)

	ListByUserAndTimeRange(ctx context.Context, userID int64, startTime, endTime time.Time) ([]UsageLog, *pagination.PaginationResult, error)
	ListByAPIKeyAndTimeRange(ctx context.Context, apiKeyID int64, startTime, endTime time.Time) ([]UsageLog, *pagination.PaginationResult, error)
	ListByAccountAndTimeRange(ctx context.Context, accountID int64, startTime, endTime time.Time) ([]UsageLog, *pagination.PaginationResult, error)
	ListByModelAndTimeRange(ctx context.Context, modelName string, startTime, endTime time.Time) ([]UsageLog, *pagination.PaginationResult, error)

	GetAccountWindowStats(ctx context.Context, accountID int64, startTime time.Time) (*usagestats.AccountStats, error)
	GetAccountTodayStats(ctx context.Context, accountID int64) (*usagestats.AccountStats, error)

	// Admin dashboard stats
	GetDashboardStats(ctx context.Context) (*usagestats.DashboardStats, error)
	GetUsageTrendWithFilters(ctx context.Context, startTime, endTime time.Time, granularity string, userID, apiKeyID, accountID, groupID int64, model string, stream *bool, billingType *int8) ([]usagestats.TrendDataPoint, error)
	GetModelStatsWithFilters(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, accountID, groupID int64, stream *bool, billingType *int8) ([]usagestats.ModelStat, error)
	GetAPIKeyUsageTrend(ctx context.Context, startTime, endTime time.Time, granularity string, limit int) ([]usagestats.APIKeyUsageTrendPoint, error)
	GetUserUsageTrend(ctx context.Context, startTime, endTime time.Time, granularity string, limit int) ([]usagestats.UserUsageTrendPoint, error)
	GetBatchUserUsageStats(ctx context.Context, userIDs []int64, startTime, endTime time.Time) (map[int64]*usagestats.BatchUserUsageStats, error)
	GetBatchAPIKeyUsageStats(ctx context.Context, apiKeyIDs []int64, startTime, endTime time.Time) (map[int64]*usagestats.BatchAPIKeyUsageStats, error)

	// User dashboard stats
	GetUserDashboardStats(ctx context.Context, userID int64) (*usagestats.UserDashboardStats, error)
	GetAPIKeyDashboardStats(ctx context.Context, apiKeyID int64) (*usagestats.UserDashboardStats, error)
	GetUserUsageTrendByUserID(ctx context.Context, userID int64, startTime, endTime time.Time, granularity string) ([]usagestats.TrendDataPoint, error)
	GetUserModelStats(ctx context.Context, userID int64, startTime, endTime time.Time) ([]usagestats.ModelStat, error)

	// Admin usage listing/stats
	ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters usagestats.UsageLogFilters) ([]UsageLog, *pagination.PaginationResult, error)
	GetGlobalStats(ctx context.Context, startTime, endTime time.Time) (*usagestats.UsageStats, error)
	GetStatsWithFilters(ctx context.Context, filters usagestats.UsageLogFilters) (*usagestats.UsageStats, error)

	// Account stats
	GetAccountUsageStats(ctx context.Context, accountID int64, startTime, endTime time.Time) (*usagestats.AccountUsageStatsResponse, error)

	// Aggregated stats (optimized)
	GetUserStatsAggregated(ctx context.Context, userID int64, startTime, endTime time.Time) (*usagestats.UsageStats, error)
	GetAPIKeyStatsAggregated(ctx context.Context, apiKeyID int64, startTime, endTime time.Time) (*usagestats.UsageStats, error)
	GetAccountStatsAggregated(ctx context.Context, accountID int64, startTime, endTime time.Time) (*usagestats.UsageStats, error)
	GetModelStatsAggregated(ctx context.Context, modelName string, startTime, endTime time.Time) (*usagestats.UsageStats, error)
	GetDailyStatsAggregated(ctx context.Context, userID int64, startTime, endTime time.Time) ([]map[string]any, error)
}

// apiUsageCache 缓存从 Anthropic API 获取的使用率数据（utilization, resets_at）
type apiUsageCache struct {
	response  *ClaudeUsageResponse
	timestamp time.Time
}

// windowStatsCache 缓存从本地数据库查询的窗口统计（requests, tokens, cost）
type windowStatsCache struct {
	stats     *WindowStats
	timestamp time.Time
}

// antigravityUsageCache 缓存 Antigravity 额度数据
type antigravityUsageCache struct {
	usageInfo *UsageInfo
	timestamp time.Time
}

const (
	apiCacheTTL         = 3 * time.Minute
	windowStatsCacheTTL = 1 * time.Minute
)

// UsageCache 封装账户使用量相关的缓存
type UsageCache struct {
	apiCache         sync.Map // accountID -> *apiUsageCache
	windowStatsCache sync.Map // accountID -> *windowStatsCache
	antigravityCache sync.Map // accountID -> *antigravityUsageCache
}

// NewUsageCache 创建 UsageCache 实例
func NewUsageCache() *UsageCache {
	return &UsageCache{}
}

// WindowStats 窗口期统计
//
// cost: 账号口径费用（total_cost * account_rate_multiplier）
// standard_cost: 标准费用（total_cost，不含倍率）
// user_cost: 用户/API Key 口径费用（actual_cost，受分组倍率影响）
type WindowStats struct {
	Requests     int64   `json:"requests"`
	Tokens       int64   `json:"tokens"`
	Cost         float64 `json:"cost"`
	StandardCost float64 `json:"standard_cost"`
	UserCost     float64 `json:"user_cost"`
}

// UsageProgress 使用量进度
type UsageProgress struct {
	Utilization      float64      `json:"utilization"`            // 使用率百分比 (0-100+，100表示100%)
	ResetsAt         *time.Time   `json:"resets_at"`              // 重置时间
	RemainingSeconds int          `json:"remaining_seconds"`      // 距重置剩余秒数
	WindowStats      *WindowStats `json:"window_stats,omitempty"` // 窗口期统计（从窗口开始到当前的使用量）
	UsedRequests     int64        `json:"used_requests,omitempty"`
	LimitRequests    int64        `json:"limit_requests,omitempty"`
}

// AntigravityModelQuota Antigravity 单个模型的配额信息
type AntigravityModelQuota struct {
	Utilization int    `json:"utilization"` // 使用率 0-100
	ResetTime   string `json:"reset_time"`  // 重置时间 ISO8601
}

// UsageInfo 账号使用量信息
type UsageInfo struct {
	UpdatedAt          *time.Time     `json:"updated_at,omitempty"`           // 更新时间
	FiveHour           *UsageProgress `json:"five_hour"`                      // 5小时窗口
	SevenDay           *UsageProgress `json:"seven_day,omitempty"`            // 7天窗口
	SevenDaySonnet     *UsageProgress `json:"seven_day_sonnet,omitempty"`     // 7天Sonnet窗口
	GeminiSharedDaily  *UsageProgress `json:"gemini_shared_daily,omitempty"`  // Gemini shared pool RPD (Google One / Code Assist)
	GeminiProDaily     *UsageProgress `json:"gemini_pro_daily,omitempty"`     // Gemini Pro 日配额
	GeminiFlashDaily   *UsageProgress `json:"gemini_flash_daily,omitempty"`   // Gemini Flash 日配额
	GeminiSharedMinute *UsageProgress `json:"gemini_shared_minute,omitempty"` // Gemini shared pool RPM (Google One / Code Assist)
	GeminiProMinute    *UsageProgress `json:"gemini_pro_minute,omitempty"`    // Gemini Pro RPM
	GeminiFlashMinute  *UsageProgress `json:"gemini_flash_minute,omitempty"`  // Gemini Flash RPM

	// Antigravity 多模型配额
	AntigravityQuota map[string]*AntigravityModelQuota `json:"antigravity_quota,omitempty"`

	// Copilot 本地用量统计（基于 usage_logs，无公开 API）
	CopilotDaily   *UsageProgress `json:"copilot_daily,omitempty"`
	CopilotMonthly *UsageProgress `json:"copilot_monthly,omitempty"`
}

// ClaudeUsageResponse Anthropic API返回的usage结构
type ClaudeUsageResponse struct {
	FiveHour struct {
		Utilization float64 `json:"utilization"`
		ResetsAt    string  `json:"resets_at"`
	} `json:"five_hour"`
	SevenDay struct {
		Utilization float64 `json:"utilization"`
		ResetsAt    string  `json:"resets_at"`
	} `json:"seven_day"`
	SevenDaySonnet struct {
		Utilization float64 `json:"utilization"`
		ResetsAt    string  `json:"resets_at"`
	} `json:"seven_day_sonnet"`
}

// ClaudeUsageFetchOptions 包含获取 Claude 用量数据所需的所有选项
type ClaudeUsageFetchOptions struct {
	AccessToken          string       // OAuth access token
	ProxyURL             string       // 代理 URL（可选）
	AccountID            int64        // 账号 ID（用于 TLS 指纹选择）
	EnableTLSFingerprint bool         // 是否启用 TLS 指纹伪装
	Fingerprint          *Fingerprint // 缓存的指纹信息（User-Agent 等）
}

// ClaudeUsageFetcher fetches usage data from Anthropic OAuth API
type ClaudeUsageFetcher interface {
	FetchUsage(ctx context.Context, accessToken, proxyURL string) (*ClaudeUsageResponse, error)
	// FetchUsageWithOptions 使用完整选项获取用量数据，支持 TLS 指纹和自定义 User-Agent
	FetchUsageWithOptions(ctx context.Context, opts *ClaudeUsageFetchOptions) (*ClaudeUsageResponse, error)
}

// CopilotQuotaDetail GitHub Copilot 单个配额详情
// 注意：GitHub API 返回的数字字段可能是浮点数（如 0.0），所以使用 float64
type CopilotQuotaDetail struct {
	Entitlement      float64 `json:"entitlement"`
	OverageCount     float64 `json:"overage_count"`
	OveragePermitted bool    `json:"overage_permitted"`
	PercentRemaining float64 `json:"percent_remaining"`
	QuotaID          string  `json:"quota_id"`
	QuotaRemaining   float64 `json:"quota_remaining"`
	Remaining        float64 `json:"remaining"`
	Unlimited        bool    `json:"unlimited"`
}

// CopilotQuotaSnapshots GitHub Copilot 配额快照
type CopilotQuotaSnapshots struct {
	Chat                CopilotQuotaDetail `json:"chat"`
	Completions         CopilotQuotaDetail `json:"completions"`
	PremiumInteractions CopilotQuotaDetail `json:"premium_interactions"`
}

// CopilotUsageResponse GitHub Copilot Usage API 响应
type CopilotUsageResponse struct {
	AccessTypeSKU         string                `json:"access_type_sku"`
	AnalyticsTrackingID   string                `json:"analytics_tracking_id"`
	AssignedDate          string                `json:"assigned_date"`
	CanSignupForLimited   bool                  `json:"can_signup_for_limited"`
	ChatEnabled           bool                  `json:"chat_enabled"`
	CopilotPlan           string                `json:"copilot_plan"`
	OrganizationLoginList []any                 `json:"organization_login_list"`
	OrganizationList      []any                 `json:"organization_list"`
	QuotaResetDate        string                `json:"quota_reset_date"`
	QuotaResetDateUTC     string                `json:"quota_reset_date_utc"`
	QuotaSnapshots        CopilotQuotaSnapshots `json:"quota_snapshots"`
}

// CopilotUsageFetcher fetches usage data from GitHub Copilot API
type CopilotUsageFetcher interface {
	FetchUsage(ctx context.Context, accessToken, proxyURL string, accountID int64) (*CopilotUsageResponse, error)
}

// AccountUsageService 账号使用量查询服务
type AccountUsageService struct {
	accountRepo             AccountRepository
	usageLogRepo            UsageLogRepository
	usageFetcher            ClaudeUsageFetcher
	copilotUsageFetcher     CopilotUsageFetcher
	geminiQuotaService      *GeminiQuotaService
	antigravityQuotaFetcher *AntigravityQuotaFetcher
	cache                   *UsageCache
	identityCache           IdentityCache
}

// NewAccountUsageService 创建AccountUsageService实例
func NewAccountUsageService(
	accountRepo AccountRepository,
	usageLogRepo UsageLogRepository,
	usageFetcher ClaudeUsageFetcher,
	copilotUsageFetcher CopilotUsageFetcher,
	geminiQuotaService *GeminiQuotaService,
	antigravityQuotaFetcher *AntigravityQuotaFetcher,
	cache *UsageCache,
	identityCache IdentityCache,
) *AccountUsageService {
	return &AccountUsageService{
		accountRepo:             accountRepo,
		usageLogRepo:            usageLogRepo,
		usageFetcher:            usageFetcher,
		copilotUsageFetcher:     copilotUsageFetcher,
		geminiQuotaService:      geminiQuotaService,
		antigravityQuotaFetcher: antigravityQuotaFetcher,
		cache:                   cache,
		identityCache:           identityCache,
	}
}

// GetUsage 获取账号使用量
// OAuth账号: 调用Anthropic API获取真实数据（需要profile scope），API响应缓存10分钟，窗口统计缓存1分钟
// Setup Token账号: 根据session_window推算5h窗口，7d数据不可用（没有profile scope）
// API Key账号: 不支持usage查询
func (s *AccountUsageService) GetUsage(ctx context.Context, accountID int64) (*UsageInfo, error) {
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("get account failed: %w", err)
	}

	if account.Platform == PlatformGemini {
		usage, err := s.getGeminiUsage(ctx, account)
		if err == nil {
			s.tryClearRecoverableAccountError(ctx, account)
		}
		return usage, err
	}

	// Copilot 平台：基于本地 usage_logs 计算用量（GitHub Copilot 没有公开的 usage API）
	if account.Platform == PlatformCopilot {
		usage, err := s.getCopilotUsage(ctx, account)
		if err == nil {
			s.tryClearRecoverableAccountError(ctx, account)
		}
		return usage, err
	}

	// Antigravity 平台：使用 AntigravityQuotaFetcher 获取额度
	if account.Platform == PlatformAntigravity {
		usage, err := s.getAntigravityUsage(ctx, account)
		if err == nil {
			s.tryClearRecoverableAccountError(ctx, account)
		}
		return usage, err
	}

	// 只有oauth类型账号可以通过API获取usage（有profile scope）
	if account.CanGetUsage() {
		var apiResp *ClaudeUsageResponse

		// 1. 检查 API 缓存（10 分钟）
		if cached, ok := s.cache.apiCache.Load(accountID); ok {
			if cache, ok := cached.(*apiUsageCache); ok && time.Since(cache.timestamp) < apiCacheTTL {
				apiResp = cache.response
			}
		}

		// 2. 如果没有缓存，从 API 获取
		if apiResp == nil {
			apiResp, err = s.fetchOAuthUsageRaw(ctx, account)
			if err != nil {
				return nil, err
			}
			// 缓存 API 响应
			s.cache.apiCache.Store(accountID, &apiUsageCache{
				response:  apiResp,
				timestamp: time.Now(),
			})
		}

		// 3. 构建 UsageInfo（每次都重新计算 RemainingSeconds）
		now := time.Now()
		usage := s.buildUsageInfo(apiResp, &now)

		// 4. 添加窗口统计（有独立缓存，1 分钟）
		s.addWindowStats(ctx, account, usage)

		s.tryClearRecoverableAccountError(ctx, account)
		return usage, nil
	}

	// Setup Token账号：根据session_window推算（没有profile scope，无法调用usage API）
	if account.Type == AccountTypeSetupToken {
		usage := s.estimateSetupTokenUsage(account)
		// 添加窗口统计
		s.addWindowStats(ctx, account, usage)
		return usage, nil
	}

	// API Key账号不支持usage查询
	return nil, fmt.Errorf("account type %s does not support usage query", account.Type)
}

func (s *AccountUsageService) getGeminiUsage(ctx context.Context, account *Account) (*UsageInfo, error) {
	now := time.Now()
	usage := &UsageInfo{
		UpdatedAt: &now,
	}

	if s.geminiQuotaService == nil || s.usageLogRepo == nil {
		return usage, nil
	}

	quota, ok := s.geminiQuotaService.QuotaForAccount(ctx, account)
	if !ok {
		return usage, nil
	}

	dayStart := geminiDailyWindowStart(now)
	stats, err := s.usageLogRepo.GetModelStatsWithFilters(ctx, dayStart, now, 0, 0, account.ID, 0, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("get gemini usage stats failed: %w", err)
	}

	dayTotals := geminiAggregateUsage(stats)
	dailyResetAt := geminiDailyResetTime(now)

	// Daily window (RPD)
	if quota.SharedRPD > 0 {
		totalReq := dayTotals.ProRequests + dayTotals.FlashRequests
		totalTokens := dayTotals.ProTokens + dayTotals.FlashTokens
		totalCost := dayTotals.ProCost + dayTotals.FlashCost
		usage.GeminiSharedDaily = buildGeminiUsageProgress(totalReq, quota.SharedRPD, dailyResetAt, totalTokens, totalCost, now)
	} else {
		usage.GeminiProDaily = buildGeminiUsageProgress(dayTotals.ProRequests, quota.ProRPD, dailyResetAt, dayTotals.ProTokens, dayTotals.ProCost, now)
		usage.GeminiFlashDaily = buildGeminiUsageProgress(dayTotals.FlashRequests, quota.FlashRPD, dailyResetAt, dayTotals.FlashTokens, dayTotals.FlashCost, now)
	}

	// Minute window (RPM) - fixed-window approximation: current minute [truncate(now), truncate(now)+1m)
	minuteStart := now.Truncate(time.Minute)
	minuteResetAt := minuteStart.Add(time.Minute)
	minuteStats, err := s.usageLogRepo.GetModelStatsWithFilters(ctx, minuteStart, now, 0, 0, account.ID, 0, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("get gemini minute usage stats failed: %w", err)
	}
	minuteTotals := geminiAggregateUsage(minuteStats)

	if quota.SharedRPM > 0 {
		totalReq := minuteTotals.ProRequests + minuteTotals.FlashRequests
		totalTokens := minuteTotals.ProTokens + minuteTotals.FlashTokens
		totalCost := minuteTotals.ProCost + minuteTotals.FlashCost
		usage.GeminiSharedMinute = buildGeminiUsageProgress(totalReq, quota.SharedRPM, minuteResetAt, totalTokens, totalCost, now)
	} else {
		usage.GeminiProMinute = buildGeminiUsageProgress(minuteTotals.ProRequests, quota.ProRPM, minuteResetAt, minuteTotals.ProTokens, minuteTotals.ProCost, now)
		usage.GeminiFlashMinute = buildGeminiUsageProgress(minuteTotals.FlashRequests, quota.FlashRPM, minuteResetAt, minuteTotals.FlashTokens, minuteTotals.FlashCost, now)
	}

	return usage, nil
}

// getAntigravityUsage 获取 Antigravity 账户额度
func (s *AccountUsageService) getAntigravityUsage(ctx context.Context, account *Account) (*UsageInfo, error) {
	if s.antigravityQuotaFetcher == nil || !s.antigravityQuotaFetcher.CanFetch(account) {
		now := time.Now()
		return &UsageInfo{UpdatedAt: &now}, nil
	}

	// 1. 检查缓存（10 分钟）
	if cached, ok := s.cache.antigravityCache.Load(account.ID); ok {
		if cache, ok := cached.(*antigravityUsageCache); ok && time.Since(cache.timestamp) < apiCacheTTL {
			// 重新计算 RemainingSeconds
			usage := cache.usageInfo
			if usage.FiveHour != nil && usage.FiveHour.ResetsAt != nil {
				usage.FiveHour.RemainingSeconds = int(time.Until(*usage.FiveHour.ResetsAt).Seconds())
			}
			return usage, nil
		}
	}

	// 2. 获取代理 URL
	proxyURL := s.antigravityQuotaFetcher.GetProxyURL(ctx, account)

	// 3. 调用 API 获取额度
	result, err := s.antigravityQuotaFetcher.FetchQuota(ctx, account, proxyURL)
	if err != nil {
		return nil, fmt.Errorf("fetch antigravity quota failed: %w", err)
	}

	// 4. 缓存结果
	s.cache.antigravityCache.Store(account.ID, &antigravityUsageCache{
		usageInfo: result.UsageInfo,
		timestamp: time.Now(),
	})

	return result.UsageInfo, nil
}

// addWindowStats 为 usage 数据添加窗口期统计
// 使用独立缓存（1 分钟），与 API 缓存分离
func (s *AccountUsageService) addWindowStats(ctx context.Context, account *Account, usage *UsageInfo) {
	// 修复：即使 FiveHour 为 nil，也要尝试获取统计数据
	// 因为 SevenDay/SevenDaySonnet 可能需要
	if usage.FiveHour == nil && usage.SevenDay == nil && usage.SevenDaySonnet == nil {
		return
	}

	// 检查窗口统计缓存（1 分钟）
	var windowStats *WindowStats
	if cached, ok := s.cache.windowStatsCache.Load(account.ID); ok {
		if cache, ok := cached.(*windowStatsCache); ok && time.Since(cache.timestamp) < windowStatsCacheTTL {
			windowStats = cache.stats
		}
	}

	// 如果没有缓存，从数据库查询
	if windowStats == nil {
		// 使用统一的窗口开始时间计算逻辑（考虑窗口过期情况）
		startTime := account.GetCurrentWindowStartTime()

		stats, err := s.usageLogRepo.GetAccountWindowStats(ctx, account.ID, startTime)
		if err != nil {
			log.Printf("Failed to get window stats for account %d: %v", account.ID, err)
			return
		}

		windowStats = &WindowStats{
			Requests:     stats.Requests,
			Tokens:       stats.Tokens,
			Cost:         stats.Cost,
			StandardCost: stats.StandardCost,
			UserCost:     stats.UserCost,
		}

		// 缓存窗口统计（1 分钟）
		s.cache.windowStatsCache.Store(account.ID, &windowStatsCache{
			stats:     windowStats,
			timestamp: time.Now(),
		})
	}

	// 为 FiveHour 添加 WindowStats（5h 窗口统计）
	if usage.FiveHour != nil {
		usage.FiveHour.WindowStats = windowStats
	}
}

// GetTodayStats 获取账号今日统计
func (s *AccountUsageService) GetTodayStats(ctx context.Context, accountID int64) (*WindowStats, error) {
	stats, err := s.usageLogRepo.GetAccountTodayStats(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("get today stats failed: %w", err)
	}

	return &WindowStats{
		Requests:     stats.Requests,
		Tokens:       stats.Tokens,
		Cost:         stats.Cost,
		StandardCost: stats.StandardCost,
		UserCost:     stats.UserCost,
	}, nil
}

func (s *AccountUsageService) GetAccountUsageStats(ctx context.Context, accountID int64, startTime, endTime time.Time) (*usagestats.AccountUsageStatsResponse, error) {
	stats, err := s.usageLogRepo.GetAccountUsageStats(ctx, accountID, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("get account usage stats failed: %w", err)
	}
	return stats, nil
}

// fetchOAuthUsageRaw 从 Anthropic API 获取原始响应（不构建 UsageInfo）
// 如果账号开启了 TLS 指纹，则使用 TLS 指纹伪装
// 如果有缓存的 Fingerprint，则使用缓存的 User-Agent 等信息
func (s *AccountUsageService) fetchOAuthUsageRaw(ctx context.Context, account *Account) (*ClaudeUsageResponse, error) {
	accessToken := account.GetCredential("access_token")
	if accessToken == "" {
		return nil, fmt.Errorf("no access token available")
	}

	var proxyURL string
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	// 构建完整的选项
	opts := &ClaudeUsageFetchOptions{
		AccessToken:          accessToken,
		ProxyURL:             proxyURL,
		AccountID:            account.ID,
		EnableTLSFingerprint: account.IsTLSFingerprintEnabled(),
	}

	// 尝试获取缓存的 Fingerprint（包含 User-Agent 等信息）
	if s.identityCache != nil {
		if fp, err := s.identityCache.GetFingerprint(ctx, account.ID); err == nil && fp != nil {
			opts.Fingerprint = fp
		}
	}

	return s.usageFetcher.FetchUsageWithOptions(ctx, opts)
}

// parseTime 尝试多种格式解析时间
func parseTime(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05.000Z",
	}
	for _, format := range formats {
		if t, err := time.Parse(format, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unable to parse time: %s", s)
}

func (s *AccountUsageService) tryClearRecoverableAccountError(ctx context.Context, account *Account) {
	if account == nil || account.Status != StatusError {
		return
	}

	msg := strings.ToLower(strings.TrimSpace(account.ErrorMessage))
	if msg == "" {
		return
	}

	if !strings.Contains(msg, "token refresh failed") &&
		!strings.Contains(msg, "invalid_client") &&
		!strings.Contains(msg, "missing_project_id") &&
		!strings.Contains(msg, "unauthenticated") {
		return
	}

	if err := s.accountRepo.ClearError(ctx, account.ID); err != nil {
		log.Printf("[usage] failed to clear recoverable account error for account %d: %v", account.ID, err)
		return
	}

	account.Status = StatusActive
	account.ErrorMessage = ""
}

// buildUsageInfo 构建UsageInfo
func (s *AccountUsageService) buildUsageInfo(resp *ClaudeUsageResponse, updatedAt *time.Time) *UsageInfo {
	info := &UsageInfo{
		UpdatedAt: updatedAt,
	}

	// 5小时窗口 - 始终创建对象（即使 ResetsAt 为空）
	info.FiveHour = &UsageProgress{
		Utilization: resp.FiveHour.Utilization,
	}
	if resp.FiveHour.ResetsAt != "" {
		if fiveHourReset, err := parseTime(resp.FiveHour.ResetsAt); err == nil {
			info.FiveHour.ResetsAt = &fiveHourReset
			info.FiveHour.RemainingSeconds = int(time.Until(fiveHourReset).Seconds())
		} else {
			log.Printf("Failed to parse FiveHour.ResetsAt: %s, error: %v", resp.FiveHour.ResetsAt, err)
		}
	}

	// 7天窗口
	if resp.SevenDay.ResetsAt != "" {
		if sevenDayReset, err := parseTime(resp.SevenDay.ResetsAt); err == nil {
			info.SevenDay = &UsageProgress{
				Utilization:      resp.SevenDay.Utilization,
				ResetsAt:         &sevenDayReset,
				RemainingSeconds: int(time.Until(sevenDayReset).Seconds()),
			}
		} else {
			log.Printf("Failed to parse SevenDay.ResetsAt: %s, error: %v", resp.SevenDay.ResetsAt, err)
			info.SevenDay = &UsageProgress{
				Utilization: resp.SevenDay.Utilization,
			}
		}
	}

	// 7天Sonnet窗口
	if resp.SevenDaySonnet.ResetsAt != "" {
		if sonnetReset, err := parseTime(resp.SevenDaySonnet.ResetsAt); err == nil {
			info.SevenDaySonnet = &UsageProgress{
				Utilization:      resp.SevenDaySonnet.Utilization,
				ResetsAt:         &sonnetReset,
				RemainingSeconds: int(time.Until(sonnetReset).Seconds()),
			}
		} else {
			log.Printf("Failed to parse SevenDaySonnet.ResetsAt: %s, error: %v", resp.SevenDaySonnet.ResetsAt, err)
			info.SevenDaySonnet = &UsageProgress{
				Utilization: resp.SevenDaySonnet.Utilization,
			}
		}
	}

	return info
}

// estimateSetupTokenUsage 根据session_window推算Setup Token账号的使用量
func (s *AccountUsageService) estimateSetupTokenUsage(account *Account) *UsageInfo {
	info := &UsageInfo{}

	// 如果有session_window信息
	if account.SessionWindowEnd != nil {
		remaining := int(time.Until(*account.SessionWindowEnd).Seconds())
		if remaining < 0 {
			remaining = 0
		}

		// 根据状态估算使用率 (百分比形式，100 = 100%)
		var utilization float64
		switch account.SessionWindowStatus {
		case "rejected":
			utilization = 100.0
		case "allowed_warning":
			utilization = 80.0
		default:
			utilization = 0.0
		}

		info.FiveHour = &UsageProgress{
			Utilization:      utilization,
			ResetsAt:         account.SessionWindowEnd,
			RemainingSeconds: remaining,
		}
	} else {
		// 没有窗口信息，返回空数据
		info.FiveHour = &UsageProgress{
			Utilization:      0,
			RemainingSeconds: 0,
		}
	}

	// Setup Token无法获取7d数据
	return info
}

func buildGeminiUsageProgress(used, limit int64, resetAt time.Time, tokens int64, cost float64, now time.Time) *UsageProgress {
	// limit <= 0 means "no local quota window" (unknown or unlimited).
	if limit <= 0 {
		return nil
	}
	utilization := (float64(used) / float64(limit)) * 100
	remainingSeconds := int(resetAt.Sub(now).Seconds())
	if remainingSeconds < 0 {
		remainingSeconds = 0
	}
	resetCopy := resetAt
	return &UsageProgress{
		Utilization:      utilization,
		ResetsAt:         &resetCopy,
		RemainingSeconds: remainingSeconds,
		UsedRequests:     used,
		LimitRequests:    limit,
		WindowStats: &WindowStats{
			Requests: used,
			Tokens:   tokens,
			Cost:     cost,
		},
	}
}

// GetAccountWindowStats 获取账号在指定时间窗口内的使用统计
// 用于账号列表页面显示当前窗口费用
func (s *AccountUsageService) GetAccountWindowStats(ctx context.Context, accountID int64, startTime time.Time) (*usagestats.AccountStats, error) {
	return s.usageLogRepo.GetAccountWindowStats(ctx, accountID, startTime)
}

// getCopilotUsage 获取 Copilot 账户用量统计
// 优先使用 GitHub Copilot Usage API，失败时回退到本地 usage_logs 表计算
func (s *AccountUsageService) getCopilotUsage(ctx context.Context, account *Account) (*UsageInfo, error) {
	now := time.Now()
	usage := &UsageInfo{
		UpdatedAt: &now,
	}

	// 获取 access_token
	tokenProvider := NewCopilotTokenProvider()
	accessToken, err := tokenProvider.GetAccessToken(ctx, account)
	if err != nil {
		log.Printf("Failed to get copilot access token for account %d: %v", account.ID, err)
		return s.getCopilotLocalUsage(ctx, account, now)
	}

	// 获取代理 URL
	var proxyURL string
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	// 调用 Copilot Usage API
	if s.copilotUsageFetcher != nil {
		apiResp, err := s.copilotUsageFetcher.FetchUsage(ctx, accessToken, proxyURL, account.ID)
		if err != nil {
			log.Printf("Failed to fetch copilot usage API for account %d: %v", account.ID, err)
			return s.getCopilotLocalUsage(ctx, account, now)
		}

		// DEBUG: 记录 API 响应原始数据
		quota := apiResp.QuotaSnapshots.Chat
		premiumQuota := apiResp.QuotaSnapshots.PremiumInteractions
		log.Printf("[DEBUG] Copilot API response for account %d: quota_reset_date=%s, plan=%s",
			account.ID, apiResp.QuotaResetDate, apiResp.CopilotPlan)
		log.Printf("[DEBUG] Chat quota: entitlement=%.0f, remaining=%.0f, percent_remaining=%.2f, unlimited=%v",
			quota.Entitlement, quota.QuotaRemaining, quota.PercentRemaining, quota.Unlimited)
		log.Printf("[DEBUG] Premium quota: entitlement=%.0f, remaining=%.0f, percent_remaining=%.2f, unlimited=%v",
			premiumQuota.Entitlement, premiumQuota.QuotaRemaining, premiumQuota.PercentRemaining, premiumQuota.Unlimited)

		// 解析 quota_reset_date（优先使用 UTC 格式，回退到日期格式）
		var resetAt time.Time
		var parseErr error
		if apiResp.QuotaResetDateUTC != "" {
			resetAt, parseErr = time.Parse(time.RFC3339Nano, apiResp.QuotaResetDateUTC)
		}
		if parseErr != nil || apiResp.QuotaResetDateUTC == "" {
			if apiResp.QuotaResetDate != "" {
				resetAt, parseErr = time.Parse("2006-01-02", apiResp.QuotaResetDate)
				// 如果解析成功，设置时间为当天的 23:59:59 UTC（月末最后一天）
				if parseErr == nil {
					resetAt = time.Date(resetAt.Year(), resetAt.Month(), resetAt.Day(), 23, 59, 59, 0, time.UTC)
				}
			}
		}

		if parseErr != nil {
			log.Printf("[ERROR] Failed to parse quota_reset_date='%s', quota_reset_date_utc='%s': %v",
				apiResp.QuotaResetDate, apiResp.QuotaResetDateUTC, parseErr)
			// 解析失败，回退到本地统计
			return s.addCopilotLocalUsage(ctx, account, now, usage)
		}

		if !resetAt.IsZero() {
			// 优先使用 premium_interactions 配额（Copilot 的主要计费指标）
			// 如果 premium_interactions 没有数据，则回退到 chat 配额
			effectiveQuota := apiResp.QuotaSnapshots.PremiumInteractions
			if effectiveQuota.Entitlement == 0 && effectiveQuota.QuotaRemaining == 0 {
				log.Printf("[DEBUG] Premium quota is empty, falling back to chat quota")
				effectiveQuota = apiResp.QuotaSnapshots.Chat
			}

			// 计算 utilization：优先使用 PercentRemaining，如果没有则用 Entitlement 计算
			utilization := 100.0 - effectiveQuota.PercentRemaining
			if effectiveQuota.Unlimited {
				utilization = 0
			} else if effectiveQuota.Entitlement == 0 && effectiveQuota.PercentRemaining == 100 {
				// Entitlement=0 且 PercentRemaining=100，说明 API 未返回有效配额数据
				// 保持 utilization=0（默认值），前端会显示为无配额信息
				utilization = 0
			}

			usedReqs := int64(0)
			if effectiveQuota.Entitlement > 0 {
				usedReqs = int64(effectiveQuota.Entitlement - effectiveQuota.QuotaRemaining)
			}

			log.Printf("[DEBUG] Copilot usage calculated for account %d: utilization=%.2f%%, used=%d, limit=%.0f",
				account.ID, utilization, usedReqs, effectiveQuota.Entitlement)

			// Copilot API 返回的是月度配额，应该放入 CopilotMonthly
			usage.CopilotMonthly = &UsageProgress{
				Utilization:      utilization,
				ResetsAt:         &resetAt,
				RemainingSeconds: int(time.Until(resetAt).Seconds()),
				UsedRequests:     usedReqs,
				LimitRequests:    int64(effectiveQuota.Entitlement),
			}
		} else {
			log.Printf("[ERROR] Failed to parse quota_reset_date for account %d: %v", account.ID, parseErr)
		}
	} else {
		log.Printf("[WARN] copilotUsageFetcher is nil for account %d", account.ID)
	}

	// 添加本地统计（即使 API 成功也获取本地数据）
	return s.addCopilotLocalUsage(ctx, account, now, usage)
}

// getCopilotLocalUsage 回退到本地 usage_logs 表计算
func (s *AccountUsageService) getCopilotLocalUsage(ctx context.Context, account *Account, now time.Time) (*UsageInfo, error) {
	usage := &UsageInfo{
		UpdatedAt: &now,
	}
	return s.addCopilotLocalUsage(ctx, account, now, usage)
}

// addCopilotLocalUsage 添加本地统计信息到 UsageInfo
func (s *AccountUsageService) addCopilotLocalUsage(ctx context.Context, account *Account, now time.Time, usage *UsageInfo) (*UsageInfo, error) {
	if s.usageLogRepo == nil {
		return usage, nil
	}

	// 计算时间窗口（使用 UTC 时间）
	// Copilot 只有月度配额，没有每日配额，不设置 CopilotDaily
	// 本地每日统计暂不展示（后续可考虑在详情页展示）
	monthStart := copilotMonthlyWindowStart(now)

	// 获取本月统计（仅在 API 未设置 CopilotMonthly 时才使用本地统计）
	if usage.CopilotMonthly == nil {
		monthStats, err := s.usageLogRepo.GetAccountStatsAggregated(ctx, account.ID, monthStart, now)
		if err != nil {
			log.Printf("Failed to get copilot month stats for account %d: %v", account.ID, err)
		} else if monthStats != nil {
			monthResetAt := copilotMonthlyResetTime(now)
			accountCost := 0.0
			if monthStats.TotalAccountCost != nil {
				accountCost = *monthStats.TotalAccountCost
			}
			usage.CopilotMonthly = &UsageProgress{
				WindowStats: &WindowStats{
					Requests:     monthStats.TotalRequests,
					Tokens:       monthStats.TotalTokens,
					Cost:         accountCost,
					StandardCost: monthStats.TotalCost,
					UserCost:     monthStats.TotalActualCost,
				},
				ResetsAt:         &monthResetAt,
				RemainingSeconds: int(time.Until(monthResetAt).Seconds()),
			}
		}
	}
	// 注意：如果 API 已经设置了 CopilotMonthly，不覆盖它，也不添加 WindowStats
	// 因为 API 返回的配额信息（UsedRequests/LimitRequests）已经足够显示

	return usage, nil
}

// copilotMonthlyWindowStart 返回 Copilot 每月窗口的开始时间（UTC 每月1日 00:00:00）
func copilotMonthlyWindowStart(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// copilotMonthlyResetTime 返回 Copilot 每月重置时间（下个月1日 00:00:00 UTC）
func copilotMonthlyResetTime(now time.Time) time.Time {
	year := now.Year()
	month := now.Month() + 1
	if month > 12 {
		month = 1
		year++
	}
	return time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
}
