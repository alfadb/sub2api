package service

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

const (
	copilotUpstreamURL          = "https://api.githubcopilot.com/chat/completions"
	copilotUpstreamResponsesURL = "https://api.githubcopilot.com/responses"
	copilotUpstreamMessagesURL  = "https://api.githubcopilot.com/v1/messages"
	copilotUpstreamModelsURL    = "https://api.githubcopilot.com/models"
)

// CopilotUsage represents token usage from a Copilot chat/completions response.
type CopilotUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// CopilotForwardResult holds the outcome of a forwarded Copilot request.
type CopilotForwardResult struct {
	RequestID    string
	Usage        CopilotUsage
	Model        string
	Stream       bool
	Duration     time.Duration
	FirstTokenMs *int
}

// usageExtractor extracts CopilotUsage from a gjson usage object.
type usageExtractor func(u gjson.Result) CopilotUsage

// chatCompletionsUsage extracts usage with prompt_tokens/completion_tokens field names.
func chatCompletionsUsage(u gjson.Result) CopilotUsage {
	return CopilotUsage{
		PromptTokens:     int(u.Get("prompt_tokens").Int()),
		CompletionTokens: int(u.Get("completion_tokens").Int()),
		TotalTokens:      int(u.Get("total_tokens").Int()),
	}
}

// responsesUsage extracts usage with input_tokens/output_tokens field names.
func responsesUsage(u gjson.Result) CopilotUsage {
	pt := int(u.Get("input_tokens").Int())
	ct := int(u.Get("output_tokens").Int())
	return CopilotUsage{PromptTokens: pt, CompletionTokens: ct, TotalTokens: pt + ct}
}

// copilotForwardConfig holds per-endpoint differences for the shared forward path.
type copilotForwardConfig struct {
	upstreamURL  string
	extractUsage usageExtractor
	detectInit   func([]byte) string
	detectVision func([]byte) bool
}

// CopilotGatewayService forwards chat/completions requests to GitHub Copilot,
// precisely mimicking opencode's request headers.
type CopilotGatewayService struct {
	accountRepo          AccountRepository
	cfg                  *config.Config
	httpUpstream         HTTPUpstream
	copilotTokenProvider *CopilotTokenProvider
	versionService       *OpenCodeVersionService
}

// NewCopilotGatewayService creates a new CopilotGatewayService.
func NewCopilotGatewayService(
	accountRepo AccountRepository,
	cfg *config.Config,
	httpUpstream HTTPUpstream,
	copilotTokenProvider *CopilotTokenProvider,
	versionService *OpenCodeVersionService,
) *CopilotGatewayService {
	return &CopilotGatewayService{
		accountRepo:          accountRepo,
		cfg:                  cfg,
		httpUpstream:         httpUpstream,
		copilotTokenProvider: copilotTokenProvider,
		versionService:       versionService,
	}
}

// setCopilotBaseHeaders sets the Copilot upstream request headers.
// IMPORTANT: This must match opencode (GitHub Copilot's official reference implementation) exactly.
// opencode is in GitHub Copilot's official whitelist and its request format is guaranteed to work.
//
// Source: https://github.com/opencode-ai/opencode/blob/main/packages/opencode/src/plugin/copilot.ts
//
// Key headers set here (x-initiator, Copilot-Vision-Request are set separately in the forward path):
//   - Authorization: Bearer {token}
//   - User-Agent: opencode/{version}
//   - Openai-Intent: conversation-edits
func (s *CopilotGatewayService) setCopilotBaseHeaders(req *http.Request, token string) {
	ua := s.versionService.UserAgent()
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", ua)
	// "conversation-edits" is the intent used by opencode for all requests.
	// This is critical for accessing the full Claude model catalog.
	req.Header.Set("Openai-Intent", "conversation-edits")
}

// Forward sends a chat/completions request to GitHub Copilot.
func (s *CopilotGatewayService) Forward(ctx context.Context, c *gin.Context, account *Account, body []byte) (*CopilotForwardResult, error) {
	return s.forward(ctx, c, account, body, copilotForwardConfig{
		upstreamURL:  copilotUpstreamURL,
		extractUsage: chatCompletionsUsage,
		detectInit:   detectInitiator,
		detectVision: detectVision,
	})
}

// ForwardResponses sends a Responses API request to GitHub Copilot.
func (s *CopilotGatewayService) ForwardResponses(ctx context.Context, c *gin.Context, account *Account, body []byte) (*CopilotForwardResult, error) {
	// Ensure "reasoning.encrypted_content" is in the include array so upstream
	// returns plaintext reasoning instead of encrypted content.
	body = ensureResponsesInclude(body)
	return s.forward(ctx, c, account, body, copilotForwardConfig{
		upstreamURL:  copilotUpstreamResponsesURL,
		extractUsage: responsesUsage,
		detectInit:   detectInitiatorResponses,
		detectVision: detectVisionResponses,
	})
}

