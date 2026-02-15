package dash

import (
	"os/exec"
	"strconv"
	"strings"
)

// GitStatus holds git repository status information.
type GitStatus struct {
	Branch      string
	Uncommitted int
}

// GetGitStatus retrieves git branch and uncommitted count.
// Returns nil if not a git repo or git is not available.
func GetGitStatus(cwd string) *GitStatus {
	// Check if git is available
	if _, err := exec.LookPath("git"); err != nil {
		return nil
	}

	// Get current branch
	branchCmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	branchCmd.Dir = cwd
	branchOut, err := branchCmd.Output()
	if err != nil {
		return nil // Not a git repo
	}

	branch := strings.TrimSpace(string(branchOut))
	if branch == "" {
		return nil
	}

	status := &GitStatus{
		Branch: branch,
	}

	// Count uncommitted changes (staged + unstaged + untracked)
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = cwd
	statusOut, err := statusCmd.Output()
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(statusOut)), "\n")
		if len(lines) == 1 && lines[0] == "" {
			status.Uncommitted = 0
		} else {
			status.Uncommitted = len(lines)
		}
	}

	// If branch is HEAD (detached), try to get commit short hash
	if branch == "HEAD" {
		hashCmd := exec.Command("git", "rev-parse", "--short", "HEAD")
		hashCmd.Dir = cwd
		if hashOut, err := hashCmd.Output(); err == nil {
			status.Branch = strings.TrimSpace(string(hashOut))
		}
	}

	return status
}

// getUncommittedCount returns the count of uncommitted changes.
// This is a helper that can be used independently.
func getUncommittedCount(cwd string) int {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return 0
	}

	output := strings.TrimSpace(string(out))
	if output == "" {
		return 0
	}

	return len(strings.Split(output, "\n"))
}

// getAheadBehind returns how many commits ahead/behind the branch is.
// This could be used for future enhancements.
func getAheadBehind(cwd string) (ahead, behind int) {
	cmd := exec.Command("git", "rev-list", "--left-right", "--count", "@{upstream}...HEAD")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return 0, 0
	}

	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) != 2 {
		return 0, 0
	}

	behind, _ = strconv.Atoi(parts[0])
	ahead, _ = strconv.Atoi(parts[1])
	return ahead, behind
}
