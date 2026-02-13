package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const (
	githubCopilotTokenExchangeURL = "https://api.github.com/copilot_internal/v2/token"

	githubCopilotTokenMinTTL     = 30 * time.Second
	githubCopilotTokenSkew       = time.Minute
	githubCopilotTokenLockTTL    = 30 * time.Second
	githubCopilotTokenLockWait   = 200 * time.Millisecond
	githubCopilotTokenMaxBodyLen = 2 << 20
)

type GitHubCopilotTokenProvider struct {
	tokenCache   GeminiTokenCache
	httpUpstream HTTPUpstream
}

func NewGitHubCopilotTokenProvider(tokenCache GeminiTokenCache, httpUpstream HTTPUpstream) *GitHubCopilotTokenProvider {
	return &GitHubCopilotTokenProvider{
		tokenCache:   tokenCache,
		httpUpstream: httpUpstream,
	}
}

func (p *GitHubCopilotTokenProvider) GetAccessToken(ctx context.Context, account *Account) (string, error) {
	if account == nil {
		return "", errors.New("account is nil")
	}
	if !isGitHubCopilotAccount(account) {
		return "", errors.New("not a github copilot apikey account")
	}

	githubToken := strings.TrimSpace(account.GetCredential("github_token"))
	if githubToken == "" {
		githubToken = strings.TrimSpace(account.GetCredential("gh_token"))
	}
	if githubToken == "" {
		return "", errors.New("github_token not found in credentials")
	}

	cacheKey := GitHubCopilotTokenCacheKey(account)

	if p.tokenCache != nil {
		if token, err := p.tokenCache.GetAccessToken(ctx, cacheKey); err == nil && strings.TrimSpace(token) != "" {
			slog.Debug("github_copilot_token_cache_hit", "account_id", account.ID)
			return token, nil
		} else if err != nil {
			slog.Warn("github_copilot_token_cache_get_failed", "account_id", account.ID, "error", err)
		}
	}

	slog.Debug("github_copilot_token_cache_miss", "account_id", account.ID)

	if p.tokenCache == nil {
		return p.exchangeCopilotToken(ctx, account, githubToken)
	}

	locked, lockErr := p.tokenCache.AcquireRefreshLock(ctx, cacheKey, githubCopilotTokenLockTTL)
	if lockErr == nil && locked {
		defer func() { _ = p.tokenCache.ReleaseRefreshLock(ctx, cacheKey) }()
		if token, err := p.tokenCache.GetAccessToken(ctx, cacheKey); err == nil && strings.TrimSpace(token) != "" {
			return token, nil
		}
		return p.exchangeAndCacheCopilotToken(ctx, account, githubToken, cacheKey)
	}
	if lockErr != nil {
		slog.Warn("github_copilot_token_lock_failed_degraded_refresh", "account_id", account.ID, "error", lockErr)
		return p.exchangeAndCacheCopilotToken(ctx, account, githubToken, cacheKey)
	}

	time.Sleep(githubCopilotTokenLockWait)
	if token, err := p.tokenCache.GetAccessToken(ctx, cacheKey); err == nil && strings.TrimSpace(token) != "" {
		slog.Debug("github_copilot_token_cache_hit_after_wait", "account_id", account.ID)
		return token, nil
	}

	return p.exchangeAndCacheCopilotToken(ctx, account, githubToken, cacheKey)
}

func (p *GitHubCopilotTokenProvider) Invalidate(ctx context.Context, account *Account) {
	if p == nil || p.tokenCache == nil || account == nil {
		return
	}
	_ = p.tokenCache.DeleteAccessToken(ctx, GitHubCopilotTokenCacheKey(account))
}

func (p *GitHubCopilotTokenProvider) exchangeAndCacheCopilotToken(ctx context.Context, account *Account, githubToken, cacheKey string) (string, error) {
	token, ttl, err := p.exchangeCopilotTokenWithTTL(ctx, account, githubToken)
	if err != nil {
		return "", err
	}
	if p.tokenCache != nil {
		if err := p.tokenCache.SetAccessToken(ctx, cacheKey, token, ttl); err != nil {
			slog.Warn("github_copilot_token_cache_set_failed", "account_id", account.ID, "error", err)
		}
	}
	return token, nil
}

func (p *GitHubCopilotTokenProvider) exchangeCopilotToken(ctx context.Context, account *Account, githubToken string) (string, error) {
	token, _, err := p.exchangeCopilotTokenWithTTL(ctx, account, githubToken)
	return token, err
}

func (p *GitHubCopilotTokenProvider) exchangeCopilotTokenWithTTL(ctx context.Context, account *Account, githubToken string) (string, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubCopilotTokenExchangeURL, nil)
	if err != nil {
		return "", 0, err
	}
	applyGitHubCopilotTokenExchangeHeaders(req, githubToken)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	resp, err := p.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return "", 0, fmt.Errorf("copilot token exchange request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, githubCopilotTokenMaxBodyLen))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(ExtractUpstreamErrorMessage(body))
		msg = sanitizeUpstreamErrorMessage(msg)
		if msg == "" {
			msg = strings.TrimSpace(string(body))
			msg = sanitizeUpstreamErrorMessage(msg)
		}
		if msg == "" {
			msg = "token exchange failed"
		}
		return "", 0, fmt.Errorf("copilot token exchange failed: status=%d message=%s", resp.StatusCode, msg)
	}

	var parsed struct {
		ExpiresAt int64  `json:"expires_at"`
		RefreshIn int64  `json:"refresh_in"`
		Token     string `json:"token"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", 0, fmt.Errorf("parse copilot token exchange response: %w", err)
	}
	token := strings.TrimSpace(parsed.Token)
	if token == "" {
		return "", 0, errors.New("copilot token is empty")
	}

	ttl := githubCopilotTokenTTL(time.Now(), parsed.ExpiresAt, parsed.RefreshIn)
	if ttl < githubCopilotTokenMinTTL {
		ttl = githubCopilotTokenMinTTL
	}
	return token, ttl, nil
}

func githubCopilotTokenTTL(now time.Time, expiresAtSec, refreshInSec int64) time.Duration {
	if refreshInSec > 0 {
		ttl := time.Duration(refreshInSec)*time.Second - githubCopilotTokenSkew
		if ttl > 0 {
			return ttl
		}
	}
	if expiresAtSec > 0 {
		expiresAt := time.Unix(expiresAtSec, 0)
		ttl := expiresAt.Sub(now) - githubCopilotTokenSkew
		if ttl > 0 {
			return ttl
		}
	}
	return 10 * time.Minute
}
