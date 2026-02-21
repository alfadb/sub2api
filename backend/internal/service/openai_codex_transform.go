package service

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
)

const (
	opencodeCodexHeaderURL = "https://raw.githubusercontent.com/anomalyco/opencode/dev/packages/opencode/src/session/prompt/codex_header.txt"
	codexCacheTTL          = 15 * time.Minute
)

//go:embed prompts/codex_cli_instructions.md
var codexCLIInstructions string

// 模型规范化模式（基于模式匹配，而非穷举）
var (
	// reasoning 后缀模式：-none, -low, -medium, -high, -xhigh
	reasoningSuffixPattern = regexp.MustCompile(`-(none|low|medium|high|xhigh)$`)
	// 日期版本模式：-2025-12-11
	dateVersionPattern = regexp.MustCompile(`-\d{4}-\d{2}-\d{2}$`)
	// chat-latest 后缀
	chatLatestPattern = regexp.MustCompile(`-chat-latest$`)
)

// codexModelAlias 定义需要特殊处理的模型别名
// key: 原始模型名, value: 规范化后的模型名
var codexModelAlias = map[string]string{
	// gpt-5 → gpt-5.1（旧版本升级）
	"gpt-5":            "gpt-5.1",
	"gpt-5-mini":       "gpt-5.1",
	"gpt-5-nano":       "gpt-5.1",
	"gpt-5-codex":      "gpt-5.1-codex",
	"gpt-5-codex-mini": "gpt-5.1-codex-mini",
	// 特殊别名
	"codex-mini-latest": "gpt-5.1-codex-mini",
}

type codexTransformResult struct {
	Modified        bool
	NormalizedModel string
	PromptCacheKey  string
}

type opencodeCacheMetadata struct {
	ETag        string `json:"etag"`
	LastFetch   string `json:"lastFetch,omitempty"`
	LastChecked int64  `json:"lastChecked"`
}

func applyCodexOAuthTransform(reqBody map[string]any, isCodexCLI bool) codexTransformResult {
	result := codexTransformResult{}
	// 工具续链需求会影响存储策略与 input 过滤逻辑。
	needsToolContinuation := NeedsToolContinuation(reqBody)

	model := ""
	if v, ok := reqBody["model"].(string); ok {
		model = v
	}
	normalizedModel := normalizeCodexModel(model)
	if normalizedModel != "" {
		if model != normalizedModel {
			reqBody["model"] = normalizedModel
			result.Modified = true
		}
		result.NormalizedModel = normalizedModel
	}

	// OAuth 走 ChatGPT internal API 时，store 必须为 false；显式 true 也会强制覆盖。
	// 避免上游返回 "Store must be set to false"。
	if v, ok := reqBody["store"].(bool); !ok || v {
		reqBody["store"] = false
		result.Modified = true
	}
	if v, ok := reqBody["stream"].(bool); !ok || !v {
		reqBody["stream"] = true
		result.Modified = true
	}

	// Strip parameters unsupported by codex models via the Responses API.
	for _, key := range []string{
		"max_output_tokens",
		"max_completion_tokens",
		"temperature",
		"top_p",
		"frequency_penalty",
		"presence_penalty",
	} {
		if _, ok := reqBody[key]; ok {
			delete(reqBody, key)
			result.Modified = true
		}
	}

	if normalizeCodexTools(reqBody) {
		result.Modified = true
	}

	if v, ok := reqBody["prompt_cache_key"].(string); ok {
		result.PromptCacheKey = strings.TrimSpace(v)
	}

	// instructions 处理逻辑：根据是否是 Codex CLI 分别调用不同方法
	if applyInstructions(reqBody, isCodexCLI) {
		result.Modified = true
	}

	// OAuth upstream (ChatGPT internal API) rejects string input; normalize to array format.
	if inputStr, ok := reqBody["input"].(string); ok && strings.TrimSpace(inputStr) != "" {
		reqBody["input"] = []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{
						"type": "input_text",
						"text": inputStr,
					},
				},
			},
		}
		result.Modified = true
	}

	// 续链场景保留 item_reference 与 id，避免 call_id 上下文丢失。
	if input, ok := reqBody["input"].([]any); ok {
		input = filterCodexInput(input, needsToolContinuation)
		reqBody["input"] = input
		result.Modified = true
	}

	return result
}

