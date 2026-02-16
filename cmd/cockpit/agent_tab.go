package main

import (
	"fmt"
	"strings"
	"time"

	"dash"

	"github.com/charmbracelet/lipgloss"
)

// AvailableAgents - agents that can be spawned via 1-7 keys
var AvailableAgents = []struct {
	Key         string
	DisplayName string
	Description string
}{
	{Key: "orchestrator", DisplayName: "🎯 Orchestrator", Description: "Pipeline manager"},
	{Key: "cockpit-backend", DisplayName: "🖥️ Backend", Description: "Go/PostgreSQL"},
	{Key: "cockpit-frontend", DisplayName: "🎨 Frontend", Description: "TypeScript/React"},
	{Key: "systemprompt-agent", DisplayName: "📝 Prompts", Description: "Prompt engineering"},
	{Key: "database-agent", DisplayName: "🗄️ DB", Description: "Database ops"},
	{Key: "system-agent", DisplayName: "⚙️ System", Description: "Architecture"},
	{Key: "shift-agent", DisplayName: "🔄 Shift", Description: "Handoff"},
}

type agentStatus int

const (
	agentIdle agentStatus = iota
	agentSpawned
	agentActive
	agentWaiting
	agentCompleted
	agentFailed
)

func (s agentStatus) String() string {
	switch s {
	case agentIdle:
		return "idle"
	case agentSpawned:
		return "spawned"
	case agentActive:
		return "active"
	case agentWaiting:
		return "waiting"
	case agentCompleted:
		return "completed"
	case agentFailed:
		return "failed"
	}
	return "unknown"
}

func (s agentStatus) Icon() string {
	switch s {
	case agentIdle:
		return "\u25cb" // ○
	case agentSpawned:
		return "\u25cb" // ○
	case agentActive:
		return "\u25b6" // ▶
	case agentWaiting:
		return "\u275a" // ❚
	case agentCompleted:
		return "\u25cf" // ●
	case agentFailed:
		return "\u25cf" // ●
	}
	return "\u25cb"
}

type agentTab struct {
	id              string
	displayName     string
	agentKey        string
	status          agentStatus
	chat            *chatModel
	mission         string
	sessionID       string
	spawnedAt       time.Time
	spawnedBy       string
	meter           tokenMeter
	pendingMessage  string // saved input while waiting for lazy spawn
	activeWorkOrder *activeWO // current work order assigned to this agent
}

// activeWO holds the essential fields of an active work order for display.
type activeWO struct {
	ID     string
	Name   string
	Status string
}

type agentManager struct {
	tabs      []*agentTab
	activeIdx int // -1 = no agent active
	nextID    int
}

func newAgentManager() *agentManager {
	return &agentManager{activeIdx: -1}
}

func (am *agentManager) active() *agentTab {
	if am.activeIdx < 0 || am.activeIdx >= len(am.tabs) {
		return nil
	}
	return am.tabs[am.activeIdx]
}

func (am *agentManager) spawn(displayName, agentKey, mission, sessionID, spawnedBy string, chat *chatModel) *agentTab {
	am.nextID++
	tab := &agentTab{
		id:          fmt.Sprintf("agent-%d", am.nextID),
		displayName: displayName,
		agentKey:    agentKey,
		status:      agentSpawned,
		chat:        chat,
		mission:     mission,
		sessionID:   sessionID,
		spawnedAt:   time.Now(),
		spawnedBy:   spawnedBy,
		meter:       newTokenMeter(128000), // updated from API on first stream
	}
	am.tabs = append(am.tabs, tab)
	return tab
}

func (am *agentManager) activate(idx int) {
	if idx >= 0 && idx < len(am.tabs) {
		am.activeIdx = idx
	}
}

func (am *agentManager) activateByID(id string) bool {
	for i, t := range am.tabs {
		if t.id == id {
			am.activeIdx = i
			return true
		}
	}
	return false
}

func (am *agentManager) deactivate() {
	am.activeIdx = -1
}

func (am *agentManager) cycleNext() int {
	if len(am.tabs) == 0 {
		return -1
	}
	am.activeIdx = (am.activeIdx + 1) % len(am.tabs)
	return am.activeIdx
}

func (am *agentManager) cyclePrev() int {
	if len(am.tabs) == 0 {
		return -1
	}
	if am.activeIdx <= 0 {
		am.activeIdx = len(am.tabs) - 1
	} else {
		am.activeIdx--
	}
	return am.activeIdx
}

func (am *agentManager) count() int {
	return len(am.tabs)
}