// ForwardResponsesRaw sends a Responses API request to GitHub Copilot and
// returns the raw *http.Response without processing the body. The caller is
// responsible for closing the response body and handling the response stream.
//
// This method applies model mapping, token auth, required headers, and the
// "reasoning.encrypted_content" include injection, but delegates response
// processing to the caller (e.g., for Stream ID synchronization via
// ProcessResponsesStream).
//
// Returns:
//   - *http.Response: the raw upstream response (caller must close Body)
//   - string: the original (pre-mapping) model name
//   - error: any error building or executing the upstream request
func (s *CopilotGatewayService) ForwardResponsesRaw(ctx context.Context, account *Account, body []byte) (*http.Response, string, error) {
	// Ensure "reasoning.encrypted_content" is in the include array
	body = ensureResponsesInclude(body)

	model := gjson.GetBytes(body, "model").String()
	mappedModel := account.GetMappedModel(model)
	logger.L().Debug("copilot model mapping applied (ForwardResponsesRaw)",
		zap.Int64("account_id", account.ID),
		zap.String("requested_model", model),
		zap.String("mapped_model", mappedModel),
		zap.Bool("was_mapped", mappedModel != model),
	)
	if mappedModel != model {
		var err error
		body, err = sjson.SetBytes(body, "model", mappedModel)
		if err != nil {
			return nil, "", fmt.Errorf("replace model in body: %w", err)
		}
	}

	token, err := s.copilotTokenProvider.GetAccessToken(ctx, account)
	if err != nil {
		return nil, "", fmt.Errorf("get copilot access token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, copilotUpstreamResponsesURL, bytes.NewReader(body))
	if err != nil {
		return nil, "", fmt.Errorf("create upstream request: %w", err)
	}
	s.setCopilotBaseHeaders(req, token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-initiator", detectInitiatorResponses(body))
	if detectVisionResponses(body) {
		req.Header.Set("Copilot-Vision-Request", "true")
	}

	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return nil, "", fmt.Errorf("copilot upstream request: %w", err)
	}

	return resp, model, nil
}

// convertedResponseHandler handles the upstream response after a format-converting forward.
type convertedResponseHandler struct {
	handleStream    func(c *gin.Context, resp *http.Response, model string, start time.Time) (*CopilotForwardResult, error)
	handleNonStream func(c *gin.Context, resp *http.Response, model string, start time.Time) (*CopilotForwardResult, error)
}

// forwardConverted is the shared pipeline for format-converting forwards.
// It takes an already-converted request body, sends it upstream, and dispatches
// the response to the appropriate handler.
func (s *CopilotGatewayService) forwardConverted(
	ctx context.Context, c *gin.Context, account *Account,
	convertedBody []byte, originalModel string, isStream bool,
	cfg copilotForwardConfig, handler convertedResponseHandler,
) (*CopilotForwardResult, error) {
	start := time.Now()

	// Apply model mapping
	model := gjson.GetBytes(convertedBody, "model").String()
	mappedModel := account.GetMappedModel(model)
	logger.L().Info("copilot model mapping applied",
		zap.Int64("account_id", account.ID),
		zap.String("requested_model", model),
		zap.String("mapped_model", mappedModel),
		zap.Bool("was_mapped", mappedModel != model),
	)
	if mappedModel != model {
		var err error
		convertedBody, err = sjson.SetBytes(convertedBody, "model", mappedModel)
		if err != nil {
			return nil, fmt.Errorf("replace model in body: %w", err)
		}
	}

	// Build upstream request
	token, err := s.copilotTokenProvider.GetAccessToken(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("get copilot access token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.upstreamURL, bytes.NewReader(convertedBody))
	if err != nil {
		return nil, fmt.Errorf("create upstream request: %w", err)
	}
	s.setCopilotBaseHeaders(req, token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-initiator", cfg.detectInit(convertedBody))
	if cfg.detectVision(convertedBody) {
		req.Header.Set("Copilot-Vision-Request", "true")
	}

	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return nil, fmt.Errorf("copilot upstream request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode >= 400 {
		return s.handleErrorResponse(c, resp, account, mappedModel, isStream, start)
	}

	if isStream {
		return handler.handleStream(c, resp, originalModel, start)
	}
	return handler.handleNonStream(c, resp, originalModel, start)
}

// ForwardChatAsAnthropic accepts an Anthropic Messages request body, converts
// it to Chat Completions format, forwards to Copilot /chat/completions, and
// converts the response back to Anthropic Messages format.
func (s *CopilotGatewayService) ForwardChatAsAnthropic(ctx context.Context, c *gin.Context, account *Account, body []byte) (*CopilotForwardResult, error) {
	var anthropicReq apicompat.AnthropicRequest
	if err := json.Unmarshal(body, &anthropicReq); err != nil {
		return nil, fmt.Errorf("parse anthropic request: %w", err)
	}

	chatReq, err := apicompat.AnthropicToChat(&anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("convert anthropic to chat: %w", err)
	}

	chatBody, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("marshal chat request: %w", err)
	}

	return s.forwardConverted(ctx, c, account, chatBody, anthropicReq.Model, anthropicReq.Stream,
		copilotForwardConfig{
			upstreamURL:  copilotUpstreamURL,
			extractUsage: chatCompletionsUsage,
			detectInit:   detectInitiator,
			detectVision: detectVision,
		},
		convertedResponseHandler{
			handleStream:    s.handleAnthropicStreamResponse,
			handleNonStream: s.handleAnthropicNonStreamResponse,
		},
	)
}

// ForwardMessages sends an Anthropic Messages request directly to GitHub
// Copilot's native /v1/messages endpoint without any format conversion.
// This preserves cache_control, thinking, metadata and other Anthropic-specific
// fields that would be lost in a Chat Completions round-trip, enabling prompt
// caching to work correctly.
func (s *CopilotGatewayService) ForwardMessages(ctx context.Context, c *gin.Context, account *Account, body []byte) (*CopilotForwardResult, error) {
	start := time.Now()

	model := gjson.GetBytes(body, "model").String()
	isStream := gjson.GetBytes(body, "stream").Bool()

	mappedModel := account.GetMappedModel(model)
	logger.L().Info("copilot messages model mapping applied",
		zap.Int64("account_id", account.ID),
		zap.String("requested_model", model),
		zap.String("mapped_model", mappedModel),
		zap.Bool("was_mapped", mappedModel != model),
	)
	if mappedModel != model {
		var err error
		body, err = sjson.SetBytes(body, "model", mappedModel)
		if err != nil {
			return nil, fmt.Errorf("replace model in body: %w", err)
		}
	}

	token, err := s.copilotTokenProvider.GetAccessToken(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("get copilot access token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, copilotUpstreamMessagesURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create upstream request: %w", err)
	}
	s.setCopilotBaseHeaders(req, token)
	s.setCopilotMessagesHeaders(req, c, body)

	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return nil, fmt.Errorf("copilot upstream request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode >= 400 {
		return s.handleErrorResponse(c, resp, account, mappedModel, isStream, start)
	}

	if isStream {
		return s.handleMessagesStreamResponse(c, resp, model, start)
	}
	return s.handleMessagesNonStreamResponse(c, resp, model, start)
}

// setCopilotMessagesHeaders sets headers specific to the /v1/messages endpoint.
//
// Headers set:
//   - Content-Type: application/json
//   - X-Initiator: user/agent (from last message role)
//   - Copilot-Vision-Request: true (if request contains image blocks)
//   - Anthropic-Beta: transparently forwarded from client, filtered for claude-code;
//     if absent but body contains thinking.budget_tokens, adds interleaved-thinking beta
//   - Anthropic-Version: transparently forwarded from client (if present)
func (s *CopilotGatewayService) setCopilotMessagesHeaders(req *http.Request, c *gin.Context, body []byte) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-initiator", detectInitiatorMessages(body))
	if detectVisionMessages(body) {
		req.Header.Set("Copilot-Vision-Request", "true")
	}

	// Anthropic-Beta: forward from client, filtering out claude-code beta
	if beta := c.Request.Header.Get("Anthropic-Beta"); beta != "" {
		req.Header.Set("Anthropic-Beta", filterAnthropicBeta(beta))
	} else if gjson.GetBytes(body, "thinking.budget_tokens").Exists() {
		// Body requests thinking but client didn't send Anthropic-Beta — add it
		req.Header.Set("Anthropic-Beta", "interleaved-thinking-2025-05-14")
	}

	// Anthropic-Version: forward as-is
	if version := c.Request.Header.Get("Anthropic-Version"); version != "" {
		req.Header.Set("Anthropic-Version", version)
	}
}

// filterAnthropicBeta removes the "claude-code-20250219" token from an
// Anthropic-Beta header value since Copilot's /v1/messages endpoint doesn't
// recognise it.
func filterAnthropicBeta(beta string) string {
	const dropToken = "claude-code-20250219"
	parts := strings.Split(beta, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || p == dropToken {
			continue
		}
		out = append(out, p)
	}
	return strings.Join(out, ",")
}

// handleMessagesNonStreamResponse reads the full Anthropic Messages response
// from upstream, extracts usage, and writes it back to the client unchanged.
func (s *CopilotGatewayService) handleMessagesNonStreamResponse(c *gin.Context, resp *http.Response, model string, start time.Time) (*CopilotForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read upstream response: %w", err)
	}

	usage := extractAnthropicUsage(respBody)

	logger.L().Debug("copilot messages non-stream: usage extracted",
		zap.String("request_id", requestID),
		zap.Int("prompt_tokens", usage.PromptTokens),
		zap.Int("completion_tokens", usage.CompletionTokens),
		zap.Int("total_tokens", usage.TotalTokens),
	)

	responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.cfg.Security.ResponseHeaders)
	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)

	return &CopilotForwardResult{
		RequestID: requestID,
		Usage:     usage,
		Model:     model,
		Stream:    false,
		Duration:  time.Since(start),
	}, nil
}

