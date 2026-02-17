package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// OpenAIGatewayHandler handles OpenAI API gateway requests
type OpenAIGatewayHandler struct {
	openaiGatewayService    *service.OpenAIGatewayService
	claudeGatewayService    *service.GatewayService
	geminiCompatService     *service.GeminiMessagesCompatService
	billingCacheService     *service.BillingCacheService
	apiKeyService           *service.APIKeyService
	errorPassthroughService *service.ErrorPassthroughService
	concurrencyHelper       *ConcurrencyHelper
	maxAccountSwitches      int
}

// NewOpenAIGatewayHandler creates a new OpenAIGatewayHandler
func NewOpenAIGatewayHandler(
	openaiGatewayService *service.OpenAIGatewayService,
	claudeGatewayService *service.GatewayService,
	geminiCompatService *service.GeminiMessagesCompatService,
	concurrencyService *service.ConcurrencyService,
	billingCacheService *service.BillingCacheService,
	apiKeyService *service.APIKeyService,
	errorPassthroughService *service.ErrorPassthroughService,
	cfg *config.Config,
) *OpenAIGatewayHandler {
	pingInterval := time.Duration(0)
	maxAccountSwitches := 3
	if cfg != nil {
		pingInterval = time.Duration(cfg.Concurrency.PingInterval) * time.Second
		if cfg.Gateway.MaxAccountSwitches > 0 {
			maxAccountSwitches = cfg.Gateway.MaxAccountSwitches
		}
	}
	return &OpenAIGatewayHandler{
		openaiGatewayService:    openaiGatewayService,
		claudeGatewayService:    claudeGatewayService,
		geminiCompatService:     geminiCompatService,
		billingCacheService:     billingCacheService,
		apiKeyService:           apiKeyService,
		errorPassthroughService: errorPassthroughService,
		concurrencyHelper:       NewConcurrencyHelper(concurrencyService, SSEPingFormatComment, pingInterval),
		maxAccountSwitches:      maxAccountSwitches,
	}
}

func (h *OpenAIGatewayHandler) resolveEffectiveAPIKey(c *gin.Context, apiKey *service.APIKey, requestedModel string) (*service.APIKey, error) {
	if apiKey.GroupID != nil && apiKey.Group != nil {
		return apiKey, nil
	}

	allowedGroups := []int64{}
	if apiKey.User != nil {
		allowedGroups = apiKey.User.AllowedGroups
		if len(allowedGroups) == 0 && h.claudeGatewayService != nil {
			if ids, err := h.claudeGatewayService.LoadUserAllowedGroupIDs(c.Request.Context(), apiKey.User.ID); err == nil {
				allowedGroups = ids
				apiKey.User.AllowedGroups = ids
			}
		}
	}

	group, err := h.claudeGatewayService.ResolveGroupFromUserPermission(c.Request.Context(), allowedGroups, requestedModel)
	if err != nil {
		return nil, err
	}

	cloned := *apiKey
	groupID := group.ID
	cloned.GroupID = &groupID
	cloned.Group = group
	return &cloned, nil
}

