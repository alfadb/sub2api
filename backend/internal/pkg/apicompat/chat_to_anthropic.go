package apicompat

import (
	"encoding/json"
	"fmt"
)

// defaultThinkingText is used when reasoning_opaque is present but
// reasoning_text is empty — Claude Code filters out empty thinking blocks.
const defaultThinkingText = "Thinking..."

// ---------------------------------------------------------------------------
// Non-streaming: ChatResponse → AnthropicResponse
// ---------------------------------------------------------------------------

// ChatToAnthropic converts a Chat Completions response into an Anthropic
// Messages response.
func ChatToAnthropic(resp *ChatResponse, model string) *AnthropicResponse {
	out := &AnthropicResponse{
		ID:    resp.ID,
		Type:  "message",
		Role:  "assistant",
		Model: model,
	}

	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		out.Content = buildAnthropicContent(choice.Message)
		out.StopReason = ChatFinishToAnthropic(choice.FinishReason)
	} else {
		out.StopReason = "end_turn"
	}

	if resp.Usage != nil {
		out.Usage = AnthropicUsage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		}
	}

	return out
}

// buildAnthropicContent converts a ChatMessage into Anthropic content blocks.
// Order: thinking → text → tool_use (matches reference implementation).
func buildAnthropicContent(msg ChatMessage) []AnthropicContentBlock {
	var blocks []AnthropicContentBlock

	// Thinking blocks (reasoning passthrough).
	if msg.ReasoningText != "" {
		blocks = append(blocks, AnthropicContentBlock{
			Type:     "thinking",
			Thinking: msg.ReasoningText,
		})
	} else if msg.ReasoningOpaque != "" {
		blocks = append(blocks, AnthropicContentBlock{
			Type:     "thinking",
			Thinking: defaultThinkingText,
		})
	}

	// Text block from message content.
	text := extractChatContentText(msg.Content)
	if text != "" {
		blocks = append(blocks, AnthropicContentBlock{Type: "text", Text: text})
	}

	// Tool use blocks.
	for _, tc := range msg.ToolCalls {
		blocks = append(blocks, AnthropicContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: json.RawMessage(tc.Function.Arguments),
		})
	}

	// Guarantee at least one content block (Anthropic requires non-empty content).
	if len(blocks) == 0 {
		blocks = append(blocks, AnthropicContentBlock{Type: "text", Text: ""})
	}

	return blocks
}

// ---------------------------------------------------------------------------
// Streaming: ChatStreamChunk → []AnthropicStreamEvent
// ---------------------------------------------------------------------------

// ChatToAnthropicStreamState tracks the state machine for converting a
// sequence of Chat SSE chunks into Anthropic SSE events.
type ChatToAnthropicStreamState struct {
	MessageStartSent  bool
	MessageStopSent   bool
	ContentBlockIndex int
	ContentBlockOpen  bool
	ThinkingBlockOpen bool
	// ToolCalls maps OpenAI tool-call index → tracked tool info.
	ToolCalls map[int]trackedToolCall
}

type trackedToolCall struct {
	ID                string
	Name              string
	AnthropicBlockIdx int
}

// NewChatToAnthropicStreamState returns an initialised stream state.
func NewChatToAnthropicStreamState() *ChatToAnthropicStreamState {
	return &ChatToAnthropicStreamState{
		ToolCalls: make(map[int]trackedToolCall),
	}
}

// isToolBlockOpen returns true if the currently open content block is a tool.
func (s *ChatToAnthropicStreamState) isToolBlockOpen() bool {
	if !s.ContentBlockOpen {
		return false
	}
	for _, tc := range s.ToolCalls {
		if tc.AnthropicBlockIdx == s.ContentBlockIndex {
			return true
		}
	}
	return false
}

// ChatChunkToAnthropicEvents converts a single Chat SSE chunk into zero or
// more Anthropic SSE events, updating state as it goes.
func ChatChunkToAnthropicEvents(
	chunk *ChatStreamChunk,
	state *ChatToAnthropicStreamState,
) []AnthropicStreamEvent {
	if len(chunk.Choices) == 0 {
		return nil
	}

	choice := chunk.Choices[0]
	delta := choice.Delta
	var events []AnthropicStreamEvent

	// 1. message_start (once)
	handleChatMessageStart(chunk, state, &events)

	// 2. thinking / reasoning
	handleChatThinking(delta, state, &events)

	// 3. text content
	handleChatContent(delta, state, &events)

	// 4. tool calls
	handleChatToolCalls(delta, state, &events)

	// 5. finish
	handleChatFinish(choice, chunk, state, &events)

	return events
}

func handleChatMessageStart(
	chunk *ChatStreamChunk,
	state *ChatToAnthropicStreamState,
	events *[]AnthropicStreamEvent,
) {
	if state.MessageStartSent {
		return
	}
	inputTokens := 0
	if chunk.Usage != nil {
		inputTokens = chunk.Usage.PromptTokens
	}
	*events = append(*events, AnthropicStreamEvent{
		Type: "message_start",
		Message: &AnthropicResponse{
			ID:      chunk.ID,
			Type:    "message",
			Role:    "assistant",
			Content: []AnthropicContentBlock{},
			Model:   chunk.Model,
			Usage: AnthropicUsage{
				InputTokens:  inputTokens,
				OutputTokens: 0,
			},
		},
	})
	state.MessageStartSent = true
}

