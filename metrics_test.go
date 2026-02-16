package dash

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

// --- EvolutionMetrics serialization round-trip ---

func TestEvolutionMetricsRoundTrip(t *testing.T) {
	m := EvolutionMetrics{
		Period: TimeRange{
			Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 1, 31, 23, 59, 59, 0, time.UTC),
		},
		WOCreated:         10,
		WOMerged:          7,
		WORejected:        2,
		BuildSuccessRate:  0.85,
		SynthesisAvgScore: 0.92,
		MeanTimeToMerge:   30 * time.Minute,
		Steps: StepDurations{
			Mutating:     5 * time.Minute,
			BuildGate:    3 * time.Minute,
			Synthesis:    10 * time.Minute,
			MergePending: 2 * time.Minute,
		},
		Agents: map[string]AgentMetrics{
			"agent-alpha": {
				WOCount:       5,
				MergedCount:   4,
				RejectedCount: 1,
				AvgScore:      0.9,
			},
			"agent-beta": {
				WOCount:       5,
				MergedCount:   3,
				RejectedCount: 1,
				AvgScore:      0.88,
			},
		},
	}

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var m2 EvolutionMetrics
	if err := json.Unmarshal(data, &m2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if m2.WOCreated != 10 {
		t.Errorf("WOCreated = %d, want 10", m2.WOCreated)
	}
	if m2.WOMerged != 7 {
		t.Errorf("WOMerged = %d, want 7", m2.WOMerged)
	}
	if m2.WORejected != 2 {
		t.Errorf("WORejected = %d, want 2", m2.WORejected)
	}
	if m2.BuildSuccessRate != 0.85 {
		t.Errorf("BuildSuccessRate = %f, want 0.85", m2.BuildSuccessRate)
	}
	if m2.MeanTimeToMerge != 30*time.Minute {
		t.Errorf("MeanTimeToMerge = %v, want 30m", m2.MeanTimeToMerge)
	}
	if m2.Steps.Mutating != 5*time.Minute {
		t.Errorf("Steps.Mutating = %v, want 5m", m2.Steps.Mutating)
	}
	if m2.Steps.BuildGate != 3*time.Minute {
		t.Errorf("Steps.BuildGate = %v, want 3m", m2.Steps.BuildGate)
	}
	if len(m2.Agents) != 2 {
		t.Fatalf("Agents count = %d, want 2", len(m2.Agents))
	}
	alpha := m2.Agents["agent-alpha"]
	if alpha.WOCount != 5 || alpha.MergedCount != 4 {
		t.Errorf("agent-alpha: WOCount=%d MergedCount=%d, want 5,4", alpha.WOCount, alpha.MergedCount)
	}
}

// --- DivergenceCheck with passing build result ---

func TestDivergenceCheckAllPassing(t *testing.T) {
	buildResult := &BuildGateResult{
		Test:  BuildResult{Passed: true},
		AST:   ASTValidationResult{Passed: true},
		Scope: ScopeCheckResult{Passed: true},
	}

	wo := &WorkOrder{
		Status:       WOStatusMerged,
		ChecksStatus: "pass",
		FilesChanged: []string{"foo.go", "bar.go"},
	}

	checks := []DivergenceCheck{
		checkTestsPass(buildResult),
		checkFilesCreated(wo),
		checkMerged(wo),
		checkNoViolations(buildResult),
	}

	for _, c := range checks {
		if !c.Match {
			t.Errorf("check %q: expected match=true, got false (detail: %s)", c.Claim, c.Detail)
		}
	}

	// Verify DivergenceResult would be Passed.
	allMatch := true
	for _, c := range checks {
		if !c.Match {
			allMatch = false
			break
		}
	}
	if !allMatch {
		t.Error("expected all checks to pass")
	}
}

// --- DivergenceCheck with failing test -> divergence ---

func TestDivergenceCheckFailingTest(t *testing.T) {
	buildResult := &BuildGateResult{
		Test:  BuildResult{Passed: false, Output: "FAIL: TestSomething"},
		AST:   ASTValidationResult{Passed: true},
		Scope: ScopeCheckResult{Passed: true},
	}

	dc := checkTestsPass(buildResult)
	if dc.Match {
		t.Error("expected match=false when tests fail")
	}
	if dc.Detail == "" {
		t.Error("expected detail to explain failure")
	}
}

func TestDivergenceCheckNilBuildResult(t *testing.T) {
	dc := checkTestsPass(nil)
	if dc.Match {
		t.Error("expected match=false with nil build result")
	}

	dcViol := checkNoViolations(nil)
	if dcViol.Match {
		t.Error("expected match=false with nil build result for violations check")
	}
	if dcViol.Detail != "no build gate result provided" {
		t.Errorf("detail = %q, want 'no build gate result provided'", dcViol.Detail)
	}
}

