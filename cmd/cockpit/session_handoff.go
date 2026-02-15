package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"dash"

	tea "github.com/charmbracelet/bubbletea"
)

type handoffCompleteMsg struct {
	agentID string
	err     error
}

func performHandoff(d *dash.Dash, tab *agentTab) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// 1. Build envelope summary from recent messages
		summary := buildEnvelopeSummary(tab)

		// 2. Store as observation
		_ = d.StoreObservation(ctx, tab.sessionID, "session_handoff", map[string]any{
			"agent_key":    tab.agentKey,
			"mission":      tab.mission,
			"summary":      summary,
			"token_usage":  tab.meter.total(),
			"token_limit":  tab.meter.limit,
			"handoff_at":   time.Now().UTC().Format(time.RFC3339),
			"message_count": len(tab.chat.messages),
		})

		// 3. Update agent session node via MCP tool
		result := d.RunTool(ctx, "update_agent", map[string]any{
			"agent_session_id": tab.sessionID,
			"status":           "waiting",
			"progress":         fmt.Sprintf("Handoff at %d%% tokens. %s", tab.meter.pct(), summary),
		}, &dash.ToolOpts{
			SessionID: tab.sessionID,
			CallerID:  "cockpit",
		})

		if !result.Success {
			return handoffCompleteMsg{agentID: tab.id, err: fmt.Errorf("update_agent: %s", result.Error)}
		}

		return handoffCompleteMsg{agentID: tab.id}
	}
}

func buildEnvelopeSummary(tab *agentTab) string {
	// Collect assistant messages for summary
	var parts []string
	for _, msg := range tab.chat.messages {
		if msg.Role == "assistant" && msg.Content != "" {
			text := msg.Content
			if len(text) > 200 {
				text = text[:200] + "..."
			}
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return "No assistant responses yet."
	}
	// Take last 3 responses max
	if len(parts) > 3 {
		parts = parts[len(parts)-3:]
	}
	summary := ""
	for i, p := range parts {
		summary += fmt.Sprintf("[%d] %s\n", i+1, p)
	}
	if len(summary) > 1000 {
		summary = summary[:1000] + "..."
	}
	return summary
}

// resumeAfterHandoff creates a new chat session for the agent with context from the handoff.
func resumeAfterHandoff(d *dash.Dash, tab *agentTab, chatCl *chatClient) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Get prompt with agent context
		text, err := d.GetPrompt(ctx, "agent-continuous", dash.PromptOptions{
			AgentKey:     tab.agentKey,
			AgentMission: tab.mission,
		})
		if err != nil {
			return handoffCompleteMsg{agentID: tab.id, err: err}
		}

		// Reset the chat but keep the system prompt context
		tab.chat.messages = []chatMessage{
			{Role: "system", Content: text},
			{Role: "user", Content: "Handoff: du har nått token-gränsen. Här är sammanfattningen av ditt arbete hittills. Fortsätt med missionen."},
		}
		tab.meter = newTokenMeter(tab.meter.limit)
		tab.status = agentActive

		return handoffCompleteMsg{agentID: tab.id}
	}
}

// agentSpawnInfo is extracted from the spawn_agent tool result.
type agentSpawnInfo struct {
	ID        string `json:"id"`
	AgentKey  string `json:"agent_key"`
	Name      string `json:"name"`
	Mission   string `json:"mission"`
	SessionID string `json:"session_id"`
}

func parseSpawnResult(resultJSON string) *agentSpawnInfo {
	var info agentSpawnInfo
	if err := json.Unmarshal([]byte(resultJSON), &info); err != nil {
		return nil
	}
	if info.AgentKey == "" || info.SessionID == "" {
		return nil
	}
	return &info
}
