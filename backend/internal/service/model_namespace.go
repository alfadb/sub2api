package service

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/domain"
)

var gptVersionRe = regexp.MustCompile(`^gpt-(\d+)`)

// ShouldUseCopilotResponsesAPI returns true if the given model should use the
// OpenAI Responses API (/responses) rather than Chat Completions
// (/chat/completions) when routing through GitHub Copilot.
//
// Routing logic (matches opencode's implementation):
//   - GPT-5+ (except gpt-5-mini variants) → Responses API
//   - Everything else (gpt-4.1, gpt-4o, gpt-5-mini, o-series, etc.) → Chat Completions
func ShouldUseCopilotResponsesAPI(modelID string) bool {
	m := strings.ToLower(strings.TrimSpace(modelID))
	match := gptVersionRe.FindStringSubmatch(m)
	if match == nil {
		return false
	}
	version, err := strconv.Atoi(match[1])
	if err != nil {
		return false
	}
	if version < 5 {
		return false
	}
	// gpt-5-mini uses Chat Completions (not Responses API)
	return !strings.HasPrefix(m, "gpt-5-mini")
}

type ModelNamespace struct {
	Provider     string
	Platform     string
	Model        string
	HasNamespace bool
}

func ParseModelNamespace(modelID string) ModelNamespace {
	trimmed := strings.TrimSpace(modelID)
	if trimmed == "" {
		return ModelNamespace{}
	}

	idx := strings.Index(trimmed, "/")
	if idx <= 0 || idx == len(trimmed)-1 {
		return inferFromModelName(trimmed)
	}

	prefix := strings.ToLower(strings.TrimSpace(trimmed[:idx]))
	rest := strings.TrimSpace(trimmed[idx+1:])
	if rest == "" {
		return ModelNamespace{Model: trimmed}
	}

	provider := normalizeNamespaceProvider(prefix)
	if provider == "" {
		return inferFromModelName(trimmed)
	}

	platform := domain.GetPlatformFromProvider(provider)
	if platform == "" {
		platform = inferPlatformFromModel(rest)
	}

	return ModelNamespace{
		Provider:     provider,
		Platform:     platform,
		Model:        rest,
		HasNamespace: true,
	}
}

func inferFromModelName(modelID string) ModelNamespace {
	platform := inferPlatformFromModel(modelID)
	var provider string
	switch platform {
	case domain.PlatformAnthropic:
		provider = domain.ProviderAnthropic
	case domain.PlatformGemini:
		provider = domain.ProviderGemini
	case domain.PlatformOpenAI:
		provider = domain.ProviderOpenAI
	}
	return ModelNamespace{
		Provider: provider,
		Platform: platform,
		Model:    modelID,
	}
}

func inferPlatformFromModel(modelID string) string {
	m := strings.ToLower(modelID)
	switch {
	case IsClaudeModelID(m):
		return domain.PlatformAnthropic
	case IsGeminiModelID(m):
		return domain.PlatformGemini
	default:
		return domain.PlatformOpenAI
	}
}

func normalizeNamespaceProvider(prefix string) string {
	p := strings.ToLower(strings.TrimSpace(prefix))
	switch p {
	case domain.ProviderOpenAI,
		domain.ProviderAzure,
		domain.ProviderCopilot,
		domain.ProviderAnthropic,
		domain.ProviderGemini,
		domain.ProviderVertexAI,
		domain.ProviderAntigravity,
		domain.ProviderBedrock,
		domain.ProviderOpenRouter,
		domain.ProviderAggregator:
		return p
	case "claude":
		return domain.ProviderAnthropic
	case "vertexai", "vertex-ai":
		return domain.ProviderVertexAI
	case "github", "github_copilot":
		return domain.ProviderCopilot
	default:
		return ""
	}
}

func IsClaudeModelID(modelID string) bool {
	m := strings.ToLower(strings.TrimSpace(modelID))
	return strings.HasPrefix(m, "claude-") || strings.HasPrefix(m, "claude_")
}

func IsGeminiModelID(modelID string) bool {
	m := strings.ToLower(strings.TrimSpace(modelID))
	return strings.HasPrefix(m, "gemini-") || strings.HasPrefix(m, "gemini_")
}

func (n ModelNamespace) NamespacedModel() string {
	if n.HasNamespace && n.Provider != "" {
		return n.Provider + "/" + n.Model
	}
	return n.Model
}
