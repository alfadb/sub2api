// Package apicompat provides stream event handling and ID synchronization
// for the OpenAI Responses API streaming protocol.
//
// Stream ID Synchronization for @ai-sdk/openai compatibility
//
// Problem: GitHub Copilot's Responses API returns different IDs for the same
// item in 'added' vs 'done' events. This breaks @ai-sdk/openai which expects
// consistent IDs across the stream lifecycle.
//
// Errors without this fix:
// - "activeReasoningPart.summaryParts" undefined
// - "text part not found"
//
// Use case: OpenCode (AI coding assistant) using Codex models (gpt-5.2-codex)
// via @ai-sdk/openai provider requires the Responses API endpoint.
package apicompat

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// StreamIdTracker maintains the mapping from output_index to the canonical
// item ID established in the 'added' event. This ensures consistent IDs
// across the stream lifecycle when the upstream returns different IDs
// in 'added' and 'done' events (a known GitHub Copilot bug).
type StreamIdTracker struct {
	// outputIndexToID maps output_index → the canonical ID from the added event.
	outputIndexToID map[int]string
}

// NewStreamIdTracker creates a new tracker with an empty ID map.
func NewStreamIdTracker() *StreamIdTracker {
	return &StreamIdTracker{
		outputIndexToID: make(map[int]string),
	}
}

// FixStreamIds processes a raw SSE data line and applies stream ID fixes.
// It parses the JSON, handles the appropriate event type, and re-serializes
// the event with consistent IDs.
//
// Parameters:
//   - data: The raw JSON data string from the SSE event
//   - eventType: The event type from the SSE "event:" line (e.g., "response.output_item.added")
//
// Returns:
//   - The JSON string with applied ID fixes
//   - An error if JSON parsing or serialization fails
func (t *StreamIdTracker) FixStreamIds(data, eventType string) (string, error) {
	if data == "" {
		return data, nil
	}

	var parsed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		return "", fmt.Errorf("failed to parse stream event JSON: %w", err)
	}

	// Extract the type field from the JSON payload if not provided
	if eventType == "" {
		var typeStr string
		if typeBytes, ok := parsed["type"]; ok {
			_ = json.Unmarshal(typeBytes, &typeStr)
			eventType = typeStr
		}
	}

	var result map[string]json.RawMessage
	var err error

	switch eventType {
	case "response.output_item.added":
		result, err = t.handleOutputItemAdded(parsed)
	case "response.output_item.done":
		result, err = t.handleOutputItemDone(parsed)
	default:
		result, err = t.handleItemId(parsed)
	}

	if err != nil {
		return "", err
	}

	fixed, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("failed to marshal fixed event: %w", err)
	}

	return string(fixed), nil
}

// handleOutputItemAdded processes the 'response.output_item.added' event.
// It generates a deterministic ID if missing, records it in the tracker,
// and ensures the event includes the canonical ID.
func (t *StreamIdTracker) handleOutputItemAdded(parsed map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	// Extract output_index
	var outputIndex int
	if idxBytes, ok := parsed["output_index"]; ok {
		_ = json.Unmarshal(idxBytes, &outputIndex)
	}

	// Extract item object
	var item map[string]json.RawMessage
	if itemBytes, ok := parsed["item"]; ok {
		_ = json.Unmarshal(itemBytes, &item)
	}

	// Generate ID if missing
	var itemID string
	if idBytes, ok := item["id"]; ok {
		_ = json.Unmarshal(idBytes, &itemID)
	}

	if itemID == "" {
		itemID = t.generateItemID(outputIndex)
		idBytes, _ := json.Marshal(itemID)
		item["id"] = idBytes
	}

	// Record the canonical ID for this output_index
	t.outputIndexToID[outputIndex] = itemID

	// Marshal item back
	itemBytes, err := json.Marshal(item)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal item: %w", err)
	}
	parsed["item"] = itemBytes

	return parsed, nil
}

// handleOutputItemDone processes the 'response.output_item.done' event.
// It replaces the item ID with the canonical ID recorded during the
// 'added' event to ensure consistency across the stream lifecycle.
func (t *StreamIdTracker) handleOutputItemDone(parsed map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	// Extract output_index
	var outputIndex int
	if idxBytes, ok := parsed["output_index"]; ok {
		_ = json.Unmarshal(idxBytes, &outputIndex)
	}

	// Extract item object
	var item map[string]json.RawMessage
	if itemBytes, ok := parsed["item"]; ok {
		_ = json.Unmarshal(itemBytes, &item)
	}

	// Look up the canonical ID from the added event
	canonicalID, ok := t.outputIndexToID[outputIndex]
	if ok {
		// Replace the item ID with the canonical one
		idBytes, _ := json.Marshal(canonicalID)
		item["id"] = idBytes
	}

	// Marshal item back
	itemBytes, err := json.Marshal(item)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal item: %w", err)
	}
	parsed["item"] = itemBytes

	return parsed, nil
}

// handleItemId processes events that have an output_index field.
// It applies the recorded canonical ID to the item_id field if present.
// This handles events like response.output_text.delta, response.output_text.done,
// response.function_call_arguments.delta, etc.
func (t *StreamIdTracker) handleItemId(parsed map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	// Only process events that have an output_index field
	idxBytes, ok := parsed["output_index"]
	if !ok {
		return parsed, nil
	}

	// Extract output_index
	var outputIndex int
	if err := json.Unmarshal(idxBytes, &outputIndex); err != nil {
		return parsed, nil
	}

	// Look up the canonical ID
	itemID, ok := t.outputIndexToID[outputIndex]
	if !ok {
		return parsed, nil
	}

	// Apply to item_id field
	idBytes, _ := json.Marshal(itemID)
	parsed["item_id"] = idBytes

	return parsed, nil
}

// generateItemID creates a deterministic item ID in the format "oi_{index}_{random}".
// The random suffix is 16 characters using [a-z0-9] charset.
func (t *StreamIdTracker) generateItemID(outputIndex int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	const idLength = 16

	var randomBuilder strings.Builder
	randomBuilder.Grow(idLength)
	for range idLength {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		_ = randomBuilder.WriteByte(charset[n.Int64()])
	}

	return fmt.Sprintf("oi_%d_%s", outputIndex, randomBuilder.String())
}

// Clear resets the tracker's internal state. This should be called
// when starting a new response stream to avoid ID collisions.
func (t *StreamIdTracker) Clear() {
	t.outputIndexToID = make(map[int]string)
}
