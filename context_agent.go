package dash

import (
	"fmt"
	"strings"
	"time"
)

// --- Agent-continuous context sources ---
// These provide the "situation" context that lets a spawned agent
// understand where it is, what's decided, what's pending, and who else is working.

// srcAgentEnvelope produces the situation summary: mission + why this agent exists.
func srcAgentEnvelope(p SourceParams) string {
	var b strings.Builder

	// Mission
	node, err := p.D.querySingleNode(p.Ctx, queryGetMission, 2*time.Second)
	if err == nil && node != nil {
		data := extractNodeData(node)
		if stmt, ok := data["statement"].(string); ok && stmt != "" {
			b.WriteString(fmt.Sprintf("MISSION: %s\n", stmt))
		}
	}

	// Agent-specific mission (why this agent was spawned)
	if p.AgentMission != "" {
		b.WriteString(fmt.Sprintf("\nYOUR ROLE: %s\n", p.AgentMission))
		b.WriteString("Börja med att bekräfta att du förstår varför du är här och vad du ska göra.\n")
	}

	// Situation summary from context_frame
	frame, err := p.D.querySingleNode(p.Ctx, queryGetContextFrame, 2*time.Second)
	if err == nil && frame != nil {
		data := extractNodeData(frame)
		if focus, ok := data["current_focus"].(string); ok && focus != "" {
			b.WriteString(fmt.Sprintf("\nSITUATION: %s\n", focus))
		}
	}

	// Active tasks (compact form)
	tasks, err := p.D.GetActiveTasksWithDeps(p.Ctx)
	if err == nil && len(tasks) > 0 {
		b.WriteString("\nAKTIVA TASKS:\n")
		max := 10
		if p.MaxItems > 0 {
			max = p.MaxItems
		}
		for i, t := range tasks {
			if i >= max {
				b.WriteString(fmt.Sprintf("  +%d fler...\n", len(tasks)-max))
				break
			}
			status := t.Status
			if t.IsBlocked {
				status += "|blocked"
			}
			b.WriteString(fmt.Sprintf("- %s [%s]\n", t.Node.Name, status))
		}
	}

	return b.String()
}

// srcRecentDecisions shows recent decisions so the agent doesn't re-propose decided things.
func srcRecentDecisions(p SourceParams) string {
	nodes, err := p.D.queryMultipleNodes(p.Ctx, queryGetRecentDecisions, 2*time.Second)
	if err != nil || len(nodes) == 0 {
		return "\nRECENT DECISIONS: inga\n"
	}

	max := 5
	if p.MaxItems > 0 {
		max = p.MaxItems
	}
	if len(nodes) > max {
		nodes = nodes[:max]
	}

	var b strings.Builder
	b.WriteString("\nRECENT DECISIONS:\n")
	for _, n := range nodes {
		data := extractNodeData(n)
		text, _ := data["text"].(string)
		if text == "" {
			text = n.Name
		}
		if len(text) > 120 {
			text = text[:117] + "..."
		}
		b.WriteString(fmt.Sprintf("- %s\n", text))
	}
	return b.String()
}

// srcPendingDecisions shows decisions waiting for human input.
func srcPendingDecisions(p SourceParams) string {
	// Query for CONTEXT.pending_decision nodes with status=pending
	query := `
		SELECT id, layer, type, name, data, created_at, updated_at, deprecated_at
		FROM nodes
		WHERE layer = 'CONTEXT' AND type = 'pending_decision'
		  AND deprecated_at IS NULL
		  AND COALESCE(data->>'status', 'pending') = 'pending'
		ORDER BY created_at DESC
		LIMIT 5
	`
	nodes, err := p.D.queryMultipleNodes(p.Ctx, query, 2*time.Second)
	if err != nil || len(nodes) == 0 {
		return "\nPENDING DECISIONS: inga\n"
	}

	max := 5
	if p.MaxItems > 0 {
		max = p.MaxItems
	}
	if len(nodes) > max {
		nodes = nodes[:max]
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("\nPENDING DECISIONS (%d):\n", len(nodes)))
	for _, n := range nodes {
		data := extractNodeData(n)
		text, _ := data["text"].(string)
		if text == "" {
			text = n.Name
		}
		options, _ := data["options"].([]any)
		b.WriteString(fmt.Sprintf("- %s", text))
		if len(options) > 0 {
			var opts []string
			for _, o := range options {
				if s, ok := o.(string); ok {
					opts = append(opts, s)
				}
			}
			if len(opts) > 0 {
				b.WriteString(fmt.Sprintf(" [alternativ: %s]", strings.Join(opts, " | ")))
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// srcActiveAgents shows what other agents are currently running to avoid duplication.
func srcActiveAgents(p SourceParams) string {
	// Query for recent agent sessions (observations of type "agent_spawn")
	query := `
		SELECT id, layer, type, name, data, created_at, updated_at, deprecated_at
		FROM nodes
		WHERE layer = 'CONTEXT' AND type = 'agent_session'
		  AND deprecated_at IS NULL
		  AND COALESCE(data->>'status', 'active') != 'ended'
		ORDER BY created_at DESC
		LIMIT 10
	`
	nodes, err := p.D.queryMultipleNodes(p.Ctx, query, 2*time.Second)
	if err != nil || len(nodes) == 0 {
		return "\nACTIVE AGENTS: inga andra\n"
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("\nACTIVE AGENTS (%d):\n", len(nodes)))
	for _, n := range nodes {
		data := extractNodeData(n)
		agentKey, _ := data["agent_key"].(string)
		mission, _ := data["mission"].(string)
		status, _ := data["status"].(string)
		if agentKey == "" {
			agentKey = n.Name
		}
		if status == "" {
			status = "active"
		}
		// Skip self
		if agentKey == p.AgentKey {
			continue
		}
		line := fmt.Sprintf("- %s [%s]", agentKey, status)
		if mission != "" {
			if len(mission) > 60 {
				mission = mission[:57] + "..."
			}
			line += ": " + mission
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}
