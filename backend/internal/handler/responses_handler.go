package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// ResponsesHandler handles Copilot /responses endpoint requests with full
// Stream ID synchronization support. Unlike the basic passthrough in
// CopilotGatewayHandler.Responses, this handler uses ProcessResponsesStream
// to ensure consistent item IDs across the stream lifecycle, which is required
// by @ai-sdk/openai clients (e.g., OpenCode with Codex models).
type ResponsesHandler struct {
	gatewayService          *service.GatewayService
	copilotGatewayService   *service.CopilotGatewayService
	billingCacheService     *service.BillingCacheService
	apiKeyService           *service.APIKeyService
	usageRecordWorkerPool   *service.UsageRecordWorkerPool
	errorPassthroughService *service.ErrorPassthroughService
	concurrencyHelper       *ConcurrencyHelper
	maxAccountSwitches      int
	cfg                     *config.Config
}

// NewResponsesHandler creates a new ResponsesHandler.
func NewResponsesHandler(
	gatewayService *service.GatewayService,
	copilotGatewayService *service.CopilotGatewayService,
	concurrencyService *service.ConcurrencyService,
	billingCacheService *service.BillingCacheService,
	apiKeyService *service.APIKeyService,
	usageRecordWorkerPool *service.UsageRecordWorkerPool,
	errorPassthroughService *service.ErrorPassthroughService,
	cfg *config.Config,
) *ResponsesHandler {
	pingInterval := time.Duration(0)
	maxAccountSwitches := 3
	if cfg != nil {
		pingInterval = time.Duration(cfg.Concurrency.PingInterval) * time.Second
		if cfg.Gateway.MaxAccountSwitches > 0 {
			maxAccountSwitches = cfg.Gateway.MaxAccountSwitches
		}
	}
	return &ResponsesHandler{
		gatewayService:          gatewayService,
		copilotGatewayService:   copilotGatewayService,
		billingCacheService:     billingCacheService,
		apiKeyService:           apiKeyService,
		usageRecordWorkerPool:   usageRecordWorkerPool,
		errorPassthroughService: errorPassthroughService,
		concurrencyHelper:       NewConcurrencyHelper(concurrencyService, SSEPingFormatComment, pingInterval),
		maxAccountSwitches:      maxAccountSwitches,
		cfg:                     cfg,
	}
}

