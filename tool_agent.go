package dash

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Edge is an alias for the graph edge type
// Defined in types.go, imported here for CreateEdge calls

// AgentSession represents a spawned agent session in the graph.
type AgentSession struct {
	ID              string    `json:"id"`
	AgentKey        string    `json:"agent_key"`
	Name            string    `json:"name"`
	Mission         string    `json:"mission"`
	Status          string    `json:"status"`
	SpawnedBy       string    `json:"spawned_by"`
	SpawnedAt       time.Time `json:"spawned_at"`
	SessionID       string    `json:"session_id,omitempty"`
	Controller      string    `json:"controller"`         // "human", "llm", "idle"
	ControllerSince time.Time `json:"controller_since"`
}

// defSpawnAgent creates the spawn_agent tool definition.
func defSpawnAgent() *ToolDef {
	return &ToolDef{
		Name:        "spawn_agent",
		Description: "Spawna en ny specialistagent. Agenten skapas som en CONTEXT.agent_session nod och kan sedan anslutas till via dess session_id. Använd detta för att delegera uppgifter till specialiserade agenter.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent_key": map[string]any{
					"type":        "string",
					"description": "Unik nyckel för agenten (t.ex. 'code-reviewer', 'architect', 'tester'). Används för att identifiera agenttypen.",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Visningsnamn för agenten (valfritt, används i UI).",
				},
				"mission": map[string]any{
					"type":        "string",
					"description": "Beskrivning av vad agenten ska göra. Bör vara konkret och åtgärdbar.",
				},
				"context_hints": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Valfria söktermer eller filer agenten bör känna till vid start.",
				},
			},
			"required": []string{"agent_key", "mission"},
		},
		Fn:   handleSpawnAgent,
		Tags: []string{"write", "graph"},
	}
}

func handleSpawnAgent(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	agentKey, _ := args["agent_key"].(string)
	mission, _ := args["mission"].(string)
	name, _ := args["name"].(string)
	hintsRaw, _ := args["context_hints"].([]any)

	if name == "" {
		name = agentKey
	}

	// Generate unique session ID
	sessionID := fmt.Sprintf("agent-%s-%d", agentKey, time.Now().Unix())

	// Create context hints
	var hints []string
	for _, h := range hintsRaw {
		if s, ok := h.(string); ok {
			hints = append(hints, s)
		}
	}

	// Create agent session node
	now := time.Now().UTC()
	nodeData := map[string]any{
		"agent_key":        agentKey,
		"mission":          mission,
		"status":           "spawned",
		"spawned_at":       now.Format(time.RFC3339),
		"context_hints":    hints,
		"controller":       "idle",
		"controller_since": now.Format(time.RFC3339),
	}

	node, err := d.GetOrCreateNode(ctx, LayerContext, "agent_session", sessionID, nodeData)
	if err != nil {
		return nil, fmt.Errorf("create agent session: %w", err)
	}

	// If there are context hints, do semantic search and link relevant files
	if len(hints) > 0 {
		for _, hint := range hints {
			results, err := d.SearchSimilarFiles(ctx, hint, 3)
			if err != nil {
				continue
			}
			for _, r := range results {
				_ = d.CreateEdge(ctx, &Edge{
					SourceID: node.ID,
					TargetID: r.ID,
					Relation: "needs_context",
				})
			}
		}
	}

	session := AgentSession{
		ID:        node.ID.String(),
		AgentKey:  agentKey,
		Name:      name,
		Mission:   mission,
		Status:    "spawned",
		SpawnedAt: time.Now(),
		SessionID: sessionID,
	}

	return session, nil
}

// defAgentStatus creates a tool to check agent status.
func defAgentStatus() *ToolDef {
	return &ToolDef{
		Name:        "agent_status",
		Description: "Hämta status för en eller alla agenter. Visar vilka agenter som är aktiva, vad de arbetar med, och vilka som är klara.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent_key": map[string]any{
					"type":        "string",
					"description": "Hämta status för en specifik agent (valfritt).",
				},
			},
		},
		Fn:   handleAgentStatus,
		Tags: []string{"read", "graph"},
	}
}

func handleAgentStatus(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	agentKey, hasKey := args["agent_key"].(string)

	var query string
	var params []any

	if hasKey && agentKey != "" {
		query = `
			SELECT id, name, data, created_at
			FROM nodes
			WHERE layer = 'CONTEXT' AND type = 'agent_session'
			  AND data->>'agent_key' = $1
			  AND deleted_at IS NULL
			ORDER BY created_at DESC
		`
		params = append(params, agentKey)
	} else {
		query = `
			SELECT id, name, data, created_at
			FROM nodes
			WHERE layer = 'CONTEXT' AND type = 'agent_session'
			  AND deleted_at IS NULL
			ORDER BY created_at DESC
			LIMIT 20
		`
	}

	rows, err := d.db.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, fmt.Errorf("query agents: %w", err)
	}
	defer rows.Close()

	var agents []AgentSession
	for rows.Next() {
		var id uuid.UUID
		var name string
		var dataJSON []byte
		var createdAt time.Time

		if err := rows.Scan(&id, &name, &dataJSON, &createdAt); err != nil {
			continue
		}

		var data map[string]any
		if err := json.Unmarshal(dataJSON, &data); err != nil {
			data = make(map[string]any)
		}

		agent := AgentSession{
			ID:        id.String(),
			Name:      name,
			SpawnedAt: createdAt,
		}

		if v, ok := data["agent_key"].(string); ok {
			agent.AgentKey = v
		}
		if v, ok := data["mission"].(string); ok {
			agent.Mission = v
		}
		if v, ok := data["status"].(string); ok {
			agent.Status = v
		}
		if v, ok := data["spawned_by"].(string); ok {
			agent.SpawnedBy = v
		}
		if v, ok := data["controller"].(string); ok {
			agent.Controller = v
		}
		if v, ok := data["controller_since"].(string); ok {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				agent.ControllerSince = t
			}
		}

		agents = append(agents, agent)
	}

	return map[string]any{
		"agents": agents,
		"count":  len(agents),
	}, rows.Err()
}