// handleMessagesStreamResponse forwards Anthropic SSE events from upstream to
// the client unchanged, extracting usage from message_start and message_delta
// events along the way.
func (s *CopilotGatewayService) handleMessagesStreamResponse(c *gin.Context, resp *http.Response, model string, start time.Time) (*CopilotForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.cfg.Security.ResponseHeaders)
	c.Writer.WriteHeader(http.StatusOK)

	var usage CopilotUsage
	var firstTokenMs *int
	firstChunk := true

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// Track first data line for TTFT
		if firstChunk && strings.HasPrefix(line, "data: ") {
			firstChunk = false
			ms := int(time.Since(start).Milliseconds())
			firstTokenMs = &ms
		}

		// Extract usage from Anthropic SSE events
		if strings.HasPrefix(line, "data: ") {
			payload := line[6:]
			eventType := gjson.Get(payload, "type").String()
			switch eventType {
			case "message_start":
				// message_start → message.usage contains input_tokens + cache fields
				u := gjson.Get(payload, "message.usage")
				if u.Exists() {
					inputTokens := int(u.Get("input_tokens").Int())
					cacheCreation := int(u.Get("cache_creation_input_tokens").Int())
					cacheRead := int(u.Get("cache_read_input_tokens").Int())
					usage.PromptTokens = inputTokens + cacheCreation + cacheRead
				}
			case "message_delta":
				// message_delta → usage.output_tokens
				u := gjson.Get(payload, "usage")
				if u.Exists() {
					usage.CompletionTokens = int(u.Get("output_tokens").Int())
				}
			}
		}

		// Transparently forward every line (event:, data:, empty lines)
		fmt.Fprintf(c.Writer, "%s\n", line)
		c.Writer.Flush()
	}

	if err := scanner.Err(); err != nil {
		logger.L().Warn("copilot messages stream read error",
			zap.Error(err),
			zap.String("request_id", requestID),
		)
	}

	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens

	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 {
		logger.L().Warn("copilot messages stream: final usage is ZERO",
			zap.String("request_id", requestID),
			zap.String("model", model),
		)
	} else {
		logger.L().Info("copilot messages stream: final usage",
			zap.String("request_id", requestID),
			zap.String("model", model),
			zap.Int("prompt_tokens", usage.PromptTokens),
			zap.Int("completion_tokens", usage.CompletionTokens),
			zap.Int("total_tokens", usage.TotalTokens),
		)
	}

	return &CopilotForwardResult{
		RequestID:    requestID,
		Usage:        usage,
		Model:        model,
		Stream:       true,
		Duration:     time.Since(start),
		FirstTokenMs: firstTokenMs,
	}, nil
}

