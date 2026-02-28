package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// CopilotGatewayHandler handles GitHub Copilot API gateway requests.
type CopilotGatewayHandler struct {
	gatewayService          *service.GatewayService
	copilotGatewayService   *service.CopilotGatewayService
	billingCacheService     *service.BillingCacheService
	apiKeyService           *service.APIKeyService
	usageRecordWorkerPool   *service.UsageRecordWorkerPool
	errorPassthroughService *service.ErrorPassthroughService
	concurrencyHelper       *ConcurrencyHelper
	maxAccountSwitches      int
}

// NewCopilotGatewayHandler creates a new CopilotGatewayHandler.
func NewCopilotGatewayHandler(
	gatewayService *service.GatewayService,
	copilotGatewayService *service.CopilotGatewayService,
	concurrencyService *service.ConcurrencyService,
	billingCacheService *service.BillingCacheService,
	apiKeyService *service.APIKeyService,
	usageRecordWorkerPool *service.UsageRecordWorkerPool,
	errorPassthroughService *service.ErrorPassthroughService,
	cfg *config.Config,
) *CopilotGatewayHandler {
	pingInterval := time.Duration(0)
	maxAccountSwitches := 3
	if cfg != nil {
		pingInterval = time.Duration(cfg.Concurrency.PingInterval) * time.Second
		if cfg.Gateway.MaxAccountSwitches > 0 {
			maxAccountSwitches = cfg.Gateway.MaxAccountSwitches
		}
	}
	return &CopilotGatewayHandler{
		gatewayService:          gatewayService,
		copilotGatewayService:   copilotGatewayService,
		billingCacheService:     billingCacheService,
		apiKeyService:           apiKeyService,
		usageRecordWorkerPool:   usageRecordWorkerPool,
		errorPassthroughService: errorPassthroughService,
		concurrencyHelper:       NewConcurrencyHelper(concurrencyService, SSEPingFormatComment, pingInterval),
		maxAccountSwitches:      maxAccountSwitches,
	}
}

// copilotForwardFunc is the signature for both Forward and ForwardResponses.
type copilotForwardFunc func(ctx context.Context, c *gin.Context, account *service.Account, body []byte) (*service.CopilotForwardResult, error)

// ChatCompletions handles Copilot chat/completions endpoint.
// POST /copilot/v1/chat/completions
// For models that require the /responses endpoint (GPT-5+ and codex models),
// automatically routes through the Chat→Responses→Chat conversion path.
// This matches opencode's behavior where GPT-5+ models use /responses.
func (h *CopilotGatewayHandler) ChatCompletions(c *gin.Context) {
	h.handleForward(c, "chat_completions", func(ctx context.Context, c *gin.Context, account *service.Account, body []byte) (*service.CopilotForwardResult, error) {
		if isResponsesOnlyModel(gjson.GetBytes(body, "model").String()) {
			return h.copilotGatewayService.ForwardChatAsResponses(ctx, c, account, body)
		}
		return h.copilotGatewayService.Forward(ctx, c, account, body)
	})
}

// isResponsesOnlyModel returns true for models that should use the /responses endpoint.
// This matches opencode's shouldUseCopilotResponsesApi logic:
// - Codex models (contain "codex" in the name) always use /responses
// - GPT-5+ models (major version >= 5) use /responses, except gpt-5-mini
func isResponsesOnlyModel(model string) bool {
	lower := strings.ToLower(model)
	// First check for codex models (explicit routing)
	if strings.Contains(lower, "codex") {
		return true
	}
	// Then check if it's a GPT-5+ model (major version >= 5)
	// Exception: gpt-5-mini uses /chat/completions (matches opencode behavior)
	if strings.HasPrefix(lower, "gpt-") {
		if strings.HasPrefix(lower, "gpt-5-mini") {
			return false
		}
		rest := model[4:] // Skip "gpt-"
		if len(rest) == 0 {
			return false
		}
		// Extract major version number
		i := 0
		for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
			i++
		}
		if i == 0 {
			return false
		}
		major := 0
		for _, ch := range rest[:i] {
			major = major*10 + int(ch-'0')
		}
		return major >= 5
	}
	return false
}

