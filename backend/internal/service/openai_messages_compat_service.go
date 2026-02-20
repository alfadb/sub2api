package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type OpenAIMessagesCompatService struct {
	openai *OpenAIGatewayService
}

func NewOpenAIMessagesCompatService(openaiGateway *OpenAIGatewayService) *OpenAIMessagesCompatService {
	return &OpenAIMessagesCompatService{openai: openaiGateway}
}

func isResponsesAPIUnsupportedError(upstreamMsg string, upstreamBody []byte) bool {
	msg := strings.ToLower(strings.TrimSpace(upstreamMsg))
	if strings.Contains(msg, "does not support responses api") {
		return true
	}
	if strings.Contains(msg, "responses api") && strings.Contains(msg, "does not support") {
		return true
	}
	if strings.Contains(msg, "not supported via responses api") {
		return true
	}
	lowerBody := strings.ToLower(string(upstreamBody))
	if strings.Contains(lowerBody, "does not support responses api") {
		return true
	}
	if strings.Contains(lowerBody, "not supported via responses api") {
		return true
	}
	var parsed struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(upstreamBody, &parsed) == nil && parsed.Error.Code == "unsupported_api_for_model" {
		return true
	}
	return false
}

func (s *OpenAIMessagesCompatService) chatCompletionsURLForAccount(account *Account) (string, error) {
	if s == nil || s.openai == nil {
		return "", fmt.Errorf("openai gateway service not configured")
	}
	baseURL := strings.TrimSpace(account.GetCredential("base_url"))
	if baseURL == "" && account.Platform == PlatformCopilot {
		baseURL = "https://api.githubcopilot.com"
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	validatedURL, err := s.openai.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return "", err
	}
	return openaiChatCompletionsURLFromBaseURL(validatedURL, isGitHubCopilotAccount(account)), nil
}

func (s *OpenAIMessagesCompatService) forwardViaChatCompletions(ctx context.Context, c *gin.Context, account *Account, parsed *ParsedRequest, claudeReq map[string]any, originalModel string, mappedModel string, token string, startTime time.Time) (*ForwardResult, error) {
	openaiMessages, err := convertClaudeMessagesToOpenAIChatCompletionsMessages(parsed.Messages, parsed.System)
	if err != nil {
		writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}

	openaiReq := map[string]any{
		"model":    mappedModel,
		"stream":   false,
		"messages": openaiMessages,
	}
	if parsed.MaxTokens > 0 {
		openaiReq["max_tokens"] = parsed.MaxTokens
	}
	if tools := convertClaudeToolsToOpenAIChatTools(claudeReq["tools"]); len(tools) > 0 {
		openaiReq["tools"] = tools
		openaiReq["tool_choice"] = "auto"
	}
	if temp, ok := claudeReq["temperature"].(float64); ok {
		openaiReq["temperature"] = temp
	}
	if topP, ok := claudeReq["top_p"].(float64); ok {
		openaiReq["top_p"] = topP
	}
	if stopSeq, ok := claudeReq["stop_sequences"].([]any); ok && len(stopSeq) > 0 {
		openaiReq["stop"] = stopSeq
	}

	openaiBody, err := json.Marshal(openaiReq)
	if err != nil {
		writeClaudeError(c, http.StatusInternalServerError, "api_error", "Failed to process request")
		return nil, err
	}
	if c != nil {
		c.Set(OpsUpstreamRequestBodyKey, string(openaiBody))
	}

	targetURL, err := s.chatCompletionsURLForAccount(account)
	if err != nil {
		writeClaudeError(c, http.StatusBadGateway, "upstream_error", err.Error())
		return nil, err
	}

	upstreamReq, err := s.openai.buildUpstreamRequestWithTargetURL(ctx, c, account, openaiBody, token, false, "", false, isGitHubCopilotAccount(account), targetURL)
	if err != nil {
		writeClaudeError(c, http.StatusBadGateway, "upstream_error", err.Error())
		return nil, err
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	resp, err := s.openai.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		setOpsUpstreamError(c, 0, safeErr, "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: 0,
			Kind:               "request_error",
			Message:            safeErr,
		})
		writeClaudeError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
		return nil, fmt.Errorf("upstream request failed: %s", safeErr)
	}
	if resp == nil || resp.Body == nil {
		writeClaudeError(c, http.StatusBadGateway, "upstream_error", "Empty upstream response")
		return nil, fmt.Errorf("empty upstream response")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
		upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
		upstreamDetail := ""
		if s.openai.cfg != nil && s.openai.cfg.Gateway.LogUpstreamErrorBody {
			maxBytes := s.openai.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
			if maxBytes <= 0 {
				maxBytes = 2048
			}
			upstreamDetail = truncateString(string(respBody), maxBytes)
		}
		if upstreamDetail == "" && isGitHubCopilotAccount(account) {
			upstreamDetail = truncateString(string(respBody), 2048)
		}
		setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)

		if s.openai.shouldFailoverUpstreamError(resp.StatusCode) {
			if s.openai.rateLimitService != nil {
				s.openai.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody)
			}
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  strings.TrimSpace(resp.Header.Get("x-request-id")),
				Kind:               "failover",
				Message:            upstreamMsg,
				Detail:             upstreamDetail,
			})
			return nil, &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBody}
		}

		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  strings.TrimSpace(resp.Header.Get("x-request-id")),
			Kind:               "http_error",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
		})

		if status, errType, errMsg, matched := applyErrorPassthroughRule(
			c,
			account.Platform,
			resp.StatusCode,
			respBody,
			http.StatusBadGateway,
			"upstream_error",
			"Upstream request failed",
		); matched {
			writeClaudeError(c, status, errType, errMsg)
			if upstreamMsg == "" {
				upstreamMsg = errMsg
			}
			if upstreamMsg == "" {
				return nil, fmt.Errorf("upstream error: %d (passthrough rule matched)", resp.StatusCode)
			}
			return nil, fmt.Errorf("upstream error: %d (passthrough rule matched) message=%s", resp.StatusCode, upstreamMsg)
		}

		status, errType, errMsg := mapOpenAIUpstreamErrorToClaude(resp.StatusCode)
		writeClaudeError(c, status, errType, errMsg)
		if upstreamMsg == "" {
			return nil, fmt.Errorf("upstream error: %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream error: %d message=%s", resp.StatusCode, upstreamMsg)
	}

	upstreamBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		writeClaudeError(c, http.StatusBadGateway, "upstream_error", "Failed to read upstream response")
		return nil, readErr
	}

	claudeResp, usage, stopReason, convErr := convertOpenAIChatCompletionsJSONToClaude(upstreamBody, originalModel)
	if convErr != nil {
		writeClaudeError(c, http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
		return nil, convErr
	}

	if reqID := strings.TrimSpace(resp.Header.Get("x-request-id")); reqID != "" {
		c.Header("x-request-id", reqID)
	}

	if !parsed.Stream {
		c.JSON(http.StatusOK, claudeResp)
		return &ForwardResult{
			RequestID: resp.Header.Get("x-request-id"),
			Usage:     *usage,
			Model:     originalModel,
			Stream:    false,
			Duration:  time.Since(startTime),
		}, nil
	}

	if err := writeClaudeStreamFromMessage(c, claudeResp, usage, originalModel, stopReason); err != nil {
		return nil, err
	}

	return &ForwardResult{
		RequestID: resp.Header.Get("x-request-id"),
		Usage:     *usage,
		Model:     originalModel,
		Stream:    true,
		Duration:  time.Since(startTime),
	}, nil
}

