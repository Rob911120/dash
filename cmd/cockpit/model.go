package main

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"dash"

	tea "github.com/charmbracelet/bubbletea"
)

type viewState int

const (
	viewChat viewState = iota
	viewDashboard
	viewAgent
	viewAgentPicker // New: shows agent selection menu
)

type model struct {
	state    viewState
	d        *dash.Dash
	chatCl   *chatClient
	width    int
	height   int
	quitting bool

	// Sub-models
	hud     hudModel
	chat    *chatModel
	overlay overlayModel
	agents  *agentManager

	// Spawn agent from dashboard
	spawnInput bool
	spawnBuf   []rune

	// Observation agent
	agent         *observationAgent
	notifications []observationNotification

	// Shared data (fetched async)
	ws         *dash.WorkingSet
	tasks      []dash.TaskWithDeps
	proposals  []dash.Proposal
	sessions   []dash.ActivitySummary
	plans      []*dash.PlanState
	services   []serviceStatus
	tree       *dash.HierarchyTree
	workOrders []*dash.WorkOrder

	// Agent dashboard
	preDashState      viewState
	activeStreamOwner string                     // agentKey that owns current stream, "" = main
	agentSnapshot     *dash.AgentContextSnapshot // nil = global dashboard
}

func newModel(d *dash.Dash, chatCl *chatClient, sessionID string, db *sql.DB) model {
	m := model{
		state:         viewChat,
		d:             d,
		chatCl:        chatCl,
		hud:           newHudModel(),
		chat:          newChatModel(chatCl, d, sessionID),
		overlay:       newOverlayModel(),
		agents:        newAgentManager(),
		agent:         newObservationAgent(db),
		notifications: make([]observationNotification, 0),
	}

	// Pre-create all agent tabs as idle (lazy spawn on first message)
	for _, a := range AvailableAgents {
		agentChat := newChatModel(chatCl, d, "")
		agentChat.scopedAgent = a.Key
		if mission, ok := agentMissions[a.Key]; ok {
			agentChat.agentMission = mission
		}
		m.agents.spawn(a.DisplayName, a.Key, "", "", "", agentChat)
	}

	return m
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		fetchContext(m.d),
		fetchDashData(m.d),
		fetchIntel(m.d),
		tickCmd(),
		observationTickCmd(),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		// Resolve global keybindings first
		globalAction := resolveGlobalKey(msg)
		switch globalAction {
		case ActionQuit:
			m.quitting = true
			return m, tea.Quit
		case ActionToggleDash:
			return m.toggleDashboard()
		case ActionCycleAgentNext:
			return m.cycleAgentNext()
		}

		// View-specific keys handled before routing
		if m.state == viewAgent {
			action := resolveAgentViewKey(msg)
			switch action {
			case ActionAgentQuickSwitch:
				return m.handleAgentQuickSwitch(msg.String())
			case ActionAgentBack:
				m.agents.deactivate()
				m.state = viewChat
				return m, nil
			}
		}
		if m.state == viewAgentPicker {
			if msg.String() == "esc" {
				m.state = viewChat
				return m, nil
			}
		}

		// Route keys to active view
		switch m.state {
		case viewChat:
			cmd := m.chat.Update(msg, m.width, m.contentHeight())
			return m, cmd
		case viewAgent:
			if tab := m.agents.active(); tab != nil {
				// Lazy spawn: intercept Enter on idle agent
				if tab.status == agentIdle {
					if msg.Type == tea.KeyEnter {
						text := strings.TrimSpace(string(tab.chat.input))
						if text != "" {
							tab.pendingMessage = text
							tab.chat.input = nil
							tab.chat.cursorPos = 0
							tab.status = agentSpawned
							tab.chat.toolStatus = "spawning agent..."
							return m, m.spawnAgentWithMission(tab.agentKey)
						}
					}
				}
				cmd := tab.chat.Update(msg, m.width, m.contentHeight())
				return m, cmd
			}
			return m, nil
		case viewDashboard:
			// Spawn input intercepts all keys
			if m.spawnInput {
				return m.handleSpawnInput(msg)
			}
			cmd := m.overlay.handleKey(msg)
			if cmd != nil {
				return m, cmd
			}
			if m.overlay.action != "" {
				action := m.overlay.action
				m.overlay.action = ""
				return m, m.handleOverlayAction(action)
			}
			return m, nil
		}

	case tea.MouseMsg:
		// Route mouse events to active view
		switch m.state {
		case viewChat:
			cmd := m.chat.Update(msg, m.width, m.contentHeight())
			return m, cmd
		case viewAgent:
			if tab := m.agents.active(); tab != nil {
				cmd := tab.chat.Update(msg, m.width, m.contentHeight())
				return m, cmd
			}
		case viewDashboard:
			// Dashboard/overlay might want mouse events later
			return m, nil
		}
		return m, nil
	}

	// Data messages
	switch msg := msg.(type) {
	case contextMsg:
		if msg.err == nil {
			m.ws = msg.ws
		}
		return m, nil

	case dashDataMsg:
		if msg.err == nil {
			m.tasks = msg.tasks
			m.sessions = msg.sessions
			m.plans = msg.plans
			m.services = msg.services
			m.workOrders = msg.workOrders
			m.overlay.rebuildItems(m.plans, m.tasks)
			// Sync work orders to agent tabs
			m.agents.updateWorkOrders(msg.workOrders)
		}
		return m, nil

	case intelMsg:
		if msg.err == nil {
			m.proposals = msg.proposals
			m.tree = msg.tree
		}
		return m, nil

	case tickMsg:
		var cmds []tea.Cmd
		cmds = append(cmds, tickCmd())
		cmds = append(cmds, fetchContext(m.d))
		if m.state == viewDashboard {
			cmds = append(cmds, fetchDashData(m.d))
		}
		return m, tea.Batch(cmds...)

	case observationTickMsg:
		return m, tea.Batch(
			pollCmd(m.agent),
			observationTickCmd(),
		)

	case observationPollMsg:
		if msg.err == nil && len(msg.notifications) > 0 {
			m.notifications = append(m.notifications, msg.notifications...)
			for _, n := range msg.notifications {
				m.chat.addSystemMessage(fmt.Sprintf("[OBSERVATION] %s: %s", n.Type, n.Message))
			}
		}
		return m, nil

	case handoffCompleteMsg:
		if tab := m.agents.active(); tab != nil && tab.id == msg.agentID {
			if msg.err != nil {
				tab.chat.addSystemMessage(fmt.Sprintf("Handoff error: %v", msg.err))
			} else {
				tab.chat.addSystemMessage("Session handoff complete. Starting new session...")
				tab.status = agentWaiting
			}
		}
		return m, nil

	case spawnAgentResultMsg:
		if msg.err != nil {
			// Check if this was a lazy spawn failure
			for _, tab := range m.agents.tabs {
				if tab.status == agentSpawned && tab.pendingMessage != "" {
					tab.status = agentIdle
					tab.chat.toolStatus = ""
					tab.chat.errMsg = fmt.Sprintf("Spawn failed: %v", msg.err)
					tab.pendingMessage = ""
					return m, nil
				}
			}
			m.chat.addSystemMessage(fmt.Sprintf("Spawn failed: %v", msg.err))
		} else if msg.info != nil {
			// Check if this is a lazy spawn for an existing idle/spawned tab
			for _, tab := range m.agents.tabs {
				if tab.agentKey == msg.info.AgentKey && tab.status == agentSpawned {
					tab.sessionID = msg.info.SessionID
					tab.chat.sessionID = msg.info.SessionID
					tab.chat.scopedAgent = msg.info.AgentKey
					tab.chat.agentMission = msg.info.Mission
					tab.chat.toolStatus = ""
					tab.status = agentActive
					tab.spawnedAt = time.Now()
					tab.mission = msg.info.Mission

					if tab.pendingMessage != "" {
						tab.chat.messages = append(tab.chat.messages, chatMessage{
							Role: "user", Content: tab.pendingMessage,
						})
						tab.pendingMessage = ""
						m.activeStreamOwner = tab.agentKey
						return m, tab.chat.startStream()
					}
					return m, nil
				}
			}
			// Fallback: new tab (orchestrator-triggered spawn)
			m.spawnAgentTab(*msg.info)
			m.state = viewAgent
			if tab := m.agents.active(); tab != nil {
				return m, tab.chat.startStream()
			}
		}
		return m, nil
		
	case agentStatusUpdateMsg:
		// Update agent tab status
		for _, tab := range m.agents.tabs {
			if tab.id == msg.agentID {
				tab.status = msg.status
				if msg.message != "" {
					tab.chat.addSystemMessage(msg.message)
				}
				break
			}
		}
		return m, nil
		
	case agentProgressMsg:
		// Show progress in agent chat
		for _, tab := range m.agents.tabs {
			if tab.id == msg.agentID {
				tab.chat.toolStatus = msg.progress
				break
			}
		}
		return m, nil

	case agentSnapshotMsg:
		if msg.err == nil {
			m.agentSnapshot = msg.snapshot
			if tab := m.agents.active(); tab != nil && tab.agentKey == msg.snapshot.AgentKey {
				m.agentSnapshot.Live = dash.LiveStatus{
					Streaming: tab.chat.streaming,
					ToolName:  tab.chat.toolStatus,
					Exchanges: tab.chat.meter.exchanges,
				}
				// Inject file tracking from TUI agent chat
				if tab.chat.lastFile != "" {
					m.agentSnapshot.RecentFiles = []string{tab.chat.lastFile}
				}
				if tab.chat.fileCounts != nil {
					m.agentSnapshot.TopFiles = topNFiles(tab.chat.fileCounts, 3)
				}
			}
		}
		return m, nil
	}

	// Chat streaming messages — route to active chat
	switch msg := msg.(type) {
	case chatChunkMsg, chatReasoningMsg, chatDoneMsg, chatErrorMsg, chatToolCallMsg:
		// Invariant: stream routes based on activeStreamOwner, not view state
		targetChat := m.chat
		if m.activeStreamOwner != "" {
			for _, tab := range m.agents.tabs {
				if tab.agentKey == m.activeStreamOwner {
					targetChat = tab.chat
					break
				}
			}
		} else if m.state == viewAgent {
			if tab := m.agents.active(); tab != nil {
				targetChat = tab.chat
			}
		}
		cmd := targetChat.Update(msg, m.width, m.contentHeight())
		if _, isDone := msg.(chatDoneMsg); isDone {
			// Check for handoff if this was an agent stream
			if m.activeStreamOwner != "" {
				for _, tab := range m.agents.tabs {
					if tab.agentKey == m.activeStreamOwner && tab.meter.shouldHandoff(20) {
						m.activeStreamOwner = ""
						return m, tea.Batch(cmd, performHandoff(m.d, tab))
					}
				}
			}
			m.activeStreamOwner = ""
		}
		return m, cmd

	case chatToolResultReady:
		if m.state == viewAgent {
			if tab := m.agents.active(); tab != nil {
				tab.chat.messages = append(tab.chat.messages, msg.results...)
				tab.chat.toolStatus = ""
				tab.chat.scrollToBottom()
				m.activeStreamOwner = tab.agentKey
				return m, tab.chat.startStream()
			}
		}
		m.activeStreamOwner = ""
		m.chat.messages = append(m.chat.messages, msg.results...)
		m.chat.toolStatus = ""
		m.chat.scrollToBottom()
		return m, m.chat.startStream()

	case chatToolResultWithSpawn:
		// Feed tool results back to orchestrator chat
		m.chat.messages = append(m.chat.messages, msg.results...)
		m.chat.toolStatus = ""
		m.chat.scrollToBottom()

		// Spawn the agent tab
		m.spawnAgentTab(msg.spawn)

		// Continue orchestrator streaming
		return m, m.chat.startStream()

	case taskContextReadyMsg:
		if msg.err == nil {
			m.chat.messages = append(m.chat.messages, chatMessage{
				Role:    "user",
				Content: "Analysera denna task och f\u00f6resl\u00e5 konkreta steg.",
			})
			return m, m.chat.startStream()
		}
		return m, nil

	case planContextReadyMsg:
		if msg.err == nil {
			m.chat.messages = append(m.chat.messages, chatMessage{
				Role:    "user",
				Content: "Starta exekveringen. Implementera alla steg i planen i ordning.",
			})
			return m, m.chat.startStream()
		}
		return m, nil
	}

	return m, nil
}