// extractAnthropicUsage extracts token usage from an Anthropic Messages
// response body (non-stream). It accounts for cache_creation_input_tokens and
// cache_read_input_tokens which are Anthropic-specific fields.
func extractAnthropicUsage(body []byte) CopilotUsage {
	u := gjson.GetBytes(body, "usage")
	if !u.Exists() {
		return CopilotUsage{}
	}
	inputTokens := int(u.Get("input_tokens").Int())
	outputTokens := int(u.Get("output_tokens").Int())
	cacheCreation := int(u.Get("cache_creation_input_tokens").Int())
	cacheRead := int(u.Get("cache_read_input_tokens").Int())
	promptTokens := inputTokens + cacheCreation + cacheRead
	return CopilotUsage{
		PromptTokens:     promptTokens,
		CompletionTokens: outputTokens,
		TotalTokens:      promptTokens + outputTokens,
	}
}

// detectInitiatorMessages returns "user" or "agent" based on the last message
// role in an Anthropic Messages request body.
func detectInitiatorMessages(body []byte) string {
	messages := gjson.GetBytes(body, "messages")
	if !messages.Exists() || !messages.IsArray() {
		return "user"
	}
	arr := messages.Array()
	if len(arr) == 0 {
		return "user"
	}
	if arr[len(arr)-1].Get("role").String() == "user" {
		return "user"
	}
	return "agent"
}

// detectVisionMessages checks if any message in an Anthropic Messages request
// contains an image content block (type == "image").
func detectVisionMessages(body []byte) bool {
	messages := gjson.GetBytes(body, "messages")
	if !messages.Exists() {
		return false
	}
	for _, msg := range messages.Array() {
		content := msg.Get("content")
		if !content.IsArray() {
			continue
		}
		for _, part := range content.Array() {
			if part.Get("type").String() == "image" {
				return true
			}
		}
	}
	return false
}

// ForwardChatAsResponses accepts a Chat Completions request body, converts it
// to Responses API format, forwards to Copilot /responses, and converts the
// response back to Chat Completions format. Used for codex models that only
// support the /responses endpoint.
func (s *CopilotGatewayService) ForwardChatAsResponses(ctx context.Context, c *gin.Context, account *Account, body []byte) (*CopilotForwardResult, error) {
	var chatReq apicompat.ChatRequest
	if err := json.Unmarshal(body, &chatReq); err != nil {
		return nil, fmt.Errorf("parse chat request: %w", err)
	}

	responsesReq, err := apicompat.ChatToResponses(&chatReq)
	if err != nil {
		return nil, fmt.Errorf("convert chat to responses: %w", err)
	}

	responsesBody, err := json.Marshal(responsesReq)
	if err != nil {
		return nil, fmt.Errorf("marshal responses request: %w", err)
	}

	return s.forwardConverted(ctx, c, account, responsesBody, chatReq.Model, chatReq.Stream,
		copilotForwardConfig{
			upstreamURL:  copilotUpstreamResponsesURL,
			extractUsage: responsesUsage,
			detectInit:   detectInitiatorResponses,
			detectVision: detectVisionResponses,
		},
		convertedResponseHandler{
			handleStream:    s.handleResponsesToChatStream,
			handleNonStream: s.handleResponsesToChatNonStream,
		},
	)
}

// handleResponsesToChatNonStream reads a Responses API response from upstream,
// converts it to Chat Completions format, and writes it to the client.
func (s *CopilotGatewayService) handleResponsesToChatNonStream(c *gin.Context, resp *http.Response, model string, start time.Time) (*CopilotForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read upstream response: %w", err)
	}

	var responsesResp apicompat.ResponsesResponse
	if err := json.Unmarshal(respBody, &responsesResp); err != nil {
		return nil, fmt.Errorf("parse responses response: %w", err)
	}

	chatResp := apicompat.ResponsesToChat(&responsesResp)
	chatResp.Model = model

	var usage CopilotUsage
	if responsesResp.Usage != nil {
		usage = CopilotUsage{
			PromptTokens:     responsesResp.Usage.InputTokens,
			CompletionTokens: responsesResp.Usage.OutputTokens,
			TotalTokens:      responsesResp.Usage.TotalTokens,
		}
	}

	responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.cfg.Security.ResponseHeaders)
	c.JSON(http.StatusOK, chatResp)

	return &CopilotForwardResult{
		RequestID: requestID,
		Usage:     usage,
		Model:     model,
		Stream:    false,
		Duration:  time.Since(start),
	}, nil
}

