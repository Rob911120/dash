package dash

import (
	"encoding/json"
	"time"
)

// HookEventName represents Claude Code hook event types.
type HookEventName string

const (
	HookSessionStart       HookEventName = "SessionStart"
	HookUserPromptSubmit   HookEventName = "UserPromptSubmit"
	HookPreToolUse         HookEventName = "PreToolUse"
	HookPostToolUse        HookEventName = "PostToolUse"
	HookPostToolUseFailure HookEventName = "PostToolUseFailure"
	HookSessionEnd         HookEventName = "SessionEnd"
	HookSubagentStart      HookEventName = "SubagentStart"
	HookSubagentStop       HookEventName = "SubagentStop"
)

// ClaudeCodeInput represents the raw input from Claude Code hooks.
type ClaudeCodeInput struct {
	// Common fields (all events)
	SessionID      string        `json:"session_id"`
	TranscriptPath string        `json:"transcript_path"`
	Cwd            string        `json:"cwd"`
	PermissionMode string        `json:"permission_mode"`
	HookEventName  HookEventName `json:"hook_event_name"`

	// Tool-specific fields (PreToolUse, PostToolUse, PostToolUseFailure)
	ToolName     string          `json:"tool_name,omitempty"`
	ToolUseID    string          `json:"tool_use_id,omitempty"`
	ToolInput    json.RawMessage `json:"tool_input,omitempty"`
	ToolResponse json.RawMessage `json:"tool_response,omitempty"`

	// PostToolUseFailure-specific
	Error       string `json:"error,omitempty"`
	IsInterrupt bool   `json:"is_interrupt,omitempty"`

	// SessionStart-specific
	Source    string `json:"source,omitempty"`     // startup, resume, clear, compact
	Model     string `json:"model,omitempty"`      // Model being used
	AgentType string `json:"agent_type,omitempty"` // Type of agent

	// SessionEnd-specific
	Reason string `json:"reason,omitempty"`
}

// ToolKind categorizes tools by their function.
type ToolKind string

const (
	ToolKindFilesystem ToolKind = "filesystem"
	ToolKindShell      ToolKind = "shell"
	ToolKindWeb        ToolKind = "web"
	ToolKindTask       ToolKind = "task"
	ToolKindMCP        ToolKind = "mcp"
	ToolKindOther      ToolKind = "other"
)

// DashHookEnvelope is the stable format for storing hook events.
type DashHookEnvelope struct {
	EnvelopeVersion string           `json:"dash_envelope_version"`
	ReceivedAt      time.Time        `json:"received_at"`
	ClaudeCode      *ClaudeCodeInput `json:"claude_code"`
	Normalized      *NormalizedEvent `json:"normalized"`

	// System awareness fields (captured at event time)
	SystemState    *SystemState    `json:"system,omitempty"`
	ProcessContext *ProcessContext `json:"process,omitempty"`
	FileMetadata   *FileMetadata   `json:"file,omitempty"`
}

// NormalizedEvent contains normalized event data for easier querying.
type NormalizedEvent struct {
	Event         string      `json:"event"`                    // e.g., "tool.pre", "session.start"
	CorrelationID string      `json:"correlation_id,omitempty"` // tool_use_id
	Subject       *SubjectRef `json:"subject,omitempty"`
	Tool          *ToolRef    `json:"tool,omitempty"`
	Outcome       *Outcome    `json:"outcome,omitempty"`
}

// SubjectRef references the subject of an operation.
type SubjectRef struct {
	Kind string `json:"kind"` // file, command, url, pattern
	Ref  string `json:"ref"`
}

// ToolRef references a tool.
type ToolRef struct {
	Name string   `json:"name"`
	Kind ToolKind `json:"kind"`
}

// Outcome represents the result of an operation.
type Outcome struct {
	Success    *bool  `json:"success,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMs *int   `json:"duration_ms,omitempty"`
}

// HookOutput represents output to return from hook processing.
type HookOutput struct {
	Content       string // Plain text for stdout injection (SessionStart)
	SystemMessage string // Message shown to user (PreToolUse)
	IsJSON        bool   // If true, output as JSON format
}

// boolPtr returns a pointer to a bool value.
func boolPtr(b bool) *bool {
	return &b
}