func (s *OpenAIMessagesCompatService) forwardCountTokensViaChatCompletions(ctx context.Context, c *gin.Context, account *Account, parsed *ParsedRequest, claudeReq map[string]any, mappedModel string, token string) error {
	openaiMessages, err := convertClaudeMessagesToOpenAIChatCompletionsMessages(parsed.Messages, parsed.System)
	if err != nil {
		writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return err
	}

	openaiReq := map[string]any{
		"model":      mappedModel,
		"stream":     false,
		"messages":   openaiMessages,
		"max_tokens": 1,
	}
	if tools := convertClaudeToolsToOpenAIChatTools(claudeReq["tools"]); len(tools) > 0 {
		openaiReq["tools"] = tools
		openaiReq["tool_choice"] = "auto"
	}

	openaiBody, err := json.Marshal(openaiReq)
	if err != nil {
		writeClaudeError(c, http.StatusInternalServerError, "api_error", "Failed to process request")
		return err
	}
	if c != nil {
		c.Set(OpsUpstreamRequestBodyKey, string(openaiBody))
	}

	targetURL, err := s.chatCompletionsURLForAccount(account)
	if err != nil {
		writeClaudeError(c, http.StatusBadGateway, "upstream_error", err.Error())
		return err
	}

	upstreamReq, err := s.openai.buildUpstreamRequestWithTargetURL(ctx, c, account, openaiBody, token, false, "", false, isGitHubCopilotAccount(account), targetURL)
	if err != nil {
		writeClaudeError(c, http.StatusBadGateway, "upstream_error", err.Error())
		return err
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	resp, err := s.openai.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		writeClaudeError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
		return fmt.Errorf("upstream request failed: %s", sanitizeUpstreamErrorMessage(err.Error()))
	}
	if resp == nil || resp.Body == nil {
		writeClaudeError(c, http.StatusBadGateway, "upstream_error", "Empty upstream response")
		return fmt.Errorf("empty upstream response")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
		upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
		upstreamDetail := ""
		if s.openai.cfg != nil && s.openai.cfg.Gateway.LogUpstreamErrorBody {
			maxBytes := s.openai.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
			if maxBytes <= 0 {
				maxBytes = 2048
			}
			upstreamDetail = truncateString(string(respBody), maxBytes)
		}
		if upstreamDetail == "" && isGitHubCopilotAccount(account) {
			upstreamDetail = truncateString(string(respBody), 2048)
		}
		setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)

		if s.openai.rateLimitService != nil {
			s.openai.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody)
		}
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  strings.TrimSpace(resp.Header.Get("x-request-id")),
			Kind:               "http_error",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
		})

		if status, errType, errMsg, matched := applyErrorPassthroughRule(
			c,
			account.Platform,
			resp.StatusCode,
			respBody,
			http.StatusBadGateway,
			"upstream_error",
			"Upstream request failed",
		); matched {
			writeClaudeError(c, status, errType, errMsg)
			if upstreamMsg == "" {
				upstreamMsg = errMsg
			}
			if upstreamMsg == "" {
				return fmt.Errorf("upstream error: %d (passthrough rule matched)", resp.StatusCode)
			}
			return fmt.Errorf("upstream error: %d (passthrough rule matched) message=%s", resp.StatusCode, upstreamMsg)
		}

		status, errType, errMsg := mapOpenAIUpstreamErrorToClaude(resp.StatusCode)
		writeClaudeError(c, status, errType, errMsg)
		if upstreamMsg == "" {
			return fmt.Errorf("upstream error: %d", resp.StatusCode)
		}
		return fmt.Errorf("upstream error: %d message=%s", resp.StatusCode, upstreamMsg)
	}

	upstreamBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		writeClaudeError(c, http.StatusBadGateway, "upstream_error", "Failed to read upstream response")
		return readErr
	}

	inputTokens, err := extractOpenAIChatCompletionsInputTokens(upstreamBody)
	if err != nil {
		writeClaudeError(c, http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
		return err
	}

	if reqID := strings.TrimSpace(resp.Header.Get("x-request-id")); reqID != "" {
		c.Header("x-request-id", reqID)
	}

	c.JSON(http.StatusOK, gin.H{"input_tokens": inputTokens})
	return nil
}

