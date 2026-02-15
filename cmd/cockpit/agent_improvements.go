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

// Agent missions based on their type
var agentMissions = map[string]string{
	"cockpit-backend": `Du är en backend-specialist för Dash cockpit.
Fokusera på:
- Go-kod och arkitektur i /dash/cmd/cockpit
- Integration med Dash core APIs
- Performance och stabilitet
- Databasinteraktioner`,

	"cockpit-frontend": `Du är en frontend-specialist för Dash cockpit TUI.
Fokusera på:
- Bubble Tea komponenter och rendering
- Användarupplevelse och interaktivitet
- Tangentbordsnavigering
- Visuell feedback och animationer`,

	"systemprompt-agent": `Du är en prompt engineering specialist.
Fokusera på:
- Optimera system prompts för olika agenter
- Skapa tydliga och effektiva instruktioner
- Anpassa prompts för specifika uppgifter
- Testa och iterera på prompt-förbättringar`,

	"database-agent": `Du är en databasspecialist för Dash.
Fokusera på:
- PostgreSQL schema och migrations
- Query-optimering
- Index-strategier
- Data integrity och constraints`,

	"system-agent": `Du är en systemarkitekt för Dash.
Fokusera på:
- Övergripande systemdesign
- API-kontrakt och interfaces
- Modularitet och separation of concerns
- Performance och skalbarhet`,

	"shift-agent": `Du är en handoff-specialist.
Din uppgift är att:
- Sammanfatta pågående arbete
- Identifiera nästa steg
- Förbereda kontext för nästa agent/session
- Dokumentera viktiga beslut och insikter`,
}

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
	return func() tea.Msg {
		// Get mission for this agent type
		mission, ok := agentMissions[agentKey]
		if !ok {
			mission = fmt.Sprintf("Du är en %s agent. Analysera koden och föreslå förbättringar.", agentKey)
		}

		// Find a good display name
		displayName := agentKey
		for _, a := range AvailableAgents {
			if a.Key == agentKey {
				displayName = a.DisplayName
				break
			}
		}

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

// Quick agent switching with number keys
func (m *model) handleAgentQuickSwitch(key string) (tea.Model, tea.Cmd) {
	if m.state != viewAgent {
		return m, nil
	}

	// Alt+1 through Alt+9 to switch between spawned agents
	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		idx := int(key[0] - '1')
		if idx < m.agents.count() {
			m.agents.activate(idx)
			// Update status to active
			if tab := m.agents.active(); tab != nil {
				tab.status = agentActive
				return m, m.updateAgentStatus(tab.id, tab.sessionID, agentActive)
			}
		}
	}
	return m, nil
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