// Responses handles OpenAI Responses API endpoint
// POST /openai/v1/responses
func (h *OpenAIGatewayHandler) Responses(c *gin.Context) {
	// Get apiKey and user from context (set by ApiKeyAuth middleware)
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}

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

	setOpsRequestContext(c, "", false, body)

	// Parse request body to map for potential modification
	var reqBody map[string]any
	if err := json.Unmarshal(body, &reqBody); err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}

	// Extract model and stream
	reqModel, _ := reqBody["model"].(string)
	reqStream, _ := reqBody["stream"].(bool)

	// 验证 model 必填
	if reqModel == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}

	requestedModelForClient := strings.TrimSpace(reqModel)
	if ns := service.ParseModelNamespace(reqModel); ns.HasNamespace {
		reqModel = ns.Model
		reqBody["model"] = reqModel
		body, err = json.Marshal(reqBody)
		if err != nil {
			h.errorResponse(c, http.StatusInternalServerError, "api_error", "Failed to process request")
			return
		}
		if ns.Platform != "" && !middleware2.HasForcePlatform(c) {
			ctx := context.WithValue(c.Request.Context(), ctxkey.ForcePlatform, ns.Platform)
			c.Request = c.Request.WithContext(ctx)
			c.Set(string(middleware2.ContextKeyForcePlatform), ns.Platform)
		}
	}

	userAgent := c.GetHeader("User-Agent")
	if !openai.IsCodexCLIRequest(userAgent) {
		existingInstructions, _ := reqBody["instructions"].(string)
		if strings.TrimSpace(existingInstructions) == "" {
			if instructions := strings.TrimSpace(service.GetOpenCodeInstructions()); instructions != "" {
				reqBody["instructions"] = instructions
				// Re-serialize body
				body, err = json.Marshal(reqBody)
				if err != nil {
					h.errorResponse(c, http.StatusInternalServerError, "api_error", "Failed to process request")
					return
				}
			}
		}
	}

	setOpsRequestContext(c, reqModel, reqStream, body)

	// 提前校验 function_call_output 是否具备可关联上下文，避免上游 400。
	// 要求 previous_response_id，或 input 内存在带 call_id 的 tool_call/function_call，
	// 或带 id 且与 call_id 匹配的 item_reference。
	if service.HasFunctionCallOutput(reqBody) {
		previousResponseID, _ := reqBody["previous_response_id"].(string)
		if strings.TrimSpace(previousResponseID) == "" && !service.HasToolCallContext(reqBody) {
			if service.HasFunctionCallOutputMissingCallID(reqBody) {
				log.Printf("[OpenAI Handler] function_call_output 缺少 call_id: model=%s", reqModel)
				h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "function_call_output requires call_id or previous_response_id; if relying on history, ensure store=true and reuse previous_response_id")
				return
			}
			callIDs := service.FunctionCallOutputCallIDs(reqBody)
			if !service.HasItemReferenceForCallIDs(reqBody, callIDs) {
				log.Printf("[OpenAI Handler] function_call_output 缺少匹配的 item_reference: model=%s", reqModel)
				h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "function_call_output requires item_reference ids matching each call_id, or previous_response_id/tool_call context; if relying on history, ensure store=true and reuse previous_response_id")
				return
			}
		}
	}

	// Track if we've started streaming (for error handling)
	streamStarted := false

	// 绑定错误透传服务，允许 service 层在非 failover 错误场景复用规则。
	if h.errorPassthroughService != nil {
		service.BindErrorPassthroughService(c, h.errorPassthroughService)
	}

	// Get subscription info (may be nil)
	subscription, _ := middleware2.GetSubscriptionFromContext(c)

	effectiveAPIKey, err := h.resolveEffectiveAPIKey(c, apiKey, reqModel)
	if err != nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "No accessible groups: "+err.Error())
		return
	}
	apiKey = effectiveAPIKey

	// 0. Check if wait queue is full
	maxWait := service.CalculateMaxWait(subject.Concurrency)
	canWait, err := h.concurrencyHelper.IncrementWaitCount(c.Request.Context(), subject.UserID, maxWait)
	waitCounted := false
	if err != nil {
		log.Printf("Increment wait count failed: %v", err)
		// On error, allow request to proceed
	} else if !canWait {
		h.errorResponse(c, http.StatusTooManyRequests, "rate_limit_error", "Too many pending requests, please retry later")
		return
	}
	if err == nil && canWait {
		waitCounted = true
	}
	defer func() {
		if waitCounted {
			h.concurrencyHelper.DecrementWaitCount(c.Request.Context(), subject.UserID)
		}
	}()

	// 1. First acquire user concurrency slot
	userReleaseFunc, err := h.concurrencyHelper.AcquireUserSlotWithWait(c, subject.UserID, subject.Concurrency, reqStream, &streamStarted)
	if err != nil {
		log.Printf("User concurrency acquire failed: %v", err)
		h.handleConcurrencyError(c, err, "user", streamStarted)
		return
	}
	// User slot acquired: no longer waiting.
	if waitCounted {
		h.concurrencyHelper.DecrementWaitCount(c.Request.Context(), subject.UserID)
		waitCounted = false
	}
	// 确保请求取消时也会释放槽位，避免长连接被动中断造成泄漏
	userReleaseFunc = wrapReleaseOnDone(c.Request.Context(), userReleaseFunc)
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	// 2. Re-check billing eligibility after wait
	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription); err != nil {
		log.Printf("Billing eligibility check failed after wait: %v", err)
		status, code, message := billingErrorDetails(err)
		h.handleStreamingAwareError(c, status, code, message, streamStarted)
		return
	}

	// Generate session hash (header first; fallback to prompt_cache_key)
	sessionHash := h.openaiGatewayService.GenerateSessionHash(c, reqBody)

	targetPlatform := ""
	if fp, ok := middleware2.GetForcePlatformFromContext(c); ok {
		targetPlatform = strings.TrimSpace(fp)
	}
	if targetPlatform == "" && apiKey.Group != nil {
		targetPlatform = strings.TrimSpace(apiKey.Group.Platform)
	}
	if strings.EqualFold(targetPlatform, "claude") {
		targetPlatform = service.PlatformAnthropic
	}
	if targetPlatform == service.PlatformAnthropic || targetPlatform == service.PlatformGemini {
		h.handleCrossPlatformResponses(c, apiKey, subscription, reqBody, body, reqModel, requestedModelForClient, reqStream, sessionHash, &streamStarted)
		return
	}

	openaiPlatform := service.PlatformOpenAI
	if targetPlatform == service.PlatformCopilot || targetPlatform == service.PlatformAggregator {
		openaiPlatform = targetPlatform
	}

	maxAccountSwitches := h.maxAccountSwitches
	switchCount := 0
	failedAccountIDs := make(map[int64]struct{})
	var lastFailoverErr *service.UpstreamFailoverError

	for {
		// Select account supporting the requested model
		log.Printf("[OpenAI Handler] Selecting account: groupID=%v model=%s", apiKey.GroupID, reqModel)
		selection, err := h.openaiGatewayService.SelectAccountWithLoadAwarenessForPlatform(c.Request.Context(), apiKey.GroupID, openaiPlatform, sessionHash, reqModel, failedAccountIDs)
		if err != nil {
			log.Printf("[OpenAI Handler] SelectAccount failed: %v", err)
			if len(failedAccountIDs) == 0 {
				h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "No available accounts: "+err.Error(), streamStarted)
				return
			}
			if lastFailoverErr != nil {
				h.handleFailoverExhausted(c, lastFailoverErr, streamStarted)
			} else {
				h.handleFailoverExhaustedSimple(c, 502, streamStarted)
			}
			return
		}
		account := selection.Account
		log.Printf("[OpenAI Handler] Selected account: id=%d name=%s", account.ID, account.Name)
		setOpsSelectedAccount(c, account.ID)

		// 3. Acquire account concurrency slot
		accountReleaseFunc := selection.ReleaseFunc
		if !selection.Acquired {
			if selection.WaitPlan == nil {
				h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "No available accounts", streamStarted)
				return
			}
			accountWaitCounted := false
			canWait, err := h.concurrencyHelper.IncrementAccountWaitCount(c.Request.Context(), account.ID, selection.WaitPlan.MaxWaiting)
			if err != nil {
				log.Printf("Increment account wait count failed: %v", err)
			} else if !canWait {
				log.Printf("Account wait queue full: account=%d", account.ID)
				h.handleStreamingAwareError(c, http.StatusTooManyRequests, "rate_limit_error", "Too many pending requests, please retry later", streamStarted)
				return
			}
			if err == nil && canWait {
				accountWaitCounted = true
			}
			defer func() {
				if accountWaitCounted {
					h.concurrencyHelper.DecrementAccountWaitCount(c.Request.Context(), account.ID)
				}
			}()

			accountReleaseFunc, err = h.concurrencyHelper.AcquireAccountSlotWithWaitTimeout(
				c,
				account.ID,
				selection.WaitPlan.MaxConcurrency,
				selection.WaitPlan.Timeout,
				reqStream,
				&streamStarted,
			)
			if err != nil {
				log.Printf("Account concurrency acquire failed: %v", err)
				h.handleConcurrencyError(c, err, "account", streamStarted)
				return
			}
			if accountWaitCounted {
				h.concurrencyHelper.DecrementAccountWaitCount(c.Request.Context(), account.ID)
				accountWaitCounted = false
			}
			if err := h.openaiGatewayService.BindStickySessionForPlatform(c.Request.Context(), apiKey.GroupID, openaiPlatform, sessionHash, account.ID); err != nil {
				log.Printf("Bind sticky session failed: %v", err)
			}
		}
		// 账号槽位/等待计数需要在超时或断开时安全回收
		accountReleaseFunc = wrapReleaseOnDone(c.Request.Context(), accountReleaseFunc)

		// Forward request
		result, err := h.openaiGatewayService.Forward(c.Request.Context(), c, account, body)
		if accountReleaseFunc != nil {
			accountReleaseFunc()
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
				log.Printf("Account %d: upstream error %d, switching account %d/%d", account.ID, failoverErr.StatusCode, switchCount, maxAccountSwitches)
				continue
			}
			// Error response already handled in Forward, just log
			log.Printf("Account %d: Forward request failed: %v", account.ID, err)
			return
		}

		// 捕获请求信息（用于异步记录，避免在 goroutine 中访问 gin.Context）
		userAgent := c.GetHeader("User-Agent")
		clientIP := ip.GetClientIP(c)

		// Async record usage
		go func(result *service.OpenAIForwardResult, usedAccount *service.Account, ua, ip string) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := h.openaiGatewayService.RecordUsage(ctx, &service.OpenAIRecordUsageInput{
				Result:        result,
				APIKey:        apiKey,
				User:          apiKey.User,
				Account:       usedAccount,
				Subscription:  subscription,
				UserAgent:     ua,
				IPAddress:     ip,
				APIKeyService: h.apiKeyService,
			}); err != nil {
				log.Printf("Record usage failed: %v", err)
			}
		}(result, account, userAgent, clientIP)
		return
	}
}

