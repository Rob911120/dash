package dash

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// PromoteInsight describes an insight to extract from a session.
type PromoteInsight struct {
	Text    string `json:"text"`
	Context string `json:"context,omitempty"`
}

// PromoteDecision describes a decision to extract from a session.
type PromoteDecision struct {
	Text      string `json:"text"`
	Rationale string `json:"rationale,omitempty"`
}

// PromoteTask describes a task to create from a session.
type PromoteTask struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status,omitempty"` // default: "pending"
}

// FrameUpdate describes updates to the context_frame singleton.
type FrameUpdate struct {
	Summary      string   `json:"summary,omitempty"`
	CurrentFocus string   `json:"current_focus,omitempty"`
	NextSteps    []string `json:"next_steps,omitempty"`
	Blockers     []string `json:"blockers,omitempty"`
}

// PromoteRequest specifies what to extract from a session during promotion.
type PromoteRequest struct {
	SessionID   string            `json:"session_id"`
	Insights    []PromoteInsight  `json:"insights,omitempty"`
	Decisions   []PromoteDecision `json:"decisions,omitempty"`
	Tasks       []PromoteTask     `json:"tasks,omitempty"`
	FrameUpdate *FrameUpdate      `json:"frame_update,omitempty"`
}

// PromoteResult contains the nodes created during promotion.
type PromoteResult struct {
	SessionID     string      `json:"session_id"`
	Insights      []*Node     `json:"insights,omitempty"`
	Decisions     []*Node     `json:"decisions,omitempty"`
	Tasks         []*Node     `json:"tasks,omitempty"`
	FrameUpdated  bool        `json:"frame_updated"`
	CompressedAt  time.Time   `json:"compressed_at"`
}

