package dash

import "encoding/json"

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

// ChatMessage is the unified message format used by the router.
type ChatMessage struct {
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	ToolCalls  []ToolCallRef   `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	RawContent json.RawMessage `json:"raw_content,omitempty"` // For multi-part content blocks
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