// handleResponsesToChatStream reads Responses SSE events from upstream,
// converts each to Chat Completions SSE chunks, and writes them to the client.
func (s *CopilotGatewayService) handleResponsesToChatStream(c *gin.Context, resp *http.Response, model string, start time.Time) (*CopilotForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.cfg.Security.ResponseHeaders)
	c.Writer.WriteHeader(http.StatusOK)

	state := apicompat.NewResponsesToChatStreamState()
	var usage CopilotUsage
	var firstTokenMs *int
	firstChunk := true
	finishSent := false
	completionEventReceived := false // Track if we received response.completed/incomplete event

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	logger.L().Info("copilot responses-to-chat stream: started",
		zap.String("request_id", requestID),
		zap.String("model", model),
	)

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
			continue
		}
		payload := line[6:]

		if firstChunk {
			firstChunk = false
			ms := int(time.Since(start).Milliseconds())
			firstTokenMs = &ms
		}

		// Parse the Responses SSE event
		var event apicompat.ResponsesStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			logger.L().Warn("copilot responses-to-chat stream: failed to parse event",
				zap.Error(err),
				zap.String("request_id", requestID),
				zap.String("payload_preview", truncate(string(payload), 256)),
			)
			continue
		}

		// Enhanced logging: Log all event types to diagnose missing usage
		if event.Type == "response.completed" || event.Type == "response.incomplete" {
			completionEventReceived = true
			hasUsage := event.Response != nil && event.Response.Usage != nil
			logger.L().Info("copilot responses-to-chat stream: completion event",
				zap.String("request_id", requestID),
				zap.String("event_type", event.Type),
				zap.Bool("has_response", event.Response != nil),
				zap.Bool("has_usage", hasUsage),
				zap.String("payload_preview", truncate(string(payload), 512)),
			)
			if hasUsage {
				logger.L().Info("copilot responses-to-chat stream: usage extracted",
					zap.String("request_id", requestID),
					zap.Int("input_tokens", event.Response.Usage.InputTokens),
					zap.Int("output_tokens", event.Response.Usage.OutputTokens),
					zap.Int("total_tokens", event.Response.Usage.TotalTokens),
				)
			} else {
				// CRITICAL: Log when completion event has no usage - this is the bug!
				logger.L().Warn("copilot responses-to-chat stream: completion event WITHOUT usage",
					zap.String("request_id", requestID),
					zap.String("event_type", event.Type),
					zap.String("payload_full", string(payload)),
				)
			}
		} else if event.Type != "" {
			// Log other event types for diagnosis (at debug level to avoid spam)
			logger.L().Debug("copilot responses-to-chat stream: event received",
				zap.String("request_id", requestID),
				zap.String("event_type", event.Type),
			)
		}

		// Convert to Chat Completions chunks
		chunks := apicompat.ResponsesEventToChatChunks(&event, state)
		for _, chunk := range chunks {
			chunk.Model = model
			// Extract usage from the final chunk if present
			if chunk.Usage != nil {
				usage = CopilotUsage{
					PromptTokens:     chunk.Usage.PromptTokens,
					CompletionTokens: chunk.Usage.CompletionTokens,
					TotalTokens:      chunk.Usage.TotalTokens,
				}
			}
			// Track if a finish_reason was sent
			for _, ch := range chunk.Choices {
				if ch.FinishReason != nil {
					finishSent = true
				}
			}
			sse, err := apicompat.ResponsesStreamEventToSSE(chunk)
			if err != nil {
				logger.L().Warn("copilot responses-to-chat stream: failed to marshal chunk",
					zap.Error(err),
					zap.String("request_id", requestID),
				)
				continue
			}
			fmt.Fprint(c.Writer, sse)
		}
		c.Writer.Flush()
	}

	if err := scanner.Err(); err != nil {
		logger.L().Warn("copilot responses-to-chat stream read error",
			zap.Error(err),
			zap.String("request_id", requestID),
		)
	}

	// CRITICAL: Log if stream ended without completion event.
	// For Codex models, this is expected behavior (they don't send response.completed).
	// For other models, this indicates a potential upstream issue.
	if !completionEventReceived {
		isCodexModel := strings.Contains(strings.ToLower(model), "codex")
		if isCodexModel {
			// Codex models don't send response.completed, usage must be fetched from Usage API
			logger.L().Info("copilot responses-to-chat stream: ended without completion event (expected for Codex models)",
				zap.String("request_id", requestID),
				zap.String("model", model),
				zap.Bool("first_chunk_received", !firstChunk),
				zap.Bool("finish_reason_sent", finishSent),
				zap.Int("final_prompt_tokens", usage.PromptTokens),
				zap.Int("final_completion_tokens", usage.CompletionTokens),
				zap.String("note", "usage not available in stream, use Usage API"),
			)
		} else {
			logger.L().Error("copilot responses-to-chat stream: ended WITHOUT completion event",
				zap.String("request_id", requestID),
				zap.String("model", model),
				zap.Bool("first_chunk_received", !firstChunk),
				zap.Bool("finish_reason_sent", finishSent),
				zap.Int("final_prompt_tokens", usage.PromptTokens),
				zap.Int("final_completion_tokens", usage.CompletionTokens),
			)
		}
	}

	// If upstream disconnected without a finish_reason, synthesize one.
	if !finishSent && !firstChunk {
		fr := "stop"
		finalChunk := apicompat.ChatStreamChunk{
			ID:      state.ResponseID,
			Object:  "chat.completion.chunk",
			Created: state.Created,
			Model:   model,
			Choices: []apicompat.ChatStreamChoice{{
				Index:        0,
				FinishReason: &fr,
			}},
		}
		if sse, err := apicompat.ResponsesStreamEventToSSE(finalChunk); err == nil {
			fmt.Fprint(c.Writer, sse)
		}
	}

	// Send [DONE] sentinel
	fmt.Fprint(c.Writer, "data: [DONE]\n\n")
	c.Writer.Flush()

	// Log final usage (Info level for production visibility)
	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 {
		logger.L().Warn("copilot responses-to-chat stream: final usage is ZERO",
			zap.String("request_id", requestID),
			zap.String("model", model),
			zap.Int("prompt_tokens", usage.PromptTokens),
			zap.Int("completion_tokens", usage.CompletionTokens),
			zap.Bool("completion_event_received", completionEventReceived),
			zap.Bool("finish_reason_sent", finishSent),
		)
	} else {
		logger.L().Info("copilot responses-to-chat stream: final usage",
			zap.String("request_id", requestID),
			zap.String("model", model),
			zap.Int("prompt_tokens", usage.PromptTokens),
			zap.Int("completion_tokens", usage.CompletionTokens),
			zap.Int("total_tokens", usage.TotalTokens),
		)
	}

	return &CopilotForwardResult{
		RequestID:    requestID,
		Usage:        usage,
		Model:        model,
		Stream:       true,
		Duration:     time.Since(start),
		FirstTokenMs: firstTokenMs,
	}, nil
}

