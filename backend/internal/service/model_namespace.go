package service

import "strings"

type ModelNamespace struct {
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
		return ModelNamespace{Model: trimmed}
	}

	prefix := strings.ToLower(strings.TrimSpace(trimmed[:idx]))
	rest := strings.TrimSpace(trimmed[idx+1:])
	if rest == "" {
		return ModelNamespace{Model: trimmed}
	}

	platform := normalizeNamespacePlatform(prefix)
	if platform == "" {
		return ModelNamespace{Model: trimmed}
	}

	return ModelNamespace{Platform: platform, Model: rest, HasNamespace: true}
}

func normalizeNamespacePlatform(prefix string) string {
	switch strings.ToLower(strings.TrimSpace(prefix)) {
	case PlatformOpenAI:
		return PlatformOpenAI
	case PlatformCopilot:
		return PlatformCopilot
	case PlatformAggregator:
		return PlatformAggregator
	case PlatformGemini:
		return PlatformGemini
	case PlatformAntigravity:
		return PlatformAntigravity
	case PlatformAnthropic:
		return PlatformAnthropic
	case "claude":
		return PlatformAnthropic
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