func (am *agentManager) removeTab(id string) {
	for i, t := range am.tabs {
		if t.id == id {
			am.tabs = append(am.tabs[:i], am.tabs[i+1:]...)
			if am.activeIdx >= len(am.tabs) {
				am.activeIdx = len(am.tabs) - 1
			}
			return
		}
	}
}

// tabBar renders the agent tab bar.
func (am *agentManager) tabBar(width int) string {
	if len(am.tabs) == 0 {
		return ""
	}

	var parts []string
	for i, t := range am.tabs {
		icon := t.status.Icon()

		// Compact label: "1○BE" for inactive, "1▶Backend [active]" for active
		shortName := t.agentKey
		// Extract short key from agent key (e.g. "cockpit-backend" → "back")
		if idx := strings.LastIndex(shortName, "-"); idx >= 0 && idx+1 < len(shortName) {
			shortName = shortName[idx+1:]
		}
		if len(shortName) > 5 {
			shortName = shortName[:5]
		}

		// WO suffix: show active work order name in tab
		woSuffix := ""
		if t.activeWorkOrder != nil {
			woIcon := woStatusIconStr(t.activeWorkOrder.Status)
			woName := t.activeWorkOrder.Name
			if len(woName) > 12 {
				woName = woName[:12]
			}
			woSuffix = fmt.Sprintf(" %s%s", woIcon, woName)
		}

		if i == am.activeIdx {
			label := fmt.Sprintf(" %d%s %s%s ", i+1, icon, t.displayName, woSuffix)
			parts = append(parts, tabAgentActive.Render(label))
		} else if t.status != agentIdle || t.activeWorkOrder != nil {
			// Spawned agents or agents with WO keep full name
			label := fmt.Sprintf(" %d%s %s%s ", i+1, icon, t.displayName, woSuffix)
			parts = append(parts, tabAgentInactive.Render(label))
		} else {
			label := fmt.Sprintf(" %d%s %s ", i+1, icon, shortName)
			parts = append(parts, tabAgentInactive.Render(label))
		}
	}

	return strings.Join(parts, "")
}

// woStatusIconStr returns a status icon string for a work order status.
func woStatusIconStr(status string) string {
	switch status {
	case "created":
		return "\u25cb" // ○
	case "assigned", "mutating":
		return "\u25b6" // ▶
	case "build_passed":
		return "\u2713" // ✓
	case "build_failed":
		return "\u2717" // ✗
	case "synthesis_pending":
		return "?"
	case "merge_pending":
		return "!"
	case "merged":
		return "\u25cf" // ●
	default:
		return "\u25cb" // ○
	}
}

// updateWorkOrders matches active work orders to agent tabs.
func (am *agentManager) updateWorkOrders(workOrders []*dash.WorkOrder) {
	// Clear all active WOs first
	for _, t := range am.tabs {
		t.activeWorkOrder = nil
	}
	if len(workOrders) == 0 {
		return
	}
	// Match work orders to tabs by agent_key
	for _, wo := range workOrders {
		if wo.AgentKey == "" {
			continue
		}
		for _, t := range am.tabs {
			if t.agentKey == wo.AgentKey {
				t.activeWorkOrder = &activeWO{
					ID:     wo.Node.ID.String(),
					Name:   wo.Node.Name,
					Status: string(wo.Status),
				}
				break
			}
		}
	}
}

// AgentPicker returns a styled overlay showing available agents.
// This is shown when user presses Shift+Tab.
func AgentPicker(width int) string {
	// Create a nice box with the agent options
	var lines []string
	
	// Title
	titleStyle := lipgloss.NewStyle().
		Foreground(cCyan).
		Bold(true)
	lines = append(lines, titleStyle.Render("  ▶ SPAWN AGENT"))
	lines = append(lines, "")
	
	// Agent options
	for i, a := range AvailableAgents {
		numStyle := lipgloss.NewStyle().
			Foreground(cPrimary).
			Bold(true)
		nameStyle := lipgloss.NewStyle().
			Foreground(cText)
		descStyle := lipgloss.NewStyle().
			Foreground(cGray)
		
		num := numStyle.Render(fmt.Sprintf("  [%d]", i+1))
		name := nameStyle.Render(fmt.Sprintf(" %s ", a.DisplayName))
		desc := descStyle.Render(a.Description)
		
		lines = append(lines, num+name+desc)
	}
	
	// Help text at bottom
	lines = append(lines, "")
	helpStyle := lipgloss.NewStyle().
		Foreground(cGray).
		Italic(true)
	lines = append(lines, helpStyle.Render("  Press 1-7 to spawn · esc to close"))
	
	// Join with newlines and wrap in a bordered box
	content := strings.Join(lines, "\n")
	
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cPrimary).
		Padding(0, 2).
		Width(min(width-4, 50))
	
	return boxStyle.Render(content)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