func (m model) contentHeight() int {
	hudH := m.hud.Height(m.ws)
	h := m.height - hudH - 1 // -1 for footer
	if m.agents.count() > 0 {
		h-- // -1 for tab bar
	}
	if h < 1 {
		h = 1
	}
	return h
}

func (m model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder

	// HUD (always visible)
	streaming := m.chat.streaming
	if m.state == viewAgent {
		if tab := m.agents.active(); tab != nil {
			streaming = tab.chat.streaming
		}
	}
	b.WriteString(m.hud.View(m.ws, m.services, streaming, m.width))
	b.WriteString("\n")

	// Agent tab bar (if agents exist)
	if m.agents.count() > 0 {
		b.WriteString(m.agents.tabBar(m.width))
		b.WriteString("\n")
	}

	// Content
	ch := m.contentHeight()
	switch m.state {
	case viewChat:
		b.WriteString(m.chat.View(m.width, ch))
	case viewDashboard:
		b.WriteString(m.overlay.View(m.width, ch, m.tasks, m.proposals, m.plans, m.sessions, m.services, m.ws, m.tree, m.chatCl, m.agents, m.spawnInput, m.spawnBuf, m.activeChat().maxToolIter, m.agentSnapshot, m.workOrders))
	case viewAgent:
		if tab := m.agents.active(); tab != nil {
			b.WriteString(tab.chat.View(m.width, ch))
		}
	case viewAgentPicker:
		b.WriteString(AgentPicker(m.width))
	}

	// Footer
	b.WriteString("\n")
	b.WriteString(footerStyle.Render(m.footer()))

	return b.String()
}