func (h *OpenAIGatewayHandler) handleCrossPlatformResponses(
	c *gin.Context,
	apiKey *service.APIKey,
	subscription *service.UserSubscription,
	reqBody map[string]any,
	body []byte,
	reqModel string,
	requestedModelForClient string,
	reqStream bool,
	sessionHash string,
	streamStarted *bool,
) {
	if h.claudeGatewayService == nil {
		h.handleStreamingAwareError(c, http.StatusInternalServerError, "api_error", "Gateway service not configured", derefBool(streamStarted))
		return
	}
	if apiKey == nil {
		h.handleStreamingAwareError(c, http.StatusUnauthorized, "authentication_error", "Invalid API key", derefBool(streamStarted))
		return
	}

	platform := ""
	if fp, ok := middleware2.GetForcePlatformFromContext(c); ok {
		platform = strings.TrimSpace(fp)
	}
	if platform == "" && apiKey.Group != nil {
		platform = strings.TrimSpace(apiKey.Group.Platform)
	}
	if strings.EqualFold(platform, "claude") {
		platform = service.PlatformAnthropic
	}

	sessionKey := sessionHash
	if platform == service.PlatformGemini && strings.TrimSpace(sessionHash) != "" {
		sessionKey = "gemini:" + sessionHash
	}

	maxAccountSwitches := h.maxAccountSwitches
	switchCount := 0
	failedAccountIDs := make(map[int64]struct{})
	var lastFailoverErr *service.UpstreamFailoverError

	for {
		selection, err := h.claudeGatewayService.SelectAccountWithLoadAwareness(c.Request.Context(), apiKey.GroupID, sessionKey, reqModel, failedAccountIDs, "")
		if err != nil {
			if len(failedAccountIDs) == 0 {
				h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "No available accounts: "+err.Error(), derefBool(streamStarted))
				return
			}
			if lastFailoverErr != nil {
				h.handleFailoverExhausted(c, lastFailoverErr, derefBool(streamStarted))
			} else {
				h.handleFailoverExhaustedSimple(c, 502, derefBool(streamStarted))
			}
			return
		}
		account := selection.Account
		setOpsSelectedAccount(c, account.ID)

		accountReleaseFunc := selection.ReleaseFunc
		if !selection.Acquired {
			if selection.WaitPlan == nil {
				h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "No available accounts", derefBool(streamStarted))
				return
			}
			accountWaitCounted := false
			canWait, err := h.concurrencyHelper.IncrementAccountWaitCount(c.Request.Context(), account.ID, selection.WaitPlan.MaxWaiting)
			if err != nil {
				log.Printf("Increment account wait count failed: %v", err)
			} else if !canWait {
				log.Printf("Account wait queue full: account=%d", account.ID)
				h.handleStreamingAwareError(c, http.StatusTooManyRequests, "rate_limit_error", "Too many pending requests, please retry later", derefBool(streamStarted))
				return
			}
			if err == nil && canWait {
				accountWaitCounted = true
			}
			defer func() {
				if accountWaitCounted {
					h.concurrencyHelper.DecrementAccountWaitCount(c.Request.Context(), account.ID)
				}
			}()

			accountReleaseFunc, err = h.concurrencyHelper.AcquireAccountSlotWithWaitTimeout(
				c,
				account.ID,
				selection.WaitPlan.MaxConcurrency,
				selection.WaitPlan.Timeout,
				reqStream,
				streamStarted,
			)
			if err != nil {
				log.Printf("Account concurrency acquire failed: %v", err)
				h.handleConcurrencyError(c, err, "account", derefBool(streamStarted))
				return
			}
			if accountWaitCounted {
				h.concurrencyHelper.DecrementAccountWaitCount(c.Request.Context(), account.ID)
				accountWaitCounted = false
			}
			if err := h.claudeGatewayService.BindStickySession(c.Request.Context(), apiKey.GroupID, sessionKey, account.ID); err != nil {
				log.Printf("Bind sticky session failed: %v", err)
			}
		}
		accountReleaseFunc = wrapReleaseOnDone(c.Request.Context(), accountReleaseFunc)

		result, err := h.forwardCrossPlatformResponses(c.Request.Context(), c, account, reqBody, body, requestedModelForClient, reqStream, streamStarted)
		if accountReleaseFunc != nil {
			accountReleaseFunc()
		}
		if err != nil {
			var failoverErr *service.UpstreamFailoverError
			if errors.As(err, &failoverErr) {
				failedAccountIDs[account.ID] = struct{}{}
				lastFailoverErr = failoverErr
				if switchCount >= maxAccountSwitches {
					h.handleFailoverExhausted(c, failoverErr, derefBool(streamStarted))
					return
				}
				switchCount++
				continue
			}
			log.Printf("Account %d: Forward request failed: %v", account.ID, err)
			return
		}

		ua := c.GetHeader("User-Agent")
		clientIP := ip.GetClientIP(c)
		go func(result *service.ForwardResult, usedAccount *service.Account, ua, ipAddr string) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := h.claudeGatewayService.RecordUsage(ctx, &service.RecordUsageInput{
				Result:        result,
				APIKey:        apiKey,
				User:          apiKey.User,
				Account:       usedAccount,
				Subscription:  subscription,
				UserAgent:     ua,
				IPAddress:     ipAddr,
				APIKeyService: h.apiKeyService,
			}); err != nil {
				log.Printf("Record usage failed: %v", err)
			}
		}(result, account, ua, clientIP)
		return
	}
}

