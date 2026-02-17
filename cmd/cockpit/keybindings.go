package main

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// KeyAction represents a resolved keyboard action.
type KeyAction int

const (
	ActionNone KeyAction = iota

	// Global actions
	ActionQuit
	ActionToggleDash
	ActionCycleAgentNext

	// Chat actions — input editing
	ActionSendMessage
	ActionDeleteCharBack
	ActionDeleteCharForward
	ActionCursorLeft
	ActionCursorRight
	ActionCursorHome
	ActionCursorEnd
	ActionDeleteToStart
	ActionDeleteToEnd
	ActionInsertSpace
	ActionInsertRunes

	// Chat actions — navigation & control
	ActionScrollUp
	ActionScrollDown
	ActionCancelStream
	ActionToggleReasoning
	ActionToggleToolCollapse
	ActionClearChat

	// Model switching
	ActionModelNext
	ActionModelPrev

	// Dashboard actions
	ActionDashColLeft
	ActionDashColRight
	ActionDashColMiddle
	ActionDashDown
	ActionDashUp
	ActionDashTop
	ActionDashBottom
	ActionDashSelect
	ActionDashRefresh
	ActionDashModelNext
	ActionDashModelPrev
	ActionDashAgentNext
	ActionDashAgentPrev
	ActionDashSpawn
	ActionDashToolLimit
	ActionDashClearContinue
	ActionDashFilter

	// Agent view actions
	ActionAgentBack
	ActionPauseAgent
)

// resolveGlobalKey maps global key events that apply regardless of view.
func resolveGlobalKey(msg tea.KeyMsg) KeyAction {
	switch msg.Type {
	case tea.KeyCtrlC:
		return ActionQuit
	case tea.KeyShiftTab:
		return ActionCycleAgentNext
	}
	switch msg.String() {
	case "tab":
		return ActionToggleDash
	case "shift+tab", "B":
		return ActionCycleAgentNext
	}
	return ActionNone
}

// resolveChatKey maps key events for the chat view.
// mode: "streaming", "normal"
func resolveChatKey(msg tea.KeyMsg, mode string) KeyAction {
	// Scroll is always available
	switch msg.Type {
	case tea.KeyPgUp:
		return ActionScrollUp
	case tea.KeyPgDown:
		return ActionScrollDown
	}

	switch mode {
	case "streaming":
		switch msg.String() {
		case "esc":
			return ActionCancelStream
		case "ctrl+o":
			return ActionToggleReasoning
		}
		return ActionNone

	}

	// Normal mode
	switch msg.Type {
	case tea.KeyEnter:
		return ActionSendMessage
	case tea.KeyBackspace:
		return ActionDeleteCharBack
	case tea.KeyDelete:
		return ActionDeleteCharForward
	case tea.KeyLeft:
		return ActionCursorLeft
	case tea.KeyRight:
		return ActionCursorRight
	case tea.KeyHome, tea.KeyCtrlA:
		return ActionCursorHome
	case tea.KeyEnd, tea.KeyCtrlE:
		return ActionCursorEnd
	case tea.KeyCtrlU:
		return ActionDeleteToStart
	case tea.KeyCtrlK:
		return ActionDeleteToEnd
	case tea.KeySpace:
		return ActionInsertSpace
	case tea.KeyRunes:
		return ActionInsertRunes
	}

	switch msg.String() {
	case "ctrl+l":
		return ActionClearChat
	case "ctrl+o":
		return ActionToggleReasoning
	case "ctrl+t":
		return ActionToggleToolCollapse
	}

	return ActionNone
}

// resolveDashKey maps key events for the dashboard overlay.
func resolveDashKey(msg tea.KeyMsg) KeyAction {
	switch msg.String() {
	case "h", "1":
		return ActionDashColLeft
	case "l", "3":
		return ActionDashColRight
	case "2":
		return ActionDashColMiddle
	case "j", "down":
		return ActionDashDown
	case "k", "up":
		return ActionDashUp
	case "g":
		return ActionDashTop
	case "G":
		return ActionDashBottom
	case "enter":
		return ActionDashSelect
	case "r":
		return ActionDashRefresh
	case "\u00e5": // å
		return ActionDashModelNext
	case "\u00e4": // ä
		return ActionDashModelPrev
	case "p":
		return ActionDashAgentNext
	case "\u00f6": // ö
		return ActionDashAgentPrev
	case "n":
		return ActionDashSpawn
	case "t":
		return ActionDashToolLimit
	case "c":
		return ActionDashClearContinue
	case "/":
		return ActionDashFilter
	}
	return ActionNone
}

// resolveAgentViewKey maps key events specific to the agent view.
func resolveAgentViewKey(msg tea.KeyMsg) KeyAction {
	switch msg.String() {
	case "esc":
		return ActionAgentBack
	case "ctrl+p":
		return ActionPauseAgent
	}
	return ActionNone
}

// --- Bubbles help.KeyMap for chat ---

type chatKeyMap struct {
	Send      key.Binding
	Clear     key.Binding
	Tools     key.Binding
	Reasoning key.Binding
	Scroll    key.Binding
	Model     key.Binding
	Stop      key.Binding
}

func newChatKeyMap() chatKeyMap {
	return chatKeyMap{
		Send:      key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "send")),
		Clear:     key.NewBinding(key.WithKeys("ctrl+l"), key.WithHelp("ctrl+l", "clear")),
		Tools:     key.NewBinding(key.WithKeys("ctrl+t"), key.WithHelp("ctrl+t", "tools")),
		Reasoning: key.NewBinding(key.WithKeys("ctrl+o"), key.WithHelp("ctrl+o", "thinking")),
		Scroll:    key.NewBinding(key.WithKeys("pgup", "pgdn"), key.WithHelp("pgup/dn", "scroll")),
		Model:     key.NewBinding(key.WithKeys("å", "ä"), key.WithHelp("tab+å/ä", "model")),
		Stop:      key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "stop")),
	}
}

func (k chatKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Send, k.Clear, k.Tools, k.Reasoning, k.Scroll, k.Model}
}

func (k chatKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Send, k.Clear, k.Tools},
		{k.Reasoning, k.Scroll, k.Model},
	}
}

func (k chatKeyMap) StreamingHelp() []key.Binding {
	return []key.Binding{k.Stop, k.Reasoning}
}

