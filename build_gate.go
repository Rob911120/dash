package dash

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// BuildResult holds the outcome of a build or test step.
type BuildResult struct {
	Passed   bool   `json:"passed"`
	Output   string `json:"output,omitempty"`
	Duration time.Duration `json:"duration"`
}

// BuildGateResult is the combined outcome of all gate checks.
type BuildGateResult struct {
	Build      BuildResult         `json:"build"`
	Test       BuildResult         `json:"test"`
	AST        ASTValidationResult `json:"ast"`
	Scope      ScopeCheckResult    `json:"scope"`
	Passed     bool                `json:"passed"`
	GoEnv      map[string]string   `json:"go_env,omitempty"`
	WorktreeAt string              `json:"worktree_at,omitempty"`
}

// RunBuildGate executes the full build gate in an isolated worktree.
// It checks scope, AST policy, build, and test — all in a clean worktree.
// The worktree is cleaned up even on panic via defer.
func RunBuildGate(git GitClient, wo *WorkOrder) (*BuildGateResult, error) {
	if wo.BranchName == "" {
		return nil, fmt.Errorf("work order has no branch name")
	}

	wtPath := fmt.Sprintf("/tmp/dash-wo/%s", wo.Node.ID)

	// Create worktree
	if err := git.AddWorktree(wtPath, wo.BranchName); err != nil {
		return nil, fmt.Errorf("add worktree: %w", err)
	}
	defer git.RemoveWorktree(wtPath) // bombproof cleanup

	result := &BuildGateResult{
		WorktreeAt: wtPath,
	}

	// Capture Go environment for reproducibility
	result.GoEnv = captureGoEnv(wtPath)

	// 1. Get changed files
	changedFiles, err := git.ChangedFiles(wo.BaseBranch)
	if err != nil {
		return result, fmt.Errorf("changed files: %w", err)
	}
	wo.FilesChanged = changedFiles

	// 2. Scope check
	result.Scope = *CheckScope(changedFiles, wo.ScopePaths)
	if !result.Scope.Passed {
		return result, nil // fail fast
	}

	// 3. AST validation (in worktree context)
	policy := DefaultASTPolicy()
	policy.AllowPublicAPIChange = wo.AllowPublicAPIChange

	// For AST validation we need the base and new paths.
	// In a worktree, the files represent the "new" state.
	// We compare against the base branch's state.
	// For now, validate that changed .go files parse correctly.
	// Full AST comparison requires checking out the base branch too,
	// which we do by comparing the worktree (new) against repo root (base).
	repoRoot := wo.RepoRoot
	if repoRoot == "" {
		repoRoot = "." // fallback
	}
	astResult, err := ValidateAppendOnly(repoRoot, wtPath, policy, wo.ScopePaths)
	if err != nil {
		// AST parse error is not a gate failure, log it
		result.AST = ASTValidationResult{
			Passed: false,
			Violations: []ASTViolation{{
				Kind:   "parse_error",
				Detail: err.Error(),
			}},
		}
	} else {
		result.AST = *astResult
	}
	if !result.AST.Passed {
		return result, nil // fail fast
	}

	// 4. Build in worktree
	buildStart := time.Now()
	buildCmd := exec.Command("go", "build", "./...")
	buildCmd.Dir = wtPath
	buildOut, buildErr := buildCmd.CombinedOutput()
	result.Build = BuildResult{
		Passed:   buildErr == nil,
		Output:   capString(string(buildOut), 8192),
		Duration: time.Since(buildStart),
	}
	if !result.Build.Passed {
		return result, nil // fail fast
	}

	// 5. Test in worktree
	testStart := time.Now()
	testCmd := exec.Command("go", "test", "./...")
	testCmd.Dir = wtPath
	testOut, testErr := testCmd.CombinedOutput()
	result.Test = BuildResult{
		Passed:   testErr == nil,
		Output:   capString(string(testOut), 8192),
		Duration: time.Since(testStart),
	}

	// Final verdict
	result.Passed = result.Scope.Passed && result.AST.Passed && result.Build.Passed && result.Test.Passed

	return result, nil
}

// captureGoEnv captures key Go environment variables for reproducibility.
func captureGoEnv(dir string) map[string]string {
	env := make(map[string]string)
	keys := []string{"GOOS", "GOARCH", "GOMODCACHE", "GOPATH", "GOVERSION"}

	for _, key := range keys {
		cmd := exec.Command("go", "env", key)
		cmd.Dir = dir
		out, err := cmd.Output()
		if err == nil {
			env[key] = strings.TrimSpace(string(out))
		}
	}
	return env
}