func (s *OpenAIMessagesCompatService) Forward(ctx context.Context, c *gin.Context, account *Account, parsed *ParsedRequest) (*ForwardResult, error) {
	startTime := time.Now()
	if s == nil || s.openai == nil {
		return nil, fmt.Errorf("openai compat service not configured")
	}
	if account == nil {
		return nil, fmt.Errorf("missing account")
	}
	if parsed == nil {
		return nil, fmt.Errorf("empty request")
	}

	originalModel := strings.TrimSpace(parsed.Model)
	if originalModel == "" {
		return nil, fmt.Errorf("missing model")
	}

	var claudeReq map[string]any
	if err := json.Unmarshal(parsed.Body, &claudeReq); err != nil {
		writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return nil, err
	}

	mappedModel := account.GetMappedModel(originalModel)
	if isGitHubCopilotAccount(account) {
		stripped := strings.TrimSpace(mappedModel)
		if strings.Contains(stripped, "/") {
			parts := strings.Split(stripped, "/")
			stripped = strings.TrimSpace(parts[len(parts)-1])
		}
		if stripped != "" {
			mappedModel = stripped
		}
	} else {
		mappedModel = normalizeCodexModel(mappedModel)
	}

	openaiInput, err := convertClaudeMessagesToOpenAIResponsesInput(parsed.Messages)
	if err != nil {
		writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}

	openaiReq := map[string]any{
		"model":  mappedModel,
		"stream": false,
		"store":  false,
		"input":  openaiInput,
	}
	if parsed.MaxTokens > 0 {
		if account.Type == AccountTypeOAuth {
			openaiReq["max_tokens"] = parsed.MaxTokens
		} else {
			openaiReq["max_output_tokens"] = parsed.MaxTokens
		}
	}

	if systemText := extractClaudeSystemText(parsed.System); systemText != "" {
		openaiReq["instructions"] = systemText
	}

	if tools := convertClaudeToolsToOpenAITools(claudeReq["tools"]); len(tools) > 0 {
		openaiReq["tools"] = tools
		openaiReq["tool_choice"] = "auto"
	}

	if temp, ok := claudeReq["temperature"].(float64); ok {
		openaiReq["temperature"] = temp
	}
	if topP, ok := claudeReq["top_p"].(float64); ok {
		openaiReq["top_p"] = topP
	}
	if stopSeq, ok := claudeReq["stop_sequences"].([]any); ok && len(stopSeq) > 0 {
		openaiReq["stop"] = stopSeq
	}

	upstreamStream := false
	if account.Type == AccountTypeOAuth {
		upstreamStream = true
		openaiReq["stream"] = true
	}

	openaiBody, err := json.Marshal(openaiReq)
	if err != nil {
		writeClaudeError(c, http.StatusInternalServerError, "api_error", "Failed to process request")
		return nil, err
	}
	if c != nil {
		c.Set(OpsUpstreamRequestBodyKey, string(openaiBody))
	}

	token, _, err := s.openai.GetAccessToken(ctx, account)
	if err != nil {
		writeClaudeError(c, http.StatusBadGateway, "upstream_error", err.Error())
		return nil, err
	}

	if isGitHubCopilotAccount(account) && !ShouldUseCopilotResponsesAPI(mappedModel) {
		return s.forwardViaChatCompletions(ctx, c, account, parsed, claudeReq, originalModel, mappedModel, token, startTime)
	}

	upstreamReq, err := s.openai.buildUpstreamRequest(ctx, c, account, openaiBody, token, upstreamStream, "", false, isGitHubCopilotAccount(account))
	if err != nil {
		writeClaudeError(c, http.StatusBadGateway, "upstream_error", err.Error())
		return nil, err
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	resp, err := s.openai.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		setOpsUpstreamError(c, 0, safeErr, "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: 0,
			Kind:               "request_error",
			Message:            safeErr,
		})
		writeClaudeError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
		return nil, fmt.Errorf("upstream request failed: %s", safeErr)
	}
	if resp == nil || resp.Body == nil {
		writeClaudeError(c, http.StatusBadGateway, "upstream_error", "Empty upstream response")
		return nil, fmt.Errorf("empty upstream response")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		rawUpstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
		if isGitHubCopilotAccount(account) && isResponsesAPIUnsupportedError(rawUpstreamMsg, respBody) {
			return s.forwardViaChatCompletions(ctx, c, account, parsed, claudeReq, originalModel, mappedModel, token, startTime)
		}
		upstreamMsg := sanitizeUpstreamErrorMessage(rawUpstreamMsg)
		upstreamDetail := ""
		if s.openai.cfg != nil && s.openai.cfg.Gateway.LogUpstreamErrorBody {
			maxBytes := s.openai.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
			if maxBytes <= 0 {
				maxBytes = 2048
			}
			upstreamDetail = truncateString(string(respBody), maxBytes)
		}
		if upstreamDetail == "" && isGitHubCopilotAccount(account) {
			upstreamDetail = truncateString(string(respBody), 2048)
		}
		setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)

		if s.openai.shouldFailoverUpstreamError(resp.StatusCode) {
			if s.openai.rateLimitService != nil {
				s.openai.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody)
			}
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  strings.TrimSpace(resp.Header.Get("x-request-id")),
				Kind:               "failover",
				Message:            upstreamMsg,
				Detail:             upstreamDetail,
			})
			return nil, &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBody}
		}

		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  strings.TrimSpace(resp.Header.Get("x-request-id")),
			Kind:               "http_error",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
		})

		if status, errType, errMsg, matched := applyErrorPassthroughRule(
			c,
			account.Platform,
			resp.StatusCode,
			respBody,
			http.StatusBadGateway,
			"upstream_error",
			"Upstream request failed",
		); matched {
			writeClaudeError(c, status, errType, errMsg)
			if upstreamMsg == "" {
				upstreamMsg = errMsg
			}
			if upstreamMsg == "" {
				return nil, fmt.Errorf("upstream error: %d (passthrough rule matched)", resp.StatusCode)
			}
			return nil, fmt.Errorf("upstream error: %d (passthrough rule matched) message=%s", resp.StatusCode, upstreamMsg)
		}

		status, errType, errMsg := mapOpenAIUpstreamErrorToClaude(resp.StatusCode)
		writeClaudeError(c, status, errType, errMsg)
		if upstreamMsg == "" {
			return nil, fmt.Errorf("upstream error: %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream error: %d message=%s", resp.StatusCode, upstreamMsg)
	}

	upstreamBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		writeClaudeError(c, http.StatusBadGateway, "upstream_error", "Failed to read upstream response")
		return nil, readErr
	}

	bodyJSON := upstreamBody
	if isEventStreamResponse(resp.Header) || bytes.Contains(upstreamBody, []byte("data:")) {
		if final, ok := extractCodexFinalResponse(string(upstreamBody)); ok {
			bodyJSON = final
		} else {
			writeClaudeError(c, http.StatusBadGateway, "upstream_error", "Failed to parse upstream stream")
			return nil, fmt.Errorf("failed to extract final openai response")
		}
	}

	claudeResp, usage, stopReason, convErr := convertOpenAIResponsesJSONToClaude(bodyJSON, originalModel)
	if convErr != nil {
		writeClaudeError(c, http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
		return nil, convErr
	}

	if reqID := strings.TrimSpace(resp.Header.Get("x-request-id")); reqID != "" {
		c.Header("x-request-id", reqID)
	}

	if !parsed.Stream {
		c.JSON(http.StatusOK, claudeResp)
		return &ForwardResult{
			RequestID: resp.Header.Get("x-request-id"),
			Usage:     *usage,
			Model:     originalModel,
			Stream:    false,
			Duration:  time.Since(startTime),
		}, nil
	}

	if err := writeClaudeStreamFromMessage(c, claudeResp, usage, originalModel, stopReason); err != nil {
		return nil, err
	}

	return &ForwardResult{
		RequestID: resp.Header.Get("x-request-id"),
		Usage:     *usage,
		Model:     originalModel,
		Stream:    true,
		Duration:  time.Since(startTime),
	}, nil
}

