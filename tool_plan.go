package dash

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

func defPlan() *ToolDef {
	return &ToolDef{
		Name:        "plan",
		Description: "Manage implementation plans. Plans progress through stages: outline → plan → prereqs → review → approved. Operations: create, advance, update, get, list.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"op"},
			"properties": map[string]any{
				"op":   map[string]any{"type": "string", "enum": []string{"create", "advance", "update", "get", "list"}, "description": "Operation to perform"},
				"id":   map[string]any{"type": "string", "description": "Plan UUID (for advance/update/get)"},
				"name": map[string]any{"type": "string", "description": "Plan name in kebab-case (required for create, or used for get by name). Auto-generated from goal if omitted on create."},
				"data": map[string]any{"type": "object", "description": "Plan data (for create/update). Fields depend on stage: outline needs goal/scope/non_goals, plan needs milestones/steps/acceptance_criteria/test_strategy, prereqs needs blocked_by/required_modules/missing_apis/migrations"},
			},
		},
		Tags: []string{"graph", "write"},
		Fn:   toolPlan,
	}
}

func toolPlan(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	op, _ := args["op"].(string)

	switch op {
	case "create":
		name, _ := args["name"].(string)
		data, _ := args["data"].(map[string]any)
		// Auto-generate name from goal if not provided
		if name == "" && data != nil {
			if goal, ok := data["goal"].(string); ok && goal != "" {
				words := strings.Fields(strings.ToLower(goal))
				if len(words) > 5 {
					words = words[:5]
				}
				name = strings.Join(words, "-")
			}
		}
		if name == "" {
			return nil, fmt.Errorf("name is required for create (pass name directly or set data.goal to auto-generate)")
		}
		node, err := d.CreatePlan(ctx, name, data)
		if err != nil {
			return nil, err
		}
		ps, _ := parsePlanData(node)
		return ps, nil

	case "advance":
		id, err := parsePlanID(args)
		if err != nil {
			return nil, err
		}
		return d.AdvancePlan(ctx, id)

	case "update":
		id, err := parsePlanID(args)
		if err != nil {
			return nil, err
		}
		data, ok := args["data"].(map[string]any)
		if !ok || len(data) == 0 {
			return nil, fmt.Errorf("data is required for update")
		}

		node, err := d.GetNodeActive(ctx, id)
		if err != nil {
			return nil, err
		}

		// Merge new data into existing
		var existing map[string]any
		if err := json.Unmarshal(node.Data, &existing); err != nil {
			existing = make(map[string]any)
		}
		for k, v := range data {
			existing[k] = v
		}
		dataBytes, err := json.Marshal(existing)
		if err != nil {
			return nil, fmt.Errorf("invalid data: %w", err)
		}
		node.Data = dataBytes

		if err := d.UpdateNode(ctx, node); err != nil {
			return nil, err
		}
		return parsePlanData(node)

	case "get":
		// Try by ID first, then by name
		if idStr, ok := args["id"].(string); ok && idStr != "" {
			id, err := uuid.Parse(idStr)
			if err != nil {
				return nil, fmt.Errorf("invalid UUID: %w", err)
			}
			return d.GetPlan(ctx, id)
		}
		if name, ok := args["name"].(string); ok && name != "" {
			return d.GetPlanByName(ctx, name)
		}
		return nil, fmt.Errorf("provide either 'id' or 'name' for get")

	case "list":
		return d.ListActivePlans(ctx)

	default:
		return nil, fmt.Errorf("unknown operation: %s (valid: create, advance, update, get, list)", op)
	}
}

func defPlanReview() *ToolDef {
	return &ToolDef{
		Name:        "plan_review",
		Description: "Review a plan without advancing it. Runs the deterministic critic and returns score, checks, and verdict. Optionally override the verdict.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"id"},
			"properties": map[string]any{
				"id":            map[string]any{"type": "string", "description": "Plan UUID"},
				"force_verdict": map[string]any{"type": "string", "enum": []string{"approve", "revise"}, "description": "Override the critic's verdict (user decision)"},
			},
		},
		Tags: []string{"graph", "read"},
		Fn:   toolPlanReview,
	}
}

func toolPlanReview(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	idStr, _ := args["id"].(string)
	if idStr == "" {
		return nil, fmt.Errorf("id is required")
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return nil, fmt.Errorf("invalid UUID: %w", err)
	}

	ps, err := d.GetPlan(ctx, id)
	if err != nil {
		return nil, err
	}

	review := reviewPlan(ps)

	// Override verdict if requested
	if forceVerdict, ok := args["force_verdict"].(string); ok && forceVerdict != "" {
		review.Verdict = forceVerdict
		review.Issues = append(review.Issues, fmt.Sprintf("Verdict overridden to '%s' by user", forceVerdict))
	}

	// Save review to node data if force_verdict was used
	if _, ok := args["force_verdict"].(string); ok {
		var data map[string]any
		json.Unmarshal(ps.Node.Data, &data)
		reviewJSON, _ := json.Marshal(review)
		var reviewMap map[string]any
		json.Unmarshal(reviewJSON, &reviewMap)
		data["review"] = reviewMap
		dataBytes, _ := json.Marshal(data)
		ps.Node.Data = dataBytes
		d.UpdateNode(ctx, ps.Node)
	}

	gate := gatePlan(ps, review)

	return map[string]any{
		"plan_name": ps.Node.Name,
		"stage":     ps.Stage,
		"review":    review,
		"gate":      gate,
	}, nil
}

func parsePlanID(args map[string]any) (uuid.UUID, error) {
	idStr, ok := args["id"].(string)
	if !ok || idStr == "" {
		return uuid.UUID{}, fmt.Errorf("id is required")
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("invalid UUID: %w", err)
	}
	return id, nil
}
