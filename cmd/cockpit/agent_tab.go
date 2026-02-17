package main

import (
	"fmt"
	"strings"
	"time"

	"dash"

	"github.com/charmbracelet/lipgloss"
)

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
	controller      string // "human", "llm", "idle"
	chat            *chatModel
	mission         string
	sessionID       string
	spawnedAt       time.Time
	spawnedBy       string
	meter           tokenMeter
	pendingMessage  string // saved input while waiting for lazy spawn
	activeWorkOrder *activeWO // current work order assigned to this agent
	answeringQuery  *pendingQuery // non-nil when answering a cross-agent query
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

// controllerIcon returns a presence icon based on controller state and focus.
func controllerIcon(controller string, isFocused, isStreaming bool) string {
	switch controller {
	case "human":
		return "👤"
	case "llm":
		if isStreaming {
			return "🤖⠋"
		}
		return "🤖"
	default: // "idle" or ""
		if isFocused {
			return "👁"
		}
		return "○"
	}
}

// controllerStyle returns the appropriate lipgloss style for a tab.
func controllerStyle(controller string, isFocused bool) lipgloss.Style {
	switch controller {
	case "human":
		if isFocused {
			return tabHumanFocused
		}
		return tabHumanUnfocused
	case "llm":
		if isFocused {
			return tabLLMFocused
		}
		return tabLLMUnfocused
	default: // "idle"
		if isFocused {
			return tabIdleFocused
		}
		return tabAgentInactive
	}
}

// tabBar renders the agent tab bar with presence and focus indicators.
func (am *agentManager) tabBar(width int) string {
	if len(am.tabs) == 0 {
		return ""
	}

	var parts []string
	for i, t := range am.tabs {
		isFocused := i == am.activeIdx
		icon := controllerIcon(t.controller, isFocused, t.chat != nil && t.chat.streaming)
		style := controllerStyle(t.controller, isFocused)

		// Name: full for focused, short for unfocused
		name := t.displayName
		if !isFocused {
			// Extract short key from agent key (e.g. "cockpit-backend" → "back")
			name = t.agentKey
			if idx := strings.LastIndex(name, "-"); idx >= 0 && idx+1 < len(name) {
				name = name[idx+1:]
			}
			if len(name) > 5 {
				name = name[:5]
			}
		}

		// Query indicator
		querySuffix := ""
		if t.answeringQuery != nil {
			caller := t.answeringQuery.callerKey
			if len(caller) > 6 {
				caller = caller[:6]
			}
			querySuffix = fmt.Sprintf(" ?←%s", caller)
		}

		// WO suffix for focused or active tabs
		woSuffix := ""
		if t.activeWorkOrder != nil {
			woIcon := woStatusIconStr(t.activeWorkOrder.Status)
			woName := t.activeWorkOrder.Name
			if len(woName) > 12 {
				woName = woName[:12]
			}
			woSuffix = fmt.Sprintf(" %s%s", woIcon, woName)
		}

		// Show full name for active agents even when not focused
		if !isFocused && (t.status != agentIdle || t.activeWorkOrder != nil || t.controller == "human" || t.controller == "llm") {
			name = t.displayName
		}

		label := fmt.Sprintf(" %s %s%s%s ", icon, name, querySuffix, woSuffix)
		_ = i
		parts = append(parts, style.Render(label))
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