// handleAnthropicNonStreamResponse reads a Chat Completions response from
// upstream, converts it to Anthropic Messages format, and writes it to the client.
func (s *CopilotGatewayService) handleAnthropicNonStreamResponse(c *gin.Context, resp *http.Response, model string, start time.Time) (*CopilotForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read upstream response: %w", err)
	}

	var chatResp apicompat.ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("parse chat response: %w", err)
	}

	anthropicResp := apicompat.ChatToAnthropic(&chatResp, model)

	var usage CopilotUsage
	if chatResp.Usage != nil {
		usage = CopilotUsage{
			PromptTokens:     chatResp.Usage.PromptTokens,
			CompletionTokens: chatResp.Usage.CompletionTokens,
			TotalTokens:      chatResp.Usage.TotalTokens,
		}
	}

	responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.cfg.Security.ResponseHeaders)
	c.JSON(http.StatusOK, anthropicResp)

	return &CopilotForwardResult{
		RequestID: requestID,
		Usage:     usage,
		Model:     model,
		Stream:    false,
		Duration:  time.Since(start),
	}, nil
}

// handleAnthropicStreamResponse reads Chat Completions SSE chunks from upstream,
// converts each to Anthropic SSE events, and writes them to the client.
func (s *CopilotGatewayService) handleAnthropicStreamResponse(c *gin.Context, resp *http.Response, model string, start time.Time) (*CopilotForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.cfg.Security.ResponseHeaders)
	c.Writer.WriteHeader(http.StatusOK)

	state := apicompat.NewChatToAnthropicStreamState()
	var usage CopilotUsage
	var firstTokenMs *int
	firstChunk := true

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
			continue
		}
		payload := line[6:]

		// Track first token timing
		if firstChunk {
			firstChunk = false
			ms := int(time.Since(start).Milliseconds())
			firstTokenMs = &ms
		}

		// Extract usage from chunk
		u := gjson.Get(payload, "usage")
		if u.Exists() {
			usage = chatCompletionsUsage(u)
		}

		// Parse the Chat chunk
		var chunk apicompat.ChatStreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			logger.L().Warn("copilot anthropic stream: failed to parse chunk",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
			continue
		}

		// Override model to return the original Anthropic model name
		chunk.Model = model

		// Convert to Anthropic events
		events := apicompat.ChatChunkToAnthropicEvents(&chunk, state)
		for _, evt := range events {
			sse, err := apicompat.ChatStreamEventToSSE(evt)
			if err != nil {
				logger.L().Warn("copilot anthropic stream: failed to marshal event",
					zap.Error(err),
					zap.String("request_id", requestID),
				)
				continue
			}
			fmt.Fprint(c.Writer, sse)
		}
		c.Writer.Flush()
	}

	if err := scanner.Err(); err != nil {
		logger.L().Warn("copilot anthropic stream read error",
			zap.Error(err),
			zap.String("request_id", requestID),
		)
	}

	// Ensure the Anthropic stream is properly terminated even if upstream
	// disconnected without sending a finish_reason chunk.
	if finalEvents := apicompat.FinalizeAnthropicStream(state); len(finalEvents) > 0 {
		for _, evt := range finalEvents {
			sse, err := apicompat.ChatStreamEventToSSE(evt)
			if err != nil {
				continue
			}
			fmt.Fprint(c.Writer, sse)
		}
		c.Writer.Flush()
	}

	// Debug: log final usage before returning
	logger.L().Debug("copilot anthropic stream: final usage",
		zap.String("request_id", requestID),
		zap.Int("prompt_tokens", usage.PromptTokens),
		zap.Int("completion_tokens", usage.CompletionTokens),
		zap.Int("total_tokens", usage.TotalTokens),
		zap.Bool("is_zero_usage", usage.PromptTokens == 0 && usage.CompletionTokens == 0),
	)

	return &CopilotForwardResult{
		RequestID:    requestID,
		Usage:        usage,
		Model:        model,
		Stream:       true,
		Duration:     time.Since(start),
		FirstTokenMs: firstTokenMs,
	}, nil
}