func (s *OpenAIMessagesCompatService) ForwardCountTokens(ctx context.Context, c *gin.Context, account *Account, parsed *ParsedRequest) error {
	if s == nil || s.openai == nil {
		writeClaudeError(c, http.StatusInternalServerError, "api_error", "OpenAI compat service not configured")
		return fmt.Errorf("openai compat service not configured")
	}
	if account == nil {
		writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "Missing account")
		return fmt.Errorf("missing account")
	}
	if parsed == nil {
		writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return fmt.Errorf("empty request")
	}

	originalModel := strings.TrimSpace(parsed.Model)
	if originalModel == "" {
		writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return fmt.Errorf("missing model")
	}

	var claudeReq map[string]any
	if err := json.Unmarshal(parsed.Body, &claudeReq); err != nil {
		writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return err
	}

	mappedModel := account.GetMappedModel(originalModel)
	if isGitHubCopilotAccount(account) {
		stripped := strings.TrimSpace(mappedModel)
		if strings.Contains(stripped, "/") {
			parts := strings.Split(stripped, "/")
			stripped = strings.TrimSpace(parts[len(parts)-1])
		}
		if stripped != "" {
			mappedModel = stripped
		}
	} else {
		mappedModel = normalizeCodexModel(mappedModel)
	}

	openaiInput, err := convertClaudeMessagesToOpenAIResponsesInput(parsed.Messages)
	if err != nil {
		writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return err
	}

	openaiReq := map[string]any{
		"model":  mappedModel,
		"stream": false,
		"store":  false,
		"input":  openaiInput,
	}
	if account.Type == AccountTypeOAuth {
		openaiReq["max_tokens"] = 1
	} else {
		openaiReq["max_output_tokens"] = 1
	}

	if systemText := extractClaudeSystemText(parsed.System); systemText != "" {
		openaiReq["instructions"] = systemText
	}

	if tools := convertClaudeToolsToOpenAITools(claudeReq["tools"]); len(tools) > 0 {
		openaiReq["tools"] = tools
		openaiReq["tool_choice"] = "auto"
	}

	upstreamStream := false
	if account.Type == AccountTypeOAuth {
		upstreamStream = true
		openaiReq["stream"] = true
	}

	openaiBody, err := json.Marshal(openaiReq)
	if err != nil {
		writeClaudeError(c, http.StatusInternalServerError, "api_error", "Failed to process request")
		return err
	}
	if c != nil {
		c.Set(OpsUpstreamRequestBodyKey, string(openaiBody))
	}

	token, _, err := s.openai.GetAccessToken(ctx, account)
	if err != nil {
		writeClaudeError(c, http.StatusBadGateway, "upstream_error", err.Error())
		return err
	}

	if isGitHubCopilotAccount(account) && !ShouldUseCopilotResponsesAPI(mappedModel) {
		return s.forwardCountTokensViaChatCompletions(ctx, c, account, parsed, claudeReq, mappedModel, token)
	}

	upstreamReq, err := s.openai.buildUpstreamRequest(ctx, c, account, openaiBody, token, upstreamStream, "", false, isGitHubCopilotAccount(account))
	if err != nil {
		writeClaudeError(c, http.StatusBadGateway, "upstream_error", err.Error())
		return err
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	resp, err := s.openai.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		setOpsUpstreamError(c, 0, safeErr, "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: 0,
			Kind:               "request_error",
			Message:            safeErr,
		})
		writeClaudeError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
		return fmt.Errorf("upstream request failed: %s", safeErr)
	}
	if resp == nil || resp.Body == nil {
		writeClaudeError(c, http.StatusBadGateway, "upstream_error", "Empty upstream response")
		return fmt.Errorf("empty upstream response")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		rawUpstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
		if isGitHubCopilotAccount(account) && isResponsesAPIUnsupportedError(rawUpstreamMsg, respBody) {
			return s.forwardCountTokensViaChatCompletions(ctx, c, account, parsed, claudeReq, mappedModel, token)
		}
		upstreamMsg := sanitizeUpstreamErrorMessage(rawUpstreamMsg)
		upstreamDetail := ""
		if s.openai.cfg != nil && s.openai.cfg.Gateway.LogUpstreamErrorBody {
			maxBytes := s.openai.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
			if maxBytes <= 0 {
				maxBytes = 2048
			}
			upstreamDetail = truncateString(string(respBody), maxBytes)
		}
		if upstreamDetail == "" && isGitHubCopilotAccount(account) {
			upstreamDetail = truncateString(string(respBody), 2048)
		}
		setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)

		if s.openai.rateLimitService != nil {
			s.openai.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody)
		}
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  strings.TrimSpace(resp.Header.Get("x-request-id")),
			Kind:               "http_error",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
		})

		if status, errType, errMsg, matched := applyErrorPassthroughRule(
			c,
			account.Platform,
			resp.StatusCode,
			respBody,
			http.StatusBadGateway,
			"upstream_error",
			"Upstream request failed",
		); matched {
			writeClaudeError(c, status, errType, errMsg)
			if upstreamMsg == "" {
				upstreamMsg = errMsg
			}
			if upstreamMsg == "" {
				return fmt.Errorf("upstream error: %d (passthrough rule matched)", resp.StatusCode)
			}
			return fmt.Errorf("upstream error: %d (passthrough rule matched) message=%s", resp.StatusCode, upstreamMsg)
		}

		status, errType, errMsg := mapOpenAIUpstreamErrorToClaude(resp.StatusCode)
		writeClaudeError(c, status, errType, errMsg)
		if upstreamMsg == "" {
			return fmt.Errorf("upstream error: %d", resp.StatusCode)
		}
		return fmt.Errorf("upstream error: %d message=%s", resp.StatusCode, upstreamMsg)
	}

	upstreamBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		writeClaudeError(c, http.StatusBadGateway, "upstream_error", "Failed to read upstream response")
		return readErr
	}

	bodyJSON := upstreamBody
	if isEventStreamResponse(resp.Header) || bytes.Contains(upstreamBody, []byte("data:")) {
		if final, ok := extractCodexFinalResponse(string(upstreamBody)); ok {
			bodyJSON = final
		} else {
			writeClaudeError(c, http.StatusBadGateway, "upstream_error", "Failed to parse upstream stream")
			return fmt.Errorf("failed to extract final openai response")
		}
	}

	inputTokens, err := extractOpenAIResponsesInputTokens(bodyJSON)
	if err != nil {
		writeClaudeError(c, http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
		return err
	}

	if reqID := strings.TrimSpace(resp.Header.Get("x-request-id")); reqID != "" {
		c.Header("x-request-id", reqID)
	}

	c.JSON(http.StatusOK, gin.H{"input_tokens": inputTokens})
	return nil
}