// HandleResponses handles a Responses API request by forwarding to the upstream
// Copilot /responses endpoint with Stream ID synchronization.
//
// For streaming responses, it uses ProcessResponsesStream to apply ID tracking
// that fixes the known GitHub Copilot bug where added/done events return
// different item IDs. For non-streaming responses, it delegates entirely to
// the copilot gateway service.
//
// POST /copilot/v1/responses
func (h *ResponsesHandler) HandleResponses(c *gin.Context) {
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
		"handler.responses",
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

	// Validate required model field
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
		reqLog.Warn("responses.user_slot_acquire_failed", zap.Error(err))
		h.handleConcurrencyError(c, err, "user", streamStarted)
		return
	}

	waitCounted := false
	if !userAcquired {
		maxWait := service.CalculateMaxWait(subject.Concurrency)
		canWait, waitErr := h.concurrencyHelper.IncrementWaitCount(c.Request.Context(), subject.UserID, maxWait)
		if waitErr != nil {
			reqLog.Warn("responses.user_wait_counter_increment_failed", zap.Error(waitErr))
		} else if !canWait {
			reqLog.Info("responses.user_wait_queue_full", zap.Int("max_wait", maxWait))
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
			reqLog.Warn("responses.user_slot_acquire_failed_after_wait", zap.Error(err))
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
		reqLog.Info("responses.billing_eligibility_check_failed", zap.Error(err))
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
		reqLog.Debug("responses.account_selecting", zap.Int("excluded_account_count", len(failedAccountIDs)))
		selection, err := h.gatewayService.SelectAccountWithLoadAwareness(c.Request.Context(), apiKey.GroupID, "", reqModel, failedAccountIDs, "")
		if err != nil {
			reqLog.Warn("responses.account_select_failed",
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
		reqLog.Debug("responses.account_selected", zap.Int64("account_id", account.ID), zap.String("account_name", account.Name))
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
				reqLog.Warn("responses.account_slot_quick_acquire_failed", zap.Int64("account_id", account.ID), zap.Error(err))
				h.handleConcurrencyError(c, err, "account", streamStarted)
				return
			}
			if fastAcquired {
				accountReleaseFunc = fastReleaseFunc
			} else {
				accountWaitCounted := false
				canWait, err := h.concurrencyHelper.IncrementAccountWaitCount(c.Request.Context(), account.ID, selection.WaitPlan.MaxWaiting)
				if err != nil {
					reqLog.Warn("responses.account_wait_counter_increment_failed", zap.Int64("account_id", account.ID), zap.Error(err))
				} else if !canWait {
					reqLog.Info("responses.account_wait_queue_full",
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
					reqLog.Warn("responses.account_slot_acquire_failed", zap.Int64("account_id", account.ID), zap.Error(err))
					releaseWait()
					h.handleConcurrencyError(c, err, "account", streamStarted)
					return
				}
				releaseWait()
			}
		}
		accountReleaseFunc = wrapReleaseOnDone(c.Request.Context(), accountReleaseFunc)

		// 5. Forward request to upstream
		service.SetOpsLatencyMs(c, service.OpsRoutingLatencyMsKey, time.Since(routingStart).Milliseconds())
		forwardStart := time.Now()
		result, err := h.forwardResponses(c, account, body, reqStream, forwardStart)
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
				reqLog.Warn("responses.upstream_failover_switching",
					zap.Int64("account_id", account.ID),
					zap.Int("upstream_status", failoverErr.StatusCode),
					zap.Int("switch_count", switchCount),
					zap.Int("max_switches", maxAccountSwitches),
				)
				continue
			}
			wroteFallback := h.ensureForwardErrorResponse(c, streamStarted)
			reqLog.Error("responses.forward_failed",
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
					zap.String("component", "handler.responses"),
					zap.Int64("user_id", subject.UserID),
					zap.Int64("api_key_id", apiKey.ID),
					zap.Any("group_id", apiKey.GroupID),
					zap.String("model", reqModel),
					zap.Int64("account_id", account.ID),
				).Error("responses.record_usage_failed", zap.Error(err))
			}
		})

		reqLog.Info("responses.request_completed",
			zap.Int64("account_id", account.ID),
			zap.String("result_model", result.Model),
			zap.Int("prompt_tokens", result.Usage.PromptTokens),
			zap.Int("completion_tokens", result.Usage.CompletionTokens),
			zap.Duration("duration", result.Duration),
		)
		return
	}
}

