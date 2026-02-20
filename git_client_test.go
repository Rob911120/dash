package dash

import (
	"fmt"
	"strings"
	"testing"
)

// TestFullWorkflow exercises the complete branch-to-merge lifecycle.
func TestFullWorkflow(t *testing.T) {
	gc := NewFakeGitClient()

	// 1. Create and checkout a feature branch.
	if err := gc.CreateBranch("feature/test-123"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if err := gc.CheckoutBranch("feature/test-123"); err != nil {
		t.Fatalf("CheckoutBranch: %v", err)
	}
	if gc.CurrentBranch != "feature/test-123" {
		t.Fatalf("CurrentBranch = %q, want %q", gc.CurrentBranch, "feature/test-123")
	}

	// 2. Stage a file and commit.
	gc.Files["main.go"] = "package main\n"
	if err := gc.CommitAll("initial commit"); err != nil {
		t.Fatalf("CommitAll: %v", err)
	}
	if len(gc.Commits) != 1 {
		t.Fatalf("len(Commits) = %d, want 1", len(gc.Commits))
	}

	// 3. Push the branch.
	if err := gc.PushBranch("feature/test-123"); err != nil {
		t.Fatalf("PushBranch: %v", err)
	}

	// 4. Create a PR.
	prNum, prURL, err := gc.CreatePR("Add main.go", "initial file", "main")
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	if prNum != 1 {
		t.Errorf("prNum = %d, want 1", prNum)
	}
	if prURL == "" {
		t.Error("prURL is empty")
	}

	// 5. Check status (default pending), then flip to pass.
	status, err := gc.PRChecksStatus(prNum)
	if err != nil {
		t.Fatalf("PRChecksStatus: %v", err)
	}
	if status != "pending" {
		t.Errorf("checks status = %q, want %q", status, "pending")
	}

	// Simulate CI passing.
	pr := gc.PRs[prNum]
	pr.Status = "pass"
	gc.PRs[prNum] = pr

	status, err = gc.PRChecksStatus(prNum)
	if err != nil {
		t.Fatalf("PRChecksStatus after pass: %v", err)
	}
	if status != "pass" {
		t.Errorf("checks status = %q, want %q", status, "pass")
	}

	// 6. Merge the PR.
	if err := gc.MergePR(prNum); err != nil {
		t.Fatalf("MergePR: %v", err)
	}
	if !gc.PRs[prNum].Merged {
		t.Error("PR should be merged")
	}
}

// TestGHAuthCheckFailure verifies that GHAuthCheck returns an error when not authenticated.
func TestGHAuthCheckFailure(t *testing.T) {
	gc := NewFakeGitClient()
	gc.GHAuthed = false

	if err := gc.GHAuthCheck(); err == nil {
		t.Fatal("GHAuthCheck should return error when not authed")
	}
}

// TestGHAuthCheckSuccess verifies that GHAuthCheck succeeds when authenticated.
func TestGHAuthCheckSuccess(t *testing.T) {
	gc := NewFakeGitClient()
	// GHAuthed is true by default.
	if err := gc.GHAuthCheck(); err != nil {
		t.Fatalf("GHAuthCheck: %v", err)
	}
}

// TestWorktreeLifecycle tests adding and removing worktrees.
func TestWorktreeLifecycle(t *testing.T) {
	gc := NewFakeGitClient()

	if err := gc.CreateBranch("wt-branch"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}

	// Add a worktree.
	wtPath := "/tmp/dash-wo/test-wt"
	if err := gc.AddWorktree(wtPath, "wt-branch"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if gc.Worktrees[wtPath] != "wt-branch" {
		t.Errorf("Worktrees[%s] = %q, want %q", wtPath, gc.Worktrees[wtPath], "wt-branch")
	}

	// Adding the same path again should fail.
	if err := gc.AddWorktree(wtPath, "wt-branch"); err == nil {
		t.Error("AddWorktree should fail for duplicate path")
	}

	// Remove the worktree.
	if err := gc.RemoveWorktree(wtPath); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, exists := gc.Worktrees[wtPath]; exists {
		t.Error("worktree should be removed")
	}

	// Removing again should fail.
	if err := gc.RemoveWorktree(wtPath); err == nil {
		t.Error("RemoveWorktree should fail for missing path")
	}
}

// TestUnifiedDiffReturnsContent verifies that UnifiedDiff returns a diff for staged files.
func TestUnifiedDiffReturnsContent(t *testing.T) {
	gc := NewFakeGitClient()
	gc.Files["hello.go"] = "package hello\n"
	gc.Files["world.go"] = "package world\n"

	diff, err := gc.UnifiedDiff("main")
	if err != nil {
		t.Fatalf("UnifiedDiff: %v", err)
	}
	if diff == "" {
		t.Fatal("UnifiedDiff returned empty string, expected content")
	}
	if !containsAll(diff, "hello.go", "world.go") {
		t.Errorf("diff should mention both files, got:\n%s", diff)
	}
}

// TestChangedFilesReturnsList verifies ChangedFiles returns all tracked files.
func TestChangedFilesReturnsList(t *testing.T) {
	gc := NewFakeGitClient()
	gc.Files["a.go"] = "package a\n"
	gc.Files["b.go"] = "package b\n"
	gc.Files["c.go"] = "package c\n"

	files, err := gc.ChangedFiles("main")
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	if len(files) != 3 {
		t.Errorf("len(files) = %d, want 3", len(files))
	}

	// Verify all files present (order may vary due to map iteration).
	have := make(map[string]bool)
	for _, f := range files {
		have[f] = true
	}
	for _, want := range []string{"a.go", "b.go", "c.go"} {
		if !have[want] {
			t.Errorf("ChangedFiles missing %q", want)
		}
	}
}

// TestCurrentHash returns a deterministic hash.
func TestCurrentHash(t *testing.T) {
	gc := NewFakeGitClient()

	h1, err := gc.CurrentHash()
	if err != nil {
		t.Fatalf("CurrentHash: %v", err)
	}
	if h1 != "fakehash0000" {
		t.Errorf("CurrentHash = %q, want %q", h1, "fakehash0000")
	}

	gc.CommitAll("one")
	h2, _ := gc.CurrentHash()
	if h2 == h1 {
		t.Error("hash should change after commit")
	}
}

// TestGlobalError verifies that setting Err causes all operations to fail.
func TestGlobalError(t *testing.T) {
	gc := NewFakeGitClient()
	gc.Err = fmt.Errorf("simulated outage")

	if err := gc.CreateBranch("x"); err == nil {
		t.Error("expected error from CreateBranch")
	}
	if err := gc.CheckoutBranch("main"); err == nil {
		t.Error("expected error from CheckoutBranch")
	}
	if err := gc.CommitAll("msg"); err == nil {
		t.Error("expected error from CommitAll")
	}
	if _, err := gc.CurrentHash(); err == nil {
		t.Error("expected error from CurrentHash")
	}
	if _, err := gc.ChangedFiles("main"); err == nil {
		t.Error("expected error from ChangedFiles")
	}
	if _, err := gc.UnifiedDiff("main"); err == nil {
		t.Error("expected error from UnifiedDiff")
	}
	if err := gc.PushBranch("x"); err == nil {
		t.Error("expected error from PushBranch")
	}
	if _, _, err := gc.CreatePR("t", "b", "main"); err == nil {
		t.Error("expected error from CreatePR")
	}
	if err := gc.MergePR(1); err == nil {
		t.Error("expected error from MergePR")
	}
	if _, err := gc.PRChecksStatus(1); err == nil {
		t.Error("expected error from PRChecksStatus")
	}
	if err := gc.AddWorktree("/tmp/x", "main"); err == nil {
		t.Error("expected error from AddWorktree")
	}
	if err := gc.RemoveWorktree("/tmp/x"); err == nil {
		t.Error("expected error from RemoveWorktree")
	}
	if _, err := gc.Status(); err == nil {
		t.Error("expected error from Status")
	}
	if err := gc.GHAuthCheck(); err == nil {
		t.Error("expected error from GHAuthCheck")
	}
}

// TestStatus verifies the fake Status method.
func TestStatus(t *testing.T) {
	gc := NewFakeGitClient()
	gc.Files["dirty.go"] = "package dirty\n"

	s, err := gc.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if s.Branch != "main" {
		t.Errorf("Branch = %q, want %q", s.Branch, "main")
	}
	if s.Uncommitted != 1 {
		t.Errorf("Uncommitted = %d, want 1", s.Uncommitted)
	}
}

// TestCreatePRRequiresAuth verifies that CreatePR fails when GH is not authed.
func TestCreatePRRequiresAuth(t *testing.T) {
	gc := NewFakeGitClient()
	gc.GHAuthed = false

	_, _, err := gc.CreatePR("title", "body", "main")
	if err == nil {
		t.Fatal("CreatePR should fail when gh is not authed")
	}
}

// TestCommitAllIn verifies the fake CommitAllIn appends to Commits.
func TestCommitAllIn(t *testing.T) {
	gc := NewFakeGitClient()
	if err := gc.CommitAllIn("/tmp/test", "patch commit"); err != nil {
		t.Fatalf("CommitAllIn: %v", err)
	}
	if len(gc.Commits) != 1 || gc.Commits[0] != "patch commit" {
		t.Errorf("Commits = %v, want [\"patch commit\"]", gc.Commits)
	}
}

// TestShowFileAtRef verifies the fake ShowFileAtRef looks up BaseFiles.
func TestShowFileAtRef(t *testing.T) {
	gc := NewFakeGitClient()
	gc.BaseFiles["main:foo.go"] = "package foo\n"

	content, err := gc.ShowFileAtRef("main", "foo.go")
	if err != nil {
		t.Fatalf("ShowFileAtRef: %v", err)
	}
	if string(content) != "package foo\n" {
		t.Errorf("content = %q, want %q", string(content), "package foo\n")
	}

	// Missing file should error
	_, err = gc.ShowFileAtRef("main", "missing.go")
	if err == nil {
		t.Error("ShowFileAtRef should error for missing file")
	}
}

// TestUpdateBranchRef verifies the fake UpdateBranchRef is a no-op.
func TestUpdateBranchRef(t *testing.T) {
	gc := NewFakeGitClient()
	if err := gc.UpdateBranchRef("feature/x", "/tmp/wt"); err != nil {
		t.Fatalf("UpdateBranchRef: %v", err)
	}
}

// TestInterfaceCompliance ensures both implementations satisfy GitClient.
func TestInterfaceCompliance(t *testing.T) {
	var _ GitClient = (*ExecGitClient)(nil)
	var _ GitClient = (*FakeGitClient)(nil)
}

// containsAll returns true if s contains every one of the given substrings.
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