func (h *OpenAIGatewayHandler) forwardCrossPlatformResponses(
	ctx context.Context,
	c *gin.Context,
	account *service.Account,
	openaiReq map[string]any,
	openaiBody []byte,
	requestedModelForClient string,
	reqStream bool,
	streamStarted *bool,
) (*service.ForwardResult, error) {
	claudeReq, convErr := service.ConvertOpenAIResponsesRequestToClaudeMessages(openaiReq)
	if convErr != nil {
		h.writeOpenAIResponsesError(c, reqStream, streamStarted, "invalid_request_error", convErr.Error())
		return nil, convErr
	}
	claudeReq["stream"] = false
	claudeBody, err := json.Marshal(claudeReq)
	if err != nil {
		h.writeOpenAIResponsesError(c, reqStream, streamStarted, "api_error", "Failed to process request")
		return nil, err
	}
	if c != nil {
		c.Set(service.OpsUpstreamRequestBodyKey, string(openaiBody))
	}

	origWriter := c.Writer
	cw := newCaptureWriter(origWriter)
	c.Writer = cw
	defer func() { c.Writer = origWriter }()

	var result *service.ForwardResult
	if account.Platform == service.PlatformGemini {
		if h.geminiCompatService == nil {
			h.writeOpenAIResponsesError(c, reqStream, streamStarted, "api_error", "Gemini compat service not configured")
			return nil, errors.New("gemini compat service not configured")
		}
		result, err = h.geminiCompatService.Forward(ctx, c, account, claudeBody)
	} else {
		parsed, perr := service.ParseGatewayRequest(claudeBody, domain.PlatformAnthropic)
		if perr != nil {
			h.writeOpenAIResponsesError(c, reqStream, streamStarted, "invalid_request_error", "Failed to parse request")
			return nil, perr
		}
		result, err = h.claudeGatewayService.Forward(ctx, c, account, parsed)
	}

	c.Writer = origWriter

	if err != nil {
		var failoverErr *service.UpstreamFailoverError
		if errors.As(err, &failoverErr) {
			return nil, failoverErr
		}
		status := cw.Status()
		errType := "upstream_error"
		if status == http.StatusBadRequest {
			errType = "invalid_request_error"
		}
		msg := extractClaudeErrorMessage(cw.buf.Bytes())
		if strings.TrimSpace(msg) == "" {
			msg = "Upstream request failed"
		}
		h.writeOpenAIResponsesError(c, reqStream, streamStarted, errType, msg)
		return nil, err
	}

	var claudeResp map[string]any
	if err := json.Unmarshal(cw.buf.Bytes(), &claudeResp); err != nil {
		h.writeOpenAIResponsesError(c, reqStream, streamStarted, "upstream_error", "Failed to parse upstream response")
		return nil, err
	}

	if strings.TrimSpace(requestedModelForClient) == "" {
		requestedModelForClient, _ = openaiReq["model"].(string)
	}

	openaiResp, err := service.ConvertClaudeMessageToOpenAIResponsesResponse(claudeResp, &result.Usage, requestedModelForClient, "")
	if err != nil {
		h.writeOpenAIResponsesError(c, reqStream, streamStarted, "upstream_error", "Failed to convert upstream response")
		return nil, err
	}

	if rid := strings.TrimSpace(cw.header.Get("x-request-id")); rid != "" {
		c.Header("x-request-id", rid)
	}

	if !reqStream {
		c.JSON(http.StatusOK, openaiResp)
		return result, nil
	}

	if streamStarted != nil {
		*streamStarted = true
	}
	writeOpenAIResponsesSSE(c, openaiResp)
	return result, nil
}

