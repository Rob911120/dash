package dash

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestParseWorkOrder(t *testing.T) {
	data := `{"status":"created","revision":0,"base_branch":"develop","scope_paths":["/dash/foo.go"]}`
	node := &Node{
		ID:    uuid.New(),
		Layer: LayerAutomation,
		Type:  "work_order",
		Name:  "test-wo",
		Data:  json.RawMessage(data),
	}

	wo, err := parseWorkOrder(node)
	if err != nil {
		t.Fatalf("parseWorkOrder: %v", err)
	}
	if wo.Status != WOStatusCreated {
		t.Errorf("status = %q, want %q", wo.Status, WOStatusCreated)
	}
	if wo.BaseBranch != "develop" {
		t.Errorf("base_branch = %q, want %q", wo.BaseBranch, "develop")
	}
	if len(wo.ScopePaths) != 1 || wo.ScopePaths[0] != "/dash/foo.go" {
		t.Errorf("scope_paths = %v, want [/dash/foo.go]", wo.ScopePaths)
	}
}

func TestParseWorkOrderDefaults(t *testing.T) {
	node := &Node{
		ID:    uuid.New(),
		Layer: LayerAutomation,
		Type:  "work_order",
		Name:  "test-wo",
		Data:  json.RawMessage(`{}`),
	}

	wo, err := parseWorkOrder(node)
	if err != nil {
		t.Fatalf("parseWorkOrder: %v", err)
	}
	if wo.Status != WOStatusCreated {
		t.Errorf("status = %q, want %q", wo.Status, WOStatusCreated)
	}
	if wo.BaseBranch != "develop" {
		t.Errorf("base_branch = %q, want %q", wo.BaseBranch, "develop")
	}
}

func TestParseWorkOrderWrongType(t *testing.T) {
	node := &Node{
		ID:    uuid.New(),
		Layer: LayerContext,
		Type:  "plan",
		Name:  "not-a-wo",
		Data:  json.RawMessage(`{}`),
	}

	_, err := parseWorkOrder(node)
	if err == nil {
		t.Fatal("expected error for non-work_order node")
	}
}

func TestValidTransitions(t *testing.T) {
	tests := []struct {
		from    WorkOrderStatus
		to      WorkOrderStatus
		allowed bool
	}{
		{WOStatusCreated, WOStatusAssigned, true},
		{WOStatusCreated, WOStatusMutating, false},
		{WOStatusAssigned, WOStatusMutating, true},
		{WOStatusMutating, WOStatusBuildPassed, true},
		{WOStatusMutating, WOStatusBuildFailed, true},
		{WOStatusBuildPassed, WOStatusSynthesisPending, true},
		{WOStatusBuildFailed, WOStatusMutating, true},
		{WOStatusBuildFailed, WOStatusRejected, true},
		{WOStatusSynthesisPending, WOStatusMergePending, true},
		{WOStatusSynthesisPending, WOStatusRejected, true},
		{WOStatusMergePending, WOStatusMerged, true},
		{WOStatusMergePending, WOStatusRejected, true},
		// Invalid
		{WOStatusMerged, WOStatusCreated, false},
		{WOStatusRejected, WOStatusCreated, false},
		{WOStatusCreated, WOStatusMerged, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.from)+"→"+string(tt.to), func(t *testing.T) {
			allowed := false
			transitions, ok := validTransitions[tt.from]
			if ok {
				for _, s := range transitions {
					if s == tt.to {
						allowed = true
						break
					}
				}
			}
			if allowed != tt.allowed {
				t.Errorf("transition %s→%s: got allowed=%v, want %v", tt.from, tt.to, allowed, tt.allowed)
			}
		})
	}
}

func TestWorkOrderSerialization(t *testing.T) {
	taskID := uuid.New()
	wo := &WorkOrder{
		Status:     WOStatusAssigned,
		Revision:   3,
		TaskID:     &taskID,
		AgentKey:   "cockpit-backend",
		BranchName: "agent/cockpit-backend/abc123",
		BaseBranch: "develop",
		ScopePaths: []string{"/dash/work_order.go", "/dash/types.go"},
		Attempt:    1,
		EventCount: 5,
		LastEvent: &WorkOrderEvent{
			Status: "assigned",
			Actor:  "cockpit-backend",
			Detail: "assigned, branch=agent/cockpit-backend/abc123",
			At:     "2026-01-01T00:00:00Z",
		},
	}

	data, err := json.Marshal(wo)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Round-trip through node
	node := &Node{
		ID:    uuid.New(),
		Layer: LayerAutomation,
		Type:  "work_order",
		Name:  "test-roundtrip",
		Data:  data,
	}

	wo2, err := parseWorkOrder(node)
	if err != nil {
		t.Fatalf("parseWorkOrder: %v", err)
	}

	if wo2.Status != WOStatusAssigned {
		t.Errorf("status = %q, want %q", wo2.Status, WOStatusAssigned)
	}
	if wo2.Revision != 3 {
		t.Errorf("revision = %d, want 3", wo2.Revision)
	}
	if wo2.AgentKey != "cockpit-backend" {
		t.Errorf("agent_key = %q, want %q", wo2.AgentKey, "cockpit-backend")
	}
	if wo2.EventCount != 5 {
		t.Errorf("event_count = %d, want 5", wo2.EventCount)
	}
	if wo2.LastEvent == nil || wo2.LastEvent.Actor != "cockpit-backend" {
		t.Error("last_event not preserved through serialization")
	}
}