// PromoteSession extracts canonical knowledge from an ephemeral session.
// This is agent-initiated: the agent decides what to extract.
func (d *Dash) PromoteSession(ctx context.Context, req *PromoteRequest) (*PromoteResult, error) {
	if req.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	result := &PromoteResult{SessionID: req.SessionID}
	now := time.Now()

	err := d.WithTx(ctx, func(tx *sql.Tx) error {
		// 1. Find the session node
		session, err := d.GetNodeByName(ctx, LayerContext, "session", req.SessionID)
		if err != nil {
			return fmt.Errorf("session not found: %w", err)
		}

		// Find mission (for linking tasks)
		var missionID *uuid.UUID
		if mission, err := d.querySingleNode(ctx, queryGetMission, 2*time.Second); err == nil && mission != nil {
			missionID = &mission.ID
		}

		// 2. Create insights
		for _, ins := range req.Insights {
			data := map[string]any{
				"text":       ins.Text,
				"created_by": "promotion",
				"source_session": req.SessionID,
			}
			if ins.Context != "" {
				data["context"] = ins.Context
			}
			dataJSON, _ := json.Marshal(data)

			name := ins.Text
			if len(name) > 255 {
				name = name[:252] + "..."
			}

			node := &Node{
				Layer: LayerContext,
				Type:  "insight",
				Name:  name,
				Data:  dataJSON,
			}

			if err := d.createNodeTx(ctx, tx, node); err != nil {
				return fmt.Errorf("create insight: %w", err)
			}

			// Link: insight --derived_from--> session
			if err := d.createEdgeTx(ctx, tx, node.ID, session.ID, RelationDerivedFrom); err != nil {
				return fmt.Errorf("link insight to session: %w", err)
			}

			result.Insights = append(result.Insights, node)
		}

		// 3. Create decisions
		for _, dec := range req.Decisions {
			data := map[string]any{
				"text":       dec.Text,
				"created_by": "promotion",
				"source_session": req.SessionID,
			}
			if dec.Rationale != "" {
				data["rationale"] = dec.Rationale
			}
			dataJSON, _ := json.Marshal(data)

			name := dec.Text
			if len(name) > 255 {
				name = name[:252] + "..."
			}

			node := &Node{
				Layer: LayerContext,
				Type:  "decision",
				Name:  name,
				Data:  dataJSON,
			}

			if err := d.createNodeTx(ctx, tx, node); err != nil {
				return fmt.Errorf("create decision: %w", err)
			}

			// Link: decision --derived_from--> session
			if err := d.createEdgeTx(ctx, tx, node.ID, session.ID, RelationDerivedFrom); err != nil {
				return fmt.Errorf("link decision to session: %w", err)
			}

			result.Decisions = append(result.Decisions, node)
		}

		// 4. Create tasks
		for _, t := range req.Tasks {
			status := t.Status
			if status == "" {
				status = "pending"
			}
			data := map[string]any{
				"description": t.Description,
				"status":      status,
				"created_by":  "promotion",
				"source_session": req.SessionID,
			}
			dataJSON, _ := json.Marshal(data)

			node := &Node{
				Layer: LayerContext,
				Type:  "task",
				Name:  t.Name,
				Data:  dataJSON,
			}

			if err := d.createNodeTx(ctx, tx, node); err != nil {
				return fmt.Errorf("create task: %w", err)
			}

			// Link: task --implements--> mission (if mission exists)
			if missionID != nil {
				d.createEdgeTx(ctx, tx, node.ID, *missionID, RelationImplements)
			}

			result.Tasks = append(result.Tasks, node)
		}

		// 5. Update context_frame if requested
		if req.FrameUpdate != nil {
			frame, err := d.GetNodeByName(ctx, LayerContext, "context_frame", "current")
			if err != nil {
				// Create if doesn't exist
				frameData := map[string]any{}
				if req.FrameUpdate.Summary != "" {
					frameData["summary"] = req.FrameUpdate.Summary
				}
				if req.FrameUpdate.CurrentFocus != "" {
					frameData["current_focus"] = req.FrameUpdate.CurrentFocus
				}
				if req.FrameUpdate.NextSteps != nil {
					frameData["next_steps"] = req.FrameUpdate.NextSteps
				}
				if req.FrameUpdate.Blockers != nil {
					frameData["blockers"] = req.FrameUpdate.Blockers
				}
				dataJSON, _ := json.Marshal(frameData)
				frame = &Node{
					Layer: LayerContext,
					Type:  "context_frame",
					Name:  "current",
					Data:  dataJSON,
				}
				if err := d.createNodeTx(ctx, tx, frame); err != nil {
					return fmt.Errorf("create context_frame: %w", err)
				}
			} else {
				// Update existing frame
				existing := extractNodeData(frame)
				if req.FrameUpdate.Summary != "" {
					existing["summary"] = req.FrameUpdate.Summary
				}
				if req.FrameUpdate.CurrentFocus != "" {
					existing["current_focus"] = req.FrameUpdate.CurrentFocus
				}
				if req.FrameUpdate.NextSteps != nil {
					existing["next_steps"] = req.FrameUpdate.NextSteps
				}
				if req.FrameUpdate.Blockers != nil {
					existing["blockers"] = req.FrameUpdate.Blockers
				}
				dataJSON, _ := json.Marshal(existing)
				frame.Data = dataJSON
				_, err = tx.ExecContext(ctx,
					`UPDATE nodes SET data = $2 WHERE id = $1 AND deleted_at IS NULL`,
					frame.ID, frame.Data)
				if err != nil {
					return fmt.Errorf("update context_frame: %w", err)
				}
			}
			result.FrameUpdated = true
		}

		// 6. Mark session as compressed
		result.CompressedAt = now
		_, err = tx.ExecContext(ctx,
			`UPDATE nodes SET data = data || $2::jsonb WHERE id = $1 AND deleted_at IS NULL`,
			session.ID,
			fmt.Sprintf(`{"status":"compressed","compressed_at":"%s","promotion_candidate":false}`, now.Format(time.RFC3339)),
		)
		if err != nil {
			return fmt.Errorf("mark session compressed: %w", err)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Create resulted_in edge_events + embed nodes (outside tx — append-only telemetry)
	session, _ := d.GetNodeByName(ctx, LayerContext, "session", req.SessionID)
	if session != nil {
		for _, node := range result.Insights {
			d.CreateEdgeEvent(ctx, &EdgeEvent{
				SourceID:   session.ID,
				TargetID:   node.ID,
				Relation:   EventRelationResultedIn,
				Success:    true,
				OccurredAt: now,
			})
			go d.EmbedNode(context.Background(), node)
		}
		for _, node := range result.Decisions {
			d.CreateEdgeEvent(ctx, &EdgeEvent{
				SourceID:   session.ID,
				TargetID:   node.ID,
				Relation:   EventRelationResultedIn,
				Success:    true,
				OccurredAt: now,
			})
			go d.EmbedNode(context.Background(), node)
		}
		for _, node := range result.Tasks {
			d.CreateEdgeEvent(ctx, &EdgeEvent{
				SourceID:   session.ID,
				TargetID:   node.ID,
				Relation:   EventRelationResultedIn,
				Success:    true,
				OccurredAt: now,
			})
			go d.EmbedNode(context.Background(), node)
		}
	}

	return result, nil
}

// createNodeTx creates a node within a transaction.
func (d *Dash) createNodeTx(ctx context.Context, tx *sql.Tx, node *Node) error {
	if node.Data == nil {
		node.Data = json.RawMessage(`{}`)
	}
	return tx.QueryRowContext(ctx,
		`INSERT INTO nodes (layer, type, name, data) VALUES ($1, $2, $3, $4) RETURNING id, created_at, updated_at`,
		node.Layer, node.Type, node.Name, node.Data,
	).Scan(&node.ID, &node.CreatedAt, &node.UpdatedAt)
}

// createEdgeTx creates an edge within a transaction.
func (d *Dash) createEdgeTx(ctx context.Context, tx *sql.Tx, sourceID, targetID uuid.UUID, relation Relation) error {
	if sourceID == targetID {
		return nil // skip self-loops silently
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO edges (source_id, target_id, relation, data) VALUES ($1, $2, $3, '{}')`,
		sourceID, targetID, relation,
	)
	return err
}