func normalizeCodexModel(model string) string {
	if model == "" {
		return "gpt-5.1"
	}

	modelID := model
	if strings.Contains(modelID, "/") {
		parts := strings.Split(modelID, "/")
		modelID = parts[len(parts)-1]
	}

	normalized := strings.ToLower(strings.TrimSpace(modelID))
	normalized = strings.ReplaceAll(normalized, " ", "-")

	// 1. 检查特殊别名映射
	if alias, ok := codexModelAlias[normalized]; ok {
		return alias
	}

	// 2. 移除 reasoning 后缀
	normalized = reasoningSuffixPattern.ReplaceAllString(normalized, "")

	// 3. 移除日期版本后缀
	normalized = dateVersionPattern.ReplaceAllString(normalized, "")

	// 4. 移除 chat-latest 后缀
	normalized = chatLatestPattern.ReplaceAllString(normalized, "")

	// 5. 再次检查别名映射（处理移除后缀后的情况）
	if alias, ok := codexModelAlias[normalized]; ok {
		return alias
	}

	// 6. 验证是否为有效的 GPT-5.x 模型格式
	// 支持的格式: gpt-5.1, gpt-5.1-codex, gpt-5.1-codex-max, gpt-5.1-codex-mini, gpt-5.2, gpt-5.2-codex
	validModelPattern := regexp.MustCompile(`^gpt-5\.[12](-codex(-max|-mini)?)?$`)
	if validModelPattern.MatchString(normalized) {
		return normalized
	}

	// 7. 模糊匹配逻辑
	if strings.Contains(normalized, "gpt-5.2-codex") {
		return "gpt-5.2-codex"
	}
	if strings.Contains(normalized, "gpt-5.2") {
		return "gpt-5.2"
	}
	if strings.Contains(normalized, "gpt-5.1-codex-max") {
		return "gpt-5.1-codex-max"
	}
	if strings.Contains(normalized, "gpt-5.1-codex-mini") {
		return "gpt-5.1-codex-mini"
	}
	if strings.Contains(normalized, "gpt-5.1-codex") {
		return "gpt-5.1-codex"
	}
	if strings.Contains(normalized, "gpt-5.1") {
		return "gpt-5.1"
	}
	if strings.Contains(normalized, "codex") {
		return "gpt-5.1-codex"
	}
	if strings.Contains(normalized, "gpt-5") {
		return "gpt-5.1"
	}

	return "gpt-5.1"
}

func getNormalizedCodexModel(modelID string) string {
	if modelID == "" {
		return ""
	}
	// 检查别名映射
	if mapped, ok := codexModelAlias[modelID]; ok {
		return mapped
	}
	lower := strings.ToLower(modelID)
	if mapped, ok := codexModelAlias[lower]; ok {
		return mapped
	}
	return ""
}

func getOpenCodeCachedPrompt(url, cacheFileName, metaFileName string) string {
	cacheDir := codexCachePath("")
	if cacheDir == "" {
		return ""
	}
	cacheFile := filepath.Join(cacheDir, cacheFileName)
	metaFile := filepath.Join(cacheDir, metaFileName)

	var cachedContent string
	if content, ok := readFile(cacheFile); ok {
		cachedContent = content
	}

	var meta opencodeCacheMetadata
	if loadJSON(metaFile, &meta) && meta.LastChecked > 0 && cachedContent != "" {
		if time.Since(time.UnixMilli(meta.LastChecked)) < codexCacheTTL {
			return cachedContent
		}
	}

	content, etag, status, err := fetchWithETag(url, meta.ETag)
	if err == nil && status == http.StatusNotModified && cachedContent != "" {
		return cachedContent
	}
	if err == nil && status >= 200 && status < 300 && content != "" {
		_ = writeFile(cacheFile, content)
		meta = opencodeCacheMetadata{
			ETag:        etag,
			LastFetch:   time.Now().UTC().Format(time.RFC3339),
			LastChecked: time.Now().UnixMilli(),
		}
		_ = writeJSON(metaFile, meta)
		return content
	}

	return cachedContent
}

func getOpenCodeCodexHeader() string {
	// 优先从 opencode 仓库缓存获取指令。
	opencodeInstructions := getOpenCodeCachedPrompt(opencodeCodexHeaderURL, "opencode-codex-header.txt", "opencode-codex-header-meta.json")

	// 若 opencode 指令可用，直接返回。
	if opencodeInstructions != "" {
		return opencodeInstructions
	}

	// 否则回退使用本地 Codex CLI 指令。
	return getCodexCLIInstructions()
}

func getCodexCLIInstructions() string {
	return codexCLIInstructions
}

func GetOpenCodeInstructions() string {
	return getOpenCodeCodexHeader()
}

