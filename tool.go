package dash

import "context"

// ToolFunc is the signature for all tool implementations.
type ToolFunc func(ctx context.Context, d *Dash, args map[string]any) (any, error)

// ToolDef defines a registered tool.
type ToolDef struct {
	Name          string
	Description   string
	InputSchema   map[string]any
	Fn            ToolFunc
	Tags          []string // "read", "write", "admin", "graph"
	ChallengeFunc func(ctx context.Context, d *Dash, args map[string]any) *Challenge
}

// ToolOpts controls execution behavior per call.
type ToolOpts struct {
	SessionID string // which session (tui-123, mcp, automation-xyz)
	CallerID  string // "mcp", "tui", "automation", "api"
	Confirm   bool   // deterministic confirmation (skips challenge)
	Reason    string // optional motivation (logged in observation)
}

// ToolResult is the unified return type from RunTool.
type ToolResult struct {
	Data       any        `json:"data,omitempty"`
	Success    bool       `json:"success"`
	Error      string     `json:"error,omitempty"`
	DurationMs int        `json:"duration_ms"`
	Challenge  *Challenge `json:"challenge,omitempty"`
}

// Challenge represents a confirmation request from a tool.
type Challenge struct {
	ID       string   `json:"id"`
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
}
