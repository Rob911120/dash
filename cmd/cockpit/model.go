package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"dash"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

type viewState int

const (
	viewChat viewState = iota
	viewDashboard
	viewAgent
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

	// Cross-agent queries
	pendingQueries map[string]*pendingQuery // queryID → pending query

	// All agent definitions loaded from DB (including non-favorites)
	allAgentDefs []dash.AgentDef
}

func newModel(d *dash.Dash, chatCl *chatClient, sessionID string, db *sql.DB) model {
	// Load agent definitions from DB
	defs := dash.LoadAgentDefs(context.Background(), d)

	m := model{
		state:          viewAgent,
		d:              d,
		chatCl:         chatCl,
		hud:            newHudModel(),
		overlay:        newOverlayModel(),
		agents:         newAgentManager(),
		agent:          newObservationAgent(db),
		notifications:  make([]observationNotification, 0),
		pendingQueries: make(map[string]*pendingQuery),
		allAgentDefs:   defs,
	}

	// Pre-create favorite agent tabs as idle (lazy spawn on first message)
	for _, def := range defs {
		if !def.Favorite {
			continue
		}
		agentChat := newChatModel(chatCl, d, "")
		if def.Key == "orchestrator" {
			agentChat = newChatModel(chatCl, d, sessionID)
		}
		agentChat.scopedAgent = def.Key
		agentChat.agentMission = def.Mission
		tab := m.agents.spawn(def.DisplayName, def.Key, "", "", "", agentChat)
		tab.controller = "idle"
	}

	// Activate orchestrator tab
	for i, tab := range m.agents.tabs {
		if tab.agentKey == "orchestrator" {
			m.agents.activate(i)
			break
		}
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
			cmds := m.releaseAllAgents()
			cmds = append(cmds, tea.Quit)
			return m, tea.Batch(cmds...)
		case ActionToggleDash:
			return m.toggleDashboard()
		case ActionCycleAgentNext:
			return m.cycleAgentNext()
		}

		// View-specific keys handled before routing
		if m.state == viewAgent {
			action := resolveAgentViewKey(msg)
			switch action {
			case ActionAgentBack:
				var cmds []tea.Cmd
				if tab := m.agents.active(); tab != nil && tab.controller == "human" {
					tab.controller = "idle"
					cmds = append(cmds, releaseControlCmd(m.d, tab, "back"))
				}
				// Switch to orchestrator tab
				for i, tab := range m.agents.tabs {
					if tab.agentKey == "orchestrator" {
						m.agents.activate(i)
						break
					}
				}
				return m, tea.Batch(cmds...)
			case ActionPauseAgent:
				if tab := m.agents.active(); tab != nil {
					prevController := tab.controller
					tab.controller = "idle"
					return m, pauseAgentCmd(m.d, tab, prevController)
				}
			}
		}
		// Route keys to active view
		switch m.state {
		case viewAgent:
			if tab := m.agents.active(); tab != nil {
				// Auto take-control on first input character
				if tab.controller != "human" && isInputChar(msg) {
					var cmds []tea.Cmd
					// Release any other human-controlled agent (agent→agent, no orchestrator)
					for _, other := range m.agents.tabs {
						if other != tab && other.controller == "human" {
							other.controller = "idle"
							cmds = append(cmds, releaseControlCmd(m.d, other, "switched"))
						}
					}
					tab.controller = "human"
					cmds = append(cmds, takeControlCmd(m.d, tab.agentKey, tab.sessionID, tab.mission))
					cmds = append(cmds, tab.chat.Update(msg, m.width, m.contentHeight()))
					return m, tea.Batch(cmds...)
				}
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
				// Set stream owner when user initiates a stream
				if tab.chat.streaming && m.activeStreamOwner == "" {
					m.activeStreamOwner = tab.agentKey
					return m, tea.Batch(cmd, m.globalSpinnerTick())
				}
				return m, cmd
			}
			return m, nil
		case viewDashboard:
			// Spawn input intercepts all keys
			if m.spawnInput {
				return m.handleSpawnInput(msg)
			}
			cmd := m.overlay.handleKey(msg)
			// Rebuild items after filter changes
			if m.overlay.filtering || m.overlay.filterText != "" {
				m.overlay.rebuildItems(m.plans, m.tasks)
			}
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
			if oc := m.orchChat(); oc != nil {
				for _, n := range msg.notifications {
					oc.addSystemMessage(fmt.Sprintf("[OBSERVATION] %s: %s", n.Type, n.Message))
				}
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
				if tab.status == agentSpawned && (tab.pendingMessage != "" || tab.answeringQuery != nil) {
					tab.status = agentIdle
					tab.chat.toolStatus = ""
					tab.chat.errMsg = fmt.Sprintf("Spawn failed: %v", msg.err)
					tab.pendingMessage = ""
					// Clean up any pending query
					if tab.answeringQuery != nil {
						delete(m.pendingQueries, tab.answeringQuery.id)
						tab.answeringQuery = nil
					}
					return m, nil
				}
			}
			if oc := m.orchChat(); oc != nil {
				oc.addSystemMessage(fmt.Sprintf("Spawn failed: %v", msg.err))
			}
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

					// Cross-agent query spawn: question already injected, start stream
					if tab.answeringQuery != nil {
						return m, m.beginStream(tab.agentKey, tab.chat)
					}

					if tab.pendingMessage != "" {
						tab.chat.appendMsg(dash.ChatMessage{
							Role: "user", Content: tab.pendingMessage,
						})
						tab.pendingMessage = ""
						return m, m.beginStream(tab.agentKey, tab.chat)
					}
					return m, nil
				}
			}
			// Fallback: new tab (orchestrator-triggered spawn)
			m.spawnAgentTab(*msg.info)
			m.state = viewAgent
			if tab := m.agents.active(); tab != nil {
				return m, m.beginStream(tab.agentKey, tab.chat)
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

	case takeControlMsg:
		if msg.err != nil {
			// Rollback optimistic controller update
			for _, tab := range m.agents.tabs {
				if tab.agentKey == msg.agentKey && tab.controller == "human" {
					tab.controller = "idle"
				}
			}
			return m, nil
		}
		// Inject briefing into the right agent tab
		for _, tab := range m.agents.tabs {
			if tab.agentKey == msg.agentKey {
				tab.chat.appendUI("control-briefing", msg.briefing)
				tab.chat.scrollToBottom()
				break
			}
		}
		return m, nil

	case releaseControlMsg:
		return m, nil

	case pauseAgentMsg:
		if msg.err == nil {
			for _, tab := range m.agents.tabs {
				if tab.agentKey == msg.agentKey {
					tab.chat.appendUI("control-release", "⏸ Agent pausad")
					tab.chat.scrollToBottom()
					// Stop any active stream
					if tab.chat.cancelFn != nil {
						tab.chat.cancelFn()
					}
					tab.chat.streaming = false
					break
				}
			}
		}
		return m, nil
	}

	// Chat streaming messages — route to active chat
	switch msg := msg.(type) {
	case spinner.TickMsg:
		// Broadcast spinner tick to ALL streaming chats (global tick)
		for _, tab := range m.agents.tabs {
			tab.chat.tickSpinner(msg)
		}
		if m.anyStreaming() {
			return m, m.globalSpinnerTick()
		}
		return m, nil

	case chatChunkMsg, chatReasoningMsg, chatDoneMsg, chatErrorMsg, chatToolCallMsg:
		// Owner-based routing — no active-tab fallback
		owner := streamMsgOwner(msg)
		targetChat := m.chatForAgent(owner)
		if targetChat == nil {
			targetChat = m.orchChat() // emergency fallback
		}
		cmd := targetChat.Update(msg, m.width, m.contentHeight())
		if _, isDone := msg.(chatDoneMsg); isDone {
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
		// Route based on activeStreamOwner, not view state (bugfix)
		if m.activeStreamOwner != "" {
			for _, tab := range m.agents.tabs {
				if tab.agentKey == m.activeStreamOwner {
					if !tab.chat.handleToolResults(msg.results) {
						m.activeStreamOwner = ""
						return m, nil
					}
					return m, m.beginStream(tab.agentKey, tab.chat)
				}
			}
		}
		oc := m.orchChat()
		if !oc.handleToolResults(msg.results) {
			m.activeStreamOwner = ""
			return m, nil
		}
		return m, m.beginStream("orchestrator", oc)

	case chatToolResultWithAsk:
		return m.handleAskDispatch(msg)

	case chatToolResultWithAnswer:
		return m.handleAnswerRoute(msg)

	case chatToolResultWithPlanRequest:
		return m.handlePlanRequest(msg)

	case chatToolResultWithSpawn:
		// Feed tool results back to orchestrator chat
		oc := m.orchChat()
		oc.appendMsgs(msg.results)
		oc.toolStatus = ""
		oc.scrollToBottom()

		// Spawn the agent tab
		m.spawnAgentTab(msg.spawn)

		// Continue orchestrator streaming
		return m, m.beginStream("orchestrator", oc)

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
	streaming := false
	if tab := m.agents.active(); tab != nil {
		streaming = tab.chat.streaming
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
	case viewDashboard:
		b.WriteString(m.overlay.View(m.width, ch, m.tasks, m.proposals, m.plans, m.sessions, m.services, m.ws, m.tree, m.chatCl, m.agents, m.spawnInput, m.spawnBuf, m.activeChat().maxToolIter, m.agentSnapshot, m.workOrders, m.activeChat().meter.View()))
	case viewAgent:
		if tab := m.agents.active(); tab != nil {
			b.WriteString(tab.chat.View(m.width, ch))
		}
	}

	// Footer
	b.WriteString("\n")
	b.WriteString(footerStyle.Render(m.footer()))

	return b.String()
}

// focusAgent switches UI focus to an agent tab (peek only — no controller change).
func (m *model) focusAgent(idx int) (tea.Model, tea.Cmd) {
	m.agents.activate(idx)
	m.state = viewAgent
	return m, nil
}

// isInputChar returns true if the key event is a text input character.
func isInputChar(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace
}

// releaseAllAgents releases all human-controlled agents (fire-and-forget).
func (m *model) releaseAllAgents() []tea.Cmd {
	var cmds []tea.Cmd
	for _, tab := range m.agents.tabs {
		if tab.controller == "human" {
			tab.controller = "idle"
			cmds = append(cmds, releaseControlCmd(m.d, tab, "quit"))
		}
	}
	return cmds
}

func (m model) footer() string {
	switch m.state {
	case viewDashboard:
		prefix := "[tab] agents"
		if m.agentSnapshot != nil {
			prefix = fmt.Sprintf("[%s rev:%d]", m.agentSnapshot.AgentKey, m.agentSnapshot.Revision)
		}
		return prefix + "  [h/l] column  [j/k] navigate  [enter] select  [/] filter  [å/ä] model  [n] spawn  [t] tools  [c] clear+continue  [r] refresh"
	case viewAgent:
		if tab := m.agents.active(); tab != nil {
			ctrlHint := "peek"
			if tab.controller == "human" {
				ctrlHint = "controlling"
			}
			agentHelp := fmt.Sprintf("[%s] %s  [shift+tab] switch  [esc] back  [ctrl+p] pause  ",
				tab.agentKey, ctrlHint)
			return agentHelp + tab.chat.FooterHelp()
		}
	}
	return ""
}

// toggleDashboard toggles between Chat and Dashboard views.
func (m *model) toggleDashboard() (tea.Model, tea.Cmd) {
	switch m.state {
	case viewDashboard:
		m.state = m.preDashState
		if m.state == 0 || m.state == viewChat {
			m.state = viewAgent
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

// anyStreaming returns true if any agent tab is currently streaming.
func (m *model) anyStreaming() bool {
	for _, tab := range m.agents.tabs {
		if tab.chat.streaming {
			return true
		}
	}
	return false
}

// globalSpinnerTick returns a spinner Tick cmd from any streaming tab.
func (m *model) globalSpinnerTick() tea.Cmd {
	for _, tab := range m.agents.tabs {
		if tab.chat.streaming {
			return tab.chat.thinkSpinner.Tick
		}
	}
	return nil
}

// beginStream sets activeStreamOwner and starts both the data stream and global spinner tick.
func (m *model) beginStream(owner string, chat *chatModel) tea.Cmd {
	m.activeStreamOwner = owner
	streamCmd := chat.startStream()
	return tea.Batch(streamCmd, m.globalSpinnerTick())
}

// streamMsgOwner extracts the owner from a stream message.
func streamMsgOwner(msg tea.Msg) string {
	switch m := msg.(type) {
	case chatChunkMsg:
		return m.owner
	case chatReasoningMsg:
		return m.owner
	case chatDoneMsg:
		return m.owner
	case chatErrorMsg:
		return m.owner
	case chatToolCallMsg:
		return m.owner
	}
	return ""
}

// orchChat returns the orchestrator tab's chatModel.
func (m *model) orchChat() *chatModel {
	for _, tab := range m.agents.tabs {
		if tab.agentKey == "orchestrator" {
			return tab.chat
		}
	}
	return nil
}

// activeChat returns the currently active chatModel (active tab or orchestrator fallback).
func (m *model) activeChat() *chatModel {
	if tab := m.agents.active(); tab != nil {
		return tab.chat
	}
	return m.orchChat()
}

// cycleAgentNext activates the next agent tab, wrapping to first.
func (m *model) cycleAgentNext() (tea.Model, tea.Cmd) {
	if m.agents.count() == 0 {
		return m, nil
	}
	if m.state != viewAgent {
		return m.focusAgent(0)
	}
	nextIdx := m.agents.activeIdx + 1
	if nextIdx < m.agents.count() {
		return m.focusAgent(nextIdx)
	}
	// Wrap to first tab
	return m.focusAgent(0)
}

// cycleAgentPrev activates the previous agent tab, wrapping to last.
func (m *model) cycleAgentPrev() (tea.Model, tea.Cmd) {
	if m.agents.count() == 0 {
		return m, nil
	}
	if m.state != viewAgent {
		return m.focusAgent(m.agents.count() - 1)
	}
	if m.agents.activeIdx > 0 {
		return m.focusAgent(m.agents.activeIdx - 1)
	}
	// Wrap to last tab
	return m.focusAgent(m.agents.count() - 1)
}

// ensureTempTab returns an existing tab for agentKey or creates a new idle tab.
// Used for non-favorite agents that need a temporary tab (e.g. via ask_agent or give_to_planner).
func (m *model) ensureTempTab(agentKey string) *agentTab {
	for _, tab := range m.agents.tabs {
		if tab.agentKey == agentKey {
			return tab
		}
	}
	// Find def
	var def *dash.AgentDef
	for i := range m.allAgentDefs {
		if m.allAgentDefs[i].Key == agentKey {
			def = &m.allAgentDefs[i]
			break
		}
	}
	displayName := agentKey
	mission := ""
	if def != nil {
		displayName = def.DisplayName
		mission = def.Mission
	}
	agentChat := newChatModel(m.chatCl, m.d, "")
	agentChat.scopedAgent = agentKey
	agentChat.agentMission = mission
	tab := m.agents.spawn(displayName, agentKey, "", "", "", agentChat)
	tab.controller = "idle"
	return tab
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
	tab.chat.appendMsg(dash.ChatMessage{
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
		oc := m.orchChat()
		oc.appendMsg(dash.ChatMessage{Role: "user", Content: "Arbeta med task: " + taskName})
		// Switch to orchestrator tab
		for i, tab := range m.agents.tabs {
			if tab.agentKey == "orchestrator" {
				m.agents.activate(i)
				break
			}
		}
		m.state = viewAgent
		return m.beginStream("orchestrator", oc)

	case strings.HasPrefix(action, "plan:"):
		planName := strings.TrimPrefix(action, "plan:")
		oc := m.orchChat()
		oc.appendMsg(dash.ChatMessage{Role: "user", Content: "Kör plan: " + planName})
		// Switch to orchestrator tab
		for i, tab := range m.agents.tabs {
			if tab.agentKey == "orchestrator" {
				m.agents.activate(i)
				break
			}
		}
		m.state = viewAgent
		return m.beginStream("orchestrator", oc)

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
		m.state = m.preDashState
		if m.state == viewDashboard {
			m.state = viewAgent
		}
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

// handlePlanRequest processes a chatToolResultWithPlanRequest: fire-and-forget to planner.
func (m *model) handlePlanRequest(msg chatToolResultWithPlanRequest) (tea.Model, tea.Cmd) {
	// 1. Feed tool results back to caller's chat (NOT blocked — fire-and-forget)
	callerChat := m.activeStreamChat()
	if callerChat != nil {
		callerChat.appendMsgs(msg.results)
		callerChat.toolStatus = ""
		callerChat.scrollToBottom()
	}

	// 2. Ensure planner tab exists (should be favorite, but ensureTempTab handles it)
	plannerTab := m.ensureTempTab("planner-agent")

	// 3. Inject request as user message in planner tab
	reqMsg := fmt.Sprintf("── PLAN REQUEST (%s) ──\n%s", msg.requestID, msg.desc)
	if msg.context != "" {
		reqMsg += fmt.Sprintf("\n\nKontext: %s", msg.context)
	}
	if msg.priority != "" {
		reqMsg += fmt.Sprintf("\nPrioritet: %s", msg.priority)
	}
	plannerTab.chat.appendMsg(dash.ChatMessage{
		Role:    "user",
		Content: reqMsg,
	})
	plannerTab.chat.toolIter = 0
	plannerTab.chat.scrollToBottom()

	// 4. Restart caller stream immediately (fire-and-forget)
	var cmds []tea.Cmd
	if m.activeStreamOwner != "" {
		cmds = append(cmds, m.beginStream(m.activeStreamOwner, callerChat))
	} else {
		cmds = append(cmds, m.beginStream("orchestrator", m.orchChat()))
	}

	// 5. Lazy spawn planner if idle, then queue stream
	if plannerTab.status == agentIdle {
		plannerTab.status = agentSpawned
		plannerTab.chat.toolStatus = "spawning planner..."
		cmds = append(cmds, m.spawnAgentWithMission("planner-agent"))
	}
	// If planner is already active but no stream running, it'll pick up on next turn

	return m, tea.Batch(cmds...)
}

// handleAskDispatch processes a chatToolResultWithAsk: caller dispatched a question.
func (m *model) handleAskDispatch(msg chatToolResultWithAsk) (tea.Model, tea.Cmd) {
	q := msg.query

	// 1. Feed tool results into caller's chat but do NOT restart stream
	callerChat := m.chatForAgent(q.callerKey)
	if callerChat != nil {
		callerChat.appendMsgs(msg.results)
		callerChat.toolStatus = fmt.Sprintf("waiting for %s...", q.targetKey)
		callerChat.scrollToBottom()
	}

	// 2. Store in pendingQueries
	m.pendingQueries[q.id] = &q

	// 3. Find target agent tab
	var targetTab *agentTab
	for _, tab := range m.agents.tabs {
		if tab.agentKey == q.targetKey {
			targetTab = tab
			break
		}
	}

	if targetTab == nil {
		// Target not found — return error to caller
		if callerChat != nil {
			callerChat.toolStatus = ""
			callerChat.addSystemMessage(fmt.Sprintf("ask_agent error: agent %q not found", q.targetKey))
		}
		delete(m.pendingQueries, q.id)
		if callerChat != nil {
			return m, m.beginStream(q.callerKey, callerChat)
		}
		return m, nil
	}

	// 4. Set target as answering this query
	targetTab.answeringQuery = &q
	targetTab.chat.answeringQueryInfo = &q

	// 5. Inject question as user message with system marker
	marker := fmt.Sprintf("── FRÅGA FRÅN %s (query_id: %s) ──\n%s", q.callerKey, q.id, q.question)
	targetTab.chat.appendMsg(dash.ChatMessage{
		Role:    "user",
		Content: marker,
	})
	targetTab.chat.toolIter = 0
	targetTab.chat.scrollToBottom()

	// 6. Lazy spawn if idle, otherwise start stream
	if targetTab.status == agentIdle {
		targetTab.pendingMessage = "" // question already injected
		targetTab.status = agentSpawned
		targetTab.chat.toolStatus = "spawning agent..."
		return m, m.spawnAgentWithMission(targetTab.agentKey)
	}

	return m, m.beginStream(targetTab.agentKey, targetTab.chat)
}

// handleAnswerRoute processes a chatToolResultWithAnswer: target committed an answer.
func (m *model) handleAnswerRoute(msg chatToolResultWithAnswer) (tea.Model, tea.Cmd) {
	// 1. Feed tool results into target's chat
	targetChat := m.activeStreamChat()
	if targetChat != nil {
		targetChat.appendMsgs(msg.results)
		targetChat.toolStatus = ""
		targetChat.scrollToBottom()
	}

	// 2. Look up the pending query
	pq, ok := m.pendingQueries[msg.queryID]
	if !ok {
		// No pending query — discard (caller may have been cancelled)
		m.activeStreamOwner = ""
		return m, nil
	}

	// 3. Replace caller's "dispatched" tool result with the real answer
	callerChat := m.chatForAgent(pq.callerKey)
	if callerChat != nil {
		answerContent := fmt.Sprintf(`{"query_id":%q,"answer":%q,"from":%q}`, msg.queryID, msg.answer, pq.targetKey)
		replaceToolResult(callerChat.messages, pq.toolCallID, answerContent)
		callerChat.toolStatus = ""
	}

	// 4. Reset target tab's answeringQuery
	for _, tab := range m.agents.tabs {
		if tab.agentKey == pq.targetKey {
			tab.answeringQuery = nil
			tab.chat.answeringQueryInfo = nil
			break
		}
	}

	// 5. Clean up
	delete(m.pendingQueries, msg.queryID)

	// 6. Restart caller's stream
	if callerChat != nil {
		return m, m.beginStream(pq.callerKey, callerChat)
	}
	return m, nil
}

// chatForAgent returns the chatModel for a given agent key.
func (m *model) chatForAgent(agentKey string) *chatModel {
	for _, tab := range m.agents.tabs {
		if tab.agentKey == agentKey {
			return tab.chat
		}
	}
	if agentKey == "orchestrator" || agentKey == "" {
		return m.orchChat()
	}
	return nil
}

// activeStreamChat returns the chatModel that currently owns the stream.
func (m *model) activeStreamChat() *chatModel {
	if m.activeStreamOwner == "" {
		return m.orchChat()
	}
	for _, tab := range m.agents.tabs {
		if tab.agentKey == m.activeStreamOwner {
			return tab.chat
		}
	}
	return m.orchChat()
}

// spawnAgentResultMsg is sent when an agent spawn completes.
type spawnAgentResultMsg struct {
	info *agentSpawnInfo
	err  error
}