func handleChatThinking(
	delta ChatStreamDelta,
	state *ChatToAnthropicStreamState,
	events *[]AnthropicStreamEvent,
) {
	if delta.ReasoningText != "" {
		if !state.ThinkingBlockOpen {
			idx := state.ContentBlockIndex
			*events = append(*events, AnthropicStreamEvent{
				Type:  "content_block_start",
				Index: &idx,
				ContentBlock: &AnthropicContentBlock{
					Type:     "thinking",
					Thinking: "",
				},
			})
			state.ThinkingBlockOpen = true
			state.ContentBlockOpen = true
		}
		idx := state.ContentBlockIndex
		*events = append(*events, AnthropicStreamEvent{
			Type:  "content_block_delta",
			Index: &idx,
			Delta: &AnthropicDelta{
				Type:     "thinking_delta",
				Thinking: delta.ReasoningText,
			},
		})
	}
}

func handleChatContent(
	delta ChatStreamDelta,
	state *ChatToAnthropicStreamState,
	events *[]AnthropicStreamEvent,
) {
	if delta.Content != "" {
		closeThinkingBlock(state, events)

		// Close a tool block if one is open before starting text.
		if state.isToolBlockOpen() {
			idx := state.ContentBlockIndex
			*events = append(*events, AnthropicStreamEvent{
				Type:  "content_block_stop",
				Index: &idx,
			})
			state.ContentBlockIndex++
			state.ContentBlockOpen = false
		}

		if !state.ContentBlockOpen {
			idx := state.ContentBlockIndex
			*events = append(*events, AnthropicStreamEvent{
				Type:  "content_block_start",
				Index: &idx,
				ContentBlock: &AnthropicContentBlock{
					Type: "text",
					Text: "",
				},
			})
			state.ContentBlockOpen = true
		}

		idx := state.ContentBlockIndex
		*events = append(*events, AnthropicStreamEvent{
			Type:  "content_block_delta",
			Index: &idx,
			Delta: &AnthropicDelta{
				Type: "text_delta",
				Text: delta.Content,
			},
		})
	}

	// Handle signature on empty content with reasoning_opaque while thinking is open.
	if delta.Content == "" && delta.ReasoningOpaque != "" && state.ThinkingBlockOpen {
		idx := state.ContentBlockIndex
		*events = append(*events, AnthropicStreamEvent{
			Type:  "content_block_delta",
			Index: &idx,
			Delta: &AnthropicDelta{
				Type:      "signature_delta",
				Signature: delta.ReasoningOpaque,
			},
		})
		stopIdx := state.ContentBlockIndex
		*events = append(*events, AnthropicStreamEvent{
			Type:  "content_block_stop",
			Index: &stopIdx,
		})
		state.ContentBlockIndex++
		state.ThinkingBlockOpen = false
		state.ContentBlockOpen = false
	}
}

func handleChatToolCalls(
	delta ChatStreamDelta,
	state *ChatToAnthropicStreamState,
	events *[]AnthropicStreamEvent,
) {
	if len(delta.ToolCalls) == 0 {
		return
	}

	closeThinkingBlock(state, events)

	// Close any open non-tool block before starting tool blocks.
	if state.ContentBlockOpen && !state.isToolBlockOpen() {
		idx := state.ContentBlockIndex
		*events = append(*events, AnthropicStreamEvent{
			Type:  "content_block_stop",
			Index: &idx,
		})
		state.ContentBlockIndex++
		state.ContentBlockOpen = false
	}

	// Handle reasoning_opaque that arrives with tool_calls.
	handleStreamReasoningOpaque(delta, state, events)

	for _, tc := range delta.ToolCalls {
		// New tool call: has ID and function name.
		if tc.ID != "" && tc.Function.Name != "" {
			if state.ContentBlockOpen {
				idx := state.ContentBlockIndex
				*events = append(*events, AnthropicStreamEvent{
					Type:  "content_block_stop",
					Index: &idx,
				})
				state.ContentBlockIndex++
				state.ContentBlockOpen = false
			}

			anthropicIdx := state.ContentBlockIndex
			state.ToolCalls[tc.Index] = trackedToolCall{
				ID:                tc.ID,
				Name:              tc.Function.Name,
				AnthropicBlockIdx: anthropicIdx,
			}

			*events = append(*events, AnthropicStreamEvent{
				Type:  "content_block_start",
				Index: &anthropicIdx,
				ContentBlock: &AnthropicContentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: json.RawMessage("{}"),
				},
			})
			state.ContentBlockOpen = true
		}

		// Argument delta.
		if tc.Function.Arguments != "" {
			if info, ok := state.ToolCalls[tc.Index]; ok {
				idx := info.AnthropicBlockIdx
				*events = append(*events, AnthropicStreamEvent{
					Type:  "content_block_delta",
					Index: &idx,
					Delta: &AnthropicDelta{
						Type:        "input_json_delta",
						PartialJSON: tc.Function.Arguments,
					},
				})
			}
		}
	}
}

