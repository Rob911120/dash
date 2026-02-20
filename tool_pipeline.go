package dash

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

func defPipeline() *ToolDef {
	return &ToolDef{
		Name:        "pipeline",
		Description: "Kör pipeline-steg för en work order. Steps: full (build gate + synthesis + merge), synthesis (bara synthesis), merge (förbereda merge).",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"work_order_id", "step"},
			"properties": map[string]any{
				"work_order_id": map[string]any{
					"type":        "string",
					"description": "UUID för work order.",
				},
				"step": map[string]any{
					"type":        "string",
					"enum":        []string{"full", "synthesis", "prepare_branch"},
					"description": "Pipeline-steg att köra.",
				},
			},
		},
		Fn:   handlePipeline,
		Tags: []string{"write", "graph"},
	}
}

func handlePipeline(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	idStr, _ := args["work_order_id"].(string)
	if idStr == "" {
		return nil, fmt.Errorf("work_order_id is required")
	}
	woID, err := uuid.Parse(idStr)
	if err != nil {
		return nil, fmt.Errorf("invalid UUID: %s", idStr)
	}

	step, _ := args["step"].(string)

	switch step {
	case "full":
		wo, err := d.GetWorkOrder(ctx, woID)
		if err != nil {
			return nil, err
		}
		git := NewExecGitClient(wo.RepoRoot)
		result, err := d.RunFullPipeline(ctx, woID, git)
		if err != nil {
			return map[string]any{
				"stage":  result.Stage,
				"passed": false,
				"error":  err.Error(),
			}, nil
		}
		return map[string]any{
			"stage":  result.Stage,
			"passed": result.Passed,
			"gate":   result.Gate != nil && result.Gate.Passed,
			"synthesis": func() string {
				if result.Synthesis != nil {
					return string(result.Synthesis.Verdict)
				}
				return ""
			}(),
		}, nil

	case "synthesis":
		wo, err := d.GetWorkOrder(ctx, woID)
		if err != nil {
			return nil, err
		}
		if wo.Status != WOStatusBuildPassed {
			return nil, fmt.Errorf("work order must be in build_passed state for synthesis, currently %s", wo.Status)
		}
		git := NewExecGitClient(wo.RepoRoot)
		result, err := d.RunSynthesisPipeline(ctx, woID, git, "")
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"verdict":   string(result.Verdict),
			"score":     result.Score,
			"reasoning": result.Reasoning,
		}, nil

	case "prepare_branch":
		wo, err := d.GetWorkOrder(ctx, woID)
		if err != nil {
			return nil, err
		}
		git := NewExecGitClient(wo.RepoRoot)
		if err := d.PrepareWorkOrderBranch(ctx, woID, git); err != nil {
			return nil, err
		}
		return map[string]any{
			"branch": wo.BranchName,
			"status": "mutating",
		}, nil

	default:
		return nil, fmt.Errorf("unknown step: %s (use: full, synthesis, prepare_branch)", step)
	}
}