// Responses handles Copilot responses endpoint.
// POST /copilot/v1/responses
func (h *CopilotGatewayHandler) Responses(c *gin.Context) {
	h.handleForward(c, "responses", h.copilotGatewayService.ForwardResponses)
}

// Messages handles Copilot messages endpoint (Anthropic Messages API format).
// POST /copilot/v1/messages
//
// For Claude models, the request is forwarded directly to Copilot's native
// /v1/messages endpoint, preserving cache_control, thinking, and other
// Anthropic-specific fields. Non-Claude models go through the existing
// Anthropic→Chat Completions conversion path.
func (h *CopilotGatewayHandler) Messages(c *gin.Context) {
	h.handleForward(c, "messages", func(ctx context.Context, c *gin.Context, account *service.Account, body []byte) (*service.CopilotForwardResult, error) {
		model := gjson.GetBytes(body, "model").String()
		if isClaudeModel(model) {
			return h.copilotGatewayService.ForwardMessages(ctx, c, account, body)
		}
		return h.copilotGatewayService.ForwardChatAsAnthropic(ctx, c, account, body)
	})
}

// isClaudeModel returns true if the model name indicates a Claude model.
func isClaudeModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(model), "claude")
}

// handleForward is the shared implementation for ChatCompletions and Responses.
func (h *CopilotGatewayHandler) handleForward(c *gin.Context, endpoint string, forwardFn copilotForwardFunc) {
	requestStart := time.Now()

	// Get apiKey and user from context (set by ApiKeyAuth middleware)
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := requestLogger(
		c,
		"handler.copilot_gateway."+endpoint,
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)

	// Read request body
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	if len(body) == 0 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}

	if !gjson.ValidBytes(body) {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}

	modelResult := gjson.GetBytes(body, "model")
	if !modelResult.Exists() || modelResult.Type != gjson.String || modelResult.String() == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	reqModel := modelResult.String()

	streamResult := gjson.GetBytes(body, "stream")
	reqStream := streamResult.Bool()
	reqLog = reqLog.With(zap.String("model", reqModel), zap.Bool("stream", reqStream))

	setOpsRequestContext(c, reqModel, reqStream, body)

	// Track if we've started streaming (for error handling)
	streamStarted := false

	// Bind error passthrough service for service-layer rule matching
	if h.errorPassthroughService != nil {
		service.BindErrorPassthroughService(c, h.errorPassthroughService)
	}

	// Get subscription info (may be nil)
	subscription, _ := middleware2.GetSubscriptionFromContext(c)

	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())
	routingStart := time.Now()

	// 1. Acquire user concurrency slot (fast path first)
	userReleaseFunc, userAcquired, err := h.concurrencyHelper.TryAcquireUserSlot(c.Request.Context(), subject.UserID, subject.Concurrency)
	if err != nil {
		reqLog.Warn("copilot.user_slot_acquire_failed", zap.Error(err))
		h.handleConcurrencyError(c, err, "user", streamStarted)
		return
	}

	waitCounted := false
	if !userAcquired {
		maxWait := service.CalculateMaxWait(subject.Concurrency)
		canWait, waitErr := h.concurrencyHelper.IncrementWaitCount(c.Request.Context(), subject.UserID, maxWait)
		if waitErr != nil {
			reqLog.Warn("copilot.user_wait_counter_increment_failed", zap.Error(waitErr))
		} else if !canWait {
			reqLog.Info("copilot.user_wait_queue_full", zap.Int("max_wait", maxWait))
			h.errorResponse(c, http.StatusTooManyRequests, "rate_limit_error", "Too many pending requests, please retry later")
			return
		}
		if waitErr == nil && canWait {
			waitCounted = true
		}
		defer func() {
			if waitCounted {
				h.concurrencyHelper.DecrementWaitCount(c.Request.Context(), subject.UserID)
			}
		}()

		userReleaseFunc, err = h.concurrencyHelper.AcquireUserSlotWithWait(c, subject.UserID, subject.Concurrency, reqStream, &streamStarted)
		if err != nil {
			reqLog.Warn("copilot.user_slot_acquire_failed_after_wait", zap.Error(err))
			h.handleConcurrencyError(c, err, "user", streamStarted)
			return
		}
	}

	if waitCounted {
		h.concurrencyHelper.DecrementWaitCount(c.Request.Context(), subject.UserID)
		waitCounted = false
	}
	userReleaseFunc = wrapReleaseOnDone(c.Request.Context(), userReleaseFunc)
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	// 2. Re-check billing eligibility after wait
	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription); err != nil {
		reqLog.Info("copilot.billing_eligibility_check_failed", zap.Error(err))
		status, code, message := billingErrorDetails(err)
		h.handleStreamingAwareError(c, status, code, message, streamStarted)
		return
	}

	// 3. Account scheduling loop with failover
	maxAccountSwitches := h.maxAccountSwitches
	switchCount := 0
	failedAccountIDs := make(map[int64]struct{})
	var lastFailoverErr *service.UpstreamFailoverError

	for {
		reqLog.Debug("copilot.account_selecting", zap.Int("excluded_account_count", len(failedAccountIDs)))
		selection, err := h.gatewayService.SelectAccountWithLoadAwareness(c.Request.Context(), apiKey.GroupID, "", reqModel, failedAccountIDs, "")
		if err != nil {
			reqLog.Warn("copilot.account_select_failed",
				zap.Error(err),
				zap.Int("excluded_account_count", len(failedAccountIDs)),
			)
			if len(failedAccountIDs) == 0 {
				h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "Service temporarily unavailable", streamStarted)
				return
			}
			if lastFailoverErr != nil {
				h.handleFailoverExhausted(c, lastFailoverErr, streamStarted)
			} else {
				h.handleStreamingAwareError(c, http.StatusBadGateway, "upstream_error", "All upstream accounts exhausted", streamStarted)
			}
			return
		}
		account := selection.Account
		reqLog.Debug("copilot.account_selected", zap.Int64("account_id", account.ID), zap.String("account_name", account.Name))
		setOpsSelectedAccount(c, account.ID, account.Platform)

		// 4. Acquire account concurrency slot
		accountReleaseFunc := selection.ReleaseFunc
		if !selection.Acquired {
			if selection.WaitPlan == nil {
				h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "No available accounts", streamStarted)
				return
			}

			fastReleaseFunc, fastAcquired, err := h.concurrencyHelper.TryAcquireAccountSlot(
				c.Request.Context(),
				account.ID,
				selection.WaitPlan.MaxConcurrency,
			)
			if err != nil {
				reqLog.Warn("copilot.account_slot_quick_acquire_failed", zap.Int64("account_id", account.ID), zap.Error(err))
				h.handleConcurrencyError(c, err, "account", streamStarted)
				return
			}
			if fastAcquired {
				accountReleaseFunc = fastReleaseFunc
			} else {
				accountWaitCounted := false
				canWait, err := h.concurrencyHelper.IncrementAccountWaitCount(c.Request.Context(), account.ID, selection.WaitPlan.MaxWaiting)
				if err != nil {
					reqLog.Warn("copilot.account_wait_counter_increment_failed", zap.Int64("account_id", account.ID), zap.Error(err))
				} else if !canWait {
					reqLog.Info("copilot.account_wait_queue_full",
						zap.Int64("account_id", account.ID),
						zap.Int("max_waiting", selection.WaitPlan.MaxWaiting),
					)
					h.handleStreamingAwareError(c, http.StatusTooManyRequests, "rate_limit_error", "Too many pending requests, please retry later", streamStarted)
					return
				}
				if err == nil && canWait {
					accountWaitCounted = true
				}
				releaseWait := func() {
					if accountWaitCounted {
						h.concurrencyHelper.DecrementAccountWaitCount(c.Request.Context(), account.ID)
						accountWaitCounted = false
					}
				}

				accountReleaseFunc, err = h.concurrencyHelper.AcquireAccountSlotWithWaitTimeout(
					c,
					account.ID,
					selection.WaitPlan.MaxConcurrency,
					selection.WaitPlan.Timeout,
					reqStream,
					&streamStarted,
				)
				if err != nil {
					reqLog.Warn("copilot.account_slot_acquire_failed", zap.Int64("account_id", account.ID), zap.Error(err))
					releaseWait()
					h.handleConcurrencyError(c, err, "account", streamStarted)
					return
				}
				releaseWait()
			}
		}
		accountReleaseFunc = wrapReleaseOnDone(c.Request.Context(), accountReleaseFunc)

		// 5. Forward request
		service.SetOpsLatencyMs(c, service.OpsRoutingLatencyMsKey, time.Since(routingStart).Milliseconds())
		forwardStart := time.Now()
		result, err := forwardFn(c.Request.Context(), c, account, body)
		forwardDurationMs := time.Since(forwardStart).Milliseconds()
		if accountReleaseFunc != nil {
			accountReleaseFunc()
		}
		service.SetOpsLatencyMs(c, service.OpsResponseLatencyMsKey, forwardDurationMs)
		if err == nil && result != nil && result.FirstTokenMs != nil {
			service.SetOpsLatencyMs(c, service.OpsTimeToFirstTokenMsKey, int64(*result.FirstTokenMs))
		}
		if err != nil {
			var failoverErr *service.UpstreamFailoverError
			if errors.As(err, &failoverErr) {
				failedAccountIDs[account.ID] = struct{}{}
				lastFailoverErr = failoverErr
				if switchCount >= maxAccountSwitches {
					h.handleFailoverExhausted(c, failoverErr, streamStarted)
					return
				}
				switchCount++
				reqLog.Warn("copilot.upstream_failover_switching",
					zap.Int64("account_id", account.ID),
					zap.Int("upstream_status", failoverErr.StatusCode),
					zap.Int("switch_count", switchCount),
					zap.Int("max_switches", maxAccountSwitches),
				)
				continue
			}
			wroteFallback := h.ensureForwardErrorResponse(c, streamStarted)
			reqLog.Error("copilot.forward_failed",
				zap.Int64("account_id", account.ID),
				zap.Bool("fallback_error_response_written", wroteFallback),
				zap.Error(err),
			)
			return
		}

		// 6. Record usage asynchronously
		userAgent := c.GetHeader("User-Agent")
		clientIP := ip.GetClientIP(c)

		h.submitUsageRecordTask(func(ctx context.Context) {
			forwardResult := copilotToForwardResult(result)
			if err := h.gatewayService.RecordUsage(ctx, &service.RecordUsageInput{
				Result:        forwardResult,
				APIKey:        apiKey,
				User:          apiKey.User,
				Account:       account,
				Subscription:  subscription,
				UserAgent:     userAgent,
				IPAddress:     clientIP,
				APIKeyService: h.apiKeyService,
			}); err != nil {
				logger.L().With(
					zap.String("component", "handler.copilot_gateway."+endpoint),
					zap.Int64("user_id", subject.UserID),
					zap.Int64("api_key_id", apiKey.ID),
					zap.Any("group_id", apiKey.GroupID),
					zap.String("model", reqModel),
					zap.Int64("account_id", account.ID),
				).Error("copilot.record_usage_failed", zap.Error(err))
			}
		})

		reqLog.Info("copilot.request_completed",
			zap.Int64("account_id", account.ID),
			zap.String("result_model", result.Model),
			zap.Int("prompt_tokens", result.Usage.PromptTokens),
			zap.Int("completion_tokens", result.Usage.CompletionTokens),
			zap.Duration("duration", result.Duration),
		)
		return
	}
}

