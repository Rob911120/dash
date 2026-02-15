package dash

import (
	"context"
	"fmt"
)

func defPromote() *ToolDef {
	return &ToolDef{
		Name:        "promote",
		Description: "Promote a session: extract insights, decisions, and tasks into canonical graph knowledge.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"session_id"},
			"properties": map[string]any{
				"session_id":   map[string]any{"type": "string", "description": "The session ID to promote"},
				"insights":     map[string]any{"type": "array", "description": "Insights to extract (0-3)"},
				"decisions":    map[string]any{"type": "array", "description": "Decisions to extract (0-2)"},
				"tasks":        map[string]any{"type": "array", "description": "Tasks to create"},
				"frame_update": map[string]any{"type": "object", "description": "Update the context_frame singleton"},
			},
		},
		Tags: []string{"write", "admin"},
		Fn:   toolPromote,
	}
}

func toolPromote(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	sessionID, _ := args["session_id"].(string)
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	req := &PromoteRequest{SessionID: sessionID}

	if insightsRaw, ok := args["insights"].([]any); ok {
		for _, raw := range insightsRaw {
			if m, ok := raw.(map[string]any); ok {
				text, _ := m["text"].(string)
				ctxStr, _ := m["context"].(string)
				if text != "" {
					req.Insights = append(req.Insights, PromoteInsight{Text: text, Context: ctxStr})
				}
			}
		}
	}

	if decisionsRaw, ok := args["decisions"].([]any); ok {
		for _, raw := range decisionsRaw {
			if m, ok := raw.(map[string]any); ok {
				text, _ := m["text"].(string)
				rationale, _ := m["rationale"].(string)
				if text != "" {
					req.Decisions = append(req.Decisions, PromoteDecision{Text: text, Rationale: rationale})
				}
			}
		}
	}

	if tasksRaw, ok := args["tasks"].([]any); ok {
		for _, raw := range tasksRaw {
			if m, ok := raw.(map[string]any); ok {
				name, _ := m["name"].(string)
				desc, _ := m["description"].(string)
				status, _ := m["status"].(string)
				if name != "" {
					req.Tasks = append(req.Tasks, PromoteTask{Name: name, Description: desc, Status: status})
				}
			}
		}
	}

	if fu, ok := args["frame_update"].(map[string]any); ok {
		update := &FrameUpdate{}
		update.Summary, _ = fu["summary"].(string)
		update.CurrentFocus, _ = fu["current_focus"].(string)
		if ns, ok := fu["next_steps"].([]any); ok {
			for _, s := range ns {
				if str, ok := s.(string); ok {
					update.NextSteps = append(update.NextSteps, str)
				}
			}
		}
		if bl, ok := fu["blockers"].([]any); ok {
			for _, s := range bl {
				if str, ok := s.(string); ok {
					update.Blockers = append(update.Blockers, str)
				}
			}
		}
		req.FrameUpdate = update
	}

	return d.PromoteSession(ctx, req)
}
