package dash

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// knownAgentKeys lists valid agent keys that match the TUI agent tabs.
var knownAgentKeys = []string{
	"orchestrator",
	"cockpit-backend",
	"cockpit-frontend",
	"systemprompt-agent",
	"database-agent",
	"system-agent",
	"shift-agent",
	"planner-agent",
}

// validateAgentKey checks that an agent key matches a registered agent.
func validateAgentKey(key string) error {
	for _, k := range knownAgentKeys {
		if k == key {
			return nil
		}
	}
	return fmt.Errorf("unknown agent key: %q (available: %v)", key, knownAgentKeys)
}

func defWorkOrder() *ToolDef {
	return &ToolDef{
		Name:        "work_order",
		Description: "Hantera work orders i pipeline. Actions: create, assign, advance, list, get. Agent keys: orchestrator, cockpit-backend, cockpit-frontend, systemprompt-agent, database-agent, system-agent, shift-agent, planner-agent.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"action"},
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"create", "assign", "advance", "list", "get"},
					"description": "Operationen att utföra.",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Work order namn (för create).",
				},
				"id": map[string]any{
					"type":        "string",
					"description": "Work order UUID (för assign/advance/get).",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Beskrivning av vad som ska göras (för create).",
				},
				"scope_paths": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Tillåtna filvägar (för create, krävs).",
				},
				"agent_key": map[string]any{
					"type":        "string",
					"description": "Agent att tilldela (för create/assign). Måste vara en registrerad agent-key.",
				},
				"base_branch": map[string]any{
					"type":        "string",
					"description": "Bas-branch (default: main).",
				},
				"status": map[string]any{
					"type":        "string",
					"description": "Målstatus (för advance).",
				},
				"detail": map[string]any{
					"type":        "string",
					"description": "Detalj/meddelande (för advance).",
				},
			},
		},
		Fn:   handleWorkOrder,
		Tags: []string{"write", "graph"},
	}
}

func handleWorkOrder(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	action, _ := args["action"].(string)

	switch action {
	case "create":
		name, _ := args["name"].(string)
		if name == "" {
			return nil, fmt.Errorf("name is required for create")
		}
		scopeRaw, _ := args["scope_paths"].([]any)
		if len(scopeRaw) == 0 {
			return nil, fmt.Errorf("scope_paths is required for create")
		}
		var scopePaths []string
		for _, s := range scopeRaw {
			if str, ok := s.(string); ok {
				scopePaths = append(scopePaths, str)
			}
		}
		agentKey, _ := args["agent_key"].(string)
		if agentKey != "" {
			if err := validateAgentKey(agentKey); err != nil {
				return nil, err
			}
		}
		description, _ := args["description"].(string)
		baseBranch, _ := args["base_branch"].(string)
		if baseBranch == "" {
			baseBranch = "main"
		}

		wo, err := d.CreateWorkOrder(ctx, name, nil, agentKey, scopePaths, WorkOrderOpts{
			BaseBranch:  baseBranch,
			RepoRoot:    "/dash",
			Description: description,
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"id":     wo.Node.ID.String(),
			"name":   wo.Node.Name,
			"status": string(wo.Status),
		}, nil

	case "assign":
		id, err := parseWOID(args)
		if err != nil {
			return nil, err
		}
		agentKey, _ := args["agent_key"].(string)
		if agentKey == "" {
			return nil, fmt.Errorf("agent_key is required for assign")
		}
		if err := validateAgentKey(agentKey); err != nil {
			return nil, err
		}
		wo, err := d.GetWorkOrder(ctx, id)
		if err != nil {
			return nil, err
		}
		branchName := fmt.Sprintf("agent/%s/%s", agentKey, wo.Node.Name)
		wo, err = d.AssignWorkOrder(ctx, id, agentKey, branchName)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"id":     wo.Node.ID.String(),
			"status": string(wo.Status),
			"branch": wo.BranchName,
			"agent":  wo.AgentKey,
		}, nil

	case "advance":
		id, err := parseWOID(args)
		if err != nil {
			return nil, err
		}
		status, _ := args["status"].(string)
		if status == "" {
			return nil, fmt.Errorf("status is required for advance")
		}
		detail, _ := args["detail"].(string)
		wo, err := d.AdvanceWorkOrder(ctx, id, WorkOrderStatus(status), "orchestrator", detail)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"id":     wo.Node.ID.String(),
			"status": string(wo.Status),
		}, nil

	case "list":
		orders, err := d.ListActiveWorkOrders(ctx)
		if err != nil {
			return nil, err
		}
		var result []map[string]any
		for _, wo := range orders {
			result = append(result, map[string]any{
				"id":       wo.Node.ID.String(),
				"name":     wo.Node.Name,
				"status":   string(wo.Status),
				"agent":    wo.AgentKey,
				"branch":   wo.BranchName,
				"attempt":  wo.Attempt,
			})
		}
		return map[string]any{"work_orders": result, "count": len(result)}, nil

	case "get":
		id, err := parseWOID(args)
		if err != nil {
			return nil, err
		}
		wo, err := d.GetWorkOrder(ctx, id)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"id":            wo.Node.ID.String(),
			"name":          wo.Node.Name,
			"status":        string(wo.Status),
			"agent":         wo.AgentKey,
			"branch":        wo.BranchName,
			"base_branch":   wo.BaseBranch,
			"attempt":       wo.Attempt,
			"scope_paths":   wo.ScopePaths,
			"files_changed": wo.FilesChanged,
			"pr_id":         wo.PRID,
			"pr_url":        wo.PRUrl,
			"last_error":    wo.LastError,
		}, nil

	default:
		return nil, fmt.Errorf("unknown action: %s (use: create, assign, advance, list, get)", action)
	}
}

func parseWOID(args map[string]any) (uuid.UUID, error) {
	idStr, _ := args["id"].(string)
	if idStr == "" {
		return uuid.Nil, fmt.Errorf("id is required")
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid UUID: %s", idStr)
	}
	return id, nil
}
