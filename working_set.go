package dash

import (
	"context"
	"time"
)

// WorkingSet represents the bounded set of canonical nodes needed for reasoning.
// Max ~25 nodes total across all fields.
type WorkingSet struct {
	Mission             *Node   `json:"mission,omitempty"`              // CONTEXT.mission (max 1)
	ContextFrame        *Node   `json:"context_frame,omitempty"`        // CONTEXT.context_frame "current" (max 1)
	LatestSummary       *Node   `json:"latest_summary,omitempty"`       // CONTEXT.summary latest (max 1)
	ActiveTasks         []*Node `json:"active_tasks,omitempty"`         // CONTEXT.task/intent active/pending/in_progress (max 10)
	Constraints         []*Node `json:"constraints,omitempty"`          // CONTEXT.constraint (max 5)
	RecentInsights      []*Node `json:"recent_insights,omitempty"`      // CONTEXT.insight (all active)
	RecentDecisions     []*Node `json:"recent_decisions,omitempty"`     // CONTEXT.decision (all active)
	PromotionCandidates []*Node `json:"promotion_candidates,omitempty"` // Sessions marked promotion_candidate (max 3)
}

const (
	queryGetMission = `
		SELECT id, layer, type, name, data, created_at, updated_at, deleted_at
		FROM nodes
		WHERE layer = 'CONTEXT' AND type = 'mission'
		  AND COALESCE(data->>'status', 'active') = 'active'
		  AND deleted_at IS NULL
		ORDER BY updated_at DESC
		LIMIT 1`

	queryGetContextFrame = `
		SELECT id, layer, type, name, data, created_at, updated_at, deleted_at
		FROM nodes
		WHERE layer = 'CONTEXT' AND type = 'context_frame' AND name = 'current'
		  AND deleted_at IS NULL
		LIMIT 1`

	queryGetLatestSummary = `
		SELECT id, layer, type, name, data, created_at, updated_at, deleted_at
		FROM nodes
		WHERE layer = 'CONTEXT' AND type = 'summary'
		  AND deleted_at IS NULL
		ORDER BY created_at DESC
		LIMIT 1`

	queryGetActiveTasks = `
		SELECT id, layer, type, name, data, created_at, updated_at, deleted_at
		FROM nodes
		WHERE layer = 'CONTEXT'
		  AND type IN ('intent', 'plan', 'task')
		  AND COALESCE(data->>'status', 'active') IN ('active', 'in_progress', 'pending')
		  AND deleted_at IS NULL
		ORDER BY updated_at DESC
		LIMIT 10`

	queryGetConstraints = `
		SELECT id, layer, type, name, data, created_at, updated_at, deleted_at
		FROM nodes
		WHERE layer = 'CONTEXT' AND type = 'constraint'
		  AND deleted_at IS NULL
		ORDER BY created_at ASC
		LIMIT 5`

	queryGetRecentInsights = `
		SELECT id, layer, type, name, data, created_at, updated_at, deleted_at
		FROM nodes
		WHERE layer = 'CONTEXT' AND type = 'insight'
		  AND deleted_at IS NULL
		ORDER BY created_at DESC`

	queryGetRecentDecisions = `
		SELECT id, layer, type, name, data, created_at, updated_at, deleted_at
		FROM nodes
		WHERE layer = 'CONTEXT' AND type = 'decision'
		  AND deleted_at IS NULL
		ORDER BY created_at DESC`

	queryGetPromotionCandidates = `
		SELECT id, layer, type, name, data, created_at, updated_at, deleted_at
		FROM nodes
		WHERE layer = 'CONTEXT' AND type = 'session'
		  AND (data->>'promotion_candidate')::boolean = true
		  AND COALESCE(data->>'status', '') = 'ended'
		  AND deleted_at IS NULL
		ORDER BY updated_at DESC
		LIMIT 3`

	queryGetActiveAgents = `
		SELECT id, layer, type, name, data, created_at, updated_at, deleted_at
		FROM nodes
		WHERE layer = 'AUTOMATION' AND type = 'agent'
		  AND COALESCE(data->>'status', 'active') != 'idle'
		  AND deleted_at IS NULL
		ORDER BY updated_at DESC
		LIMIT 25`
)

