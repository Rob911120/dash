package dash

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// --- Anthropic-format types ---

type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Stream      bool               `json:"stream,omitempty"`
	Tools       []anthropicTool    `json:"tools,omitempty"`
	Temperature *float64           `json:"temperature,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []anthropicContentBlock
}

type anthropicContentBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Input     any    `json:"input,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"` // for tool_result
}

type anthropicTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	} `json:"content"`
	Usage *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage,omitempty"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// translateToAnthropic converts ChatMessages into Anthropic's system + messages format.
// Consecutive tool results are merged into a single user message with multiple
// tool_result content blocks (required by the Anthropic API).
func translateToAnthropic(messages []ChatMessage) (system string, out []anthropicMessage) {
	for _, m := range messages {
		if m.Role == "system" {
			system = m.Content
			continue
		}

		if m.Role == "tool" {
			block := anthropicContentBlock{
				Type:      "tool_result",
				ToolUseID: m.ToolCallID,
				Content:   m.Content,
			}
			// Merge consecutive tool results into one user message
			if n := len(out); n > 0 && out[n-1].Role == "user" {
				if existing, ok := out[n-1].Content.([]anthropicContentBlock); ok {
					out[n-1].Content = append(existing, block)
					continue
				}
			}
			out = append(out, anthropicMessage{
				Role:    "user",
				Content: []anthropicContentBlock{block},
			})
			continue
		}

		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			// Assistant message with tool calls
			var blocks []anthropicContentBlock
			if m.Content != "" {
				blocks = append(blocks, anthropicContentBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				var input any
				json.Unmarshal([]byte(tc.Function.Arguments), &input)
				blocks = append(blocks, anthropicContentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: input,
				})
			}
			out = append(out, anthropicMessage{Role: "assistant", Content: blocks})
			continue
		}

		out = append(out, anthropicMessage{Role: m.Role, Content: m.Content})
	}
	return
}

// translateToolsToAnthropic converts OpenAI-style tool definitions to Anthropic format.
func translateToolsToAnthropic(tools []map[string]any) []anthropicTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]anthropicTool, 0, len(tools))
	for _, t := range tools {
		fn, ok := t["function"].(map[string]any)
		if !ok {
			continue
		}
		name, _ := fn["name"].(string)
		desc, _ := fn["description"].(string)
		schema := fn["parameters"]
		out = append(out, anthropicTool{
			Name:        name,
			Description: desc,
			InputSchema: schema,
		})
	}
	return out
}

// --- Anthropic non-streaming completion ---

func doAnthropicComplete(ctx context.Context, client *http.Client, prov ProviderConfig, model string, messages []ChatMessage, opts CompleteOpts) (string, error) {
	system, apiMsgs := translateToAnthropic(messages)

	maxTokens := opts.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	reqBody := anthropicRequest{
		Model:     model,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  apiMsgs,
	}
	if opts.Temperature != nil {
		reqBody.Temperature = opts.Temperature
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal anthropic request: %w", err)
	}

	req, err := newProviderRequest(ctx, prov, "POST", "/messages", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("anthropic http: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read anthropic response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("anthropic api error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result anthropicResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("unmarshal anthropic response: %w", err)
	}
	if result.Error != nil {
		return "", fmt.Errorf("anthropic api error: %s", result.Error.Message)
	}

	// Extract text from content blocks
	var text strings.Builder
	for _, block := range result.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	if text.Len() == 0 {
		return "", fmt.Errorf("no text in anthropic response")
	}
	return text.String(), nil
}

// --- Anthropic SSE streaming ---

// streamAnthropic streams messages in Anthropic SSE format and sends StreamEvents to ch.
//
// Anthropic SSE event mapping:
//
//	content_block_start (type:text)      -> track block index
//	content_block_delta (text_delta)     -> EventContent
//	content_block_delta (thinking_delta) -> EventReasoning
//	content_block_delta (input_json_delta) -> accumulate tool args
//	content_block_stop  (tool_use block) -> EventToolCall
//	message_delta                        -> EventUsage
//	message_stop                         -> EventDone
func streamAnthropic(ctx context.Context, client *http.Client, prov ProviderConfig, model string, messages []ChatMessage, tools []map[string]any, ch chan<- StreamEvent) {
	system, apiMsgs := translateToAnthropic(messages)

	reqBody := anthropicRequest{
		Model:     model,
		MaxTokens: 8192,
		System:    system,
		Messages:  apiMsgs,
		Stream:    true,
	}
	if anthTools := translateToolsToAnthropic(tools); len(anthTools) > 0 {
		reqBody.Tools = anthTools
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		ch <- StreamEvent{Type: EventError, Error: fmt.Errorf("marshal: %w", err)}
		return
	}

	req, err := newProviderRequest(ctx, prov, "POST", "/messages", bytes.NewReader(bodyBytes))
	if err != nil {
		ch <- StreamEvent{Type: EventError, Error: err}
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		ch <- StreamEvent{Type: EventError, Error: fmt.Errorf("http: %w", err)}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errBuf bytes.Buffer
		errBuf.ReadFrom(resp.Body)
		ch <- StreamEvent{Type: EventError, Error: fmt.Errorf("API %d: %s", resp.StatusCode, errBuf.String())}
		return
	}

	// Track content blocks by index
	type blockInfo struct {
		Type    string // "text", "thinking", "tool_use"
		ID      string
		Name    string
		ArgsBuf strings.Builder
	}
	blocks := make(map[int]*blockInfo)
	var pendingToolCalls []StreamToolCall

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)

	var eventType string
	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		switch eventType {
		case "content_block_start":
			var ev struct {
				Index        int `json:"index"`
				ContentBlock struct {
					Type string `json:"type"`
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"content_block"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil {
				blocks[ev.Index] = &blockInfo{
					Type: ev.ContentBlock.Type,
					ID:   ev.ContentBlock.ID,
					Name: ev.ContentBlock.Name,
				}
			}

		case "content_block_delta":
			var ev struct {
				Index int `json:"index"`
				Delta struct {
					Type     string `json:"type"`
					Text     string `json:"text"`
					Thinking string `json:"thinking"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			if json.Unmarshal([]byte(data), &ev) != nil {
				continue
			}
			switch ev.Delta.Type {
			case "text_delta":
				if ev.Delta.Text != "" {
					ch <- StreamEvent{Type: EventContent, Content: ev.Delta.Text}
				}
			case "thinking_delta":
				if ev.Delta.Thinking != "" {
					ch <- StreamEvent{Type: EventReasoning, Reasoning: ev.Delta.Thinking}
				}
			case "input_json_delta":
				if b, ok := blocks[ev.Index]; ok {
					b.ArgsBuf.WriteString(ev.Delta.PartialJSON)
				}
			}

		case "content_block_stop":
			var ev struct {
				Index int `json:"index"`
			}
			if json.Unmarshal([]byte(data), &ev) != nil {
				continue
			}
			if b, ok := blocks[ev.Index]; ok && b.Type == "tool_use" {
				pendingToolCalls = append(pendingToolCalls, StreamToolCall{
					ID:        b.ID,
					Name:      b.Name,
					Arguments: b.ArgsBuf.String(),
				})
			}

		case "message_delta":
			var ev struct {
				Usage *struct {
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil && ev.Usage != nil {
				ch <- StreamEvent{Type: EventUsage, Usage: &TokenUsage{
					CompletionTokens: ev.Usage.OutputTokens,
				}}
			}

		case "message_stop":
			if len(pendingToolCalls) > 0 {
				ch <- StreamEvent{Type: EventToolCall, ToolCalls: pendingToolCalls}
			}
			ch <- StreamEvent{Type: EventDone}
			return

		case "error":
			var ev struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil {
				ch <- StreamEvent{Type: EventError, Error: fmt.Errorf("anthropic: %s", ev.Error.Message)}
			}
			return
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- StreamEvent{Type: EventError, Error: fmt.Errorf("scan: %w", err)}
		return
	}

	// Stream ended without message_stop
	if len(pendingToolCalls) > 0 {
		ch <- StreamEvent{Type: EventToolCall, ToolCalls: pendingToolCalls}
	}
	ch <- StreamEvent{Type: EventDone}
}