func (m model) footer() string {
	tabHint := "[tab] agents"
	switch m.state {
	case viewChat:
		return tabHint + "  " + m.chat.FooterHelp()
	case viewDashboard:
		prefix := tabHint
		if m.agentSnapshot != nil {
			prefix = fmt.Sprintf("[%s rev:%d]", m.agentSnapshot.AgentKey, m.agentSnapshot.Revision)
		}
		return prefix + "  [h/l] column  [j/k] navigate  [enter] select  [å/ä] model  [p/ö] agent  [n] spawn  [t] tools  [c] clear+continue  [r] refresh"
	case viewAgent:
		if tab := m.agents.active(); tab != nil {
			agentHelp := fmt.Sprintf("[1-%d] switch  [tab] agents  [esc] back  ", m.agents.count())
			return tabHint + "  " + agentHelp + tab.chat.FooterHelp()
		}
	case viewAgentPicker:
		return "[1-7] spawn  [esc/Shift+Tab] close"
	}
	return ""
}

// toggleDashboard toggles between Chat and Dashboard views.
func (m *model) toggleDashboard() (tea.Model, tea.Cmd) {
	switch m.state {
	case viewDashboard:
		m.state = m.preDashState
		if m.state == 0 {
			m.state = viewChat
		}
		m.agentSnapshot = nil
		return m, nil
	default:
		m.preDashState = m.state
		m.state = viewDashboard
		cmds := []tea.Cmd{fetchDashData(m.d)}
		if m.preDashState == viewAgent {
			if tab := m.agents.active(); tab != nil {
				cmds = append(cmds, fetchAgentSnapshot(m.d, tab.agentKey, tab.mission))
			}
		} else {
			m.agentSnapshot = nil
		}
		return m, tea.Batch(cmds...)
	}
}