func extractOpenAIResponsesInputTokens(openaiResp []byte) (int, error) {
	var resp map[string]any
	if err := json.Unmarshal(openaiResp, &resp); err != nil {
		return 0, err
	}
	usageAny, ok := resp["usage"].(map[string]any)
	if !ok {
		return 0, errors.New("missing usage")
	}
	inputTokens, ok := asInt(usageAny["input_tokens"])
	if !ok {
		return 0, errors.New("missing usage.input_tokens")
	}
	return inputTokens, nil
}

func mapOpenAIUpstreamErrorToClaude(statusCode int) (int, string, string) {
	switch statusCode {
	case 401:
		return http.StatusBadGateway, "upstream_error", "Upstream authentication failed, please contact administrator"
	case 402:
		return http.StatusBadGateway, "upstream_error", "Upstream payment required: insufficient balance or billing issue"
	case 403:
		return http.StatusBadGateway, "upstream_error", "Upstream access forbidden, please contact administrator"
	case 429:
		return http.StatusTooManyRequests, "rate_limit_error", "Upstream rate limit exceeded, please retry later"
	case 529:
		return http.StatusServiceUnavailable, "overloaded_error", "Upstream service overloaded, please retry later"
	case 500, 502, 503, 504:
		return http.StatusBadGateway, "upstream_error", "Upstream service temporarily unavailable"
	default:
		return http.StatusBadGateway, "upstream_error", "Upstream request failed"
	}
}

