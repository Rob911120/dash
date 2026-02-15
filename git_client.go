package dash

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// maxDiffBytes is the maximum size of a unified diff returned by UnifiedDiff (500KB).
const maxDiffBytes = 500 * 1024

// maxStderrBytes is the maximum size of stderr captured for logging (8KB).
const maxStderrBytes = 8 * 1024

// ---------------------------------------------------------------------------
// GitClient interface
// ---------------------------------------------------------------------------

// GitClient defines all git operations needed by the WorkOrder pipeline.
type GitClient interface {
	CreateBranch(name string) error
	CheckoutBranch(name string) error
	CommitAll(message string) error
	CurrentHash() (string, error)
	ChangedFiles(baseBranch string) ([]string, error)
	UnifiedDiff(baseBranch string) (string, error)
	PushBranch(name string) error
	CreatePR(title, body, base string) (prNum int, prURL string, err error)
	MergePR(prNum int) error
	AddWorktree(path, branch string) error
	RemoveWorktree(path string) error
	PRChecksStatus(prNum int) (string, error)
	Status() (*GitStatus, error)
	GHAuthCheck() error
}

// ---------------------------------------------------------------------------
// ExecGitClient -- real implementation using os/exec
// ---------------------------------------------------------------------------

// ExecGitClient implements GitClient by shelling out to git and gh.
type ExecGitClient struct {
	repoRoot string
	logger   func(cmd string, args []string, exitCode int, stderr string)
}

// NewExecGitClient returns an ExecGitClient rooted at repoRoot.
// If no logger is needed, pass nil and a no-op logger will be used.
func NewExecGitClient(repoRoot string) *ExecGitClient {
	return &ExecGitClient{
		repoRoot: repoRoot,
		logger:   nil,
	}
}

// SetLogger sets the logger function called after every command execution.
func (g *ExecGitClient) SetLogger(fn func(cmd string, args []string, exitCode int, stderr string)) {
	g.logger = fn
}

// run executes a command with Dir=repoRoot, captures stdout+stderr, logs, and
// returns stdout bytes on success or an error containing stderr.
func (g *ExecGitClient) run(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = g.repoRoot

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	// Cap stderr for logging.
	stderrStr := stderr.String()
	if len(stderrStr) > maxStderrBytes {
		stderrStr = stderrStr[:maxStderrBytes]
	}

	if g.logger != nil {
		g.logger(name, args, exitCode, stderrStr)
	}

	if err != nil {
		return nil, fmt.Errorf("%s %s failed (exit %d): %s", name, strings.Join(args, " "), exitCode, stderrStr)
	}
	return stdout.Bytes(), nil
}

// CreateBranch creates a new local branch.
func (g *ExecGitClient) CreateBranch(name string) error {
	_, err := g.run("git", "branch", name)
	return err
}

// CheckoutBranch switches to the named branch.
func (g *ExecGitClient) CheckoutBranch(name string) error {
	_, err := g.run("git", "checkout", name)
	return err
}

// CommitAll stages all changes and commits with the given message.
func (g *ExecGitClient) CommitAll(message string) error {
	if _, err := g.run("git", "add", "-A"); err != nil {
		return err
	}
	_, err := g.run("git", "commit", "-m", message)
	return err
}

