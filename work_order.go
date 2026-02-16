package dash

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// WorkOrderStatus represents the lifecycle state of a work order.
type WorkOrderStatus string

const (
	WOStatusCreated          WorkOrderStatus = "created"
	WOStatusAssigned         WorkOrderStatus = "assigned"
	WOStatusMutating         WorkOrderStatus = "mutating"
	WOStatusBuildPassed      WorkOrderStatus = "build_passed"
	WOStatusBuildFailed      WorkOrderStatus = "build_failed"
	WOStatusSynthesisPending WorkOrderStatus = "synthesis_pending"
	WOStatusMergePending     WorkOrderStatus = "merge_pending"
	WOStatusMerged           WorkOrderStatus = "merged"
	WOStatusRejected         WorkOrderStatus = "rejected"
)

const maxWorkOrderAttempts = 3

// WorkOrderEvent is the most recent event snapshot kept inline in the WorkOrder JSON.
type WorkOrderEvent struct {
	Status string `json:"status"`
	Actor  string `json:"actor"`
	Detail string `json:"detail,omitempty"`
	At     string `json:"at"`
}

// WorkOrder is the parsed representation of an AUTOMATION.work_order node's data.
type WorkOrder struct {
	Node *Node `json:"-"`

	Status   WorkOrderStatus `json:"status"`
	Revision int             `json:"revision"`

	TaskID   *uuid.UUID `json:"task_id,omitempty"`
	AgentKey string     `json:"agent_key,omitempty"`

	BranchName string `json:"branch_name,omitempty"`
	BaseBranch string `json:"base_branch"`
	RepoRoot   string `json:"repo_root,omitempty"`
	CommitHash string `json:"commit_hash,omitempty"`

	PRID         int    `json:"pr_id,omitempty"`
	PRUrl        string `json:"pr_url,omitempty"`
	ChecksStatus string `json:"checks_status,omitempty"` // "pass" | "fail" | "pending"
	ChecksAt     string `json:"checks_at,omitempty"`

	FilesChanged []string `json:"files_changed,omitempty"`
	ScopePaths   []string `json:"scope_paths,omitempty"`

	Attempt     int    `json:"attempt"`
	LastError   string `json:"last_error,omitempty"`
	LastErrorAt string `json:"last_error_at,omitempty"`

	WorktreePath string `json:"worktree_path,omitempty"`

	LastEvent  *WorkOrderEvent `json:"last_event,omitempty"`
	EventCount int             `json:"event_count"`

	AllowPublicAPIChange bool   `json:"allow_public_api_change,omitempty"`
	Description          string `json:"description,omitempty"`
}

// validTransitions defines allowed status transitions.
var validTransitions = map[WorkOrderStatus][]WorkOrderStatus{
	WOStatusCreated:          {WOStatusAssigned},
	WOStatusAssigned:         {WOStatusMutating},
	WOStatusMutating:         {WOStatusBuildPassed, WOStatusBuildFailed},
	WOStatusBuildPassed:      {WOStatusSynthesisPending},
	WOStatusBuildFailed:      {WOStatusMutating, WOStatusRejected}, // retry or reject
	WOStatusSynthesisPending: {WOStatusMergePending, WOStatusRejected},
	WOStatusMergePending:     {WOStatusMerged, WOStatusRejected},
	// Terminal states: merged, rejected — no transitions out
}

// parseWorkOrder extracts WorkOrder from an AUTOMATION.work_order node.
func parseWorkOrder(node *Node) (*WorkOrder, error) {
	if node == nil {
		return nil, fmt.Errorf("nil node")
	}
	if node.Layer != LayerAutomation || node.Type != "work_order" {
		return nil, fmt.Errorf("node %s is not an AUTOMATION.work_order", node.ID)
	}

	var wo WorkOrder
	if err := json.Unmarshal(node.Data, &wo); err != nil {
		return nil, fmt.Errorf("invalid work_order data: %w", err)
	}
	wo.Node = node

	if wo.Status == "" {
		wo.Status = WOStatusCreated
	}
	if wo.BaseBranch == "" {
		wo.BaseBranch = "develop"
	}

	return &wo, nil
}

// saveWorkOrder persists the WorkOrder state back to the node.
func (d *Dash) saveWorkOrder(ctx context.Context, wo *WorkOrder) error {
	wo.Revision++

	dataJSON, err := json.Marshal(wo)
	if err != nil {
		return fmt.Errorf("marshal work_order: %w", err)
	}
	wo.Node.Data = dataJSON

	return d.UpdateNode(ctx, wo.Node)
}

