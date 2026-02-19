package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
)

const (
	openAITokenRefreshSkew = 3 * time.Minute
	openAITokenCacheSkew   = 5 * time.Minute
	openAILockWaitTime     = 200 * time.Millisecond
	openAIModelsMaxBodyLen = 1024 * 1024
)

// OpenAITokenCache Token 缓存接口（复用 GeminiTokenCache 接口定义）
type OpenAITokenCache = GeminiTokenCache

// OpenAITokenProvider 管理 OpenAI OAuth 账户的 access_token
type OpenAITokenProvider struct {
	accountRepo        AccountRepository
	tokenCache         OpenAITokenCache
	openAIOAuthService *OpenAIOAuthService
	httpUpstream       HTTPUpstream
}

func NewOpenAITokenProvider(
	accountRepo AccountRepository,
	tokenCache OpenAITokenCache,
	openAIOAuthService *OpenAIOAuthService,
	httpUpstream HTTPUpstream,
) *OpenAITokenProvider {
	return &OpenAITokenProvider{
		accountRepo:        accountRepo,
		tokenCache:         tokenCache,
		openAIOAuthService: openAIOAuthService,
		httpUpstream:       httpUpstream,
	}
}

// GetAccessToken 获取有效的 access_token
func (p *OpenAITokenProvider) GetAccessToken(ctx context.Context, account *Account) (string, error) {
	if account == nil {
		return "", errors.New("account is nil")
	}
	if account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth {
		return "", errors.New("not an openai oauth account")
	}

	cacheKey := OpenAITokenCacheKey(account)

	// 1. 先尝试缓存
	if p.tokenCache != nil {
		if token, err := p.tokenCache.GetAccessToken(ctx, cacheKey); err == nil && strings.TrimSpace(token) != "" {
			slog.Debug("openai_token_cache_hit", "account_id", account.ID)
			return token, nil
		} else if err != nil {
			slog.Warn("openai_token_cache_get_failed", "account_id", account.ID, "error", err)
		}
	}

	slog.Debug("openai_token_cache_miss", "account_id", account.ID)

	// 2. 如果即将过期则刷新
	expiresAt := account.GetCredentialAsTime("expires_at")
	needsRefresh := expiresAt == nil || time.Until(*expiresAt) <= openAITokenRefreshSkew
	refreshFailed := false
	if needsRefresh && p.tokenCache != nil {
		locked, lockErr := p.tokenCache.AcquireRefreshLock(ctx, cacheKey, 30*time.Second)
		if lockErr == nil && locked {
			defer func() { _ = p.tokenCache.ReleaseRefreshLock(ctx, cacheKey) }()

			// 拿到锁后再次检查缓存（另一个 worker 可能已刷新）
			if token, err := p.tokenCache.GetAccessToken(ctx, cacheKey); err == nil && strings.TrimSpace(token) != "" {
				return token, nil
			}

			// 从数据库获取最新账户信息
			fresh, err := p.accountRepo.GetByID(ctx, account.ID)
			if err == nil && fresh != nil {
				account = fresh
			}
			expiresAt = account.GetCredentialAsTime("expires_at")
			if expiresAt == nil || time.Until(*expiresAt) <= openAITokenRefreshSkew {
				if p.openAIOAuthService == nil {
					slog.Warn("openai_oauth_service_not_configured", "account_id", account.ID)
					refreshFailed = true // 无法刷新，标记失败
				} else {
					tokenInfo, err := p.openAIOAuthService.RefreshAccountToken(ctx, account)
					if err != nil {
						// 刷新失败时记录警告，但不立即返回错误，尝试使用现有 token
						slog.Warn("openai_token_refresh_failed", "account_id", account.ID, "error", err)
						refreshFailed = true // 刷新失败，标记以使用短 TTL
					} else {
						newCredentials := p.openAIOAuthService.BuildAccountCredentials(tokenInfo)
						for k, v := range account.Credentials {
							if _, exists := newCredentials[k]; !exists {
								newCredentials[k] = v
							}
						}
						account.Credentials = newCredentials
						if updateErr := p.accountRepo.Update(ctx, account); updateErr != nil {
							slog.Error("openai_token_provider_update_failed", "account_id", account.ID, "error", updateErr)
						}
						expiresAt = account.GetCredentialAsTime("expires_at")
					}
				}
			}
		} else if lockErr != nil {
			// Redis 错误导致无法获取锁，降级为无锁刷新（仅在 token 接近过期时）
			slog.Warn("openai_token_lock_failed_degraded_refresh", "account_id", account.ID, "error", lockErr)

			// 检查 ctx 是否已取消
			if ctx.Err() != nil {
				return "", ctx.Err()
			}

			// 从数据库获取最新账户信息
			if p.accountRepo != nil {
				fresh, err := p.accountRepo.GetByID(ctx, account.ID)
				if err == nil && fresh != nil {
					account = fresh
				}
			}
			expiresAt = account.GetCredentialAsTime("expires_at")

			// 仅在 expires_at 已过期/接近过期时才执行无锁刷新
			if expiresAt == nil || time.Until(*expiresAt) <= openAITokenRefreshSkew {
				if p.openAIOAuthService == nil {
					slog.Warn("openai_oauth_service_not_configured", "account_id", account.ID)
					refreshFailed = true
				} else {
					tokenInfo, err := p.openAIOAuthService.RefreshAccountToken(ctx, account)
					if err != nil {
						slog.Warn("openai_token_refresh_failed_degraded", "account_id", account.ID, "error", err)
						refreshFailed = true
					} else {
						newCredentials := p.openAIOAuthService.BuildAccountCredentials(tokenInfo)
						for k, v := range account.Credentials {
							if _, exists := newCredentials[k]; !exists {
								newCredentials[k] = v
							}
						}
						account.Credentials = newCredentials
						if updateErr := p.accountRepo.Update(ctx, account); updateErr != nil {
							slog.Error("openai_token_provider_update_failed", "account_id", account.ID, "error", updateErr)
						}
						expiresAt = account.GetCredentialAsTime("expires_at")
					}
				}
			}
		} else {
			// 锁获取失败（被其他 worker 持有），等待 200ms 后重试读取缓存
			time.Sleep(openAILockWaitTime)
			if token, err := p.tokenCache.GetAccessToken(ctx, cacheKey); err == nil && strings.TrimSpace(token) != "" {
				slog.Debug("openai_token_cache_hit_after_wait", "account_id", account.ID)
				return token, nil
			}
		}
	}

	accessToken := account.GetOpenAIAccessToken()
	if strings.TrimSpace(accessToken) == "" {
		return "", errors.New("access_token not found in credentials")
	}

	// 3. 存入缓存（验证版本后再写入，避免异步刷新任务与请求线程的竞态条件）
	if p.tokenCache != nil {
		latestAccount, isStale := CheckTokenVersion(ctx, account, p.accountRepo)
		if isStale && latestAccount != nil {
			// 版本过时，使用 DB 中的最新 token
			slog.Debug("openai_token_version_stale_use_latest", "account_id", account.ID)
			accessToken = latestAccount.GetOpenAIAccessToken()
			if strings.TrimSpace(accessToken) == "" {
				return "", errors.New("access_token not found after version check")
			}
			// 不写入缓存，让下次请求重新处理
		} else {
			ttl := 30 * time.Minute
			if refreshFailed {
				// 刷新失败时使用短 TTL，避免失效 token 长时间缓存导致 401 抖动
				ttl = time.Minute
				slog.Debug("openai_token_cache_short_ttl", "account_id", account.ID, "reason", "refresh_failed")
			} else if expiresAt != nil {
				until := time.Until(*expiresAt)
				switch {
				case until > openAITokenCacheSkew:
					ttl = until - openAITokenCacheSkew
				case until > 0:
					ttl = until
				default:
					ttl = time.Minute
				}
			}
			if err := p.tokenCache.SetAccessToken(ctx, cacheKey, accessToken, ttl); err != nil {
				slog.Warn("openai_token_cache_set_failed", "account_id", account.ID, "error", err)
			}
		}
	}

	return accessToken, nil
}