// CurrentHash returns the full SHA of HEAD.
func (g *ExecGitClient) CurrentHash() (string, error) {
	out, err := g.run("git", "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ChangedFiles returns the list of files changed between baseBranch and HEAD.
func (g *ExecGitClient) ChangedFiles(baseBranch string) ([]string, error) {
	out, err := g.run("git", "diff", "--name-only", baseBranch+"...HEAD")
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}
	return strings.Split(raw, "\n"), nil
}

// UnifiedDiff returns the unified diff between baseBranch and HEAD, capped at 500KB.
func (g *ExecGitClient) UnifiedDiff(baseBranch string) (string, error) {
	out, err := g.run("git", "diff", baseBranch+"...HEAD")
	if err != nil {
		return "", err
	}
	s := string(out)
	if len(s) > maxDiffBytes {
		s = s[:maxDiffBytes]
	}
	return s, nil
}

// PushBranch pushes the named branch to origin.
func (g *ExecGitClient) PushBranch(name string) error {
	_, err := g.run("git", "push", "-u", "origin", name)
	return err
}

// CreatePR creates a pull request via gh CLI and returns the PR number and URL.
func (g *ExecGitClient) CreatePR(title, body, base string) (int, string, error) {
	out, err := g.run("gh", "pr", "create",
		"--title", title,
		"--body", body,
		"--base", base,
		"--json", "number,url",
	)
	if err != nil {
		return 0, "", err
	}

	var result struct {
		Number int    `json:"number"`
		URL    string `json:"url"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return 0, "", fmt.Errorf("parse gh pr create output: %w", err)
	}
	return result.Number, result.URL, nil
}

// MergePR merges a pull request by number using a merge commit.
func (g *ExecGitClient) MergePR(prNum int) error {
	_, err := g.run("gh", "pr", "merge", strconv.Itoa(prNum), "--merge")
	return err
}

// PRChecksStatus returns "pass", "fail", or "pending" for the given PR's checks.
func (g *ExecGitClient) PRChecksStatus(prNum int) (string, error) {
	out, err := g.run("gh", "pr", "checks", strconv.Itoa(prNum))
	if err != nil {
		// gh pr checks exits non-zero when checks fail; still parse output.
		// If we got no output at all, return the error.
		if out == nil {
			return "fail", err
		}
	}

	output := string(out)
	if strings.Contains(output, "fail") {
		return "fail", nil
	}
	if strings.Contains(output, "pending") {
		return "pending", nil
	}
	return "pass", nil
}

// AddWorktree creates a linked worktree at path for branch.
func (g *ExecGitClient) AddWorktree(path, branch string) error {
	_, err := g.run("git", "worktree", "add", path, branch)
	return err
}

// RemoveWorktree forcefully removes the worktree at path.
func (g *ExecGitClient) RemoveWorktree(path string) error {
	_, err := g.run("git", "worktree", "remove", path, "--force")
	return err
}

// Status returns the current git status using the existing GitStatus struct.
func (g *ExecGitClient) Status() (*GitStatus, error) {
	s := GetGitStatus(g.repoRoot)
	if s == nil {
		return nil, fmt.Errorf("git status unavailable for %s", g.repoRoot)
	}
	return s, nil
}

// GHAuthCheck verifies that gh CLI is authenticated.
func (g *ExecGitClient) GHAuthCheck() error {
	_, err := g.run("gh", "auth", "status")
	return err
}

// ---------------------------------------------------------------------------
// FakeGitClient -- in-memory implementation for tests
// ---------------------------------------------------------------------------

// FakePR represents a pull request in the FakeGitClient.
type FakePR struct {
	Num    int
	Title  string
	Status string // "pass" | "fail" | "pending"
	Merged bool
}

// FakeGitClient is an in-memory GitClient for testing purposes.
type FakeGitClient struct {
	Branches      map[string]bool
	CurrentBranch string
	Files         map[string]string // filename -> content
	Commits       []string
	Worktrees     map[string]string // path -> branch
	PRs           map[int]FakePR
	NextPRNum     int
	GHAuthed      bool
	Err           error // if set, all operations return this
}

// NewFakeGitClient creates a FakeGitClient pre-configured with sensible defaults.
func NewFakeGitClient() *FakeGitClient {
	return &FakeGitClient{
		Branches:      map[string]bool{"main": true},
		CurrentBranch: "main",
		Files:         make(map[string]string),
		Commits:       nil,
		Worktrees:     make(map[string]string),
		PRs:           make(map[int]FakePR),
		NextPRNum:     1,
		GHAuthed:      true,
		Err:           nil,
	}
}

func (f *FakeGitClient) CreateBranch(name string) error {
	if f.Err != nil {
		return f.Err
	}
	if f.Branches[name] {
		return fmt.Errorf("branch %q already exists", name)
	}
	f.Branches[name] = true
	return nil
}

func (f *FakeGitClient) CheckoutBranch(name string) error {
	if f.Err != nil {
		return f.Err
	}
	if !f.Branches[name] {
		return fmt.Errorf("branch %q does not exist", name)
	}
	f.CurrentBranch = name
	return nil
}

func (f *FakeGitClient) CommitAll(message string) error {
	if f.Err != nil {
		return f.Err
	}
	f.Commits = append(f.Commits, message)
	return nil
}

func (f *FakeGitClient) CurrentHash() (string, error) {
	if f.Err != nil {
		return "", f.Err
	}
	// Return a deterministic fake hash based on commit count.
	h := fmt.Sprintf("fakehash%04d", len(f.Commits))
	return h, nil
}

func (f *FakeGitClient) ChangedFiles(baseBranch string) ([]string, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	var files []string
	for name := range f.Files {
		files = append(files, name)
	}
	return files, nil
}

func (f *FakeGitClient) UnifiedDiff(baseBranch string) (string, error) {
	if f.Err != nil {
		return "", f.Err
	}
	var buf strings.Builder
	for name, content := range f.Files {
		buf.WriteString(fmt.Sprintf("diff --git a/%s b/%s\n", name, name))
		buf.WriteString(fmt.Sprintf("+++ b/%s\n", name))
		for _, line := range strings.Split(content, "\n") {
			buf.WriteString("+" + line + "\n")
		}
	}
	s := buf.String()
	if len(s) > maxDiffBytes {
		s = s[:maxDiffBytes]
	}
	return s, nil
}

func (f *FakeGitClient) PushBranch(name string) error {
	if f.Err != nil {
		return f.Err
	}
	if !f.Branches[name] {
		return fmt.Errorf("branch %q does not exist", name)
	}
	return nil
}

func (f *FakeGitClient) CreatePR(title, body, base string) (int, string, error) {
	if f.Err != nil {
		return 0, "", f.Err
	}
	if !f.GHAuthed {
		return 0, "", fmt.Errorf("gh not authenticated")
	}
	num := f.NextPRNum
	f.NextPRNum++
	f.PRs[num] = FakePR{
		Num:    num,
		Title:  title,
		Status: "pending",
		Merged: false,
	}
	url := fmt.Sprintf("https://github.com/fake/repo/pull/%d", num)
	return num, url, nil
}

func (f *FakeGitClient) MergePR(prNum int) error {
	if f.Err != nil {
		return f.Err
	}
	pr, ok := f.PRs[prNum]
	if !ok {
		return fmt.Errorf("PR #%d not found", prNum)
	}
	pr.Merged = true
	f.PRs[prNum] = pr
	return nil
}

func (f *FakeGitClient) PRChecksStatus(prNum int) (string, error) {
	if f.Err != nil {
		return "", f.Err
	}
	pr, ok := f.PRs[prNum]
	if !ok {
		return "", fmt.Errorf("PR #%d not found", prNum)
	}
	return pr.Status, nil
}

func (f *FakeGitClient) AddWorktree(path, branch string) error {
	if f.Err != nil {
		return f.Err
	}
	if _, exists := f.Worktrees[path]; exists {
		return fmt.Errorf("worktree %q already exists", path)
	}
	f.Worktrees[path] = branch
	return nil
}

func (f *FakeGitClient) RemoveWorktree(path string) error {
	if f.Err != nil {
		return f.Err
	}
	if _, exists := f.Worktrees[path]; !exists {
		return fmt.Errorf("worktree %q not found", path)
	}
	delete(f.Worktrees, path)
	return nil
}

func (f *FakeGitClient) Status() (*GitStatus, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	return &GitStatus{
		Branch:      f.CurrentBranch,
		Uncommitted: len(f.Files),
	}, nil
}

func (f *FakeGitClient) GHAuthCheck() error {
	if f.Err != nil {
		return f.Err
	}
	if !f.GHAuthed {
		return fmt.Errorf("gh auth: not logged in")
	}
	return nil
}

// ---------------------------------------------------------------------------
// CleanStaleWorktrees -- worktree janitor
// ---------------------------------------------------------------------------

const worktreeBaseDir = "/tmp/dash-wo/"

// CleanStaleWorktrees removes worktree directories under /tmp/dash-wo/ that
// are older than maxAge, then runs "git worktree prune" on repoRoot.
func CleanStaleWorktrees(repoRoot string, maxAge time.Duration) (cleaned int, err error) {
	entries, dirErr := os.ReadDir(worktreeBaseDir)
	if dirErr != nil {
		if os.IsNotExist(dirErr) {
			// Nothing to clean; still prune.
			_ = pruneWorktrees(repoRoot)
			return 0, nil
		}
		return 0, fmt.Errorf("read %s: %w", worktreeBaseDir, dirErr)
	}

	cutoff := time.Now().Add(-maxAge)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			fullPath := filepath.Join(worktreeBaseDir, entry.Name())
			if rmErr := os.RemoveAll(fullPath); rmErr == nil {
				cleaned++
			}
		}
	}

	if pruneErr := pruneWorktrees(repoRoot); pruneErr != nil {
		return cleaned, fmt.Errorf("git worktree prune: %w", pruneErr)
	}
	return cleaned, nil
}

// pruneWorktrees runs "git worktree prune" in repoRoot.
func pruneWorktrees(repoRoot string) error {
	cmd := exec.Command("git", "worktree", "prune")
	cmd.Dir = repoRoot
	return cmd.Run()
}