// CreateWorkOrder creates a new AUTOMATION.work_order node.
func (d *Dash) CreateWorkOrder(ctx context.Context, name string, taskID *uuid.UUID, agentKey string, scopePaths []string, opts WorkOrderOpts) (*WorkOrder, error) {
	if name == "" {
		return nil, fmt.Errorf("work order name is required")
	}
	if len(scopePaths) == 0 {
		return nil, fmt.Errorf("scope_paths is required (at least one path)")
	}

	baseBranch := opts.BaseBranch
	if baseBranch == "" {
		baseBranch = "develop"
	}

	wo := &WorkOrder{
		Status:               WOStatusCreated,
		Revision:             0,
		TaskID:               taskID,
		AgentKey:             agentKey,
		BaseBranch:           baseBranch,
		RepoRoot:             opts.RepoRoot,
		ScopePaths:           scopePaths,
		AllowPublicAPIChange: opts.AllowPublicAPIChange,
		Description:          opts.Description,
	}

	dataJSON, err := json.Marshal(wo)
	if err != nil {
		return nil, fmt.Errorf("marshal work_order: %w", err)
	}

	node := &Node{
		Layer: LayerAutomation,
		Type:  "work_order",
		Name:  name,
		Data:  dataJSON,
	}

	if err := d.CreateNode(ctx, node); err != nil {
		return nil, fmt.Errorf("create work_order node: %w", err)
	}
	wo.Node = node

	// Link to task if provided
	if taskID != nil {
		d.CreateEdge(ctx, &Edge{
			SourceID: node.ID,
			TargetID: *taskID,
			Relation: RelationImplements,
		})
	}

	// Log creation event
	d.appendWorkOrderEvent(ctx, wo, WOStatusCreated, "system", "work order created")

	return wo, nil
}

// WorkOrderOpts holds optional parameters for CreateWorkOrder.
type WorkOrderOpts struct {
	BaseBranch           string
	RepoRoot             string
	Description          string
	AllowPublicAPIChange bool
}

// GetWorkOrder retrieves and parses a work order by ID.
func (d *Dash) GetWorkOrder(ctx context.Context, id uuid.UUID) (*WorkOrder, error) {
	node, err := d.GetNodeActive(ctx, id)
	if err != nil {
		return nil, err
	}
	return parseWorkOrder(node)
}

// GetWorkOrderByName retrieves a work order by name.
func (d *Dash) GetWorkOrderByName(ctx context.Context, name string) (*WorkOrder, error) {
	node, err := d.GetNodeByName(ctx, LayerAutomation, "work_order", name)
	if err != nil {
		return nil, err
	}
	return parseWorkOrder(node)
}

const queryListActiveWorkOrders = `
	SELECT id, layer, type, name, data, created_at, updated_at, deleted_at
	FROM nodes
	WHERE layer = 'AUTOMATION' AND type = 'work_order'
	  AND deleted_at IS NULL
	  AND COALESCE(data->>'status', 'created') NOT IN ('merged', 'rejected')
	ORDER BY updated_at DESC`

// ListActiveWorkOrders returns all work orders that are not in a terminal state.
func (d *Dash) ListActiveWorkOrders(ctx context.Context) ([]*WorkOrder, error) {
	rows, err := d.db.QueryContext(ctx, queryListActiveWorkOrders)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []*WorkOrder
	for rows.Next() {
		node, err := scanNode(rows)
		if err != nil {
			continue
		}
		wo, err := parseWorkOrder(node)
		if err != nil {
			continue
		}
		orders = append(orders, wo)
	}
	return orders, rows.Err()
}

// AdvanceWorkOrder transitions a work order to the next status.
// It is idempotent: if the work order is already at the target status, it returns ok.
func (d *Dash) AdvanceWorkOrder(ctx context.Context, id uuid.UUID, targetStatus WorkOrderStatus, actor, detail string) (*WorkOrder, error) {
	wo, err := d.GetWorkOrder(ctx, id)
	if err != nil {
		return nil, err
	}

	// Idempotent: already at target
	if wo.Status == targetStatus {
		return wo, nil
	}

	// Validate transition
	allowed, ok := validTransitions[wo.Status]
	if !ok {
		return wo, fmt.Errorf("work order %s is in terminal state %s", id, wo.Status)
	}

	valid := false
	for _, s := range allowed {
		if s == targetStatus {
			valid = true
			break
		}
	}
	if !valid {
		return wo, fmt.Errorf("invalid transition: %s → %s (allowed: %v)", wo.Status, targetStatus, allowed)
	}

	// Handle build_failed retry logic
	if targetStatus == WOStatusBuildFailed {
		wo.Attempt++
		if wo.Attempt >= maxWorkOrderAttempts {
			// Max retries exceeded → reject
			targetStatus = WOStatusRejected
			detail = fmt.Sprintf("max attempts (%d) exceeded: %s", maxWorkOrderAttempts, detail)
		}
	}

	// Handle error tracking
	if targetStatus == WOStatusBuildFailed || targetStatus == WOStatusRejected {
		wo.LastError = detail
		wo.LastErrorAt = time.Now().UTC().Format(time.RFC3339)
	}

	wo.Status = targetStatus

	// Log event
	d.appendWorkOrderEvent(ctx, wo, targetStatus, actor, detail)

	// Save
	if err := d.saveWorkOrder(ctx, wo); err != nil {
		return wo, fmt.Errorf("save work_order: %w", err)
	}

	return wo, nil
}

