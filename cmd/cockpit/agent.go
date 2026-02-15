package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// observationAgent polls the observations table and notifies about interesting events
type observationAgent struct {
	db            *sql.DB
	lastCheckedAt time.Time
	notifications []observationNotification
	mutedTypes    map[string]bool
}

// observationNotification represents a notification from the agent
type observationNotification struct {
	ID        string
	Type      string
	Message   string
	Timestamp time.Time
	Seen      bool
}

// newObservationAgent creates a new observation agent
func newObservationAgent(db *sql.DB) *observationAgent {
	return &observationAgent{
		db:            db,
		lastCheckedAt: time.Now().Add(-5 * time.Minute), // Start with last 5 minutes
		notifications: make([]observationNotification, 0),
		mutedTypes: map[string]bool{
			"tool_event": false, // We want these
		},
	}
}

// poll checks for new observations and returns any notifications
func (a *observationAgent) poll(ctx context.Context) ([]observationNotification, error) {
	query := `
		SELECT id, type, value, observed_at
		FROM observations
		WHERE observed_at > $1
		  AND type IN ('agent_reasoning', 'tool_event')
		ORDER BY observed_at ASC
		LIMIT 10
	`

	rows, err := a.db.QueryContext(ctx, query, a.lastCheckedAt)
	if err != nil {
		return nil, fmt.Errorf("query observations: %w", err)
	}
	defer rows.Close()

	var notifications []observationNotification
	for rows.Next() {
		var id, obsType string
		var value interface{}
		var observedAt time.Time

		if err := rows.Scan(&id, &obsType, &value, &observedAt); err != nil {
			continue
		}

		// Update last checked time
		if observedAt.After(a.lastCheckedAt) {
			a.lastCheckedAt = observedAt
		}

		// Create notification from observation
		notif := a.createNotification(id, obsType, value, observedAt)
		if notif != nil {
			notifications = append(notifications, *notif)
			a.notifications = append(a.notifications, *notif)
		}
	}

	return notifications, nil
}

// createNotification creates a human-readable notification from an observation
func (a *observationAgent) createNotification(id, obsType string, value interface{}, ts time.Time) *observationNotification {
	switch obsType {
	case "agent_reasoning":
		// Extract reasoning message if available
		msg := extractReasoningMessage(value)
		if msg != "" {
			return &observationNotification{
				ID:        id,
				Type:      "insight",
				Message:   msg,
				Timestamp: ts,
				Seen:      false,
			}
		}

	case "tool_event":
		// Check if it's an interesting tool event
		eventType := extractToolEventType(value)
		if eventType == "tool.failure" {
			return &observationNotification{
				ID:        id,
				Type:      "alert",
				Message:   "Tool execution failed",
				Timestamp: ts,
				Seen:      false,
			}
		}
		// Pattern detection
		if eventType == "tool.post" {
			toolName := extractToolName(value)
			if toolName == "patterns" {
				return &observationNotification{
					ID:        id,
					Type:      "pattern",
					Message:   "New pattern detected",
					Timestamp: ts,
					Seen:      false,
				}
			}
		}
	}

	return nil
}

// getUnreadNotifications returns all unseen notifications
func (a *observationAgent) getUnreadNotifications() []observationNotification {
	var unread []observationNotification
	for i := range a.notifications {
		if !a.notifications[i].Seen {
			unread = append(unread, a.notifications[i])
		}
	}
	return unread
}

// markAsSeen marks notifications as seen
func (a *observationAgent) markAsSeen(ids []string) {
	idSet := make(map[string]bool)
	for _, id := range ids {
		idSet[id] = true
	}
	for i := range a.notifications {
		if idSet[a.notifications[i].ID] {
			a.notifications[i].Seen = true
		}
	}
}

// markAllAsSeen marks all notifications as seen
func (a *observationAgent) markAllAsSeen() {
	for i := range a.notifications {
		a.notifications[i].Seen = true
	}
}

// extractReasoningMessage extracts a message from agent_reasoning value
func extractReasoningMessage(value interface{}) string {
	if m, ok := value.(map[string]interface{}); ok {
		if content, ok := m["content"].(string); ok {
			return truncate(content, 80)
		}
		if summary, ok := m["summary"].(string); ok {
			return summary
		}
	}
	return ""
}

// extractToolEventType extracts event type from tool_event value
func extractToolEventType(value interface{}) string {
	if m, ok := value.(map[string]interface{}); ok {
		if norm, ok := m["normalized"].(map[string]interface{}); ok {
			if event, ok := norm["event"].(string); ok {
				return event
			}
		}
	}
	return ""
}

// extractToolName extracts tool name from tool_event value
func extractToolName(value interface{}) string {
	if m, ok := value.(map[string]interface{}); ok {
		if norm, ok := m["normalized"].(map[string]interface{}); ok {
			if tool, ok := norm["tool"].(string); ok {
				return tool
			}
		}
	}
	return ""
}



// pollCmd creates a tea.Cmd that polls the observation agent
func pollCmd(agent *observationAgent) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		notifications, err := agent.poll(ctx)
		if err != nil {
			return observationPollMsg{err: err}
		}
		return observationPollMsg{notifications: notifications}
	}
}

// observationPollMsg is sent when the agent finishes polling
type observationPollMsg struct {
	notifications []observationNotification
	err           error
}

// observationTickCmd returns a command that ticks every few seconds for polling
func observationTickCmd() tea.Cmd {
	return tea.Tick(5*time.Second, func(t time.Time) tea.Msg {
		return observationTickMsg{t}
	})
}

type observationTickMsg struct{ time.Time }