// Models returns the list of available Copilot models by fetching from upstream.
// Falls back to DefaultCopilotModelMapping if no account is available.
// GET /copilot/v1/models
func (h *CopilotGatewayHandler) Models(c *gin.Context) {
	ctx := c.Request.Context()
	body, err := h.copilotGatewayService.FetchModelsFromUpstream(ctx)
	if err == nil {
		c.Data(http.StatusOK, "application/json", body)
		return
	}
	// Log and fall through to static list
	logger.FromContext(ctx).Warn("copilot.fetch_models_failed, falling back to static list",
		zap.Error(err))

	// Fallback: return static model list from DefaultCopilotModelMapping
	type modelEntry struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		OwnedBy string `json:"owned_by"`
	}

	models := make([]modelEntry, 0, len(domain.DefaultCopilotModelMapping))
	seen := make(map[string]struct{}, len(domain.DefaultCopilotModelMapping))
	for _, mapped := range domain.DefaultCopilotModelMapping {
		if _, ok := seen[mapped]; ok {
			continue
		}
		seen[mapped] = struct{}{}
		models = append(models, modelEntry{
			ID:      mapped,
			Object:  "model",
			OwnedBy: "github-copilot",
		})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })

	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   models,
	})
}

// copilotToForwardResult converts a CopilotForwardResult to a ForwardResult
// for unified usage recording via GatewayService.RecordUsage.
func copilotToForwardResult(r *service.CopilotForwardResult) *service.ForwardResult {
	return &service.ForwardResult{
		RequestID: r.RequestID,
		Usage: service.ClaudeUsage{
			InputTokens:  r.Usage.PromptTokens,
			OutputTokens: r.Usage.CompletionTokens,
		},
		Model:        r.Model,
		Stream:       r.Stream,
		Duration:     r.Duration,
		FirstTokenMs: r.FirstTokenMs,
	}
}