// AssignWorkOrder assigns a work order to an agent and sets the branch name.
func (d *Dash) AssignWorkOrder(ctx context.Context, id uuid.UUID, agentKey, branchName string) (*WorkOrder, error) {
	wo, err := d.GetWorkOrder(ctx, id)
	if err != nil {
		return nil, err
	}

	// Idempotent: already assigned to same agent
	if wo.Status == WOStatusAssigned && wo.AgentKey == agentKey {
		return wo, nil
	}

	if wo.Status != WOStatusCreated {
		return wo, fmt.Errorf("can only assign from 'created' state, currently '%s'", wo.Status)
	}

	wo.AgentKey = agentKey
	if branchName == "" {
		branchName = fmt.Sprintf("agent/%s/%s", agentKey, wo.Node.ID)
	}
	wo.BranchName = branchName

	wo.Status = WOStatusAssigned

	// Create assigned_to edge if we can find the agent node
	agentNode, err := d.GetNodeByName(ctx, LayerAutomation, "agent", agentKey)
	if err == nil {
		d.CreateEdge(ctx, &Edge{
			SourceID: wo.Node.ID,
			TargetID: agentNode.ID,
			Relation: RelationAssignedTo,
		})
	}

	d.appendWorkOrderEvent(ctx, wo, WOStatusAssigned, agentKey, fmt.Sprintf("assigned, branch=%s", branchName))

	if err := d.saveWorkOrder(ctx, wo); err != nil {
		return wo, err
	}

	return wo, nil
}

// UpdateWorkOrderFiles updates the list of changed files and optionally the commit hash.
func (d *Dash) UpdateWorkOrderFiles(ctx context.Context, id uuid.UUID, files []string, commitHash string) error {
	wo, err := d.GetWorkOrder(ctx, id)
	if err != nil {
		return err
	}
	wo.FilesChanged = files
	if commitHash != "" {
		wo.CommitHash = commitHash
	}
	return d.saveWorkOrder(ctx, wo)
}

// UpdateWorkOrderPR stores PR information on the work order.
func (d *Dash) UpdateWorkOrderPR(ctx context.Context, id uuid.UUID, prID int, prURL string) error {
	wo, err := d.GetWorkOrder(ctx, id)
	if err != nil {
		return err
	}
	wo.PRID = prID
	wo.PRUrl = prURL
	return d.saveWorkOrder(ctx, wo)
}

// UpdateWorkOrderChecks stores CI check status.
func (d *Dash) UpdateWorkOrderChecks(ctx context.Context, id uuid.UUID, checksStatus string) error {
	wo, err := d.GetWorkOrder(ctx, id)
	if err != nil {
		return err
	}
	wo.ChecksStatus = checksStatus
	wo.ChecksAt = time.Now().UTC().Format(time.RFC3339)
	return d.saveWorkOrder(ctx, wo)
}

// appendWorkOrderEvent logs a status change as an observation and updates inline event state.
func (d *Dash) appendWorkOrderEvent(ctx context.Context, wo *WorkOrder, status WorkOrderStatus, actor, detail string) {
	now := time.Now().UTC()

	evt := &WorkOrderEvent{
		Status: string(status),
		Actor:  actor,
		Detail: detail,
		At:     now.Format(time.RFC3339),
	}

	wo.LastEvent = evt
	wo.EventCount++

	// Write to observations table
	obsData, _ := json.Marshal(map[string]any{
		"status":     string(status),
		"actor":      actor,
		"detail":     detail,
		"revision":   wo.Revision,
		"attempt":    wo.Attempt,
		"event_num":  wo.EventCount,
		"branch":     wo.BranchName,
		"agent_key":  wo.AgentKey,
	})

	d.CreateObservation(ctx, &Observation{
		NodeID:     wo.Node.ID,
		Type:       "work_order_event",
		Data:       obsData,
		ObservedAt: now,
	})
}