// AssembleWorkingSet queries the graph and returns the bounded working set.
func (d *Dash) AssembleWorkingSet(ctx context.Context) (*WorkingSet, error) {
	ws := &WorkingSet{}

	// Each query gets its own 2s timeout
	timeout := 2 * time.Second

	// Mission (max 1)
	if node, err := d.querySingleNode(ctx, queryGetMission, timeout); err == nil {
		ws.Mission = node
	}

	// Context frame (max 1)
	if node, err := d.querySingleNode(ctx, queryGetContextFrame, timeout); err == nil {
		ws.ContextFrame = node
	}

	// Latest summary (max 1)
	if node, err := d.querySingleNode(ctx, queryGetLatestSummary, timeout); err == nil {
		ws.LatestSummary = node
	}

	// Active tasks (max 10)
	if nodes, err := d.queryMultipleNodes(ctx, queryGetActiveTasks, timeout); err == nil {
		ws.ActiveTasks = nodes
	}

	// Constraints (max 5)
	if nodes, err := d.queryMultipleNodes(ctx, queryGetConstraints, timeout); err == nil {
		ws.Constraints = nodes
	}

	// Recent insights (max 5)
	if nodes, err := d.queryMultipleNodes(ctx, queryGetRecentInsights, timeout); err == nil {
		ws.RecentInsights = nodes
	}

	// Recent decisions (max 3)
	if nodes, err := d.queryMultipleNodes(ctx, queryGetRecentDecisions, timeout); err == nil {
		ws.RecentDecisions = nodes
	}

	// Promotion candidates (max 3)
	if nodes, err := d.queryMultipleNodes(ctx, queryGetPromotionCandidates, timeout); err == nil {
		ws.PromotionCandidates = nodes
	}

	return ws, nil
}

// QueryMission returns the active mission node.
func (d *Dash) QueryMission(ctx context.Context) (*Node, error) {
	return d.querySingleNode(ctx, queryGetMission, 2*time.Second)
}

// QueryContextFrame returns the current context frame.
func (d *Dash) QueryContextFrame(ctx context.Context) (*Node, error) {
	return d.querySingleNode(ctx, queryGetContextFrame, 2*time.Second)
}

// QueryRecentDecisions returns recent decision nodes.
func (d *Dash) QueryRecentDecisions(ctx context.Context) ([]*Node, error) {
	return d.queryMultipleNodes(ctx, queryGetRecentDecisions, 2*time.Second)
}

// QueryActiveAgents returns active agent nodes from the AUTOMATION layer.
func (d *Dash) QueryActiveAgents(ctx context.Context) ([]*Node, error) {
	return d.queryMultipleNodes(ctx, queryGetActiveAgents, 2*time.Second)
}

// QueryConstraints returns active constraint nodes.
func (d *Dash) QueryConstraints(ctx context.Context) ([]*Node, error) {
	return d.queryMultipleNodes(ctx, queryGetConstraints, 2*time.Second)
}

// QueryActiveTasks returns active task/intent/plan nodes.
func (d *Dash) QueryActiveTasks(ctx context.Context) ([]*Node, error) {
	return d.queryMultipleNodes(ctx, queryGetActiveTasks, 2*time.Second)
}

func (d *Dash) querySingleNode(ctx context.Context, query string, timeout time.Duration) (*Node, error) {
	qCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	row := d.db.QueryRowContext(qCtx, query)
	return scanNode(row)
}

func (d *Dash) queryMultipleNodes(ctx context.Context, query string, timeout time.Duration) ([]*Node, error) {
	qCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	rows, err := d.db.QueryContext(qCtx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanNodes(rows)
}