func writeClaudeError(c *gin.Context, status int, errType, message string) {
	if c == nil {
		return
	}
	c.JSON(status, gin.H{
		"type": "error",
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
}

func convertClaudeToolsToOpenAITools(tools any) []any {
	arr, ok := tools.([]any)
	if !ok || len(arr) == 0 {
		return nil
	}
	out := make([]any, 0, len(arr))
	for _, t := range arr {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}

		toolType, _ := tm["type"].(string)
		toolType = strings.TrimSpace(toolType)

		name := ""
		desc := ""
		params := any(nil)
		if toolType == "custom" {
			name, _ = tm["name"].(string)
			if custom, ok := tm["custom"].(map[string]any); ok {
				desc, _ = custom["description"].(string)
				params = custom["input_schema"]
			}
		} else {
			name, _ = tm["name"].(string)
			desc, _ = tm["description"].(string)
			params = tm["input_schema"]
		}

		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}

		out = append(out, map[string]any{
			"type":        "function",
			"name":        name,
			"description": strings.TrimSpace(desc),
			"parameters":  params,
		})
	}
	return out
}

func convertClaudeMessagesToOpenAIResponsesInput(messages []any) ([]any, error) {
	if len(messages) == 0 {
		return []any{}, nil
	}

	toolIDToName := make(map[string]string)
	out := make([]any, 0, len(messages))

	flushMessage := func(role string, content []any) {
		role = strings.ToLower(strings.TrimSpace(role))
		if role == "" {
			role = "user"
		}
		if len(content) == 0 {
			return
		}
		out = append(out, map[string]any{
			"type":    "message",
			"role":    role,
			"content": content,
		})
	}

	for _, m := range messages {
		mm, ok := m.(map[string]any)
		if !ok {
			continue
		}
		role, _ := mm["role"].(string)
		role = strings.ToLower(strings.TrimSpace(role))
		if role == "" {
			role = "user"
		}

		switch content := mm["content"].(type) {
		case string:
			flushMessage(role, []any{map[string]any{"type": "input_text", "text": content}})
		case []any:
			msgContent := make([]any, 0)
			for _, block := range content {
				bm, ok := block.(map[string]any)
				if !ok {
					continue
				}
				bt, _ := bm["type"].(string)
				bt = strings.ToLower(strings.TrimSpace(bt))

				switch bt {
				case "text":
					if text, ok := bm["text"].(string); ok {
						msgContent = append(msgContent, map[string]any{"type": "input_text", "text": text})
					}
				case "thinking":
					if t, ok := bm["thinking"].(string); ok && strings.TrimSpace(t) != "" {
						msgContent = append(msgContent, map[string]any{"type": "input_text", "text": t})
					}
				case "image":
					if src, ok := bm["source"].(map[string]any); ok {
						if srcType, _ := src["type"].(string); srcType == "base64" {
							mediaType, _ := src["media_type"].(string)
							data, _ := src["data"].(string)
							mediaType = strings.TrimSpace(mediaType)
							data = strings.TrimSpace(data)
							if mediaType != "" && data != "" {
								url := fmt.Sprintf("data:%s;base64,%s", mediaType, data)
								msgContent = append(msgContent, map[string]any{
									"type":      "input_image",
									"image_url": map[string]any{"url": url},
								})
							}
						}
					}
				case "tool_use":
					flushMessage(role, msgContent)
					msgContent = make([]any, 0)

					id, _ := bm["id"].(string)
					name, _ := bm["name"].(string)
					id = strings.TrimSpace(id)
					name = strings.TrimSpace(name)
					if id != "" && name != "" {
						toolIDToName[id] = name
					}
					argsJSON, _ := json.Marshal(bm["input"])
					out = append(out, map[string]any{
						"type":      "function_call",
						"id":        id,
						"call_id":   id,
						"name":      name,
						"arguments": string(argsJSON),
					})
				case "tool_result":
					flushMessage(role, msgContent)
					msgContent = make([]any, 0)

					toolUseID, _ := bm["tool_use_id"].(string)
					toolUseID = strings.TrimSpace(toolUseID)
					name := ""
					if v, ok := bm["name"].(string); ok {
						name = strings.TrimSpace(v)
					}
					if name == "" {
						name = toolIDToName[toolUseID]
					}
					output := extractClaudeContentText(bm["content"])
					out = append(out, map[string]any{
						"type":    "function_call_output",
						"call_id": toolUseID,
						"name":    name,
						"output":  output,
					})
				default:
					if b, err := json.Marshal(bm); err == nil {
						msgContent = append(msgContent, map[string]any{"type": "input_text", "text": string(b)})
					}
				}
			}
			flushMessage(role, msgContent)
		default:
		}
	}
	return out, nil
}