func extractClaudeErrorMessage(body []byte) string {
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	if errObj, ok := parsed["error"].(map[string]any); ok {
		if msg, ok := errObj["message"].(string); ok {
			return strings.TrimSpace(msg)
		}
	}
	return ""
}

func writeOpenAIResponsesSSE(c *gin.Context, resp map[string]any) {
	if c == nil {
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	w := c.Writer
	flusher, _ := w.(http.Flusher)
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}

	responseID, _ := resp["id"].(string)
	if strings.TrimSpace(responseID) == "" {
		responseID = "resp_" + randomHex(12)
	}

	writeEvent := func(eventType string, payload map[string]any) {
		b, err := json.Marshal(payload)
		if err != nil {
			return
		}
		_, _ = fmt.Fprintf(w, "event: %s\n", eventType)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
		flush()
	}

	writeEvent("response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": responseID}})

	if output, ok := resp["output"].([]any); ok {
		for _, item := range output {
			writeEvent("response.output_item.done", map[string]any{"type": "response.output_item.done", "item": item})
		}
	}

	completed := map[string]any{"id": responseID}
	if usage := resp["usage"]; usage != nil {
		completed["usage"] = usage
	}
	writeEvent("response.completed", map[string]any{"type": "response.completed", "response": completed})
}

func (h *OpenAIGatewayHandler) writeOpenAIResponsesError(c *gin.Context, stream bool, streamStarted *bool, errType string, message string) {
	if stream {
		if streamStarted != nil {
			*streamStarted = true
		}
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		payload := map[string]any{
			"type": "response.failed",
			"response": map[string]any{
				"id": "resp_" + randomHex(12),
				"error": map[string]any{
					"code":    errType,
					"message": message,
				},
			},
		}
		b, _ := json.Marshal(payload)
		_, _ = fmt.Fprintf(c.Writer, "event: response.failed\n")
		_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", b)
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
		return
	}
	h.errorResponse(c, http.StatusBadGateway, errType, message)
}