// GetCodexCLIInstructions 返回内置的 Codex CLI 指令内容。
func GetCodexCLIInstructions() string {
	return getCodexCLIInstructions()
}

// applyInstructions 处理 instructions 字段
// isCodexCLI=true: 仅补充缺失的 instructions（使用 opencode 指令）
// isCodexCLI=false: 优先使用 opencode 指令覆盖
func applyInstructions(reqBody map[string]any, isCodexCLI bool) bool {
	if isCodexCLI {
		return applyCodexCLIInstructions(reqBody)
	}
	return applyOpenCodeInstructions(reqBody)
}

// applyCodexCLIInstructions 为 Codex CLI 请求补充缺失的 instructions
// 仅在 instructions 为空时添加 opencode 指令
func applyCodexCLIInstructions(reqBody map[string]any) bool {
	if !isInstructionsEmpty(reqBody) {
		return false // 已有有效 instructions，不修改
	}

	instructions := strings.TrimSpace(getOpenCodeCodexHeader())
	if instructions != "" {
		reqBody["instructions"] = instructions
		return true
	}

	return false
}

// applyOpenCodeInstructions 为非 Codex CLI 请求应用 opencode 指令
// 优先使用 opencode 指令覆盖
func applyOpenCodeInstructions(reqBody map[string]any) bool {
	instructions := strings.TrimSpace(getOpenCodeCodexHeader())
	existingInstructions, _ := reqBody["instructions"].(string)
	existingInstructions = strings.TrimSpace(existingInstructions)

	if instructions != "" {
		if existingInstructions != instructions {
			reqBody["instructions"] = instructions
			return true
		}
	} else if existingInstructions == "" {
		codexInstructions := strings.TrimSpace(getCodexCLIInstructions())
		if codexInstructions != "" {
			reqBody["instructions"] = codexInstructions
			return true
		}
	}

	return false
}

// isInstructionsEmpty 检查 instructions 字段是否为空
// 处理以下情况：字段不存在、nil、空字符串、纯空白字符串
func isInstructionsEmpty(reqBody map[string]any) bool {
	val, exists := reqBody["instructions"]
	if !exists {
		return true
	}
	if val == nil {
		return true
	}
	str, ok := val.(string)
	if !ok {
		return true
	}
	return strings.TrimSpace(str) == ""
}

// filterCodexInput 按需过滤 item_reference 与 id。
// preserveReferences 为 true 时保持引用与 id，以满足续链请求对上下文的依赖。
func filterCodexInput(input []any, preserveReferences bool) []any {
	filtered := make([]any, 0, len(input))
	for _, item := range input {
		m, ok := item.(map[string]any)
		if !ok {
			filtered = append(filtered, item)
			continue
		}
		typ, _ := m["type"].(string)
		if typ == "item_reference" {
			if !preserveReferences {
				continue
			}
			newItem := make(map[string]any, len(m))
			for key, value := range m {
				newItem[key] = value
			}
			filtered = append(filtered, newItem)
			continue
		}

		newItem := m
		copied := false
		// 仅在需要修改字段时创建副本，避免直接改写原始输入。
		ensureCopy := func() {
			if copied {
				return
			}
			newItem = make(map[string]any, len(m))
			for key, value := range m {
				newItem[key] = value
			}
			copied = true
		}

		if isCodexToolCallItemType(typ) {
			if callID, ok := m["call_id"].(string); !ok || strings.TrimSpace(callID) == "" {
				if id, ok := m["id"].(string); ok && strings.TrimSpace(id) != "" {
					ensureCopy()
					newItem["call_id"] = id
				}
			}
		}

		if !preserveReferences {
			ensureCopy()
			delete(newItem, "id")
			if !isCodexToolCallItemType(typ) {
				delete(newItem, "call_id")
			}
		}

		filtered = append(filtered, newItem)
	}
	return filtered
}

func isCodexToolCallItemType(typ string) bool {
	if typ == "" {
		return false
	}
	return strings.HasSuffix(typ, "_call") || strings.HasSuffix(typ, "_call_output")
}