// defUpdateAgentStatus allows an agent to report its progress.
func defUpdateAgentStatus() *ToolDef {
	return &ToolDef{
		Name:        "update_agent",
		Description: "Uppdatera status för en agent-session. Används av agenter för att rapportera progress, blockers, eller att de är klara.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent_session_id": map[string]any{
					"type":        "string",
					"description": "Session ID för agenten som ska uppdateras.",
				},
				"status": map[string]any{
					"type":        "string",
					"enum":        []string{"active", "waiting", "blocked", "completed", "failed"},
					"description": "Ny status för agenten.",
				},
				"progress": map[string]any{
					"type":        "string",
					"description": "Kort sammanfattning av vad agenten gjort hittills.",
				},
				"blocker": map[string]any{
					"type":        "string",
					"description": "Om status är 'blocked', beskriv vad som blockerar.",
				},
			},
			"required": []string{"agent_session_id", "status"},
		},
		Fn:   handleUpdateAgent,
		Tags: []string{"write", "graph"},
	}
}

// defAskAgent creates the ask_agent tool for cross-agent communication.
func defAskAgent() *ToolDef {
	return &ToolDef{
		Name:        "ask_agent",
		Description: "Ställ en fråga till en annan agent. Frågan dispatchar till target-agenten som svarar med answer_query. Du får svaret tillbaka som tool result.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target": map[string]any{
					"type":        "string",
					"description": "Agent-nyckel att fråga (t.ex. 'database-agent', 'cockpit-backend').",
				},
				"question": map[string]any{
					"type":        "string",
					"description": "Frågan att ställa till target-agenten.",
				},
			},
			"required": []string{"target", "question"},
		},
		Fn:   handleAskAgent,
		Tags: []string{"write", "graph"},
	}
}

func handleAskAgent(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	target, _ := args["target"].(string)
	question, _ := args["question"].(string)

	if target == "" || question == "" {
		return nil, fmt.Errorf("target and question are required")
	}

	queryID := fmt.Sprintf("query-%d-%s", time.Now().UnixMilli(), target)

	// Store observation for audit trail
	_ = d.StoreObservation(ctx, "", "agent_query", map[string]any{
		"query_id": queryID,
		"target":   target,
		"question": question,
		"status":   "dispatched",
	})

	return map[string]any{
		"query_id": queryID,
		"target":   target,
		"question": question,
		"status":   "dispatched",
	}, nil
}

// defAnswerQuery creates the answer_query tool for responding to cross-agent queries.
func defAnswerQuery() *ToolDef {
	return &ToolDef{
		Name:        "answer_query",
		Description: "Svara på en fråga från en annan agent. Använd detta när du fått en fråga via ask_agent och har ett svar. Svaret routas tillbaka till den frågande agenten.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query_id": map[string]any{
					"type":        "string",
					"description": "Query-ID från frågan du svarar på.",
				},
				"answer": map[string]any{
					"type":        "string",
					"description": "Ditt svar på frågan.",
				},
			},
			"required": []string{"query_id", "answer"},
		},
		Fn:   handleAnswerQuery,
		Tags: []string{"write", "graph"},
	}
}

func handleAnswerQuery(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	queryID, _ := args["query_id"].(string)
	answer, _ := args["answer"].(string)

	if queryID == "" || answer == "" {
		return nil, fmt.Errorf("query_id and answer are required")
	}

	// Store observation for audit trail
	_ = d.StoreObservation(ctx, "", "agent_answer", map[string]any{
		"query_id": queryID,
		"answer":   answer,
		"status":   "answered",
	})

	return map[string]any{
		"query_id": queryID,
		"ok":       true,
	}, nil
}

func handleUpdateAgent(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	sessionID, _ := args["agent_session_id"].(string)
	status, _ := args["status"].(string)
	progress, _ := args["progress"].(string)
	blocker, _ := args["blocker"].(string)
	controller, hasController := args["controller"].(string)
	controllerSince, _ := args["controller_since"].(string)

	// Find the agent session node
	node, err := d.GetNodeByName(ctx, LayerContext, "agent_session", sessionID)
	if err != nil {
		return nil, fmt.Errorf("agent session not found: %w", err)
	}

	// Update data
	updates := map[string]any{
		"status":     status,
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	}
	if progress != "" {
		updates["progress"] = progress
	}
	if blocker != "" {
		updates["blocker"] = blocker
	}
	if hasController {
		updates["controller"] = controller
		if controllerSince != "" {
			updates["controller_since"] = controllerSince
		} else {
			updates["controller_since"] = time.Now().UTC().Format(time.RFC3339)
		}
	}

	if err := d.UpdateNodeData(ctx, node, updates); err != nil {
		return nil, fmt.Errorf("update agent: %w", err)
	}

	return map[string]any{
		"agent_session_id": sessionID,
		"status":           status,
		"updated":          true,
	}, nil
}