func derefBool(v *bool) bool {
	if v == nil {
		return false
	}
	return *v
}

func randomHex(nBytes int) string {
	if nBytes <= 0 {
		return ""
	}
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// handleConcurrencyError handles concurrency-related errors with proper 429 response
func (h *OpenAIGatewayHandler) handleConcurrencyError(c *gin.Context, err error, slotType string, streamStarted bool) {
	h.handleStreamingAwareError(c, http.StatusTooManyRequests, "rate_limit_error",
		fmt.Sprintf("Concurrency limit exceeded for %s, please retry later", slotType), streamStarted)
}

func (h *OpenAIGatewayHandler) handleFailoverExhausted(c *gin.Context, failoverErr *service.UpstreamFailoverError, streamStarted bool) {
	statusCode := failoverErr.StatusCode
	responseBody := failoverErr.ResponseBody

	// 先检查透传规则
	if h.errorPassthroughService != nil && len(responseBody) > 0 {
		if rule := h.errorPassthroughService.MatchRule("openai", statusCode, responseBody); rule != nil {
			// 确定响应状态码
			respCode := statusCode
			if !rule.PassthroughCode && rule.ResponseCode != nil {
				respCode = *rule.ResponseCode
			}

			// 确定响应消息
			msg := service.ExtractUpstreamErrorMessage(responseBody)
			if !rule.PassthroughBody && rule.CustomMessage != nil {
				msg = *rule.CustomMessage
			}

			if rule.SkipMonitoring {
				c.Set(service.OpsSkipPassthroughKey, true)
			}

			h.handleStreamingAwareError(c, respCode, "upstream_error", msg, streamStarted)
			return
		}
	}

	// 使用默认的错误映射
	status, errType, errMsg := h.mapUpstreamError(statusCode)
	h.handleStreamingAwareError(c, status, errType, errMsg, streamStarted)
}

// handleFailoverExhaustedSimple 简化版本，用于没有响应体的情况
func (h *OpenAIGatewayHandler) handleFailoverExhaustedSimple(c *gin.Context, statusCode int, streamStarted bool) {
	status, errType, errMsg := h.mapUpstreamError(statusCode)
	h.handleStreamingAwareError(c, status, errType, errMsg, streamStarted)
}

func (h *OpenAIGatewayHandler) mapUpstreamError(statusCode int) (int, string, string) {
	switch statusCode {
	case 401:
		return http.StatusBadGateway, "upstream_error", "Upstream authentication failed, please contact administrator"
	case 403:
		return http.StatusBadGateway, "upstream_error", "Upstream access forbidden, please contact administrator"
	case 429:
		return http.StatusTooManyRequests, "rate_limit_error", "Upstream rate limit exceeded, please retry later"
	case 529:
		return http.StatusServiceUnavailable, "upstream_error", "Upstream service overloaded, please retry later"
	case 500, 502, 503, 504:
		return http.StatusBadGateway, "upstream_error", "Upstream service temporarily unavailable"
	default:
		return http.StatusBadGateway, "upstream_error", "Upstream request failed"
	}
}

// handleStreamingAwareError handles errors that may occur after streaming has started
func (h *OpenAIGatewayHandler) handleStreamingAwareError(c *gin.Context, status int, errType, message string, streamStarted bool) {
	if streamStarted {
		// Stream already started, send error as SSE event then close
		flusher, ok := c.Writer.(http.Flusher)
		if ok {
			// Send error event in OpenAI SSE format
			errorEvent := fmt.Sprintf(`event: error`+"\n"+`data: {"error": {"type": "%s", "message": "%s"}}`+"\n\n", errType, message)
			if _, err := fmt.Fprint(c.Writer, errorEvent); err != nil {
				_ = c.Error(err)
			}
			flusher.Flush()
		}
		return
	}

	// Normal case: return JSON response with proper status code
	h.errorResponse(c, status, errType, message)
}

// errorResponse returns OpenAI API format error response
func (h *OpenAIGatewayHandler) errorResponse(c *gin.Context, status int, errType, message string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
}