// ensureResponsesInclude adds "reasoning.encrypted_content" to the include array if not present.
func ensureResponsesInclude(body []byte) []byte {
	const target = "reasoning.encrypted_content"
	includes := gjson.GetBytes(body, "include")
	if includes.Exists() && includes.IsArray() {
		for _, v := range includes.Array() {
			if v.String() == target {
				return body // already present
			}
		}
		// Append to existing array
		body, _ = sjson.SetBytes(body, "include.-1", target)
		return body
	}
	// No include field — create it
	body, _ = sjson.SetBytes(body, "include", []string{target})
	return body
}

// forward is the shared implementation for both endpoints.
func (s *CopilotGatewayService) forward(ctx context.Context, c *gin.Context, account *Account, body []byte, cfg copilotForwardConfig) (*CopilotForwardResult, error) {
	start := time.Now()

	model := gjson.GetBytes(body, "model").String()
	isStream := gjson.GetBytes(body, "stream").Bool()

	mappedModel := account.GetMappedModel(model)
	logger.L().Info("copilot model mapping applied",
		zap.Int64("account_id", account.ID),
		zap.String("requested_model", model),
		zap.String("mapped_model", mappedModel),
		zap.Bool("was_mapped", mappedModel != model),
	)
	if mappedModel != model {
		var err error
		body, err = sjson.SetBytes(body, "model", mappedModel)
		if err != nil {
			return nil, fmt.Errorf("replace model in body: %w", err)
		}
	}

	token, err := s.copilotTokenProvider.GetAccessToken(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("get copilot access token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.upstreamURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create upstream request: %w", err)
	}
	s.setCopilotBaseHeaders(req, token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-initiator", cfg.detectInit(body))
	if cfg.detectVision(body) {
		req.Header.Set("Copilot-Vision-Request", "true")
	}

	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return nil, fmt.Errorf("copilot upstream request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode >= 400 {
		return s.handleErrorResponse(c, resp, account, mappedModel, isStream, start)
	}

	if isStream {
		return s.handleStreamResponse(c, resp, mappedModel, start, cfg.extractUsage)
	}
	return s.handleNonStreamResponse(c, resp, mappedModel, start, cfg.extractUsage)
}

// shouldFailoverCopilot returns true for status codes that should trigger account failover.
func shouldFailoverCopilot(statusCode int) bool {
	switch statusCode {
	case 401, 402, 403, 429:
		return true
	default:
		return statusCode >= 500
	}
}

// HandleResponsesError processes an error response from the upstream /responses
// endpoint. It checks error passthrough rules, determines whether to failover,
// and writes the appropriate error response to the client. This is the exported
// counterpart of handleErrorResponse, used by the ResponsesHandler when handling
// raw upstream responses.
func (s *CopilotGatewayService) HandleResponsesError(c *gin.Context, resp *http.Response, account *Account, model string, stream bool, start time.Time) (*CopilotForwardResult, error) {
	return s.handleErrorResponse(c, resp, account, model, stream, start)
}

// handleErrorResponse checks error passthrough rules, then either returns
// UpstreamFailoverError for retriable codes or writes the error to the client.
func (s *CopilotGatewayService) handleErrorResponse(c *gin.Context, resp *http.Response, account *Account, model string, stream bool, start time.Time) (*CopilotForwardResult, error) {
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read error response: %w", err)
	}

	requestID := resp.Header.Get("x-request-id")
	logger.L().Warn("copilot upstream error",
		zap.Int("status", resp.StatusCode),
		zap.String("model", model),
		zap.String("request_id", requestID),
		zap.Int64("account_id", account.ID),
		zap.String("body", truncate(string(respBody), 512)),
	)

	// Check error passthrough rules — if matched, write response and return non-failover error
	if status, errType, errMsg, matched := applyErrorPassthroughRule(
		c, PlatformCopilot, resp.StatusCode, respBody,
		http.StatusBadGateway, "upstream_error", "Upstream request failed",
	); matched {
		c.JSON(status, gin.H{
			"error": gin.H{"type": errType, "message": errMsg},
		})
		return nil, fmt.Errorf("upstream error: %d (passthrough rule matched)", resp.StatusCode)
	}

	// Retriable codes → failover
	if shouldFailoverCopilot(resp.StatusCode) {
		return nil, &UpstreamFailoverError{
			StatusCode:   resp.StatusCode,
			ResponseBody: respBody,
		}
	}

	// Non-retriable error → write directly to client
	responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.cfg.Security.ResponseHeaders)
	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)

	return &CopilotForwardResult{
		RequestID: requestID,
		Model:     model,
		Stream:    stream,
		Duration:  time.Since(start),
	}, nil
}

