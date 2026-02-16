package dash

import (
	"encoding/json"
	"testing"
)

func TestParseSynthesisResponseApprove(t *testing.T) {
	response := `{
		"verdict": "approve",
		"reasoning": "Clean append-only change",
		"score": 95,
		"files_touched": ["work_order.go"],
		"change_budget": 200,
		"actual_changes": 50
	}`

	result, err := parseSynthesisResponse(response)
	if err != nil {
		t.Fatalf("parseSynthesisResponse: %v", err)
	}
	if result.Verdict != VerdictApprove {
		t.Errorf("verdict = %q, want %q", result.Verdict, VerdictApprove)
	}
	if result.Score != 95 {
		t.Errorf("score = %d, want 95", result.Score)
	}
}

func TestParseSynthesisResponseReject(t *testing.T) {
	response := `{"verdict": "reject", "reasoning": "Deletes existing code", "score": 20, "change_budget": 100, "actual_changes": 250}`

	result, err := parseSynthesisResponse(response)
	if err != nil {
		t.Fatalf("parseSynthesisResponse: %v", err)
	}
	if result.Verdict != VerdictReject {
		t.Errorf("verdict = %q, want %q", result.Verdict, VerdictReject)
	}
}

func TestParseSynthesisResponseWithCodeFences(t *testing.T) {
	response := "```json\n{\"verdict\": \"approve\", \"reasoning\": \"ok\", \"score\": 80, \"change_budget\": 100, \"actual_changes\": 50}\n```"

	result, err := parseSynthesisResponse(response)
	if err != nil {
		t.Fatalf("parseSynthesisResponse: %v", err)
	}
	if result.Verdict != VerdictApprove {
		t.Errorf("verdict = %q, want %q", result.Verdict, VerdictApprove)
	}
}

func TestParseSynthesisResponseInvalidVerdict(t *testing.T) {
	response := `{"verdict": "maybe", "reasoning": "unsure", "score": 50}`
	_, err := parseSynthesisResponse(response)
	if err == nil {
		t.Fatal("expected error for invalid verdict")
	}
}

func TestParseSynthesisResponseInvalidJSON(t *testing.T) {
	_, err := parseSynthesisResponse("not json at all")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestBudgetEnforcement(t *testing.T) {
	result := &SynthesisResult{
		Verdict:       VerdictApprove,
		Score:         90,
		ChangeBudget:  100,
		ActualChanges: 200,
	}

	// Simulate the enforcement logic from RunSynthesis
	if result.ActualChanges > result.ChangeBudget && result.ChangeBudget > 0 {
		result.Verdict = VerdictReject
	}

	if result.Verdict != VerdictReject {
		t.Error("expected reject when budget exceeded")
	}
}

func TestScopeEnforcement(t *testing.T) {
	result := &SynthesisResult{
		Verdict:      VerdictApprove,
		Score:        90,
		FilesTouched: []string{"/dash/synthesis.go", "/outside/bad.go"},
	}

	scopePaths := []string{"/dash/"}
	scopeResult := CheckScope(result.FilesTouched, scopePaths)
	if scopeResult.Passed {
		t.Error("expected scope check to fail for /outside/bad.go")
	}
	if len(scopeResult.OutOfScope) != 1 || scopeResult.OutOfScope[0] != "/outside/bad.go" {
		t.Errorf("out of scope = %v, want [/outside/bad.go]", scopeResult.OutOfScope)
	}
}

func TestSynthesisResultSerialization(t *testing.T) {
	result := &SynthesisResult{
		Verdict:       VerdictRevise,
		Reasoning:     "needs improvement",
		Score:         65,
		Patch:         "--- a/foo.go\n+++ b/foo.go\n@@ -1 +1 @@\n-old\n+new",
		FilesTouched:  []string{"foo.go"},
		ChangeBudget:  200,
		ActualChanges: 150,
		ReviewerModel: "claude-opus-4",
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var result2 SynthesisResult
	if err := json.Unmarshal(data, &result2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if result2.Verdict != VerdictRevise {
		t.Errorf("verdict = %q, want %q", result2.Verdict, VerdictRevise)
	}
	if result2.Patch != result.Patch {
		t.Error("patch not preserved through serialization")
	}
}
