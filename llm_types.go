package dash

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type llmContextKey string

const llmAgentKey llmContextKey = "llm-agent"

// WithLLMAgent attaches an agent name to the context for per-agent API logging.
func WithLLMAgent(ctx context.Context, agent string) context.Context {
	return context.WithValue(ctx, llmAgentKey, agent)
}

// LLMAgentFromContext extracts the agent name, defaulting to "default".
func LLMAgentFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(llmAgentKey).(string); ok && v != "" {
		return v
	}
	return "default"
}

// APIFormat represents the API wire format used by a provider.
type APIFormat string

const (
	FormatOpenAI    APIFormat = "openai"
	FormatAnthropic APIFormat = "anthropic"
)

// AuthStyle controls how the API key is sent to the provider.
type AuthStyle string

const (
	AuthDefault AuthStyle = ""       // Infer from Format: Anthropic→x-api-key, OpenAI→Bearer
	AuthBearer  AuthStyle = "bearer" // Authorization: Bearer <key>
	AuthXAPIKey AuthStyle = "x-api-key"
)

// ProviderConfig holds connection details for an LLM provider.
// API keys are NEVER stored here — only the env var name.
type ProviderConfig struct {
	Name          string            `json:"name"`
	Format        APIFormat         `json:"format"`
	AuthStyle     AuthStyle         `json:"auth_style,omitempty"` // Override default auth header style
	BaseURL       string            `json:"base_url"`
	APIKeyEnv     string            `json:"api_key_env"`
	ExtraHeaders  map[string]string `json:"extra_headers,omitempty"`
	Enabled       bool              `json:"enabled"`
	SupportsTools bool              `json:"supports_tools"` // Whether provider accepts tool definitions
}

// RoleConfig maps a logical role to a specific provider + model.
type RoleConfig struct {
	Role        string   `json:"role"`
	Provider    string   `json:"provider"`
	Model       string   `json:"model"`
	MaxTokens   int      `json:"max_tokens,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
}

// ModelConfig describes a model available for chat/streaming.
type ModelConfig struct {
	Name          string `json:"name"`           // Display/API name, e.g. "anthropic/claude-opus-4"
	Provider      string `json:"provider"`       // Provider key, e.g. "openrouter"
	ContextLength int    `json:"context_length"` // Context window size in tokens
}

// RouterConfig is the full configuration for the LLM router.
type RouterConfig struct {
	Providers    map[string]ProviderConfig `json:"providers"`
	Roles        map[string]RoleConfig     `json:"roles"`
	ModelAliases map[string]string         `json:"model_aliases,omitempty"` // model name → provider name
	Models       map[string]ModelConfig    `json:"models,omitempty"`        // model name → config
	EnvFile      string                    `json:"env_file,omitempty"`      // Path to .mcp.json for API key loading
}

// StreamEventType classifies streaming events.
type StreamEventType int

const (
	EventContent   StreamEventType = iota // Text content chunk
	EventReasoning                        // Reasoning/thinking chunk
	EventToolCall                         // Tool call completed
	EventUsage                            // Token usage info
	EventDone                             // Stream finished
	EventError                            // Error occurred
)

// StreamEvent is a unified streaming event emitted by the router.
type StreamEvent struct {
	Type      StreamEventType
	Content   string
	Reasoning string
	ToolCalls []StreamToolCall
	Usage     *TokenUsage
	Error     error
}

// StreamToolCall represents a tool call extracted from a streaming response.
type StreamToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// TokenUsage tracks token consumption for a request.
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ErrInvalidRole is returned when a ChatMessage has an unrecognized role.
var ErrInvalidRole = errors.New("invalid message role")

// ValidRoles lists the roles accepted by LLM APIs.
var ValidRoles = map[string]bool{
	"user": true, "assistant": true, "tool": true, "system": true,
}

// ChatMessage is the canonical message format used throughout dash.
// All messages entering the system must use this type.
type ChatMessage struct {
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	Name       string          `json:"name,omitempty"`        // Tool name (for role=tool)
	ToolCalls  []ToolCallRef   `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolError  bool            `json:"tool_error,omitempty"`  // Structured error flag
	Reasoning  string          `json:"-"`                     // LLM thinking (not serialized)
	RawContent json.RawMessage `json:"raw_content,omitempty"` // For multi-part content blocks
}

// Validate checks that the message has a valid role and required fields.
func (m ChatMessage) Validate() error {
	if !ValidRoles[m.Role] {
		return fmt.Errorf("%w: %q", ErrInvalidRole, m.Role)
	}
	if m.Role == "tool" && m.ToolCallID == "" {
		return fmt.Errorf("tool message missing tool_call_id")
	}
	return nil
}

// NewChatMessage creates a validated ChatMessage. Returns error for invalid roles.
func NewChatMessage(role, content string) (ChatMessage, error) {
	m := ChatMessage{Role: role, Content: content}
	if err := m.Validate(); err != nil {
		return ChatMessage{}, err
	}
	return m, nil
}

// NewToolResult creates a tool result message with explicit error flag.
func NewToolResult(toolCallID, name, content string, isError bool) ChatMessage {
	return ChatMessage{
		Role:       "tool",
		Name:       name,
		Content:    content,
		ToolCallID: toolCallID,
		ToolError:  isError,
	}
}

// ToolCallRef references a tool call in a message.
type ToolCallRef struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolCallFunc `json:"function"`
}

// ToolCallFunc holds the function name and arguments for a tool call.
type ToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// CompleteOpts are optional parameters for non-streaming completions.
type CompleteOpts struct {
	MaxTokens   int
	Temperature *float64
	Tools       []map[string]any
}
