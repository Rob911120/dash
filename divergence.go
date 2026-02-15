package dash

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// DivergenceCheck represents a single claim-vs-artifact verification.
type DivergenceCheck struct {
	Claim    string `json:"claim"`
	Artifact string `json:"artifact"`
	Match    bool   `json:"match"`
	Detail   string `json:"detail,omitempty"`
}

// DivergenceResult holds all checks for a work order.
type DivergenceResult struct {
	WorkOrderID uuid.UUID         `json:"work_order_id"`
	Passed      bool              `json:"passed"`
	Checks      []DivergenceCheck `json:"checks"`
}

// checkTestsPass verifies the "tests pass" claim against the build gate result.
func checkTestsPass(buildResult *BuildGateResult) DivergenceCheck {
	dc := DivergenceCheck{
		Claim:    "tests pass",
		Artifact: "BuildGateResult.Test.Passed",
	}
	if buildResult != nil && buildResult.Test.Passed {
		dc.Match = true
	} else {
		dc.Match = false
		dc.Detail = "test step did not pass"
	}
	return dc
}

// checkFilesCreated verifies the "files created" claim by checking that
// the work order has at least one entry in FilesChanged.
func checkFilesCreated(wo *WorkOrder) DivergenceCheck {
	dc := DivergenceCheck{
		Claim:    "files created",
		Artifact: "WorkOrder.FilesChanged",
	}
	if len(wo.FilesChanged) > 0 {
		dc.Match = true
	} else {
		dc.Match = false
		dc.Detail = "no files recorded in work order"
	}
	return dc
}

// checkMerged verifies the "merged" claim by checking both status and CI checks.
func checkMerged(wo *WorkOrder) DivergenceCheck {
	dc := DivergenceCheck{
		Claim:    "merged",
		Artifact: "WorkOrder.Status + ChecksStatus",
	}
	if wo.Status == WOStatusMerged && wo.ChecksStatus == "pass" {
		dc.Match = true
	} else {
		dc.Match = false
		dc.Detail = fmt.Sprintf("status=%s checks_status=%s", wo.Status, wo.ChecksStatus)
	}
	return dc
}

// checkNoViolations verifies the "no violations" claim against scope and AST results.
func checkNoViolations(buildResult *BuildGateResult) DivergenceCheck {
	dc := DivergenceCheck{
		Claim:    "no violations",
		Artifact: "ScopeCheckResult.Passed + ASTValidationResult.Passed",
	}
	if buildResult != nil && buildResult.Scope.Passed && buildResult.AST.Passed {
		dc.Match = true
	} else {
		dc.Match = false
		if buildResult == nil {
			dc.Detail = "no build gate result provided"
		} else {
			dc.Detail = fmt.Sprintf("scope_passed=%v ast_passed=%v", buildResult.Scope.Passed, buildResult.AST.Passed)
		}
	}
	return dc
}

// CheckClaims verifies work order claims against actual artifacts.
//
// Hard-coupled checks (no text heuristics):
//   - "tests pass"     -> BuildGateResult.Test.Passed
//   - "files created"  -> WorkOrder.FilesChanged
//   - "merged"         -> WorkOrder.Status == "merged" && ChecksStatus == "pass"
//   - "no violations"  -> ScopeCheckResult.Passed && ASTValidationResult.Passed
func (d *Dash) CheckClaims(ctx context.Context, woID uuid.UUID, buildResult *BuildGateResult) (*DivergenceResult, error) {
	wo, err := d.GetWorkOrder(ctx, woID)
	if err != nil {
		return nil, fmt.Errorf("get work order %s: %w", woID, err)
	}

	checks := []DivergenceCheck{
		checkTestsPass(buildResult),
		checkFilesCreated(wo),
		checkMerged(wo),
		checkNoViolations(buildResult),
	}

	allMatch := true
	for _, c := range checks {
		if !c.Match {
			allMatch = false
			break
		}
	}

	return &DivergenceResult{
		WorkOrderID: woID,
		Passed:      allMatch,
		Checks:      checks,
	}, nil
}
