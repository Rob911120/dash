package main

import tea "github.com/charmbracelet/bubbletea"

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
	ActionClearChat
	ActionExitScope
	ActionRunModeContinue
	ActionExitRunMode

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

	// Agent view actions
	ActionAgentQuickSwitch
	ActionAgentBack
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
// mode: "streaming", "runMode", "normal"
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

	case "runMode":
		switch msg.String() {
		case "enter":
			return ActionRunModeContinue
		case "ctrl+o":
			return ActionToggleReasoning
		case "esc":
			return ActionExitRunMode
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
	case "esc":
		return ActionExitScope
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
	}
	return ActionNone
}

// resolveAgentViewKey maps key events specific to the agent view.
func resolveAgentViewKey(msg tea.KeyMsg) KeyAction {
	switch msg.String() {
	case "esc":
		return ActionAgentBack
	case "1", "2", "3", "4", "5", "6":
		return ActionAgentQuickSwitch
	}
	return ActionNone
}