func convertOpenAIResponsesJSONToClaude(openaiResp []byte, originalModel string) (map[string]any, *ClaudeUsage, string, error) {
	var resp map[string]any
	if err := json.Unmarshal(openaiResp, &resp); err != nil {
		return nil, nil, "", err
	}

	usage := &ClaudeUsage{}
	if u, ok := resp["usage"].(map[string]any); ok {
		if in, ok := asInt(u["input_tokens"]); ok {
			usage.InputTokens = in
		}
		if out, ok := asInt(u["output_tokens"]); ok {
			usage.OutputTokens = out
		}
		if details, ok := u["input_tokens_details"].(map[string]any); ok {
			if cached, ok := asInt(details["cached_tokens"]); ok {
				usage.CacheReadInputTokens = cached
			}
		}
	}

	content := make([]any, 0)
	stopReason := "end_turn"

	if outputItems, ok := resp["output"].([]any); ok {
		for _, item := range outputItems {
			im, ok := item.(map[string]any)
			if !ok {
				continue
			}
			t, _ := im["type"].(string)
			t = strings.TrimSpace(t)
			switch t {
			case "message":
				if blocks, ok := im["content"].([]any); ok {
					for _, b := range blocks {
						bm, ok := b.(map[string]any)
						if !ok {
							continue
						}
						bt, _ := bm["type"].(string)
						bt = strings.TrimSpace(bt)
						switch bt {
						case "output_text", "text":
							if text, ok := bm["text"].(string); ok {
								content = append(content, map[string]any{"type": "text", "text": text})
							}
						}
					}
				}
			case "function_call", "tool_call":
				stopReason = "tool_use"
				callID, _ := im["call_id"].(string)
				if strings.TrimSpace(callID) == "" {
					callID, _ = im["id"].(string)
				}
				name, _ := im["name"].(string)
				argsAny := im["arguments"]
				var input any
				switch v := argsAny.(type) {
				case string:
					if strings.TrimSpace(v) != "" {
						var parsed any
						if json.Unmarshal([]byte(v), &parsed) == nil {
							input = parsed
						} else {
							input = v
						}
					}
				default:
					input = v
				}
				content = append(content, map[string]any{
					"type":  "tool_use",
					"id":    strings.TrimSpace(callID),
					"name":  strings.TrimSpace(name),
					"input": input,
				})
			}
		}
	}

	if len(content) == 0 {
		content = append(content, map[string]any{"type": "text", "text": ""})
	}

	msgID, _ := resp["id"].(string)
	msgID = strings.TrimSpace(msgID)
	if msgID == "" {
		msgID = "msg_" + randomHex(12)
	}

	if stopReason != "tool_use" {
		if inc, ok := resp["incomplete_details"].(map[string]any); ok {
			if reason, _ := inc["reason"].(string); strings.EqualFold(strings.TrimSpace(reason), "max_output_tokens") {
				stopReason = "max_tokens"
			}
		}
	}

	claudeResp := map[string]any{
		"id":            msgID,
		"type":          "message",
		"role":          "assistant",
		"model":         originalModel,
		"content":       content,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":                usage.InputTokens,
			"output_tokens":               usage.OutputTokens,
			"cache_creation_input_tokens": usage.CacheCreationInputTokens,
			"cache_read_input_tokens":     usage.CacheReadInputTokens,
		},
	}

	return claudeResp, usage, stopReason, nil
}