func (p *OpenAITokenProvider) ListModels(ctx context.Context, account *Account) ([]openai.Model, error) {
	if p == nil {
		return nil, errors.New("openai token provider is nil")
	}
	if account == nil {
		return nil, errors.New("account is nil")
	}
	if account.Platform != PlatformOpenAI {
		return nil, errors.New("not an openai account")
	}
	if p.httpUpstream == nil {
		return nil, errors.New("http upstream is nil")
	}

	var accessToken string
	var err error

	if account.Type == AccountTypeOAuth {
		accessToken, err = p.GetAccessToken(ctx, account)
		if err != nil {
			return nil, fmt.Errorf("get access token failed: %w", err)
		}
	} else {
		accessToken = account.GetCredential("api_key")
		if accessToken == "" {
			return nil, errors.New("api_key not found in credentials")
		}
	}

	baseURL := strings.TrimSpace(account.GetCredential("base_url"))
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}

	normalizedBaseURL, err := urlvalidator.ValidateURLFormat(baseURL, false)
	if err != nil {
		return nil, fmt.Errorf("invalid base_url: %w", err)
	}

	modelsURL := normalizedBaseURL + "/v1/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("authorization", "Bearer "+strings.TrimSpace(accessToken))
	req.Header.Set("accept", "application/json")

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	resp, err := p.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return nil, fmt.Errorf("openai models request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, openAIModelsMaxBodyLen))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := ExtractUpstreamErrorMessage(body)
		msg = sanitizeUpstreamErrorMessage(msg)
		if msg == "" {
			msg = fmt.Sprintf("models request failed with status %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("openai models request failed: %s", msg)
	}

	var parsed struct {
		Data []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Created int64  `json:"created"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse openai models response: %w", err)
	}
	if len(parsed.Data) == 0 {
		return nil, errors.New("openai models response is empty")
	}

	seen := make(map[string]struct{}, len(parsed.Data))
	result := make([]openai.Model, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		display := id
		result = append(result, openai.Model{
			ID:          id,
			Object:      "model",
			Created:     m.Created,
			OwnedBy:     strings.TrimSpace(m.OwnedBy),
			Type:        "model",
			DisplayName: display,
		})
	}
	if len(result) == 0 {
		return nil, errors.New("openai models response contained no model ids")
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result, nil
}
