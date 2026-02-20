package dash

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// PipelineResult holds the outcome of a full pipeline run.
type PipelineResult struct {
	Stage     string           `json:"stage"`
	Passed    bool             `json:"passed"`
	Gate      *BuildGateResult `json:"gate,omitempty"`
	Synthesis *SynthesisResult `json:"synthesis,omitempty"`
	Error     string           `json:"error,omitempty"`
}

// PrepareWorkOrderBranch creates and checks out the branch for a work order.
func (d *Dash) PrepareWorkOrderBranch(ctx context.Context, woID uuid.UUID, git GitClient) error {
	wo, err := d.GetWorkOrder(ctx, woID)
	if err != nil {
		return fmt.Errorf("get work order: %w", err)
	}

	if wo.BranchName == "" {
		return fmt.Errorf("work order has no branch name — assign first")
	}

	// Create and checkout branch
	if err := git.CreateBranch(wo.BranchName); err != nil {
		// Branch may already exist, try checkout
		if err2 := git.CheckoutBranch(wo.BranchName); err2 != nil {
			return fmt.Errorf("create/checkout branch %s: create=%v checkout=%v", wo.BranchName, err, err2)
		}
	} else {
		if err := git.CheckoutBranch(wo.BranchName); err != nil {
			return fmt.Errorf("checkout branch %s: %w", wo.BranchName, err)
		}
	}

	// Advance to mutating
	_, err = d.AdvanceWorkOrder(ctx, woID, WOStatusMutating, "pipeline", "branch prepared: "+wo.BranchName)
	return err
}

// ResetWorkOrderBranch checks out the base branch for a work order (cleanup after pipeline).
func (d *Dash) ResetWorkOrderBranch(ctx context.Context, woID uuid.UUID, git GitClient) error {
	wo, err := d.GetWorkOrder(ctx, woID)
	if err != nil {
		return err
	}
	base := wo.BaseBranch
	if base == "" {
		base = "main"
	}
	return git.CheckoutBranch(base)
}

// RunFullPipeline executes the complete pipeline: build gate → synthesis → merge.
// It assumes the work order is in mutating state and the agent has committed changes.
// A single worktree is created and shared across both build gate and synthesis stages.
func (d *Dash) RunFullPipeline(ctx context.Context, woID uuid.UUID, git GitClient) (*PipelineResult, error) {
	defer d.ResetWorkOrderBranch(ctx, woID, git)

	wo, err := d.GetWorkOrder(ctx, woID)
	if err != nil {
		return nil, fmt.Errorf("get work order: %w", err)
	}

	result := &PipelineResult{Stage: "build_gate"}

	// Create a single worktree for the entire pipeline
	wtPath := fmt.Sprintf("/tmp/dash-wo/%s", wo.Node.ID)
	if err := git.AddWorktree(wtPath, wo.BranchName); err != nil {
		result.Error = err.Error()
		return result, fmt.Errorf("add worktree: %w", err)
	}
	defer git.RemoveWorktree(wtPath)

	// 1. Build gate (reuse worktree)
	gateResult, err := RunBuildGate(git, wo, wtPath)
	if err != nil {
		result.Error = err.Error()
		return result, err
	}
	result.Gate = gateResult

	if !gateResult.Passed {
		result.Passed = false
		d.AdvanceWorkOrder(ctx, woID, WOStatusBuildFailed, "pipeline", "build gate failed")
		return result, nil
	}

	d.AdvanceWorkOrder(ctx, woID, WOStatusBuildPassed, "pipeline", "build gate passed")
	result.Stage = "synthesis"

	// 2. Synthesis (reuse worktree)
	synthResult, err := d.RunSynthesisPipeline(ctx, woID, git, wtPath)
	if err != nil {
		result.Error = err.Error()
		return result, err
	}
	result.Synthesis = synthResult

	// Refresh WO to get latest status after synthesis pipeline
	wo, _ = d.GetWorkOrder(ctx, woID)
	result.Stage = string(wo.Status)
	result.Passed = wo.Status == WOStatusMerged || wo.Status == WOStatusMergePending

	return result, nil
}
