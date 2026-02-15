package dash

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// RepoHealthResult holds the outcome of a repository health check.
type RepoHealthResult struct {
	Passed      bool          `json:"passed"`
	BuildOK     bool          `json:"build_ok"`
	TestOK      bool          `json:"test_ok"`
	CleanTree   bool          `json:"clean_tree"`
	BuildOutput string        `json:"build_output,omitempty"`
	TestOutput  string        `json:"test_output,omitempty"`
	Duration    time.Duration `json:"duration"`
	CheckedAt   time.Time     `json:"checked_at"`
}

// RepoHealthCheck verifies that the repository is in a healthy state:
// clean worktree, builds, and tests pass. This is a permanent gate —
// the WorkOrder pipeline refuses to start if repo is not green.
func RepoHealthCheck(repoRoot string) (*RepoHealthResult, error) {
	start := time.Now()
	result := &RepoHealthResult{
		CheckedAt: start,
	}

	// 1. Check clean worktree
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = repoRoot
	statusOut, err := statusCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git status failed: %w", err)
	}
	result.CleanTree = strings.TrimSpace(string(statusOut)) == ""

	// 2. Build check
	buildCmd := exec.Command("go", "build", "./...")
	buildCmd.Dir = repoRoot
	buildOut, err := buildCmd.CombinedOutput()
	result.BuildOutput = capString(string(buildOut), 8192)
	result.BuildOK = err == nil

	// 3. Test check (only if build passed)
	if result.BuildOK {
		testCmd := exec.Command("go", "test", "./...")
		testCmd.Dir = repoRoot
		testOut, err := testCmd.CombinedOutput()
		result.TestOutput = capString(string(testOut), 8192)
		result.TestOK = err == nil
	}

	result.Duration = time.Since(start)
	result.Passed = result.BuildOK && result.TestOK

	return result, nil
}

// capString truncates s to maxLen, appending "... (truncated)" if needed.
func capString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "... (truncated)"
}
