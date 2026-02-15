package main

import (
	"fmt"
	"strings"
	"time"

	"dash"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type overlayItem struct {
	kind  string // "plan", "task"
	name  string
	label string
}

type overlayModel struct {
	focusCol int        // 0=WORK, 1=INTEL, 2=SYSTEM
	cursor   [3]int     // cursor per column
	items    [3][]overlayItem
	action   string     // set by Enter: "task:name", "plan:name", "refresh"
}

func newOverlayModel() overlayModel {
	return overlayModel{}
}

// rebuildItems updates selectable items for navigation.
func (o *overlayModel) rebuildItems(plans []*dash.PlanState, tasks []dash.TaskWithDeps) {
	o.items[0] = nil
	for _, p := range plans {
		stage := string(p.Stage)
		gate := ""
		if p.Gate != nil {
			gate = " " + p.Gate.Decision
		}
		o.items[0] = append(o.items[0], overlayItem{
			kind:  "plan",
			name:  p.Node.Name,
			label: fmt.Sprintf("%s [%s]%s", p.Node.Name, stage, gate),
		})
	}
	for _, t := range tasks {
		status := t.Status
		if t.IsBlocked {
			status = "blocked"
		}
		o.items[0] = append(o.items[0], overlayItem{
			kind:  "task",
			name:  t.Node.Name,
			label: fmt.Sprintf("%s [%s]", t.Node.Name, status),
		})
	}
	// Clamp cursors
	for i := range o.cursor {
		max := len(o.items[i])
		if max == 0 {
			o.cursor[i] = 0
		} else if o.cursor[i] >= max {
			o.cursor[i] = max - 1
		}
	}
}

func (o *overlayModel) handleKey(msg tea.KeyMsg) tea.Cmd {
	action := resolveDashKey(msg)
	switch action {
	case ActionDashColLeft:
		o.focusCol = 0
	case ActionDashColRight:
		o.focusCol = 2
	case ActionDashColMiddle:
		o.focusCol = 1
	case ActionDashDown:
		max := len(o.items[o.focusCol])
		if o.focusCol == 1 {
			max = 10 // intel items are view-only, allow some scrolling
		} else if o.focusCol == 2 {
			max = 10
		}
		if o.cursor[o.focusCol] < max-1 {
			o.cursor[o.focusCol]++
		}
	case ActionDashUp:
		if o.cursor[o.focusCol] > 0 {
			o.cursor[o.focusCol]--
		}
	case ActionDashTop:
		o.cursor[o.focusCol] = 0
	case ActionDashBottom:
		max := len(o.items[o.focusCol])
		if max > 0 {
			o.cursor[o.focusCol] = max - 1
		}
	case ActionDashSelect:
		items := o.items[o.focusCol]
		cur := o.cursor[o.focusCol]
		if cur < len(items) {
			item := items[cur]
			o.action = item.kind + ":" + item.name
		}
		return nil
	case ActionDashRefresh:
		o.action = "refresh"
		return nil
	case ActionDashModelNext:
		o.action = "model-next"
		return nil
	case ActionDashModelPrev:
		o.action = "model-prev"
		return nil
	case ActionDashAgentNext:
		o.action = "agent-next"
		return nil
	case ActionDashAgentPrev:
		o.action = "agent-prev"
		return nil
	case ActionDashSpawn:
		o.action = "spawn"
		return nil
	case ActionDashToolLimit:
		o.action = "tool-limit"
		return nil
	case ActionDashClearContinue:
		o.action = "clear-continue"
		return nil
	}
	return nil
}

// View renders the 3-column dashboard overlay with a status bar.
func (o *overlayModel) View(width, height int, tasks []dash.TaskWithDeps, proposals []dash.Proposal, plans []*dash.PlanState, sessions []dash.ActivitySummary, services []serviceStatus, ws *dash.WorkingSet, tree *dash.HierarchyTree, client *chatClient, agents *agentManager, spawnInput bool, spawnBuf []rune, maxToolIter int, snapshot *dash.AgentContextSnapshot) string {
	if width < 40 {
		width = 80
	}

	// Status bar at top
	bar := o.renderStatusBar(width, client, agents, spawnInput, spawnBuf, maxToolIter)
	barLines := strings.Count(bar, "\n") + 1
	colHeight := height - barLines - 1

	// Responsive layout
	var cols int
	switch {
	case width >= 120:
		cols = 3
	case width >= 80:
		cols = 2
	default:
		cols = 1
	}

	var col1, col2, col3 string
	if snapshot != nil {
		col1 = o.renderAgentWorkColumn(width/cols-4, colHeight, snapshot)
		col2 = o.renderAgentIntelColumn(width/cols-4, colHeight, snapshot)
		col3 = o.renderAgentSystemColumn(width/cols-4, colHeight, snapshot)
	} else {
		col1 = o.renderWorkColumn(width/cols-4, colHeight, plans, tasks)
		col2 = o.renderIntelColumn(width/cols-4, colHeight, proposals, ws)
		col3 = o.renderSystemColumn(width/cols-4, colHeight, services, sessions, ws)
	}

	// Apply panel styles
	style0, style1, style2 := panelNormal, panelNormal, panelNormal
	if o.focusCol == 0 {
		style0 = panelFocused
	}
	if o.focusCol == 1 {
		style1 = panelFocused
	}
	if o.focusCol == 2 {
		style2 = panelFocused
	}

	colW := width/cols - 4
	if colW < 20 {
		colW = 20
	}

	var columns string
	switch cols {
	case 3:
		columns = lipgloss.JoinHorizontal(lipgloss.Top,
			style0.Width(colW).Height(colHeight-2).Render(col1),
			style1.Width(colW).Height(colHeight-2).Render(col2),
			style2.Width(colW).Height(colHeight-2).Render(col3),
		)
	case 2:
		rightStack := col2 + "\n" + sectionHeader.Render("────") + "\n" + col3
		columns = lipgloss.JoinHorizontal(lipgloss.Top,
			style0.Width(colW).Height(colHeight-2).Render(col1),
			style1.Width(colW).Height(colHeight-2).Render(rightStack),
		)
	default:
		columns = style0.Width(width-4).Height(colHeight-2).Render(col1 + "\n\n" + col2 + "\n\n" + col3)
	}

	return bar + "\n" + columns
}

// --- Column renderers ---

func (o *overlayModel) renderWorkColumn(w, h int, plans []*dash.PlanState, tasks []dash.TaskWithDeps) string {
	var b strings.Builder
	b.WriteString(sectionHeader.Render("WORK"))
	b.WriteString("\n")
	b.WriteString(sectionDivider.Render(strings.Repeat("\u2500", min(w, 30))))
	b.WriteString("\n")

	// Plans
	if len(plans) > 0 {
		b.WriteString(textPrimary.Render(fmt.Sprintf("PLANS (%d)", len(plans))))
		b.WriteString("\n")
		for i, p := range plans {
			stage := string(p.Stage)
			gate := ""
			if p.Gate != nil {
				gate = " " + p.Gate.Decision
			}

			// Steps progress
			done := 0
			for _, s := range p.Steps {
				if s.Done {
					done++
				}
			}
			progress := ""
			if len(p.Steps) > 0 {
				progress = fmt.Sprintf(" %d/%d", done, len(p.Steps))
			}

			line := fmt.Sprintf("  %s [%s]%s%s", truncate(p.Node.Name, w-20), stage, gate, progress)
			idx := i // index in items[0]
			if o.focusCol == 0 && o.cursor[0] == idx {
				b.WriteString(cursorActive.Render("> ") + textPrimary.Render(line))
			} else {
				b.WriteString("  " + line)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// Tasks: in_progress first, then pending, blocked last
	inProgress, pending, blocked := categorizeTasks(tasks)

	if len(inProgress) > 0 {
		b.WriteString(statusActive.Render(fmt.Sprintf("IN PROGRESS (%d)", len(inProgress))))
		b.WriteString("\n")
		for _, t := range inProgress {
			idx := o.findTaskIndex(t.Node.Name)
			intent := ""
			if t.Intent != "" {
				intent = textDim.Render(" \u2192 " + t.Intent)
			}
			line := fmt.Sprintf("  %s", truncate(t.Node.Name, w-10))
			if o.focusCol == 0 && o.cursor[0] == idx {
				b.WriteString(cursorActive.Render("> ") + statusActive.Render(line) + intent)
			} else {
				b.WriteString("  " + statusActive.Render(line) + intent)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if len(pending) > 0 {
		b.WriteString(statusPending.Render(fmt.Sprintf("QUEUED (%d)", len(pending))))
		b.WriteString("\n")
		for _, t := range pending {
			idx := o.findTaskIndex(t.Node.Name)
			line := fmt.Sprintf("  %s", truncate(t.Node.Name, w-10))
			if o.focusCol == 0 && o.cursor[0] == idx {
				b.WriteString(cursorActive.Render("> ") + line)
			} else {
				b.WriteString("  " + line)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if len(blocked) > 0 {
		b.WriteString(statusBlocked.Render(fmt.Sprintf("BLOCKED (%d)", len(blocked))))
		b.WriteString("\n")
		for _, t := range blocked {
			idx := o.findTaskIndex(t.Node.Name)
			blockedBy := ""
			if len(t.BlockedBy) > 0 {
				blockedBy = textDim.Render(" \u2190 " + strings.Join(t.BlockedBy, ", "))
			}
			line := fmt.Sprintf("  %s", truncate(t.Node.Name, w-10))
			if o.focusCol == 0 && o.cursor[0] == idx {
				b.WriteString(cursorActive.Render("> ") + statusBlocked.Render(line) + blockedBy)
			} else {
				b.WriteString("  " + statusBlocked.Render(line) + blockedBy)
			}
			b.WriteString("\n")
		}
	}

	if len(plans) == 0 && len(tasks) == 0 {
		b.WriteString(textDim.Render("  no work items"))
	}

	return b.String()
}

func (o *overlayModel) renderIntelColumn(w, h int, proposals []dash.Proposal, ws *dash.WorkingSet) string {
	var b strings.Builder
	b.WriteString(sectionHeader.Render("INTELLIGENCE"))
	b.WriteString("\n")
	b.WriteString(sectionDivider.Render(strings.Repeat("\u2500", min(w, 30))))
	b.WriteString("\n")

	// Proposals
	if len(proposals) > 0 {
		b.WriteString(textPrimary.Render(fmt.Sprintf("PROPOSALS (%d)", len(proposals))))
		b.WriteString("\n")
		for _, p := range proposals {
			bar := renderAlignmentBar(p.Alignment, 10)
			b.WriteString(fmt.Sprintf("  %s %d%% %s\n", bar, p.Alignment, truncate(p.Name, w-20)))
		}
		b.WriteString("\n")
	}

	// Insights
	if ws != nil && len(ws.RecentInsights) > 0 {
		b.WriteString(textPrimary.Render("INSIGHTS"))
		b.WriteString("\n")
		for i, ins := range ws.RecentInsights {
			if i >= 5 {
				b.WriteString(textDim.Render(fmt.Sprintf("  +%d more...", len(ws.RecentInsights)-5)))
				b.WriteString("\n")
				break
			}
			text := ins.Name
			if data := nodeData(ins); data != nil {
				if t, ok := data["text"].(string); ok && len(t) > 0 {
					text = t
				}
			}
			b.WriteString("  " + textCyan.Render("\u2192") + " " + truncate(text, w-4) + "\n")
		}
		b.WriteString("\n")
	}

	// Decisions
	if ws != nil && len(ws.RecentDecisions) > 0 {
		b.WriteString(textPrimary.Render("DECISIONS"))
		b.WriteString("\n")
		for i, dec := range ws.RecentDecisions {
			if i >= 3 {
				b.WriteString(textDim.Render(fmt.Sprintf("  +%d more...", len(ws.RecentDecisions)-3)))
				b.WriteString("\n")
				break
			}
			text := dec.Name
			if data := nodeData(dec); data != nil {
				if t, ok := data["text"].(string); ok && len(t) > 0 {
					text = t
				}
			}
			b.WriteString("  " + textCyan.Render("\u2192") + " " + truncate(text, w-4) + "\n")
		}
	}

	if len(proposals) == 0 && (ws == nil || (len(ws.RecentInsights) == 0 && len(ws.RecentDecisions) == 0)) {
		b.WriteString(textDim.Render("  no intelligence data"))
	}

	return b.String()
}

func (o *overlayModel) renderSystemColumn(w, h int, services []serviceStatus, sessions []dash.ActivitySummary, ws *dash.WorkingSet) string {
	var b strings.Builder
	b.WriteString(sectionHeader.Render("SYSTEM"))
	b.WriteString("\n")
	b.WriteString(sectionDivider.Render(strings.Repeat("\u2500", min(w, 30))))
	b.WriteString("\n")

	// Services
	b.WriteString(textPrimary.Render("SERVICES"))
	b.WriteString("\n")
	for _, s := range services {
		indicator := textSuccess.Render("\u25cf")
		pidInfo := ""
		if !s.Running {
			indicator = textAlert.Render("\u25cb")
		} else if s.PID != "" {
			pidInfo = textDim.Render(" pid:" + s.PID)
		}
		b.WriteString(fmt.Sprintf("  %s %s%s\n", indicator, s.Name, pidInfo))
	}
	b.WriteString("\n")

	// Sessions
	if len(sessions) > 0 {
		b.WriteString(textPrimary.Render(fmt.Sprintf("SESSIONS (%d)", len(sessions))))
		b.WriteString("\n")
		for i, s := range sessions {
			if i >= 5 {
				b.WriteString(textDim.Render(fmt.Sprintf("  +%d more...", len(sessions)-5)))
				b.WriteString("\n")
				break
			}
			statusIcon := textSuccess.Render("\u25cf")
			if s.Status == "ended" {
				statusIcon = textDim.Render("\u25cb")
			}
			dur := formatDuration(s.StartedAt, s.EndedAt)
			sid := s.SessionID
			if len(sid) > 8 {
				sid = sid[:8]
			}
			b.WriteString(fmt.Sprintf("  %s %s %s %dR %dW\n",
				statusIcon, sid, dur, s.FilesRead, s.FilesWrote))
		}
		b.WriteString("\n")
	}

	// Promote candidates
	if ws != nil && len(ws.PromotionCandidates) > 0 {
		b.WriteString(textWarning.Render("PROMOTE?"))
		b.WriteString("\n")
		for _, pc := range ws.PromotionCandidates {
			b.WriteString(fmt.Sprintf("  %s\n", truncate(pc.Name, w-4)))
		}
		b.WriteString("\n")
	}

	// Constraints
	if ws != nil && len(ws.Constraints) > 0 {
		b.WriteString(textPrimary.Render("CONSTRAINTS"))
		b.WriteString("\n")
		for _, c := range ws.Constraints {
			b.WriteString("  " + truncate(c.Name, w-4) + "\n")
		}
	}

	return b.String()
}

// --- Status bar ---

func (o *overlayModel) renderStatusBar(w int, client *chatClient, agents *agentManager, spawnInput bool, spawnBuf []rune, maxToolIter int) string {
	var b strings.Builder

	// Model section
	shortModel := "?"
	if client != nil {
		shortModel = client.model
		if idx := strings.LastIndex(shortModel, "/"); idx >= 0 {
			shortModel = shortModel[idx+1:]
		}
	}
	b.WriteString(sectionHeader.Render("MODEL"))
	b.WriteString(" ")
	b.WriteString(textPrimary.Render("▸ " + shortModel))
	b.WriteString(textDim.Render(" [å/ä]"))

	// Agents section
	b.WriteString("    ")
	b.WriteString(sectionHeader.Render("AGENTS"))
	if agents != nil && agents.count() > 0 {
		for i, tab := range agents.tabs {
			icon := "  "
			if i == agents.activeIdx {
				icon = " ●"
			}
			b.WriteString(textDim.Render(icon) + " " + textPrimary.Render(tab.displayName))
		}
		b.WriteString(textDim.Render(" [p/ö]"))
	}
	b.WriteString(textDim.Render(" [n]new"))

	// Tools section
	b.WriteString("    ")
	b.WriteString(sectionHeader.Render("TOOLS"))
	b.WriteString(" ")
	toolStr := fmt.Sprintf("%d", maxToolIter)
	if maxToolIter == 0 {
		toolStr = "∞"
	}
	b.WriteString(textPrimary.Render("▸ " + toolStr + " iter"))
	b.WriteString(textDim.Render(" [t]"))

	// Context rotation
	b.WriteString("    ")
	b.WriteString(textDim.Render("[c] clear+continue"))

	line1 := b.String()

	// Spawn input line
	if spawnInput {
		return line1 + "\n" + hudLabel.Render("Agent key: ") + textPrimary.Render(string(spawnBuf)) + chatCursor.Render("|")
	}

	return line1
}

func (o *overlayModel) renderAgentWorkColumn(w, h int, s *dash.AgentContextSnapshot) string {
	var b strings.Builder
	b.WriteString(sectionHeader.Render("AGENT"))
	b.WriteString("\n")
	b.WriteString(sectionDivider.Render(strings.Repeat("\u2500", min(w, 30))))
	b.WriteString("\n")

	// Mission
	b.WriteString(textPrimary.Render("MISSION"))
	b.WriteString("\n")
	if s.Mission != "" {
		b.WriteString("  " + truncate(s.Mission, w-4) + "\n")
	} else {
		b.WriteString("  " + textDim.Render("(none)") + "\n")
	}
	b.WriteString("\n")

	// Role
	b.WriteString(textPrimary.Render("YOUR ROLE"))
	b.WriteString("\n")
	if s.Role != "" {
		b.WriteString("  " + textCyan.Render(truncate(s.Role, w-4)) + "\n")
	} else {
		b.WriteString("  " + textDim.Render(s.AgentKey) + "\n")
	}
	b.WriteString("\n")

	// Situation
	b.WriteString(textPrimary.Render("SITUATION"))
	b.WriteString("\n")
	if s.Situation != "" {
		b.WriteString("  " + truncate(s.Situation, w-4) + "\n")
	} else {
		b.WriteString("  " + textDim.Render("(no context frame)") + "\n")
	}
	b.WriteString("\n")

	// Next action
	if s.NextAction != "" {
		b.WriteString(statusActive.Render("NEXT ACTION"))
		b.WriteString("\n")
		b.WriteString("  " + truncate(s.NextAction, w-4) + "\n")
		b.WriteString("\n")
	}

	// Tasks categorized
	var active, pending, blocked []dash.TaskSummary
	for _, t := range s.Tasks {
		if len(t.BlockedBy) > 0 {
			blocked = append(blocked, t)
		} else if t.Status == "in_progress" || t.Status == "active" {
			active = append(active, t)
		} else {
			pending = append(pending, t)
		}
	}

	if len(active) > 0 {
		b.WriteString(statusActive.Render(fmt.Sprintf("ACTIVE (%d)", len(active))))
		b.WriteString("\n")
		for _, t := range active {
			intent := ""
			if t.Intent != "" {
				intent = textDim.Render(" \u2192 " + t.Intent)
			}
			b.WriteString("  " + statusActive.Render(truncate(t.Name, w-10)) + intent + "\n")
		}
		b.WriteString("\n")
	}

	if len(pending) > 0 {
		b.WriteString(statusPending.Render(fmt.Sprintf("QUEUED (%d)", len(pending))))
		b.WriteString("\n")
		for _, t := range pending {
			b.WriteString("  " + truncate(t.Name, w-10) + "\n")
		}
		b.WriteString("\n")
	}

	if len(blocked) > 0 {
		b.WriteString(statusBlocked.Render(fmt.Sprintf("BLOCKED (%d)", len(blocked))))
		b.WriteString("\n")
		for _, t := range blocked {
			blockedBy := ""
			if len(t.BlockedBy) > 0 {
				blockedBy = textDim.Render(" \u2190 " + strings.Join(t.BlockedBy, ", "))
			}
			b.WriteString("  " + statusBlocked.Render(truncate(t.Name, w-10)) + blockedBy + "\n")
		}
	}

	return b.String()
}

func (o *overlayModel) renderAgentIntelColumn(w, h int, s *dash.AgentContextSnapshot) string {
	var b strings.Builder
	b.WriteString(sectionHeader.Render("INTELLIGENCE"))
	b.WriteString("\n")
	b.WriteString(sectionDivider.Render(strings.Repeat("\u2500", min(w, 30))))
	b.WriteString("\n")

	// Decisions
	if len(s.Decisions) > 0 {
		b.WriteString(textPrimary.Render(fmt.Sprintf("RECENT DECISIONS (%d)", len(s.Decisions))))
		b.WriteString("\n")
		for i, d := range s.Decisions {
			if i >= 5 {
				b.WriteString(textDim.Render(fmt.Sprintf("  +%d more...", len(s.Decisions)-5)))
				b.WriteString("\n")
				break
			}
			age := time.Since(d.CreatedAt)
			ageStr := ""
			switch {
			case age < time.Minute:
				ageStr = "just now"
			case age < time.Hour:
				ageStr = fmt.Sprintf("%dm ago", int(age.Minutes()))
			default:
				ageStr = fmt.Sprintf("%dh ago", int(age.Hours()))
			}
			b.WriteString("  " + textCyan.Render("\u2192") + " " + truncate(d.Text, w-15) + " " + textDim.Render(ageStr) + "\n")
		}
		b.WriteString("\n")
	}

	if len(s.Decisions) == 0 {
		b.WriteString(textDim.Render("  no intelligence data"))
	}

	return b.String()
}

func (o *overlayModel) renderAgentSystemColumn(w, h int, s *dash.AgentContextSnapshot) string {
	var b strings.Builder
	b.WriteString(sectionHeader.Render("SYSTEM"))
	b.WriteString("\n")
	b.WriteString(sectionDivider.Render(strings.Repeat("\u2500", min(w, 30))))
	b.WriteString("\n")

	// Live status
	b.WriteString(textPrimary.Render("LIVE STATUS"))
	b.WriteString("\n")
	if s.Live.Streaming {
		b.WriteString("  " + statusActive.Render("STREAMING") + "\n")
	} else {
		b.WriteString("  " + textDim.Render("idle") + "\n")
	}
	if s.Live.ToolName != "" {
		b.WriteString("  " + textCyan.Render(s.Live.ToolName) + "\n")
	}
	if s.Live.Exchanges > 0 {
		b.WriteString(fmt.Sprintf("  exchanges: %d\n", s.Live.Exchanges))
	}
	b.WriteString("\n")

	// Active peers
	if len(s.Peers) > 0 {
		b.WriteString(textPrimary.Render(fmt.Sprintf("ACTIVE PEERS (%d)", len(s.Peers))))
		b.WriteString("\n")
		for _, p := range s.Peers {
			statusStyle := textDim
			if p.Status == "active" {
				statusStyle = statusActive
			}
			line := fmt.Sprintf("  %s %s", p.AgentKey, statusStyle.Render("["+p.Status+"]"))
			if p.Mission != "" {
				line += " " + textDim.Render(truncate(p.Mission, w-30))
			}
			b.WriteString(line + "\n")
		}
		if s.PeersTruncated {
			b.WriteString(textDim.Render(fmt.Sprintf("  +%d more...", s.PeersTotal-len(s.Peers))))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// Constraints
	if len(s.Constraints) > 0 {
		b.WriteString(textPrimary.Render("CONSTRAINTS"))
		b.WriteString("\n")
		for _, c := range s.Constraints {
			b.WriteString("  " + truncate(c, w-4) + "\n")
		}
		b.WriteString("\n")
	}

	// Revision info
	b.WriteString(textDim.Render(fmt.Sprintf("rev:%d  %s", s.Revision, s.FetchedAt.Format("15:04:05"))))

	return b.String()
}

// --- Helpers ---

func (o *overlayModel) findTaskIndex(name string) int {
	for i, item := range o.items[0] {
		if item.name == name {
			return i
		}
	}
	return -1
}

func categorizeTasks(tasks []dash.TaskWithDeps) (inProgress, pending, blocked []dash.TaskWithDeps) {
	for _, t := range tasks {
		if t.IsBlocked {
			blocked = append(blocked, t)
		} else if t.Status == "in_progress" || t.Status == "active" {
			inProgress = append(inProgress, t)
		} else {
			pending = append(pending, t)
		}
	}
	return
}

func renderAlignmentBar(pct, width int) string {
	filled := pct * width / 100
	if filled > width {
		filled = width
	}
	empty := width - filled
	return "[" + barFilled.Render(strings.Repeat("#", filled)) + barEmpty.Render(strings.Repeat(".", empty)) + "]"
}

func formatDuration(start time.Time, end *time.Time) string {
	e := time.Now()
	if end != nil {
		e = *end
	}
	d := e.Sub(start)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