func TestDivergenceCheckScopeViolation(t *testing.T) {
	buildResult := &BuildGateResult{
		Test:  BuildResult{Passed: true},
		AST:   ASTValidationResult{Passed: true},
		Scope: ScopeCheckResult{Passed: false, OutOfScope: []string{"/other/hack.go"}},
	}

	dc := checkNoViolations(buildResult)
	if dc.Match {
		t.Error("expected match=false when scope check fails")
	}
}

func TestDivergenceCheckNotMerged(t *testing.T) {
	wo := &WorkOrder{
		Status:       WOStatusMergePending,
		ChecksStatus: "pending",
		FilesChanged: []string{"foo.go"},
	}

	dc := checkMerged(wo)
	if dc.Match {
		t.Error("expected match=false when status is not merged")
	}
}

func TestDivergenceCheckNoFiles(t *testing.T) {
	wo := &WorkOrder{
		Status:       WOStatusMerged,
		ChecksStatus: "pass",
	}

	dc := checkFilesCreated(wo)
	if dc.Match {
		t.Error("expected match=false when no files changed")
	}
}

func TestDivergenceResultSerialization(t *testing.T) {
	woID := uuid.New()
	result := DivergenceResult{
		WorkOrderID: woID,
		Passed:      false,
		Checks: []DivergenceCheck{
			{Claim: "tests pass", Artifact: "BuildGateResult.Test.Passed", Match: true},
			{Claim: "files created", Artifact: "WorkOrder.FilesChanged", Match: false, Detail: "no files"},
		},
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var result2 DivergenceResult
	if err := json.Unmarshal(data, &result2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if result2.WorkOrderID != woID {
		t.Errorf("WorkOrderID = %s, want %s", result2.WorkOrderID, woID)
	}
	if result2.Passed {
		t.Error("expected Passed=false")
	}
	if len(result2.Checks) != 2 {
		t.Fatalf("Checks count = %d, want 2", len(result2.Checks))
	}
	if result2.Checks[1].Detail != "no files" {
		t.Errorf("Checks[1].Detail = %q, want 'no files'", result2.Checks[1].Detail)
	}
}

// --- Step duration calculation ---

func TestComputeStepDurations(t *testing.T) {
	base := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	woID := uuid.New()

	grouped := map[uuid.UUID][]timestampedEvent{
		woID: {
			{Event: woEventData{Status: "created"}, At: base},
			{Event: woEventData{Status: "assigned"}, At: base.Add(1 * time.Minute)},
			{Event: woEventData{Status: "mutating"}, At: base.Add(2 * time.Minute)},
			{Event: woEventData{Status: "build_passed"}, At: base.Add(12 * time.Minute)},
			{Event: woEventData{Status: "synthesis_pending"}, At: base.Add(13 * time.Minute)},
			{Event: woEventData{Status: "merge_pending"}, At: base.Add(22 * time.Minute)},
			{Event: woEventData{Status: "merged"}, At: base.Add(25 * time.Minute)},
		},
	}

	sd := computeStepDurations(grouped)

	// mutating: assigned(+1m) -> build_passed(+12m) = 11m
	if sd.Mutating != 11*time.Minute {
		t.Errorf("Mutating = %v, want 11m", sd.Mutating)
	}

	// build_gate: mutating(+2m) -> build_passed(+12m) = 10m
	if sd.BuildGate != 10*time.Minute {
		t.Errorf("BuildGate = %v, want 10m", sd.BuildGate)
	}

	// synthesis: build_passed(+12m) -> merge_pending(+22m) = 10m
	if sd.Synthesis != 10*time.Minute {
		t.Errorf("Synthesis = %v, want 10m", sd.Synthesis)
	}

	// merge_pending: merge_pending(+22m) -> merged(+25m) = 3m
	if sd.MergePending != 3*time.Minute {
		t.Errorf("MergePending = %v, want 3m", sd.MergePending)
	}
}

func TestComputeStepDurationsMultipleWOs(t *testing.T) {
	base := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	wo1 := uuid.New()
	wo2 := uuid.New()

	grouped := map[uuid.UUID][]timestampedEvent{
		wo1: {
			{Event: woEventData{Status: "mutating"}, At: base},
			{Event: woEventData{Status: "build_passed"}, At: base.Add(10 * time.Minute)},
		},
		wo2: {
			{Event: woEventData{Status: "mutating"}, At: base},
			{Event: woEventData{Status: "build_passed"}, At: base.Add(20 * time.Minute)},
		},
	}

	sd := computeStepDurations(grouped)

	// Average build_gate: (10m + 20m) / 2 = 15m
	if sd.BuildGate != 15*time.Minute {
		t.Errorf("BuildGate = %v, want 15m (average of 10m and 20m)", sd.BuildGate)
	}
}

func TestComputeStepDurationsEmpty(t *testing.T) {
	grouped := map[uuid.UUID][]timestampedEvent{}
	sd := computeStepDurations(grouped)

	if sd.Mutating != 0 {
		t.Errorf("Mutating = %v, want 0", sd.Mutating)
	}
	if sd.BuildGate != 0 {
		t.Errorf("BuildGate = %v, want 0", sd.BuildGate)
	}
	if sd.Synthesis != 0 {
		t.Errorf("Synthesis = %v, want 0", sd.Synthesis)
	}
	if sd.MergePending != 0 {
		t.Errorf("MergePending = %v, want 0", sd.MergePending)
	}
}

// --- Event parsing helpers ---

func TestParseWOEventData(t *testing.T) {
	raw := json.RawMessage(`{"status":"assigned","actor":"agent-alpha","detail":"assigned, branch=foo","revision":1,"attempt":0,"event_num":2,"branch":"agent/alpha/abc","agent_key":"agent-alpha"}`)

	evt, err := parseWOEventData(raw)
	if err != nil {
		t.Fatalf("parseWOEventData: %v", err)
	}
	if evt.Status != "assigned" {
		t.Errorf("Status = %q, want 'assigned'", evt.Status)
	}
	if evt.AgentKey != "agent-alpha" {
		t.Errorf("AgentKey = %q, want 'agent-alpha'", evt.AgentKey)
	}
	if evt.EventNum != 2 {
		t.Errorf("EventNum = %d, want 2", evt.EventNum)
	}
}

func TestParseWOEventDataInvalid(t *testing.T) {
	raw := json.RawMessage(`{invalid json`)
	_, err := parseWOEventData(raw)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestGroupEventsByNode(t *testing.T) {
	id1 := uuid.New()
	id2 := uuid.New()
	base := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

	observations := []*Observation{
		{NodeID: id1, Data: json.RawMessage(`{"status":"created","agent_key":"a1"}`), ObservedAt: base.Add(2 * time.Minute)},
		{NodeID: id1, Data: json.RawMessage(`{"status":"assigned","agent_key":"a1"}`), ObservedAt: base},
		{NodeID: id2, Data: json.RawMessage(`{"status":"created","agent_key":"a2"}`), ObservedAt: base.Add(1 * time.Minute)},
	}

	grouped, err := groupEventsByNode(observations)
	if err != nil {
		t.Fatalf("groupEventsByNode: %v", err)
	}

	if len(grouped) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(grouped))
	}

	events1 := grouped[id1]
	if len(events1) != 2 {
		t.Fatalf("id1 events = %d, want 2", len(events1))
	}
	// Should be sorted chronologically: assigned (base) before created (base+2m).
	if events1[0].Event.Status != "assigned" {
		t.Errorf("id1 first event status = %q, want 'assigned'", events1[0].Event.Status)
	}
	if events1[1].Event.Status != "created" {
		t.Errorf("id1 second event status = %q, want 'created'", events1[1].Event.Status)
	}
}

func TestGroupEventsByNodeSkipsInvalid(t *testing.T) {
	id := uuid.New()
	observations := []*Observation{
		{NodeID: id, Data: json.RawMessage(`not json`), ObservedAt: time.Now()},
		{NodeID: id, Data: json.RawMessage(`{"status":"created"}`), ObservedAt: time.Now()},
	}

	grouped, err := groupEventsByNode(observations)
	if err != nil {
		t.Fatalf("groupEventsByNode: %v", err)
	}

	events := grouped[id]
	if len(events) != 1 {
		t.Fatalf("expected 1 valid event, got %d", len(events))
	}
	if events[0].Event.Status != "created" {
		t.Errorf("status = %q, want 'created'", events[0].Event.Status)
	}
}

// --- AgentMetrics serialization ---

func TestAgentMetricsSerialization(t *testing.T) {
	am := AgentMetrics{
		WOCount:       10,
		MergedCount:   8,
		RejectedCount: 2,
		AvgScore:      0.95,
	}

	data, err := json.Marshal(am)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var am2 AgentMetrics
	if err := json.Unmarshal(data, &am2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if am2.WOCount != 10 {
		t.Errorf("WOCount = %d, want 10", am2.WOCount)
	}
	if am2.AvgScore != 0.95 {
		t.Errorf("AvgScore = %f, want 0.95", am2.AvgScore)
	}
}

func TestAgentMetricsOmitEmptyScore(t *testing.T) {
	am := AgentMetrics{
		WOCount: 5,
	}

	data, err := json.Marshal(am)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// avg_score should be omitted when zero due to omitempty.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	if _, exists := raw["avg_score"]; exists {
		t.Error("expected avg_score to be omitted when zero")
	}
}