func handleChatFinish(
	choice ChatStreamChoice,
	chunk *ChatStreamChunk,
	state *ChatToAnthropicStreamState,
	events *[]AnthropicStreamEvent,
) {
	if choice.FinishReason == nil || *choice.FinishReason == "" {
		return
	}

	// Close any open content block.
	if state.ContentBlockOpen {
		wasToolBlock := state.isToolBlockOpen()
		idx := state.ContentBlockIndex
		*events = append(*events, AnthropicStreamEvent{
			Type:  "content_block_stop",
			Index: &idx,
		})
		state.ContentBlockOpen = false
		state.ContentBlockIndex++

		// Emit reasoning_opaque after closing a non-tool block.
		if !wasToolBlock {
			handleStreamReasoningOpaque(choice.Delta, state, events)
		}
	}

	// message_delta with stop_reason and final usage.
	outputTokens := 0
	inputTokens := 0
	if chunk.Usage != nil {
		outputTokens = chunk.Usage.CompletionTokens
		inputTokens = chunk.Usage.PromptTokens
	}
	stopReason := ChatFinishToAnthropic(*choice.FinishReason)
	*events = append(*events, AnthropicStreamEvent{
		Type: "message_delta",
		Delta: &AnthropicDelta{
			StopReason: stopReason,
		},
		Usage: &AnthropicUsage{
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
		},
	})

	// message_stop
	*events = append(*events, AnthropicStreamEvent{Type: "message_stop"})
	state.MessageStopSent = true
}

// FinalizeAnthropicStream emits synthetic termination events if the stream
// ended without a proper finish_reason (e.g. upstream disconnect). Returns nil
// if the stream was already properly terminated or never started.
func FinalizeAnthropicStream(state *ChatToAnthropicStreamState) []AnthropicStreamEvent {
	if !state.MessageStartSent || state.MessageStopSent {
		return nil
	}

	// Synthesize a finish via handleChatFinish with stop reason "end_turn".
	fr := "stop"
	var events []AnthropicStreamEvent
	fakeChoice := ChatStreamChoice{FinishReason: &fr}
	fakeChunk := &ChatStreamChunk{}
	handleChatFinish(fakeChoice, fakeChunk, state, &events)
	return events
}

// closeThinkingBlock closes an open thinking block by emitting a signature_delta
// (empty) and content_block_stop, then advances the block index.
func closeThinkingBlock(
	state *ChatToAnthropicStreamState,
	events *[]AnthropicStreamEvent,
) {
	if !state.ThinkingBlockOpen {
		return
	}
	idx := state.ContentBlockIndex
	*events = append(*events,
		AnthropicStreamEvent{
			Type:  "content_block_delta",
			Index: &idx,
			Delta: &AnthropicDelta{
				Type:      "signature_delta",
				Signature: "",
			},
		},
		AnthropicStreamEvent{
			Type:  "content_block_stop",
			Index: &idx,
		},
	)
	state.ContentBlockIndex++
	state.ThinkingBlockOpen = false
	state.ContentBlockOpen = false
}

// handleStreamReasoningOpaque emits a thinking block for reasoning_opaque
// that arrives outside of the normal thinking flow (e.g. with tool_calls).
func handleStreamReasoningOpaque(
	delta ChatStreamDelta,
	state *ChatToAnthropicStreamState,
	events *[]AnthropicStreamEvent,
) {
	if delta.ReasoningOpaque == "" || state.ThinkingBlockOpen {
		return
	}
	// Only emit if there's no content (pure opaque reasoning).
	if delta.Content != "" || delta.ReasoningText != "" {
		return
	}
	idx := state.ContentBlockIndex
	*events = append(*events,
		AnthropicStreamEvent{
			Type:  "content_block_start",
			Index: &idx,
			ContentBlock: &AnthropicContentBlock{
				Type:     "thinking",
				Thinking: "",
			},
		},
		AnthropicStreamEvent{
			Type:  "content_block_delta",
			Index: &idx,
			Delta: &AnthropicDelta{
				Type:     "thinking_delta",
				Thinking: defaultThinkingText,
			},
		},
		AnthropicStreamEvent{
			Type:  "content_block_delta",
			Index: &idx,
			Delta: &AnthropicDelta{
				Type:      "signature_delta",
				Signature: delta.ReasoningOpaque,
			},
		},
		AnthropicStreamEvent{
			Type:  "content_block_stop",
			Index: &idx,
		},
	)
	state.ContentBlockIndex++
}

// ChatStreamEventToSSE formats an AnthropicStreamEvent as an SSE line pair.
func ChatStreamEventToSSE(evt AnthropicStreamEvent) (string, error) {
	data, err := json.Marshal(evt)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("event: %s\ndata: %s\n\n", evt.Type, data), nil
}