// activeChat returns the currently active chatModel (main or agent).
func (m *model) activeChat() *chatModel {
	if m.state == viewAgent {
		if tab := m.agents.active(); tab != nil {
			return tab.chat
		}
	}
	return m.chat
}

// cycleAgentNext activates the next agent tab, or wraps to main chat.
func (m *model) cycleAgentNext() (tea.Model, tea.Cmd) {
	if m.agents.count() == 0 {
		return m, nil
	}
	if m.state != viewAgent {
		m.agents.activate(0)
		m.state = viewAgent
		return m, nil
	}
	nextIdx := m.agents.activeIdx + 1
	if nextIdx < m.agents.count() {
		m.agents.activate(nextIdx)
		return m, nil
	}
	// Wrap back to chat
	m.agents.deactivate()
	m.state = viewChat
	return m, nil
}

// selectAgentByIndex activates agent at given index (0-5), or spawns new if none exists.
func (m *model) selectAgentByIndex(idx int) (tea.Model, tea.Cmd) {
	if idx < 0 || idx > 5 {
		return m, nil
	}
	
	// If agent already exists at this index, activate it
	if idx < m.agents.count() {
		m.agents.activate(idx)
		m.state = viewAgent
		return m, nil
	}
	
	// Otherwise, open spawn dialog for this agent slot
	// For now, just show agent picker
	if m.agents.count() > 0 {
		m.state = viewAgent
		m.agents.activate(0)
	}
	return m, nil
}

// cycleAgentPrev activates the previous agent tab, or wraps to last.
func (m *model) cycleAgentPrev() (tea.Model, tea.Cmd) {
	if m.agents.count() == 0 {
		return m, nil
	}
	if m.state != viewAgent {
		m.agents.activate(m.agents.count() - 1)
		m.state = viewAgent
		return m, nil
	}
	if m.agents.activeIdx > 0 {
		m.agents.activate(m.agents.activeIdx - 1)
		return m, nil
	}
	// Wrap back to chat
	m.agents.deactivate()
	m.state = viewChat
	return m, nil
}

// spawnAgentByIndex spawns an agent by index (0-5) from AvailableAgents.
// Deprecated: Use spawnAgentWithMission instead for better UX
func (m *model) spawnAgentByIndex(idx int) (tea.Model, tea.Cmd) {
	if idx < 0 || idx >= len(AvailableAgents) {
		return m, nil
	}

	agent := AvailableAgents[idx]
	return m, m.spawnAgentWithMission(agent.Key)
}

