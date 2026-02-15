package dash

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// RunTool is the single entry point for all tool execution.
// It handles pre/post observation logging automatically.
func (d *Dash) RunTool(ctx context.Context, name string, args map[string]any, opts *ToolOpts) *ToolResult {
	if opts == nil {
		opts = &ToolOpts{}
	}

	// 1. Lookup tool
	def, ok := d.registry.Get(name)
	if !ok {
		return &ToolResult{Success: false, Error: fmt.Sprintf("unknown tool: %s", name)}
	}

	// 2. PRE: log observation
	start := time.Now()
	d.logToolObs(ctx, opts, name, args, "tool.pre", true, 0)

	// 3. Challenge check
	if def.ChallengeFunc != nil && !opts.Confirm {
		if ch := def.ChallengeFunc(ctx, d, args); ch != nil {
			d.logToolObs(ctx, opts, name, args, "tool.challenge", true, 0)
			return &ToolResult{Success: true, Challenge: ch}
		}
	}

	// 4. Execute
	data, err := def.Fn(ctx, d, args)

	// 5. POST: log observation
	durationMs := int(time.Since(start).Milliseconds())
	success := err == nil
	d.logToolObs(ctx, opts, name, args, "tool.post", success, durationMs)

	if err != nil {
		return &ToolResult{Success: false, Error: err.Error(), DurationMs: durationMs}
	}
	return &ToolResult{Success: true, Data: data, DurationMs: durationMs}
}

// Registry returns the tool registry for external consumers (e.g. TUI tool definitions).
func (d *Dash) Registry() *ToolRegistry {
	return d.registry
}

// logToolObs logs a tool execution observation to the session node.
func (d *Dash) logToolObs(ctx context.Context, opts *ToolOpts, toolName string, args map[string]any, phase string, success bool, durationMs int) {
	if opts.SessionID == "" {
		return
	}

	session, err := d.GetOrCreateNode(ctx, LayerContext, "session", opts.SessionID, map[string]any{
		"status": "active",
		"source": opts.CallerID,
	})
	if err != nil {
		return
	}

	data := map[string]any{
		"phase":     phase,
		"tool_name": toolName,
		"args":      args,
		"success":   success,
		"caller":    opts.CallerID,
	}
	if durationMs > 0 {
		data["duration_ms"] = durationMs
	}
	if opts.Reason != "" {
		data["reason"] = opts.Reason
	}
	dataJSON, _ := json.Marshal(data)

	_ = d.CreateObservation(ctx, &Observation{
		NodeID:     session.ID,
		Type:       "tool_event",
		Data:       dataJSON,
		ObservedAt: time.Now(),
	})

	// On successful post-phase: create SYSTEM.file node + edge_event (like hooks do)
	if phase == "tool.post" && success {
		d.maybeCreateFileEdge(ctx, session, toolName, args)
	}
}

// maybeCreateFileEdge creates a SYSTEM.file node and edge_event for file-related tool calls.
// This mirrors the behavior of hook_handler.go PostToolUse for TUI tool calls.
func (d *Dash) maybeCreateFileEdge(ctx context.Context, session *Node, toolName string, args map[string]any) {
	// Extract file path from args
	filePath := extractFilePathFromArgs(args)
	if filePath == "" {
		return
	}

	// Determine relation based on tool name
	relation := determineToolRelation(toolName)

	// Create or get the SYSTEM.file node
	fileNode, err := d.GetOrCreateNode(ctx, LayerSystem, "file", filePath, map[string]any{
		"last_seen": time.Now().Format(time.RFC3339),
	})
	if err != nil {
		return
	}

	// Create edge_event: session → file
	_ = d.CreateEdgeEvent(ctx, &EdgeEvent{
		SourceID:   session.ID,
		TargetID:   fileNode.ID,
		Relation:   relation,
		Success:    true,
		OccurredAt: time.Now(),
	})
}

// extractFilePathFromArgs extracts a file path from tool arguments.
func extractFilePathFromArgs(args map[string]any) string {
	// Try file_path first (most common for dash tools like "file")
	if p, ok := args["file_path"].(string); ok && p != "" {
		return p
	}
	// Try path
	if p, ok := args["path"].(string); ok && p != "" {
		return p
	}
	// Try query for search-like tools
	return ""
}

// determineToolRelation returns the edge event relation for a dash tool.
func determineToolRelation(toolName string) EventRelation {
	switch toolName {
	case "file", "search", "query", "activity", "session", "summary", "working_set", "embed",
		"read", "grep", "glob", "ls":
		return EventRelationObserved
	case "node", "link", "remember", "promote", "gc",
		"write", "edit", "mkdir":
		return EventRelationModified
	case "exec":
		return EventRelationTriggered
	default:
		return EventRelationTriggered
	}
}
