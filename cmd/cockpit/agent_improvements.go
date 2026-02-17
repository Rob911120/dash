package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dash"

	"github.com/charmbracelet/lipgloss"
	tea "github.com/charmbracelet/bubbletea"
)

// Color shortcuts matching theme.go
var (
	colorGreen  = lipgloss.Color("#04B575")  // cSuccess
	colorYellow = lipgloss.Color("#F1FA8C")  // cWarning
	colorRed    = lipgloss.Color("#FF2A6D")  // cAlert
	colorBlue   = lipgloss.Color("#22D3EE")  // cCyan
)

// agentStatusUpdateMsg updates agent status in the UI
type agentStatusUpdateMsg struct {
	agentID string
	status  agentStatus
	message string
}

// agentProgressMsg shows progress/activity for an agent
type agentProgressMsg struct {
	agentID  string
	progress string
}

// Enhanced spawn with automatic mission assignment
func (m *model) spawnAgentWithMission(agentKey string) tea.Cmd {
	// Resolve mission and display name from DB-loaded defs
	mission := fmt.Sprintf("Du är en %s agent. Analysera koden och föreslå förbättringar.", agentKey)
	displayName := agentKey
	for _, def := range m.allAgentDefs {
		if def.Key == agentKey {
			if def.Mission != "" {
				mission = def.Mission
			}
			displayName = def.DisplayName
			break
		}
	}
	return func() tea.Msg {

		// Call the dash spawn_agent tool
		args := map[string]any{
			"agent_key": agentKey,
			"name":      displayName,
			"mission":   mission,
			"context_hints": []string{
				"cockpit",
				agentKey,
				"recent changes",
			},
		}

		result, err := handleToolCall(context.Background(), m.d, "spawn_agent", args)
		if err != nil {
			return spawnAgentResultMsg{err: err}
		}

		// Parse the result to get session ID
		if resultMap, ok := result.(map[string]any); ok {
			sessionID := fmt.Sprintf("%v", resultMap["session_id"])
			agentID := fmt.Sprintf("%v", resultMap["id"])
			if agentID == "" || agentID == "<nil>" {
				agentID = agentKey + "-" + time.Now().Format("20060102-150405")
			}
			return spawnAgentResultMsg{
				info: &agentSpawnInfo{
					ID:        agentID,
					AgentKey:  agentKey,
					Name:      displayName,
					Mission:   mission,
					SessionID: sessionID,
				},
			}
		}

		return spawnAgentResultMsg{err: fmt.Errorf("unexpected spawn result format")}
	}
}

// updateAgentStatus syncs agent status with the graph
func (m *model) updateAgentStatus(agentID, sessionID string, status agentStatus) tea.Cmd {
	return func() tea.Msg {
		statusStr := status.String()
		args := map[string]any{
			"agent_session_id": sessionID,
			"status":           statusStr,
		}

		if status == agentActive {
			args["progress"] = "Agent aktiverad och redo"
		}

		_, err := handleToolCall(context.Background(), m.d, "update_agent", args)
		if err != nil {
			// Log but don't fail UI
			return agentStatusUpdateMsg{
				agentID: agentID,
				status:  status,
				message: fmt.Sprintf("Status update failed: %v", err),
			}
		}

		return agentStatusUpdateMsg{
			agentID: agentID,
			status:  status,
			message: fmt.Sprintf("Status: %s", statusStr),
		}
	}
}

// Enhanced agent tab rendering with live status
func (at *agentTab) renderStatus() string {
	var parts []string

	// Status icon and text
	statusStyle := tabAgentActive
	switch at.status {
	case agentActive:
		statusStyle = statusStyle.Foreground(colorGreen)
	case agentWaiting:
		statusStyle = statusStyle.Foreground(colorYellow)
	case agentFailed:
		statusStyle = statusStyle.Foreground(colorRed)
	case agentCompleted:
		statusStyle = statusStyle.Foreground(colorBlue)
	}

	parts = append(parts, statusStyle.Render(fmt.Sprintf("%s %s", at.status.Icon(), at.status.String())))

	// Token usage if available
	if at.meter.limit > 0 {
		usage := float64(at.meter.usedTokens()) / float64(at.meter.limit) * 100
		parts = append(parts, fmt.Sprintf("%.0f%% tokens", usage))
	}

	// Time since spawn
	elapsed := time.Since(at.spawnedAt)
	if elapsed < time.Minute {
		parts = append(parts, fmt.Sprintf("%ds", int(elapsed.Seconds())))
	} else {
		parts = append(parts, fmt.Sprintf("%dm", int(elapsed.Minutes())))
	}

	return strings.Join(parts, " · ")
}

// handleToolCall executes a tool via Dash and returns the result.
// This is used by agent spawning and status updates.
func handleToolCall(ctx context.Context, d *dash.Dash, toolName string, args map[string]any) (any, error) {
	if d == nil {
		return nil, fmt.Errorf("Dash client not available")
	}

	result := d.RunTool(ctx, toolName, args, &dash.ToolOpts{
		CallerID: "cockpit",
	})
	if result.Success {
		return result.Data, nil
	}
	return nil, fmt.Errorf("%s", result.Error)
}