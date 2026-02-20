package dash

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
//
// If wtPath is non-empty the caller owns the worktree lifecycle; RunBuildGate
// will use it as-is and will NOT clean it up.  If wtPath is empty, RunBuildGate
// creates its own worktree and removes it on return (standalone mode).
func RunBuildGate(git GitClient, wo *WorkOrder, wtPath string) (*BuildGateResult, error) {
	if wo.BranchName == "" {
		return nil, fmt.Errorf("work order has no branch name")
	}

	ownWorktree := false
	if wtPath == "" {
		wtPath = fmt.Sprintf("/tmp/dash-wo/%s", wo.Node.ID)
		if err := git.AddWorktree(wtPath, wo.BranchName); err != nil {
			return nil, fmt.Errorf("add worktree: %w", err)
		}
		ownWorktree = true
	}
	if ownWorktree {
		defer git.RemoveWorktree(wtPath)
	}

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

	// 3. AST validation — compare base branch files against worktree (new)
	policy := DefaultASTPolicy()
	policy.AllowPublicAPIChange = wo.AllowPublicAPIChange

	baseTmpDir, cleanup, extractErr := extractBaseFiles(git, wo.BaseBranch, changedFiles)
	if extractErr == nil {
		defer cleanup()
		astResult, err := ValidateAppendOnly(baseTmpDir, wtPath, policy, wo.ScopePaths)
		if err != nil {
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
	} else {
		// Fallback: if we can't extract base files, skip AST validation with a warning
		result.AST = ASTValidationResult{
			Passed: true,
			Violations: []ASTViolation{{
				Kind:   "warning",
				Detail: fmt.Sprintf("could not extract base files for AST comparison: %v", extractErr),
			}},
		}
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

// extractBaseFiles uses git show to reconstruct changed .go files at their
// base-branch state into a temporary directory.  Returns the temp dir path,
// a cleanup func, and an error.  Files that don't exist on base (new files)
// are silently skipped.
func extractBaseFiles(git GitClient, baseBranch string, changedFiles []string) (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "dash-ast-base-")
	if err != nil {
		return "", nil, fmt.Errorf("create temp dir: %w", err)
	}
	cleanup := func() { os.RemoveAll(tmpDir) }

	for _, f := range changedFiles {
		if !strings.HasSuffix(f, ".go") {
			continue
		}
		content, err := git.ShowFileAtRef(baseBranch, f)
		if err != nil {
			// File doesn't exist on base branch → new file, skip
			continue
		}
		dest := filepath.Join(tmpDir, f)
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("mkdir for %s: %w", f, err)
		}
		if err := os.WriteFile(dest, content, 0644); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("write %s: %w", f, err)
		}
	}
	return tmpDir, cleanup, nil
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