// handleStreamResponse reads SSE events from upstream and forwards them to the client,
// extracting usage via the provided extractor.
func (s *CopilotGatewayService) handleStreamResponse(c *gin.Context, resp *http.Response, model string, start time.Time, extract usageExtractor) (*CopilotForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.cfg.Security.ResponseHeaders)
	c.Writer.WriteHeader(http.StatusOK)

	var usage CopilotUsage
	var firstTokenMs *int
	firstChunk := true

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if firstChunk && strings.HasPrefix(line, "data: ") && line != "data: [DONE]" {
			firstChunk = false
			ms := int(time.Since(start).Milliseconds())
			firstTokenMs = &ms
		}

		if strings.HasPrefix(line, "data: ") && line != "data: [DONE]" {
			payload := line[6:]
			// Try Chat Completions format (usage at top level)
			u := gjson.Get(payload, "usage")
			if u.Exists() {
				usage = extract(u)
			} else {
				// Try Responses API format (usage nested in response.usage)
				u = gjson.Get(payload, "response.usage")
				if u.Exists() {
					// Convert Responses API format to Chat Completions format
					pt := int(u.Get("input_tokens").Int())
					ct := int(u.Get("output_tokens").Int())
					usage = CopilotUsage{PromptTokens: pt, CompletionTokens: ct, TotalTokens: pt + ct}
				}
			}
		}

		fmt.Fprintf(c.Writer, "%s\n", line)
		c.Writer.Flush()
	}

	if err := scanner.Err(); err != nil {
		logger.L().Warn("copilot stream read error",
			zap.Error(err),
			zap.String("request_id", requestID),
		)
	}

	// Debug: log final usage before returning
	logger.L().Debug("copilot chat completions stream: final usage",
		zap.String("request_id", requestID),
		zap.Int("prompt_tokens", usage.PromptTokens),
		zap.Int("completion_tokens", usage.CompletionTokens),
		zap.Int("total_tokens", usage.TotalTokens),
		zap.Bool("is_zero_usage", usage.PromptTokens == 0 && usage.CompletionTokens == 0),
	)

	return &CopilotForwardResult{
		RequestID:    requestID,
		Usage:        usage,
		Model:        model,
		Stream:       true,
		Duration:     time.Since(start),
		FirstTokenMs: firstTokenMs,
	}, nil
}

// handleNonStreamResponse reads the full upstream response, extracts usage, and writes it back.
func (s *CopilotGatewayService) handleNonStreamResponse(c *gin.Context, resp *http.Response, model string, start time.Time, extract usageExtractor) (*CopilotForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read upstream response: %w", err)
	}

	var usage CopilotUsage
	u := gjson.GetBytes(respBody, "usage")
	if u.Exists() {
		usage = extract(u)
		logger.L().Debug("copilot non-stream: usage extracted from response",
			zap.String("request_id", requestID),
			zap.Int("prompt_tokens", usage.PromptTokens),
			zap.Int("completion_tokens", usage.CompletionTokens),
			zap.Int("total_tokens", usage.TotalTokens),
		)
	} else {
		logger.L().Debug("copilot non-stream: no usage field in response",
			zap.String("request_id", requestID),
			zap.String("body_preview", truncate(string(respBody), 512)),
		)
	}

	responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.cfg.Security.ResponseHeaders)
	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)

	return &CopilotForwardResult{
		RequestID: requestID,
		Usage:     usage,
		Model:     model,
		Stream:    false,
		Duration:  time.Since(start),
	}, nil
}

// detectInitiator returns "user" if the last message role is "user", otherwise "agent".
// Used for /chat/completions requests.
func detectInitiator(body []byte) string {
	messages := gjson.GetBytes(body, "messages")
	if !messages.Exists() || !messages.IsArray() {
		return "user"
	}
	arr := messages.Array()
	if len(arr) == 0 {
		return "user"
	}
	last := arr[len(arr)-1]
	if last.Get("role").String() == "user" {
		return "user"
	}
	return "agent"
}

// detectVision checks if any message contains an image_url content part.
// Used for /chat/completions requests.
func detectVision(body []byte) bool {
	messages := gjson.GetBytes(body, "messages")
	if !messages.Exists() {
		return false
	}
	for _, msg := range messages.Array() {
		content := msg.Get("content")
		if !content.IsArray() {
			continue
		}
		for _, part := range content.Array() {
			if part.Get("type").String() == "image_url" {
				return true
			}
		}
	}
	return false
}

// detectInitiatorResponses returns "user" or "agent" for Responses API requests.
// Checks the "input" array (Responses API format).
func detectInitiatorResponses(body []byte) string {
	input := gjson.GetBytes(body, "input")
	if !input.Exists() || !input.IsArray() {
		return "user"
	}
	arr := input.Array()
	if len(arr) == 0 {
		return "user"
	}
	last := arr[len(arr)-1]
	if last.Get("role").String() == "user" {
		return "user"
	}
	return "agent"
}

// detectVisionResponses checks if any input item contains an input_image content part.
// Used for Responses API requests.
func detectVisionResponses(body []byte) bool {
	input := gjson.GetBytes(body, "input")
	if !input.Exists() {
		return false
	}
	for _, item := range input.Array() {
		content := item.Get("content")
		if !content.IsArray() {
			continue
		}
		for _, part := range content.Array() {
			if part.Get("type").String() == "input_image" {
				return true
			}
		}
	}
	return false
}

// FetchModelsFromUpstream fetches the model list from GitHub Copilot upstream
// using any available schedulable copilot account.
func (s *CopilotGatewayService) FetchModelsFromUpstream(ctx context.Context) ([]byte, error) {
	accounts, err := s.accountRepo.ListSchedulableByPlatform(ctx, PlatformCopilot)
	if err != nil {
		return nil, fmt.Errorf("list copilot accounts: %w", err)
	}
	if len(accounts) == 0 {
		return nil, fmt.Errorf("no schedulable copilot accounts")
	}
	return s.FetchModels(ctx, &accounts[0])
}

// FetchModels fetches the model list from GitHub Copilot upstream.
// GET https://api.githubcopilot.com/models with opencode-style headers.
func (s *CopilotGatewayService) FetchModels(ctx context.Context, account *Account) ([]byte, error) {
	token, err := s.copilotTokenProvider.GetAccessToken(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("get copilot access token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, copilotUpstreamModelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create models request: %w", err)
	}
	s.setCopilotBaseHeaders(req, token)
	req.Header.Set("x-initiator", "user")

	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return nil, fmt.Errorf("copilot models request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read models response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("copilot models returned %d: %s", resp.StatusCode, truncate(string(body), 512))
	}
	return body, nil
}

// truncate returns s truncated to maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
