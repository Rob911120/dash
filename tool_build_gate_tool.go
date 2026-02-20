package dash

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

func defBuildGateTool() *ToolDef {
	return &ToolDef{
		Name:        "build_gate",
		Description: "Kör build gate (scope check, AST validation, build, test) för en work order. Kräver att work order är i mutating-status.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"work_order_id"},
			"properties": map[string]any{
				"work_order_id": map[string]any{
					"type":        "string",
					"description": "UUID för work order att köra build gate på.",
				},
			},
		},
		Fn:   handleBuildGateTool,
		Tags: []string{"write", "graph"},
	}
}

func handleBuildGateTool(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	idStr, _ := args["work_order_id"].(string)
	if idStr == "" {
		return nil, fmt.Errorf("work_order_id is required")
	}
	woID, err := uuid.Parse(idStr)
	if err != nil {
		return nil, fmt.Errorf("invalid UUID: %s", idStr)
	}

	wo, err := d.GetWorkOrder(ctx, woID)
	if err != nil {
		return nil, err
	}

	if wo.Status != WOStatusMutating {
		return nil, fmt.Errorf("work order must be in mutating state, currently %s", wo.Status)
	}

	git := NewExecGitClient(wo.RepoRoot)
	result, err := RunBuildGate(git, wo, "")
	if err != nil {
		return nil, fmt.Errorf("build gate error: %w", err)
	}

	// Advance status based on result
	if result.Passed {
		d.AdvanceWorkOrder(ctx, woID, WOStatusBuildPassed, "build_gate", "all checks passed")
	} else {
		detail := "gate failed:"
		if !result.Scope.Passed {
			detail += " scope"
		}
		if !result.AST.Passed {
			detail += " ast"
		}
		if !result.Build.Passed {
			detail += " build"
		}
		if !result.Test.Passed {
			detail += " test"
		}
		d.AdvanceWorkOrder(ctx, woID, WOStatusBuildFailed, "build_gate", detail)
	}

	return map[string]any{
		"passed":      result.Passed,
		"scope":       result.Scope.Passed,
		"ast":         result.AST.Passed,
		"build":       result.Build.Passed,
		"test":        result.Test.Passed,
		"build_out":   capString(result.Build.Output, 2000),
		"test_out":    capString(result.Test.Output, 2000),
		"worktree_at": result.WorktreeAt,
	}, nil
}