func writeClaudeStreamFromMessage(c *gin.Context, claudeResp map[string]any, usage *ClaudeUsage, model string, stopReason string) error {
	if c == nil {
		return errors.New("nil context")
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	w := c.Writer
	flusher, ok := w.(http.Flusher)
	if !ok {
		return errors.New("streaming not supported")
	}

	id, _ := claudeResp["id"].(string)
	if strings.TrimSpace(id) == "" {
		id = "msg_" + randomHex(12)
	}

	messageStart := map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            id,
			"type":          "message",
			"role":          "assistant",
			"model":         model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  usage.InputTokens,
				"output_tokens": 0,
			},
		},
	}
	if err := writeClaudeSSEEvent(w, "message_start", messageStart); err != nil {
		return err
	}
	flusher.Flush()

	blocksAny, _ := claudeResp["content"].([]any)
	blockIndex := 0
	for _, block := range blocksAny {
		bm, ok := block.(map[string]any)
		if !ok {
			continue
		}
		bt, _ := bm["type"].(string)
		bt = strings.TrimSpace(bt)
		if bt == "" {
			continue
		}

		start := map[string]any{
			"type":          "content_block_start",
			"index":         blockIndex,
			"content_block": bm,
		}
		if err := writeClaudeSSEEvent(w, "content_block_start", start); err != nil {
			return err
		}

		switch bt {
		case "text":
			text, _ := bm["text"].(string)
			delta := map[string]any{
				"type":  "content_block_delta",
				"index": blockIndex,
				"delta": map[string]any{"type": "text_delta", "text": text},
			}
			if err := writeClaudeSSEEvent(w, "content_block_delta", delta); err != nil {
				return err
			}
		case "thinking":
			thinking, _ := bm["thinking"].(string)
			if strings.TrimSpace(thinking) != "" {
				delta := map[string]any{
					"type":  "content_block_delta",
					"index": blockIndex,
					"delta": map[string]any{"type": "thinking_delta", "thinking": thinking},
				}
				if err := writeClaudeSSEEvent(w, "content_block_delta", delta); err != nil {
					return err
				}
			}
			if sig, _ := bm["signature"].(string); strings.TrimSpace(sig) != "" {
				delta := map[string]any{
					"type":  "content_block_delta",
					"index": blockIndex,
					"delta": map[string]any{"type": "signature_delta", "signature": sig},
				}
				if err := writeClaudeSSEEvent(w, "content_block_delta", delta); err != nil {
					return err
				}
			}
		case "tool_use":
			inputJSON, _ := json.Marshal(bm["input"])
			delta := map[string]any{
				"type":  "content_block_delta",
				"index": blockIndex,
				"delta": map[string]any{"type": "input_json_delta", "partial_json": string(inputJSON)},
			}
			if err := writeClaudeSSEEvent(w, "content_block_delta", delta); err != nil {
				return err
			}
		}

		stop := map[string]any{"type": "content_block_stop", "index": blockIndex}
		if err := writeClaudeSSEEvent(w, "content_block_stop", stop); err != nil {
			return err
		}
		flusher.Flush()
		blockIndex++
	}

	messageDelta := map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"input_tokens":                usage.InputTokens,
			"output_tokens":               usage.OutputTokens,
			"cache_creation_input_tokens": usage.CacheCreationInputTokens,
			"cache_read_input_tokens":     usage.CacheReadInputTokens,
		},
	}
	if err := writeClaudeSSEEvent(w, "message_delta", messageDelta); err != nil {
		return err
	}
	if err := writeClaudeSSEEvent(w, "message_stop", map[string]any{"type": "message_stop"}); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func writeClaudeSSEEvent(w io.Writer, event string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\n", event)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", string(b))
	return err
}