func normalizeCodexTools(reqBody map[string]any) bool {
	rawTools, ok := reqBody["tools"]
	if !ok || rawTools == nil {
		return false
	}
	tools, ok := rawTools.([]any)
	if !ok {
		return false
	}

	modified := false
	validTools := make([]any, 0, len(tools))

	for _, tool := range tools {
		toolMap, ok := tool.(map[string]any)
		if !ok {
			// Keep unknown structure as-is to avoid breaking upstream behavior.
			validTools = append(validTools, tool)
			continue
		}

		toolType, _ := toolMap["type"].(string)
		toolType = strings.TrimSpace(toolType)
		if toolType != "function" {
			validTools = append(validTools, toolMap)
			continue
		}

		// OpenAI Responses-style tools use top-level name/parameters.
		if name, ok := toolMap["name"].(string); ok && strings.TrimSpace(name) != "" {
			validTools = append(validTools, toolMap)
			continue
		}

		// ChatCompletions-style tools use {type:"function", function:{...}}.
		functionValue, hasFunction := toolMap["function"]
		function, ok := functionValue.(map[string]any)
		if !hasFunction || functionValue == nil || !ok || function == nil {
			// Drop invalid function tools.
			modified = true
			continue
		}

		if _, ok := toolMap["name"]; !ok {
			if name, ok := function["name"].(string); ok && strings.TrimSpace(name) != "" {
				toolMap["name"] = name
				modified = true
			}
		}
		if _, ok := toolMap["description"]; !ok {
			if desc, ok := function["description"].(string); ok && strings.TrimSpace(desc) != "" {
				toolMap["description"] = desc
				modified = true
			}
		}
		if _, ok := toolMap["parameters"]; !ok {
			if params, ok := function["parameters"]; ok {
				toolMap["parameters"] = params
				modified = true
			}
		}
		if _, ok := toolMap["strict"]; !ok {
			if strict, ok := function["strict"]; ok {
				toolMap["strict"] = strict
				modified = true
			}
		}

		validTools = append(validTools, toolMap)
	}

	if modified {
		reqBody["tools"] = validTools
	}

	return modified
}

// copilotUnsupportedToolTypes lists built-in tool types rejected by the
// GitHub Copilot upstream (api.githubcopilot.com).
var copilotUnsupportedToolTypes = map[string]bool{
	"web_search":          true,
	"web_search_20250305": true,
	"web_search_preview":  true,
	"code_interpreter":    true,
	"computer_use":        true,
}

func stripUnsupportedCopilotTools(reqBody map[string]any) bool {
	rawTools, ok := reqBody["tools"]
	if !ok || rawTools == nil {
		return false
	}
	tools, ok := rawTools.([]any)
	if !ok || len(tools) == 0 {
		return false
	}

	filtered := make([]any, 0, len(tools))
	stripped := false
	for _, tool := range tools {
		toolMap, ok := tool.(map[string]any)
		if !ok {
			filtered = append(filtered, tool)
			continue
		}
		toolType, _ := toolMap["type"].(string)
		if copilotUnsupportedToolTypes[toolType] {
			stripped = true
			continue
		}
		filtered = append(filtered, tool)
	}

	if !stripped {
		return false
	}

	if len(filtered) == 0 {
		delete(reqBody, "tools")
	} else {
		reqBody["tools"] = filtered
	}
	return true
}

func codexCachePath(filename string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	cacheDir := filepath.Join(home, ".opencode", "cache")
	if filename == "" {
		return cacheDir
	}
	return filepath.Join(cacheDir, filename)
}

func readFile(path string) (string, bool) {
	if path == "" {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

func writeFile(path, content string) error {
	if path == "" {
		return fmt.Errorf("empty cache path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func loadJSON(path string, target any) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	if err := json.Unmarshal(data, target); err != nil {
		return false
	}
	return true
}

func writeJSON(path string, value any) error {
	if path == "" {
		return fmt.Errorf("empty json path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func fetchWithETag(url, etag string) (string, string, int, error) {
	validatedURL, err := urlvalidator.ValidateHTTPSURL(url, urlvalidator.ValidationOptions{
		AllowedHosts:     []string{"raw.githubusercontent.com"},
		RequireAllowlist: true,
		AllowPrivate:     false,
	})
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid url: %w", err)
	}

	req, err := http.NewRequest(http.MethodGet, validatedURL, nil)
	if err != nil {
		return "", "", 0, err
	}
	req.Header.Set("User-Agent", "sub2api-codex")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	client, err := httpclient.GetClient(httpclient.Options{
		Timeout:            10 * time.Second,
		ValidateResolvedIP: true,
	})
	if err != nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	// #nosec G704 -- validatedURL allowlisted to raw.githubusercontent.com (private hosts blocked); resolved IP validated when available
	resp, err := client.Do(req)
	if err != nil {
		return "", "", 0, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", resp.StatusCode, err
	}
	return string(body), resp.Header.Get("etag"), resp.StatusCode, nil
}
