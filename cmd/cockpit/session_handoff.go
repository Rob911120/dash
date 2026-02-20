package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dash"

	tea "github.com/charmbracelet/bubbletea"
)

// --- Control transfer messages ---

type takeControlMsg struct {
	agentKey  string
	sessionID string
	briefing  string
	err       error
}

type releaseControlMsg struct {
	agentKey string
	summary  string
	err      error
}

type pauseAgentMsg struct {
	agentKey string
	err      error
}

// takeControlCmd transfers control of an agent to human.
func takeControlCmd(d *dash.Dash, agentKey, sessionID, mission string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		now := time.Now().UTC()

		// Update controller in graph (if session exists)
		if sessionID != "" {
			d.RunTool(ctx, "update_agent", map[string]any{
				"agent_session_id": sessionID,
				"status":           "active",
				"controller":       "human",
				"controller_since": now.Format(time.RFC3339),
			}, &dash.ToolOpts{CallerID: "cockpit"})
		}

		// Store observation
		_ = d.StoreObservation(ctx, sessionID, "control_transfer", map[string]any{
			"agent_key": agentKey,
			"from":      "idle",
			"to":        "human",
			"reason":    "take_control",
			"at":        now.Format(time.RFC3339),
		})

		// Assemble snapshot for briefing
		snap, _ := d.AssembleAgentSnapshot(ctx, agentKey, mission)
		briefing := generateBriefing(snap, now)

		return takeControlMsg{
			agentKey:  agentKey,
			sessionID: sessionID,
			briefing:  briefing,
		}
	}
}

// releaseControlCmd releases human control of an agent.
func releaseControlCmd(d *dash.Dash, tab *agentTab, reason string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		now := time.Now().UTC()
		summary := buildEnvelopeSummary(tab)

		// Store observation
		_ = d.StoreObservation(ctx, tab.sessionID, "control_transfer", map[string]any{
			"agent_key": tab.agentKey,
			"from":      "human",
			"to":        "idle",
			"reason":    reason,
			"summary":   summary,
			"at":        now.Format(time.RFC3339),
		})

		// Update controller in graph
		if tab.sessionID != "" {
			d.RunTool(ctx, "update_agent", map[string]any{
				"agent_session_id": tab.sessionID,
				"status":           "active",
				"controller":       "idle",
				"controller_since": now.Format(time.RFC3339),
			}, &dash.ToolOpts{CallerID: "cockpit"})
		}

		return releaseControlMsg{
			agentKey: tab.agentKey,
			summary:  summary,
		}
	}
}

// pauseAgentCmd pauses an agent (stops autonomous execution).
func pauseAgentCmd(d *dash.Dash, tab *agentTab, prevController string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		now := time.Now().UTC()

		// Store observation
		_ = d.StoreObservation(ctx, tab.sessionID, "control_transfer", map[string]any{
			"agent_key": tab.agentKey,
			"from":      prevController,
			"to":        "idle",
			"reason":    "paused",
			"at":        now.Format(time.RFC3339),
		})

		// Update controller in graph
		if tab.sessionID != "" {
			d.RunTool(ctx, "update_agent", map[string]any{
				"agent_session_id": tab.sessionID,
				"status":           "active",
				"controller":       "idle",
				"controller_since": now.Format(time.RFC3339),
			}, &dash.ToolOpts{CallerID: "cockpit"})
		}

		return pauseAgentMsg{agentKey: tab.agentKey}
	}
}

// generateBriefing creates a human-readable control briefing from a snapshot.
func generateBriefing(snap *dash.AgentContextSnapshot, now time.Time) string {
	if snap == nil {
		return fmt.Sprintf("⚡ KONTROLL ÖVERTAGEN (%s)\nDu styr nu.", now.Format("15:04"))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "⚡ KONTROLL ÖVERTAGEN (%s)\n", now.Format("15:04"))

	if snap.Situation != "" {
		fmt.Fprintf(&b, "📌 Situation: %s\n", snap.Situation)
	}

	taskCount := len(snap.Tasks)
	peerCount := snap.PeersTotal
	fmt.Fprintf(&b, "📝 Tasks: %d aktiva | 🤖 Agenter: %d aktiva\n", taskCount, peerCount)
	b.WriteString("Du styr nu.")

	return b.String()
}

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
		tab.chat.messages = nil
		tab.chat.uiMessages = nil
		tab.chat.renderLog = nil
		tab.chat.appendMsg(dash.ChatMessage{Role: "system", Content: text})
		tab.chat.appendMsg(dash.ChatMessage{Role: "user", Content: "Handoff: du har nått token-gränsen. Här är sammanfattningen av ditt arbete hittills. Fortsätt med missionen."})
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
