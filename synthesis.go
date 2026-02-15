package dash

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SynthesisVerdict represents the outcome of a synthesis review.
type SynthesisVerdict string

const (
	VerdictApprove SynthesisVerdict = "approve"
	VerdictRevise  SynthesisVerdict = "revise"
	VerdictReject  SynthesisVerdict = "reject"
)

// SynthesisResult is the outcome of the synthesis review.
type SynthesisResult struct {
	Verdict       SynthesisVerdict `json:"verdict"`
	Reasoning     string           `json:"reasoning"`
	Score         int              `json:"score"`
	Patch         string           `json:"patch,omitempty"`
	FilesTouched  []string         `json:"files_touched,omitempty"`
	ChangeBudget  int              `json:"change_budget"`
	ActualChanges int              `json:"actual_changes"`
	ReviewerModel string           `json:"reviewer_model"`
}

const synthesisSystemPrompt = `You are a code evolution reviewer for the Dash graph system.
You review diffs produced by automated agents and decide whether to approve, revise, or reject.

Review criteria:
1. Code correctness: Does the change do what it claims?
2. Append-only: No deletions of existing code
3. Scope compliance: Only files within the allowed scope are modified
4. Test coverage: New code should have tests
5. No regressions: Existing functionality preserved

Respond with ONLY valid JSON (no markdown fences):
{
  "verdict": "approve" | "revise" | "reject",
  "reasoning": "explanation of your decision",
  "score": 0-100,
  "patch": "unified diff format patch if revisions needed, empty if approve",
  "files_touched": ["list", "of", "files"],
  "change_budget": 200,
  "actual_changes": 150
}

Rules:
- Score 80+ → approve
- Score 40-79 → revise (provide patch with improvements)
- Score <40 → reject
- If actual_changes > change_budget → reject
- Always list files_touched accurately`

// RunSynthesis executes the synthesis review pipeline for a work order.
// It fetches the diff, sends it to the synthesizer model, and processes the result.
func (d *Dash) RunSynthesis(ctx context.Context, woID uuid.UUID, git GitClient) (*SynthesisResult, error) {
	wo, err := d.GetWorkOrder(ctx, woID)
	if err != nil {
		return nil, fmt.Errorf("get work order: %w", err)
	}

	if wo.Status != WOStatusBuildPassed {
		return nil, fmt.Errorf("work order must be in build_passed state, currently %s", wo.Status)
	}

	// Get the full diff
	diff, err := git.UnifiedDiff(wo.BaseBranch)
	if err != nil {
		return nil, fmt.Errorf("unified diff: %w", err)
	}

	if diff == "" {
		return &SynthesisResult{
			Verdict:   VerdictReject,
			Reasoning: "no changes found in diff",
			Score:     0,
		}, nil
	}

	// Build context for review
	var userPrompt strings.Builder
	userPrompt.WriteString(fmt.Sprintf("## Work Order: %s\n", wo.Node.Name))
	userPrompt.WriteString(fmt.Sprintf("Agent: %s\n", wo.AgentKey))
	userPrompt.WriteString(fmt.Sprintf("Branch: %s → %s\n", wo.BaseBranch, wo.BranchName))
	userPrompt.WriteString(fmt.Sprintf("Scope: %s\n", strings.Join(wo.ScopePaths, ", ")))
	userPrompt.WriteString(fmt.Sprintf("Allow public API change: %v\n\n", wo.AllowPublicAPIChange))

	// Add context pack if available
	if d.router != nil {
		taskDesc := wo.Node.Name
		if wo.TaskID != nil {
			if taskNode, err := d.GetNodeActive(ctx, *wo.TaskID); err == nil {
				var taskData map[string]any
				if json.Unmarshal(taskNode.Data, &taskData) == nil {
					if desc, ok := taskData["description"].(string); ok {
						taskDesc = desc
					}
				}
			}
		}
		pack, err := d.AssembleContextPack(ctx, taskDesc, ProfilePlan, nil)
		if err == nil && pack != nil {
			userPrompt.WriteString("## Context\n")
			userPrompt.WriteString(pack.RenderForPrompt())
			userPrompt.WriteString("\n")
		}
	}

	userPrompt.WriteString("## Diff\n```diff\n")
	userPrompt.WriteString(diff)
	userPrompt.WriteString("\n```\n")

	// Call synthesizer
	if d.router == nil {
		return nil, fmt.Errorf("LLM router not configured")
	}

	response, err := d.router.CompleteWithRole(ctx, "synthesizer", synthesisSystemPrompt, userPrompt.String())
	if err != nil {
		return nil, fmt.Errorf("synthesizer call: %w", err)
	}

	// Parse response
	result, err := parseSynthesisResponse(response)
	if err != nil {
		return nil, fmt.Errorf("parse synthesis response: %w", err)
	}

	// Enforce budget
	if result.ActualChanges > result.ChangeBudget && result.ChangeBudget > 0 {
		result.Verdict = VerdictReject
		result.Reasoning = fmt.Sprintf("change budget exceeded: %d > %d. %s", result.ActualChanges, result.ChangeBudget, result.Reasoning)
	}

	// Enforce scope on files_touched
	if len(result.FilesTouched) > 0 && len(wo.ScopePaths) > 0 {
		scopeResult := CheckScope(result.FilesTouched, wo.ScopePaths)
		if !scopeResult.Passed {
			result.Verdict = VerdictReject
			result.Reasoning = fmt.Sprintf("patch touches files outside scope: %v. %s", scopeResult.OutOfScope, result.Reasoning)
		}
	}

	return result, nil
}

