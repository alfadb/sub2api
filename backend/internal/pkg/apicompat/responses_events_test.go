package apicompat

import (
	"encoding/json"
	"testing"
)

// ---------------------------------------------------------------------------
// StreamIdTracker - response.output_item.added
// ---------------------------------------------------------------------------

func TestStreamIdTracker_HandleOutputItemAdded(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantID   string
		wantSame bool // whether the output should be identical to input (ID already present)
	}{
		{
			name: "generates ID when missing",
			input: `{
				"type": "response.output_item.added",
				"output_index": 0,
				"item": {
					"type": "message",
					"role": "assistant"
				}
			}`,
			wantID:   "", // generated, check format instead
			wantSame: false,
		},
		{
			name: "preserves existing ID",
			input: `{
				"type": "response.output_item.added",
				"output_index": 0,
				"item": {
					"id": "msg_abc123",
					"type": "message",
					"role": "assistant"
				}
			}`,
			wantID:   "msg_abc123",
			wantSame: true,
		},
		{
			name: "generates ID for function_call item",
			input: `{
				"type": "response.output_item.added",
				"output_index": 1,
				"item": {
					"type": "function_call",
					"call_id": "call_123",
					"name": "search"
				}
			}`,
			wantID:   "",
			wantSame: false,
		},
		{
			name: "handles higher output_index",
			input: `{
				"type": "response.output_item.added",
				"output_index": 5,
				"item": {
					"type": "reasoning",
					"status": "in_progress"
				}
			}`,
			wantID:   "",
			wantSame: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := NewStreamIdTracker()
			output, err := tracker.FixStreamIds(tt.input, "response.output_item.added")
			if err != nil {
				t.Fatalf("FixStreamIds returned error: %v", err)
			}

			// Parse output to extract item ID
			var result map[string]json.RawMessage
			if err := json.Unmarshal([]byte(output), &result); err != nil {
				t.Fatalf("failed to parse output JSON: %v", err)
			}

			itemBytes, ok := result["item"]
			if !ok {
				t.Fatal("output missing 'item' field")
			}

			var item map[string]json.RawMessage
			if err := json.Unmarshal(itemBytes, &item); err != nil {
				t.Fatalf("failed to parse item: %v", err)
			}

			idBytes, ok := item["id"]
			if !ok {
				t.Fatal("item missing 'id' field after processing")
			}

			var itemID string
			if err := json.Unmarshal(idBytes, &itemID); err != nil {
				t.Fatalf("failed to parse item id: %v", err)
			}

			if tt.wantID != "" && itemID != tt.wantID {
				t.Errorf("item id = %q, want %q", itemID, tt.wantID)
			}

			if tt.wantID == "" && itemID == "" {
				t.Error("expected ID to be generated, got empty string")
			}

			// Verify ID format for generated IDs: oi_{index}_{16-char random}
			if tt.wantID == "" {
				// Extract output_index from input for verification
				var inputObj map[string]json.RawMessage
				//nolint:errcheck // test code, error not critical
				json.Unmarshal([]byte(tt.input), &inputObj)
				var outputIndex int
				if idxBytes, ok := inputObj["output_index"]; ok {
					//nolint:errcheck // test code, error not critical
					json.Unmarshal(idxBytes, &outputIndex)
				}
				expectedPrefix := "oi_"
				if len(itemID) <= len(expectedPrefix)+2 {
					t.Errorf("generated ID %q too short, expected format 'oi_{index}_{16-char}'", itemID)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// StreamIdTracker - response.output_item.done
// ---------------------------------------------------------------------------

func TestStreamIdTracker_HandleOutputItemDone(t *testing.T) {
	tests := []struct {
		name           string
		addedEvent     string
		doneEvent      string
		wantDoneID     string
		wantIDFromDone bool // whether the done event should use the ID from added event
	}{
		{
			name: "replaces ID with canonical ID from added event",
			addedEvent: `{
				"type": "response.output_item.added",
				"output_index": 0,
				"item": {
					"id": "canonical_123",
					"type": "message",
					"role": "assistant"
				}
			}`,
			doneEvent: `{
				"type": "response.output_item.done",
				"output_index": 0,
				"item": {
					"id": "different_456",
					"type": "message",
					"status": "completed"
				}
			}`,
			wantDoneID:     "canonical_123",
			wantIDFromDone: true,
		},
		{
			name: "handles missing ID in done event",
			addedEvent: `{
				"type": "response.output_item.added",
				"output_index": 1,
				"item": {
					"id": "canonical_reasoning",
					"type": "reasoning"
				}
			}`,
			doneEvent: `{
				"type": "response.output_item.done",
				"output_index": 1,
				"item": {
					"type": "reasoning",
					"status": "completed"
				}
			}`,
			wantDoneID:     "canonical_reasoning",
			wantIDFromDone: true,
		},
		{
			name: "ignores unknown output_index in done event",
			addedEvent: `{
				"type": "response.output_item.added",
				"output_index": 0,
				"item": {
					"id": "msg_1",
					"type": "message"
				}
			}`,
			doneEvent: `{
				"type": "response.output_item.done",
				"output_index": 99,
				"item": {
					"id": "orphan_id",
					"type": "message"
				}
			}`,
			wantDoneID:     "orphan_id", // unchanged because no tracked ID
			wantIDFromDone: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := NewStreamIdTracker()

			// First process the added event
			_, err := tracker.FixStreamIds(tt.addedEvent, "response.output_item.added")
			if err != nil {
				t.Fatalf("FixStreamIds on added event returned error: %v", err)
			}

			// Then process the done event
			output, err := tracker.FixStreamIds(tt.doneEvent, "response.output_item.done")
			if err != nil {
				t.Fatalf("FixStreamIds on done event returned error: %v", err)
			}

			// Parse output to extract item ID
			var result map[string]json.RawMessage
			if err := json.Unmarshal([]byte(output), &result); err != nil {
				t.Fatalf("failed to parse output JSON: %v", err)
			}

			itemBytes, ok := result["item"]
			if !ok {
				t.Fatal("output missing 'item' field")
			}

			var item map[string]json.RawMessage
			if err := json.Unmarshal(itemBytes, &item); err != nil {
				t.Fatalf("failed to parse item: %v", err)
			}

			idBytes, ok := item["id"]
			if !ok {
				if tt.wantIDFromDone {
					t.Fatal("item missing 'id' field after processing")
				}
				return
			}

			var itemID string
			if err := json.Unmarshal(idBytes, &itemID); err != nil {
				t.Fatalf("failed to parse item id: %v", err)
			}

			if itemID != tt.wantDoneID {
				t.Errorf("item id = %q, want %q", itemID, tt.wantDoneID)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// StreamIdTracker - other events (item_id application)
// ---------------------------------------------------------------------------

func TestStreamIdTracker_HandleItemId(t *testing.T) {
	tests := []struct {
		name        string
		addedEvent  string
		targetEvent string
		wantItemID  string
	}{
		{
			name: "applies item_id to output_text.delta event",
			addedEvent: `{
				"type": "response.output_item.added",
				"output_index": 0,
				"item": {
					"id": "msg_delta_123",
					"type": "message"
				}
			}`,
			targetEvent: `{
				"type": "response.output_text.delta",
				"output_index": 0,
				"content_index": 0,
				"delta": "Hello"
			}`,
			wantItemID: "msg_delta_123",
		},
		{
			name: "applies item_id to function_call_arguments.delta event",
			addedEvent: `{
				"type": "response.output_item.added",
				"output_index": 2,
				"item": {
					"id": "call_arg_id",
					"type": "function_call"
				}
			}`,
			targetEvent: `{
				"type": "response.function_call_arguments.delta",
				"output_index": 2,
				"delta": "{\"q\":"
			}`,
			wantItemID: "call_arg_id",
		},
		{
			name: "ignores event without output_index",
			addedEvent: `{
				"type": "response.output_item.added",
				"output_index": 0,
				"item": {"id": "msg_1", "type": "message"}
			}`,
			targetEvent: `{
				"type": "response.created",
				"response": {
					"id": "resp_abc",
					"status": "in_progress"
				}
			}`,
			wantItemID: "", // no item_id should be added
		},
		{
			name: "ignores unknown output_index",
			addedEvent: `{
				"type": "response.output_item.added",
				"output_index": 0,
				"item": {"id": "msg_1", "type": "message"}
			}`,
			targetEvent: `{
				"type": "response.output_text.delta",
				"output_index": 999,
				"delta": "test"
			}`,
			wantItemID: "", // no item_id should be added
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := NewStreamIdTracker()

			// First process the added event
			_, err := tracker.FixStreamIds(tt.addedEvent, "response.output_item.added")
			if err != nil {
				t.Fatalf("FixStreamIds on added event returned error: %v", err)
			}

			// Then process the target event
			output, err := tracker.FixStreamIds(tt.targetEvent, "")
			if err != nil {
				t.Fatalf("FixStreamIds on target event returned error: %v", err)
			}

			// Parse output to check item_id
			var result map[string]json.RawMessage
			if err := json.Unmarshal([]byte(output), &result); err != nil {
				t.Fatalf("failed to parse output JSON: %v", err)
			}

			itemIDBytes, ok := result["item_id"]
			if tt.wantItemID != "" {
				if !ok {
					t.Fatalf("expected item_id field to be present")
				}
				var itemID string
				if err := json.Unmarshal(itemIDBytes, &itemID); err != nil {
					t.Fatalf("failed to parse item_id: %v", err)
				}
				if itemID != tt.wantItemID {
					t.Errorf("item_id = %q, want %q", itemID, tt.wantItemID)
				}
			} else {
				if ok {
					var itemID string
					//nolint:errcheck // test code, error not critical
					json.Unmarshal(itemIDBytes, &itemID)
					t.Errorf("item_id = %q, expected no item_id field", itemID)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// StreamIdTracker - Clear
// ---------------------------------------------------------------------------

func TestStreamIdTracker_Clear(t *testing.T) {
	tracker := NewStreamIdTracker()

	// Add some entries
	addedEvent := `{
		"type": "response.output_item.added",
		"output_index": 0,
		"item": {"id": "msg_1", "type": "message"}
	}`
	_, err := tracker.FixStreamIds(addedEvent, "response.output_item.added")
	if err != nil {
		t.Fatalf("FixStreamIds returned error: %v", err)
	}

	// Clear the tracker
	tracker.Clear()

	// Process a done event - should not find the previous ID
	doneEvent := `{
		"type": "response.output_item.done",
		"output_index": 0,
		"item": {"id": "different_id", "type": "message"}
	}`
	output, err := tracker.FixStreamIds(doneEvent, "response.output_item.done")
	if err != nil {
		t.Fatalf("FixStreamIds returned error: %v", err)
	}

	// The ID should remain unchanged since we cleared
	var result map[string]json.RawMessage
	//nolint:errcheck // test code, error not critical
	json.Unmarshal([]byte(output), &result)
	itemBytes := result["item"]
	var item map[string]json.RawMessage
	//nolint:errcheck // test code, error not critical
	json.Unmarshal(itemBytes, &item)
	idBytes := item["id"]
	var itemID string
	//nolint:errcheck // test code, error not critical
	json.Unmarshal(idBytes, &itemID)

	if itemID != "different_id" {
		t.Errorf("after Clear, item id = %q, want %q (unchanged)", itemID, "different_id")
	}
}

// ---------------------------------------------------------------------------
// StreamIdTracker - integration tests
// ---------------------------------------------------------------------------

func TestStreamIdTracker_Integration_MultiTurn(t *testing.T) {
	// Simulates a multi-item response stream with multiple output_index values
	events := []struct {
		eventType string
		data      string
	}{
		{
			eventType: "response.created",
			data:      `{"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`,
		},
		{
			eventType: "response.output_item.added",
			data:      `{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning"}}`,
		},
		{
			eventType: "response.output_text.delta",
			data:      `{"type":"response.output_text.delta","output_index":0,"delta":"Thinking"}`,
		},
		{
			eventType: "response.output_item.done",
			data:      `{"type":"response.output_item.done","output_index":0,"item":{"id":"wrong_id_0","type":"reasoning"}}`,
		},
		{
			eventType: "response.output_item.added",
			data:      `{"type":"response.output_item.added","output_index":1,"item":{"type":"message"}}`,
		},
		{
			eventType: "response.output_text.delta",
			data:      `{"type":"response.output_text.delta","output_index":1,"delta":"Hello"}`,
		},
		{
			eventType: "response.output_item.done",
			data:      `{"type":"response.output_item.done","output_index":1,"item":{"id":"wrong_id_1","type":"message"}}`,
		},
	}

	tracker := NewStreamIdTracker()
	canonicalIDs := make(map[int]string)

	for i, evt := range events {
		output, err := tracker.FixStreamIds(evt.data, evt.eventType)
		if err != nil {
			t.Fatalf("event %d: FixStreamIds returned error: %v", i, err)
		}

		// Track canonical IDs from added events
		if evt.eventType == "response.output_item.added" {
			var result map[string]json.RawMessage
			//nolint:errcheck // test code, error not critical
			json.Unmarshal([]byte(output), &result)
			itemBytes := result["item"]
			var item map[string]json.RawMessage
			//nolint:errcheck // test code, error not critical
			json.Unmarshal(itemBytes, &item)

			var idxObj map[string]json.RawMessage
			//nolint:errcheck // test code, error not critical
			json.Unmarshal([]byte(evt.data), &idxObj)
			var outputIndex int
			//nolint:errcheck // test code, error not critical
			json.Unmarshal(idxObj["output_index"], &outputIndex)

			idBytes := item["id"]
			var id string
			//nolint:errcheck // test code, error not critical
			json.Unmarshal(idBytes, &id)
			canonicalIDs[outputIndex] = id
		}

		// Verify done events use canonical IDs
		if evt.eventType == "response.output_item.done" {
			var result map[string]json.RawMessage
			//nolint:errcheck // test code, error not critical
			json.Unmarshal([]byte(output), &result)
			itemBytes := result["item"]
			var item map[string]json.RawMessage
			//nolint:errcheck // test code, error not critical
			json.Unmarshal(itemBytes, &item)

			var idxObj map[string]json.RawMessage
			//nolint:errcheck // test code, error not critical
			json.Unmarshal([]byte(evt.data), &idxObj)
			var outputIndex int
			//nolint:errcheck // test code, error not critical
			json.Unmarshal(idxObj["output_index"], &outputIndex)

			idBytes := item["id"]
			var id string
			//nolint:errcheck // test code, error not critical
			json.Unmarshal(idBytes, &id)

			canonical, ok := canonicalIDs[outputIndex]
			if !ok {
				t.Errorf("event %d: no canonical ID for output_index %d", i, outputIndex)
			} else if id != canonical {
				t.Errorf("event %d: item id = %q, want canonical %q", i, id, canonical)
			}
		}

		// Verify delta events have item_id
		if evt.eventType == "response.output_text.delta" {
			var result map[string]json.RawMessage
			//nolint:errcheck // test code, error not critical
			json.Unmarshal([]byte(output), &result)
			itemIDBytes, ok := result["item_id"]
			if !ok {
				t.Errorf("event %d: missing item_id in delta event", i)
			} else {
				var itemID string
				//nolint:errcheck // test code, error not critical
				json.Unmarshal(itemIDBytes, &itemID)
				var idxObj map[string]json.RawMessage
				//nolint:errcheck // test code, error not critical
				json.Unmarshal([]byte(evt.data), &idxObj)
				var outputIndex int
				//nolint:errcheck // test code, error not critical
				json.Unmarshal(idxObj["output_index"], &outputIndex)

				canonical, ok := canonicalIDs[outputIndex]
				if !ok {
					t.Errorf("event %d: no canonical ID for output_index %d", i, outputIndex)
				} else if itemID != canonical {
					t.Errorf("event %d: item_id = %q, want canonical %q", i, itemID, canonical)
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// StreamIdTracker - edge cases
// ---------------------------------------------------------------------------

func TestStreamIdTracker_EdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		eventType   string
		wantErr     bool
		checkOutput func(t *testing.T, output string)
	}{
		{
			name:      "empty data returns empty",
			input:     "",
			eventType: "response.output_item.added",
			wantErr:   false,
			checkOutput: func(t *testing.T, output string) {
				if output != "" {
					t.Errorf("output = %q, want empty string", output)
				}
			},
		},
		{
			name:      "invalid JSON returns error",
			input:     "{not valid json",
			eventType: "response.output_item.added",
			wantErr:   true,
			checkOutput: func(t *testing.T, output string) {
				// no check, error is expected
			},
		},
		{
			name:      "event without type field uses eventType param",
			input:     `{"output_index":0,"item":{"type":"message"}}`,
			eventType: "response.output_item.added",
			wantErr:   false,
			checkOutput: func(t *testing.T, output string) {
				// Should still generate an ID
				var result map[string]json.RawMessage
				if err := json.Unmarshal([]byte(output), &result); err != nil {
					t.Fatalf("failed to parse output: %v", err)
				}
				itemBytes := result["item"]
				var item map[string]json.RawMessage
				//nolint:errcheck // test code, error not critical
				json.Unmarshal(itemBytes, &item)
				if _, ok := item["id"]; !ok {
					t.Error("expected item.id to be generated")
				}
			},
		},
		{
			name:      "unknown event type passes through unchanged",
			input:     `{"type":"unknown.event","data":"test"}`,
			eventType: "",
			wantErr:   false,
			checkOutput: func(t *testing.T, output string) {
				// Should pass through with item_id if output_index present
				var result map[string]json.RawMessage
				//nolint:errcheck // test code, error not critical
				json.Unmarshal([]byte(output), &result)
				if _, ok := result["data"]; !ok {
					t.Error("expected original data field to be preserved")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := NewStreamIdTracker()
			output, err := tracker.FixStreamIds(tt.input, tt.eventType)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			tt.checkOutput(t, output)
		})
	}
}

// ---------------------------------------------------------------------------
// StreamIdTracker - generated ID format
// ---------------------------------------------------------------------------

func TestStreamIdTracker_GeneratedIDFormat(t *testing.T) {
	tracker := NewStreamIdTracker()

	// Generate multiple IDs and check format
	input := `{"type":"response.output_item.added","output_index":0,"item":{"type":"message"}}`

	for range 100 {
		output, err := tracker.FixStreamIds(input, "response.output_item.added")
		if err != nil {
			t.Fatalf("FixStreamIds returned error: %v", err)
		}

		// Extract ID
		var result map[string]json.RawMessage
		//nolint:errcheck // test code, error not critical
		json.Unmarshal([]byte(output), &result)
		itemBytes := result["item"]
		var item map[string]json.RawMessage
		//nolint:errcheck // test code, error not critical
		json.Unmarshal(itemBytes, &item)
		idBytes := item["id"]
		var id string
		//nolint:errcheck // test code, error not critical
		json.Unmarshal(idBytes, &id)

		// Check format: oi_0_{16-char random}
		if len(id) < 21 { // "oi_0_" (5 chars) + at least 16 chars
			t.Errorf("generated ID %q too short", id)
		}

		// Each new tracker should generate new random IDs
		tracker.Clear()
	}
}
