package dash

import (
	"context"
	"encoding/json"
	"time"
)

// LiveStatus holds ephemeral cockpit state (not from graph).
type LiveStatus struct {
	Streaming bool
	ToolName  string
	Exchanges int
}

// AgentContextSnapshot is the graph's interpretation at a point in time, projected for UI.
type AgentContextSnapshot struct {
	AgentKey       string
	Revision       int64
	FetchedAt      time.Time
	Mission        string
	Role           string
	Situation      string
	NextAction     string
	Tasks          []TaskSummary
	Decisions      []DecisionSummary
	Peers          []PeerSummary
	PeersTruncated bool
	PeersTotal     int
	Constraints    []string
	Live           LiveStatus
}

// TaskSummary is a lightweight task representation for agent dashboard rendering.
type TaskSummary struct {
	Name      string
	Status    string
	Intent    string
	BlockedBy []string
}

// DecisionSummary is a lightweight decision for agent dashboard rendering.
type DecisionSummary struct {
	Text      string
	CreatedAt time.Time
}

// PeerSummary represents another active agent.
type PeerSummary struct {
	AgentKey string
	Status   string
	Mission  string
}

// trackMaxUpdated updates max to the latest UpdatedAt among the given nodes.
func trackMaxUpdated(max *time.Time, nodes ...*Node) {
	for _, n := range nodes {
		if n != nil && n.UpdatedAt.After(*max) {
			*max = n.UpdatedAt
		}
	}
}

// AssembleAgentSnapshot builds a snapshot of the graph state from a specific agent's perspective.
func (d *Dash) AssembleAgentSnapshot(ctx context.Context, agentKey, mission string) (*AgentContextSnapshot, error) {
	snap := &AgentContextSnapshot{
		AgentKey:  agentKey,
		FetchedAt: time.Now(),
		Mission:   mission,
	}

	var maxUpdated time.Time

	// Mission from graph (override if found)
	if node, err := d.QueryMission(ctx); err == nil && node != nil {
		snap.Mission = node.Name
		if data := parseNodeData(node); data != nil {
			if t, ok := data["text"].(string); ok && t != "" {
				snap.Mission = t
			}
		}
		trackMaxUpdated(&maxUpdated, node)
	}

	// Situation from context frame
	if node, err := d.QueryContextFrame(ctx); err == nil && node != nil {
		if data := parseNodeData(node); data != nil {
			if focus, ok := data["current_focus"].(string); ok {
				snap.Situation = focus
			}
			if snap.Situation == "" {
				if t, ok := data["text"].(string); ok {
					snap.Situation = t
				}
			}
		}
		if snap.Situation == "" {
			snap.Situation = node.Name
		}
		trackMaxUpdated(&maxUpdated, node)
	}

	// Fetch all active agents once (used for role lookup + peers)
	allAgents, _ := d.QueryActiveAgents(ctx)
	trackMaxUpdated(&maxUpdated, allAgents...)

	// Agent's own role
	for _, a := range allAgents {
		if a.Name == agentKey {
			if data := parseNodeData(a); data != nil {
				if desc, ok := data["description"].(string); ok {
					snap.Role = desc
				}
			}
			if snap.Role == "" {
				snap.Role = a.Name
			}
			break
		}
	}

	// Tasks
	if nodes, err := d.QueryActiveTasks(ctx); err == nil {
		for _, n := range nodes {
			ts := TaskSummary{
				Name:   n.Name,
				Status: "pending",
			}
			if data := parseNodeData(n); data != nil {
				if s, ok := data["status"].(string); ok {
					ts.Status = s
				}
				if intent, ok := data["intent"].(string); ok {
					ts.Intent = intent
				}
				if bb, ok := data["blocked_by"].([]interface{}); ok {
					for _, b := range bb {
						if s, ok := b.(string); ok {
							ts.BlockedBy = append(ts.BlockedBy, s)
						}
					}
				}
			}
			snap.Tasks = append(snap.Tasks, ts)
		}
		trackMaxUpdated(&maxUpdated, nodes...)
	}

	// Decisions
	if nodes, err := d.QueryRecentDecisions(ctx); err == nil {
		for i, n := range nodes {
			if i >= 5 {
				break
			}
			text := n.Name
			if data := parseNodeData(n); data != nil {
				if t, ok := data["text"].(string); ok && t != "" {
					text = t
				}
			}
			snap.Decisions = append(snap.Decisions, DecisionSummary{
				Text:      text,
				CreatedAt: n.CreatedAt,
			})
		}
		trackMaxUpdated(&maxUpdated, nodes...)
	}

	// Peers (active agents, excluding self)
	for _, a := range allAgents {
		if a.Name == agentKey {
			continue
		}
		peer := PeerSummary{
			AgentKey: a.Name,
			Status:   "active",
		}
		if data := parseNodeData(a); data != nil {
			if s, ok := data["status"].(string); ok {
				peer.Status = s
			}
			if m, ok := data["mission"].(string); ok {
				peer.Mission = m
			}
		}
		snap.Peers = append(snap.Peers, peer)
	}
	snap.PeersTotal = len(allAgents) - 1
	if snap.PeersTotal < 0 {
		snap.PeersTotal = 0
	}
	if len(snap.Peers) > 10 {
		snap.Peers = snap.Peers[:10]
		snap.PeersTruncated = true
	}

	// Constraints
	if nodes, err := d.QueryConstraints(ctx); err == nil {
		for _, n := range nodes {
			snap.Constraints = append(snap.Constraints, n.Name)
		}
		trackMaxUpdated(&maxUpdated, nodes...)
	}

	// DB-based revision: 0 if no data (clear signal)
	snap.Revision = maxUpdated.UnixMilli()

	return snap, nil
}

// parseNodeData unmarshals node.Data into a map.
func parseNodeData(n *Node) map[string]any {
	if n == nil || len(n.Data) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(n.Data, &m); err != nil {
		return nil
	}
	return m
}