// ApplyPatch applies a unified diff patch to the worktree with dry-run validation.
func ApplyPatch(git GitClient, wtPath, patch string) error {
	if patch == "" {
		return nil
	}

	// Write patch to temp file
	patchFile := filepath.Join(os.TempDir(), fmt.Sprintf("dash-patch-%d.diff", time.Now().UnixNano()))
	if err := os.WriteFile(patchFile, []byte(patch), 0644); err != nil {
		return fmt.Errorf("write patch file: %w", err)
	}
	defer os.Remove(patchFile)

	// Dry-run first
	dryCmd := exec.Command("git", "apply", "--check", patchFile)
	dryCmd.Dir = wtPath
	if dryOut, err := dryCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("patch dry-run failed: %s: %w", string(dryOut), err)
	}

	// Apply
	applyCmd := exec.Command("git", "apply", patchFile)
	applyCmd.Dir = wtPath
	if applyOut, err := applyCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("patch apply failed: %s: %w", string(applyOut), err)
	}

	return nil
}

// RunSynthesisPipeline orchestrates the full synthesis flow:
// review → optional patch → rebuild → commit → push → PR.
func (d *Dash) RunSynthesisPipeline(ctx context.Context, woID uuid.UUID, git GitClient) (*SynthesisResult, error) {
	// 1. Run synthesis review
	result, err := d.RunSynthesis(ctx, woID, git)
	if err != nil {
		return nil, err
	}

	wo, err := d.GetWorkOrder(ctx, woID)
	if err != nil {
		return result, err
	}

	// Advance to synthesis_pending
	d.AdvanceWorkOrder(ctx, woID, WOStatusSynthesisPending, "synthesizer", fmt.Sprintf("verdict=%s score=%d", result.Verdict, result.Score))

	// 2. Handle verdict
	switch result.Verdict {
	case VerdictReject:
		d.AdvanceWorkOrder(ctx, woID, WOStatusRejected, "synthesizer", result.Reasoning)
		return result, nil

	case VerdictRevise:
		if result.Patch != "" {
			wtPath := fmt.Sprintf("/tmp/dash-wo/%s", wo.Node.ID)
			if err := ApplyPatch(git, wtPath, result.Patch); err != nil {
				d.AdvanceWorkOrder(ctx, woID, WOStatusRejected, "synthesizer", fmt.Sprintf("patch apply failed: %v", err))
				return result, nil
			}

			// Rebuild after patch
			gateResult, err := RunBuildGate(git, wo)
			if err != nil || !gateResult.Passed {
				reason := "rebuild after patch failed"
				if err != nil {
					reason = err.Error()
				}
				d.AdvanceWorkOrder(ctx, woID, WOStatusRejected, "synthesizer", reason)
				return result, nil
			}

			// Commit the patch
			if err := git.CommitAll(fmt.Sprintf("synthesis: apply reviewer patch for %s", wo.Node.Name)); err != nil {
				return result, fmt.Errorf("commit patch: %w", err)
			}
		}
		// Fall through to merge flow
		fallthrough

	case VerdictApprove:
		// 3. Push and create PR
		if err := git.PushBranch(wo.BranchName); err != nil {
			return result, fmt.Errorf("push: %w", err)
		}

		prTitle := fmt.Sprintf("[dash-auto] %s", wo.Node.Name)
		prBody := fmt.Sprintf("## Auto-generated by Dash synthesis pipeline\n\nAgent: %s\nScore: %d\nVerdict: %s\n\n%s",
			wo.AgentKey, result.Score, result.Verdict, result.Reasoning)

		prNum, prURL, err := git.CreatePR(prTitle, prBody, wo.BaseBranch)
		if err != nil {
			return result, fmt.Errorf("create PR: %w", err)
		}

		// Store PR info
		d.UpdateWorkOrderPR(ctx, woID, prNum, prURL)

		// 4. Advance to merge_pending
		d.AdvanceWorkOrder(ctx, woID, WOStatusMergePending, "synthesizer", fmt.Sprintf("PR #%d created: %s", prNum, prURL))

		// 5. Check CI and merge
		checksStatus, err := git.PRChecksStatus(prNum)
		if err == nil {
			d.UpdateWorkOrderChecks(ctx, woID, checksStatus)
		}

		if checksStatus == "pass" {
			if err := git.MergePR(prNum); err != nil {
				// Merge failed but PR exists — stay in merge_pending
				return result, nil
			}
			d.AdvanceWorkOrder(ctx, woID, WOStatusMerged, "synthesizer", fmt.Sprintf("PR #%d merged", prNum))
		}
		// If checks are pending or failed, stay in merge_pending
	}

	return result, nil
}

// parseSynthesisResponse parses the JSON response from the synthesizer model.
func parseSynthesisResponse(response string) (*SynthesisResult, error) {
	// Strip markdown code fences if present
	response = strings.TrimSpace(response)
	if strings.HasPrefix(response, "```") {
		if idx := strings.Index(response[3:], "\n"); idx >= 0 {
			response = response[3+idx+1:]
		}
		if strings.HasSuffix(response, "```") {
			response = response[:len(response)-3]
		}
		response = strings.TrimSpace(response)
	}

	var result SynthesisResult
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return nil, fmt.Errorf("invalid JSON from synthesizer: %w (response: %.200s)", err, response)
	}

	// Validate verdict
	switch result.Verdict {
	case VerdictApprove, VerdictRevise, VerdictReject:
		// ok
	default:
		return nil, fmt.Errorf("invalid verdict: %q", result.Verdict)
	}

	return &result, nil
}
