package dash

import (
	"testing"

	"github.com/google/uuid"
)

func TestBuildGateNoBranch(t *testing.T) {
	gc := NewFakeGitClient()
	wo := &WorkOrder{
		Node: &Node{ID: uuid.New()},
		// No BranchName
	}
	_, err := RunBuildGate(gc, wo, "")
	if err == nil {
		t.Fatal("expected error for missing branch name")
	}
}

func TestBuildGateScopeBlock(t *testing.T) {
	gc := NewFakeGitClient()
	gc.Branches["agent/test/1"] = true
	gc.Files["outside/foo.go"] = "package outside"

	wo := &WorkOrder{
		Node:       &Node{ID: uuid.New()},
		BranchName: "agent/test/1",
		BaseBranch: "main",
		ScopePaths: []string{"/dash/"},
	}

	result, err := RunBuildGate(gc, wo, "")
	if err != nil {
		t.Fatalf("RunBuildGate: %v", err)
	}
	if result.Scope.Passed {
		t.Error("scope should have failed for out-of-scope file")
	}
	if result.Passed {
		t.Error("overall gate should have failed")
	}
}

func TestBuildGateWorktreeCleanup(t *testing.T) {
	gc := NewFakeGitClient()
	gc.Branches["agent/test/2"] = true

	wo := &WorkOrder{
		Node:       &Node{ID: uuid.New()},
		BranchName: "agent/test/2",
		BaseBranch: "main",
		ScopePaths: []string{"/dash/"},
	}

	_, _ = RunBuildGate(gc, wo, "")

	// Verify worktree was cleaned up
	if len(gc.Worktrees) != 0 {
		t.Errorf("worktree should be cleaned up, got %d remaining", len(gc.Worktrees))
	}
}

func TestBuildGateGoEnvCapture(t *testing.T) {
	env := captureGoEnv(".")
	if env["GOOS"] == "" {
		t.Error("expected GOOS to be captured")
	}
	if env["GOARCH"] == "" {
		t.Error("expected GOARCH to be captured")
	}
}

func TestBuildGateWithExternalWorktree(t *testing.T) {
	gc := NewFakeGitClient()
	gc.Branches["agent/test/ext"] = true

	wo := &WorkOrder{
		Node:       &Node{ID: uuid.New()},
		BranchName: "agent/test/ext",
		BaseBranch: "main",
		ScopePaths: []string{"/dash/"},
	}

	// Pre-create a worktree (simulating caller-managed lifecycle)
	wtPath := "/tmp/dash-wo/ext-test"
	if err := gc.AddWorktree(wtPath, wo.BranchName); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	_, _ = RunBuildGate(gc, wo, wtPath)

	// Worktree should NOT be cleaned up — caller owns it
	if _, exists := gc.Worktrees[wtPath]; !exists {
		t.Error("caller-managed worktree should NOT be removed by RunBuildGate")
	}
}