func (h *CopilotGatewayHandler) submitUsageRecordTask(task service.UsageRecordTask) {
	if task == nil {
		return
	}
	if h.usageRecordWorkerPool != nil {
		h.usageRecordWorkerPool.Submit(task)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	task(ctx)
}

func (h *CopilotGatewayHandler) handleConcurrencyError(c *gin.Context, err error, slotType string, streamStarted bool) {
	h.handleStreamingAwareError(c, http.StatusTooManyRequests, "rate_limit_error",
		fmt.Sprintf("Concurrency limit exceeded for %s, please retry later", slotType), streamStarted)
}

func (h *CopilotGatewayHandler) handleFailoverExhausted(c *gin.Context, failoverErr *service.UpstreamFailoverError, streamStarted bool) {
	if status, errType, errMsg, matched := applyFailoverPassthroughRule(c, h.errorPassthroughService, "copilot", failoverErr); matched {
		h.handleStreamingAwareError(c, status, errType, errMsg, streamStarted)
		return
	}
	status, errType, errMsg := mapCopilotUpstreamError(failoverErr.StatusCode)
	h.handleStreamingAwareError(c, status, errType, errMsg, streamStarted)
}

func mapCopilotUpstreamError(statusCode int) (int, string, string) {
	switch statusCode {
	case 401:
		return http.StatusBadGateway, "upstream_error", "Upstream authentication failed, please contact administrator"
	case 403:
		return http.StatusBadGateway, "upstream_error", "Upstream access forbidden, please contact administrator"
	case 429:
		return http.StatusTooManyRequests, "rate_limit_error", "Upstream rate limit exceeded, please retry later"
	case 500, 502, 503, 504:
		return http.StatusBadGateway, "upstream_error", "Upstream service temporarily unavailable"
	default:
		return http.StatusBadGateway, "upstream_error", "Upstream request failed"
	}
}

func (h *CopilotGatewayHandler) handleStreamingAwareError(c *gin.Context, status int, errType, message string, streamStarted bool) {
	if streamStarted {
		if flusher, ok := c.Writer.(http.Flusher); ok {
			errorEvent := fmt.Sprintf("data: {\"error\":{\"type\":%q,\"message\":%q}}\n\n", errType, message)
			if _, err := fmt.Fprint(c.Writer, errorEvent); err != nil {
				_ = c.Error(err)
			}
			flusher.Flush()
		}
		return
	}
	h.errorResponse(c, status, errType, message)
}

func (h *CopilotGatewayHandler) ensureForwardErrorResponse(c *gin.Context, streamStarted bool) bool {
	if c == nil || c.Writer == nil || c.Writer.Written() {
		return false
	}
	h.handleStreamingAwareError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed", streamStarted)
	return true
}

func (h *CopilotGatewayHandler) errorResponse(c *gin.Context, status int, errType, message string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
}
