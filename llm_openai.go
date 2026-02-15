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

// newProviderRequest creates an HTTP request with the correct auth headers for a provider.
func newProviderRequest(ctx context.Context, prov ProviderConfig, method, path string, body io.Reader) (*http.Request, error) {
	url := strings.TrimRight(prov.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}

	apiKey := resolveAPIKey(prov)
	if apiKey == "" {
		return nil, fmt.Errorf("no API key for provider %s (env: %s)", prov.Name, prov.APIKeyEnv)
	}

	// Auth header: use explicit AuthStyle if set, otherwise infer from Format
	authStyle := prov.AuthStyle
	if authStyle == AuthDefault {
		if prov.Format == FormatAnthropic {
			authStyle = AuthXAPIKey
		} else {
			authStyle = AuthBearer
		}
	}
	switch authStyle {
	case AuthXAPIKey:
		req.Header.Set("x-api-key", apiKey)
	default:
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	if prov.Format == FormatAnthropic {
		req.Header.Set("anthropic-version", "2023-06-01")
	}

	req.Header.Set("Content-Type", "application/json")

	// Extra headers (e.g. HTTP-Referer, X-Title for OpenRouter)
	for k, v := range prov.ExtraHeaders {
		req.Header.Set(k, v)
	}

	return req, nil
}

// resolveAPIKey reads the API key from the environment variable specified in the provider config.
func resolveAPIKey(prov ProviderConfig) string {
	if prov.APIKeyEnv == "" {
		return ""
	}
	return EnvOr(prov.APIKeyEnv, "")
}

// --- OpenAI-format embeddings ---

type openAIEmbedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type openAIEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func doOpenAIEmbed(ctx context.Context, client *http.Client, prov ProviderConfig, model, text string) ([]float32, error) {
	reqBody := openAIEmbedRequest{Model: model, Input: text}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	req, err := newProviderRequest(ctx, prov, "POST", "/embeddings", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed http: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read embed response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embed api error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result openAIEmbedResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("unmarshal embed response: %w", err)
	}
	if result.Error != nil {
		return nil, fmt.Errorf("embed api error: %s", result.Error.Message)
	}
	if len(result.Data) == 0 || len(result.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return result.Data[0].Embedding, nil
}

// --- OpenAI-format chat completions ---

type openAIChatRequest struct {
	Model    string           `json:"model"`
	Messages []openAIMessage  `json:"messages"`
	Stream   bool             `json:"stream,omitempty"`
	Tools    []map[string]any `json:"tools,omitempty"`
	ToolChoice any            `json:"tool_choice,omitempty"`
	MaxTokens  int            `json:"max_tokens,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	IncludeReasoning bool     `json:"include_reasoning,omitempty"`
	StreamOptions    *openAIStreamOpts `json:"stream_options,omitempty"`
}

type openAIStreamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAIMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content,omitempty"`
	ToolCalls  []ToolCallRef `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func translateToOpenAI(messages []ChatMessage) []openAIMessage {
	out := make([]openAIMessage, len(messages))
	for i, m := range messages {
		out[i] = openAIMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
		}
	}
	return out
}

func doOpenAIComplete(ctx context.Context, client *http.Client, prov ProviderConfig, model string, messages []ChatMessage, opts CompleteOpts) (string, error) {
	reqBody := openAIChatRequest{
		Model:    model,
		Messages: translateToOpenAI(messages),
	}
	if opts.MaxTokens > 0 {
		reqBody.MaxTokens = opts.MaxTokens
	}
	if opts.Temperature != nil {
		reqBody.Temperature = opts.Temperature
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal complete request: %w", err)
	}

	req, err := newProviderRequest(ctx, prov, "POST", "/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("complete http: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read complete response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("complete api error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result openAIChatResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("unmarshal complete response: %w", err)
	}
	if result.Error != nil {
		return "", fmt.Errorf("complete api error: %s", result.Error.Message)
	}
	if len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("no response returned")
	}
	return result.Choices[0].Message.Content, nil
}

// --- OpenAI-format SSE streaming ---

// streamOpenAI streams chat completions in OpenAI SSE format and sends StreamEvents to ch.
func streamOpenAI(ctx context.Context, client *http.Client, prov ProviderConfig, model string, messages []ChatMessage, tools []map[string]any, ch chan<- StreamEvent) {
	reqBody := openAIChatRequest{
		Model:            model,
		Messages:         translateToOpenAI(messages),
		Stream:           true,
		IncludeReasoning: true,
		StreamOptions:    &openAIStreamOpts{IncludeUsage: true},
	}
	if len(tools) > 0 {
		reqBody.Tools = tools
		reqBody.ToolChoice = "auto"
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		ch <- StreamEvent{Type: EventError, Error: fmt.Errorf("marshal: %w", err)}
		return
	}

	req, err := newProviderRequest(ctx, prov, "POST", "/chat/completions", bytes.NewReader(bodyBytes))
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

	type deltaToolCall struct {
		Index    int    `json:"index"`
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}

	toolCalls := make(map[int]*struct {
		ID      string
		Name    string
		ArgsBuf strings.Builder
	})
	hasToolCalls := false
	var lastUsage *TokenUsage

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			if hasToolCalls {
				var calls []StreamToolCall
				for i := 0; i < len(toolCalls); i++ {
					if tc, ok := toolCalls[i]; ok {
						calls = append(calls, StreamToolCall{
							ID:        tc.ID,
							Name:      tc.Name,
							Arguments: tc.ArgsBuf.String(),
						})
					}
				}
				ch <- StreamEvent{Type: EventToolCall, ToolCalls: calls}
			}
			if lastUsage != nil {
				ch <- StreamEvent{Type: EventUsage, Usage: lastUsage}
			}
			ch <- StreamEvent{Type: EventDone}
			return
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string          `json:"content"`
					Reasoning string          `json:"reasoning"`
					ToolCalls []deltaToolCall `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			ch <- StreamEvent{Type: EventError, Error: fmt.Errorf("API: %s", chunk.Error.Message)}
			return
		}
		if chunk.Usage != nil {
			lastUsage = &TokenUsage{
				PromptTokens:     chunk.Usage.PromptTokens,
				CompletionTokens: chunk.Usage.CompletionTokens,
				TotalTokens:      chunk.Usage.TotalTokens,
			}
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		if delta.Reasoning != "" {
			ch <- StreamEvent{Type: EventReasoning, Reasoning: delta.Reasoning}
		}
		if delta.Content != "" {
			ch <- StreamEvent{Type: EventContent, Content: delta.Content}
		}
		for _, tc := range delta.ToolCalls {
			hasToolCalls = true
			existing, ok := toolCalls[tc.Index]
			if !ok {
				existing = &struct {
					ID      string
					Name    string
					ArgsBuf strings.Builder
				}{}
				toolCalls[tc.Index] = existing
			}
			if tc.ID != "" {
				existing.ID = tc.ID
			}
			if tc.Function.Name != "" {
				existing.Name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				existing.ArgsBuf.WriteString(tc.Function.Arguments)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		ch <- StreamEvent{Type: EventError, Error: fmt.Errorf("scan: %w", err)}
		return
	}
	// Stream ended without [DONE] — emit what we have
	if hasToolCalls {
		var calls []StreamToolCall
		for i := 0; i < len(toolCalls); i++ {
			if tc, ok := toolCalls[i]; ok {
				calls = append(calls, StreamToolCall{
					ID:        tc.ID,
					Name:      tc.Name,
					Arguments: tc.ArgsBuf.String(),
				})
			}
		}
		ch <- StreamEvent{Type: EventToolCall, ToolCalls: calls}
	}
	if lastUsage != nil {
		ch <- StreamEvent{Type: EventUsage, Usage: lastUsage}
	}
	ch <- StreamEvent{Type: EventDone}
}