func (m *model) spawnAgentTab(info agentSpawnInfo) {
	sessionID := info.SessionID
	agentChat := newChatModel(m.chatCl, m.d, sessionID)
	agentChat.scopedAgent = info.AgentKey
	agentChat.agentMission = info.Mission

	displayName := info.Name
	if displayName == "" {
		displayName = info.AgentKey
	}

	tab := m.agents.spawn(displayName, info.AgentKey, info.Mission, sessionID, "orchestrator", agentChat)
	tab.status = agentActive

	// Auto-start: inject mission as first user message
	tab.chat.messages = append(tab.chat.messages, chatMessage{
		Role:    "user",
		Content: info.Mission,
	})
	
	// Activate the new tab
	m.agents.activateByID(tab.id)
}

func (m *model) handleOverlayAction(action string) tea.Cmd {
	switch {
	case strings.HasPrefix(action, "task:"):
		taskName := strings.TrimPrefix(action, "task:")
		m.chat.scopedTask = taskName
		m.chat.scopedPlan = ""
		m.chat.messages = nil
		m.chat.errMsg = ""
		m.chat.scrollY = 0
		m.chat.runMode = false
		m.state = viewChat
		return refreshTaskContext(m.d, taskName)

	case strings.HasPrefix(action, "plan:"):
		planName := strings.TrimPrefix(action, "plan:")
		m.chat.scopedPlan = planName
		m.chat.scopedTask = ""
		m.chat.messages = nil
		m.chat.errMsg = ""
		m.chat.scrollY = 0
		m.chat.runMode = true
		m.state = viewChat
		return refreshPlanContext(m.d, planName)

	case action == "refresh":
		return tea.Batch(fetchDashData(m.d), fetchIntel(m.d))

	case action == "model-next":
		return m.activeChat().switchModel()

	case action == "model-prev":
		return m.activeChat().switchModelBack()

	case action == "agent-next":
		_, cmd := m.cycleAgentNext()
		// Stay in dashboard after cycling
		m.state = viewDashboard
		return cmd

	case action == "agent-prev":
		_, cmd := m.cycleAgentPrev()
		// Stay in dashboard after cycling
		m.state = viewDashboard
		return cmd

	case action == "tool-limit":
		m.activeChat().cycleToolLimit()
		return nil

	case action == "clear-continue":
		m.activeChat().clearAndContinue()
		m.activeChat().addSystemMessage("Session roterad manuellt.")
		m.state = viewChat
		return nil

	case action == "spawn":
		m.spawnInput = true
		m.spawnBuf = nil
	}
	return nil
}

// handleSpawnInput handles key input during agent spawn prompt.
func (m *model) handleSpawnInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		key := strings.TrimSpace(string(m.spawnBuf))
		m.spawnInput = false
		m.spawnBuf = nil
		if key != "" {
			return m, m.spawnAgentWithMission(key)
		}
		return m, nil
	case tea.KeyEsc:
		m.spawnInput = false
		m.spawnBuf = nil
		return m, nil
	case tea.KeyBackspace:
		if len(m.spawnBuf) > 0 {
			m.spawnBuf = m.spawnBuf[:len(m.spawnBuf)-1]
		}
		return m, nil
	case tea.KeySpace:
		m.spawnBuf = append(m.spawnBuf, '-') // spaces → dashes in agent keys
		return m, nil
	case tea.KeyRunes:
		m.spawnBuf = append(m.spawnBuf, msg.Runes...)
		return m, nil
	}
	return m, nil
}

// topNFiles returns the top N files by frequency from a count map.
func topNFiles(counts map[string]int, n int) []string {
	if len(counts) == 0 {
		return nil
	}
	type fc struct {
		file  string
		count int
	}
	var all []fc
	for f, c := range counts {
		all = append(all, fc{f, c})
	}
	// Simple selection sort for small N
	for i := 0; i < len(all) && i < n; i++ {
		maxIdx := i
		for j := i + 1; j < len(all); j++ {
			if all[j].count > all[maxIdx].count {
				maxIdx = j
			}
		}
		all[i], all[maxIdx] = all[maxIdx], all[i]
	}
	result := make([]string, 0, n)
	for i := 0; i < len(all) && i < n; i++ {
		result = append(result, all[i].file)
	}
	return result
}

// spawnAgentResultMsg is sent when an agent spawn completes.
type spawnAgentResultMsg struct {
	info *agentSpawnInfo
	err  error
}