// forwardResponses sends the request to upstream and handles the response.
// For streaming, it uses ProcessResponsesStream for Stream ID synchronization.
// For non-streaming, it delegates entirely to the copilot gateway service.
func (h *ResponsesHandler) forwardResponses(c *gin.Context, account *service.Account, body []byte, isStream bool, start time.Time) (*service.CopilotForwardResult, error) {
	if !isStream {
		// Non-streaming: delegate entirely to the service which handles model
		// mapping, auth, upstream request, and response writing.
		return h.copilotGatewayService.ForwardResponses(c.Request.Context(), c, account, body)
	}

	// Streaming: get the raw HTTP response so we can process it with
	// ProcessResponsesStream for Stream ID synchronization.
	resp, model, err := h.copilotGatewayService.ForwardResponsesRaw(c.Request.Context(), account, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck

	// Handle upstream errors via the service's error handling pipeline which
	// checks passthrough rules and determines failover.
	if resp.StatusCode >= 400 {
		return h.copilotGatewayService.HandleResponsesError(c, resp, account, model, true, start)
	}

	return h.processResponsesStream(c, resp, model, start)
}

// processResponsesStream reads SSE events from the upstream response, applies
// Stream ID synchronization via ProcessResponsesStream, and forwards the
// processed events to the client.
//
// ProcessResponsesStream internally creates a StreamIdTracker that:
//   - Records canonical item IDs from response.output_item.added events
//   - Replaces mismatched IDs in response.output_item.done events
//   - Applies canonical IDs to item_id fields in delta events
//
// This fixes the known GitHub Copilot bug where added/done events return
// different item IDs, which breaks @ai-sdk/openai clients.
func (h *ResponsesHandler) processResponsesStream(c *gin.Context, resp *http.Response, model string, start time.Time) (*service.CopilotForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")

	// Set SSE response headers
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	if h.cfg != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, h.cfg.Security.ResponseHeaders)
	}
	c.Writer.WriteHeader(http.StatusOK)

	var firstTokenMs *int
	firstEvent := true

	// Process the upstream stream with Stream ID synchronization.
	usage, err := apicompat.ProcessResponsesStream(resp.Body, func(eventType string, data string) error {
		if firstEvent && eventType != "" {
			firstEvent = false
			ms := int(time.Since(start).Milliseconds())
			firstTokenMs = &ms
		}

		// Write the processed (ID-fixed) event to the client
		if eventType != "" {
			fmt.Fprint(c.Writer, apicompat.FormatSSEEvent(eventType, data))
		} else if data != "" {
			// Comments or non-typed events
			fmt.Fprint(c.Writer, apicompat.FormatSSEData(data))
		}
		c.Writer.Flush()
		return nil
	})

	if err != nil {
		logger.L().Warn("responses.stream_processing_error",
			zap.Error(err),
			zap.String("request_id", requestID),
		)
	}

	// Send [DONE] sentinel
	fmt.Fprint(c.Writer, apicompat.FormatSSEDone())
	c.Writer.Flush()

	// Build usage result
	var copilotUsage service.CopilotUsage
	if usage != nil {
		copilotUsage = service.CopilotUsage{
			PromptTokens:     usage.InputTokens,
			CompletionTokens: usage.OutputTokens,
			TotalTokens:      usage.TotalTokens,
		}
	}

	logger.L().Debug("responses.stream_completed",
		zap.String("request_id", requestID),
		zap.Int("input_tokens", copilotUsage.PromptTokens),
		zap.Int("output_tokens", copilotUsage.CompletionTokens),
	)

	return &service.CopilotForwardResult{
		RequestID:    requestID,
		Usage:        copilotUsage,
		Model:        model,
		Stream:       true,
		Duration:     time.Since(start),
		FirstTokenMs: firstTokenMs,
	}, nil
}

func (h *ResponsesHandler) submitUsageRecordTask(task service.UsageRecordTask) {
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

func (h *ResponsesHandler) handleConcurrencyError(c *gin.Context, err error, slotType string, streamStarted bool) {
	h.handleStreamingAwareError(c, http.StatusTooManyRequests, "rate_limit_error",
		fmt.Sprintf("Concurrency limit exceeded for %s, please retry later", slotType), streamStarted)
}

func (h *ResponsesHandler) handleFailoverExhausted(c *gin.Context, failoverErr *service.UpstreamFailoverError, streamStarted bool) {
	if status, errType, errMsg, matched := applyFailoverPassthroughRule(c, h.errorPassthroughService, "copilot", failoverErr); matched {
		h.handleStreamingAwareError(c, status, errType, errMsg, streamStarted)
		return
	}
	status, errType, errMsg := mapCopilotUpstreamError(failoverErr.StatusCode)
	h.handleStreamingAwareError(c, status, errType, errMsg, streamStarted)
}

func (h *ResponsesHandler) handleStreamingAwareError(c *gin.Context, status int, errType, message string, streamStarted bool) {
	if streamStarted {
		if flusher, ok := c.Writer.(http.Flusher); ok {
			errorData := map[string]any{
				"error": map[string]string{
					"type":    errType,
					"message": message,
				},
			}
			jsonBytes, err := json.Marshal(errorData)
			if err != nil {
				_ = c.Error(err)
				return
			}
			errorEvent := fmt.Sprintf("event: error\ndata: %s\n\n", string(jsonBytes))
			if _, err := fmt.Fprint(c.Writer, errorEvent); err != nil {
				_ = c.Error(err)
			}
			flusher.Flush()
		}
		return
	}
	h.errorResponse(c, status, errType, message)
}

func (h *ResponsesHandler) ensureForwardErrorResponse(c *gin.Context, streamStarted bool) bool {
	if c == nil || c.Writer == nil || c.Writer.Written() {
		return false
	}
	h.handleStreamingAwareError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed", streamStarted)
	return true
}

func (h *ResponsesHandler) errorResponse(c *gin.Context, status int, errType, message string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
}
