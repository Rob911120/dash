package main

import "github.com/charmbracelet/lipgloss"

// Cyberpunk/dark color palette
const (
	cPrimary = lipgloss.Color("#7D56F4") // Purple (HUD, accents)
	cSuccess = lipgloss.Color("#04B575") // Green (running, active)
	cAlert   = lipgloss.Color("#FF2A6D") // Red/Pink (errors, blockers)
	cWarning = lipgloss.Color("#F1FA8C") // Yellow (pending)
	cDark    = lipgloss.Color("#1A1B26") // Background
	cGray    = lipgloss.Color("#565F89") // Inactive
	cCyan    = lipgloss.Color("#22D3EE") // Info/links
	cText    = lipgloss.Color("#E4E4E7") // Primary text
	cDim     = lipgloss.Color("#414868") // Very dim
	cMagenta = lipgloss.Color("#C084FC") // Tool accent
)

var (
	// HUD
	hudBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cPrimary)

	hudLabel = lipgloss.NewStyle().
			Foreground(cPrimary).
			Bold(true)

	hudValue = lipgloss.NewStyle().
			Foreground(cText)

	hudSep = " " + lipgloss.NewStyle().Foreground(cGray).Render("|") + " "

	blockerLine = lipgloss.NewStyle().
			Foreground(cAlert).
			Bold(true)

	// Section headers (overlay columns)
	sectionHeader = lipgloss.NewStyle().
			Foreground(cSuccess).
			Bold(true)

	sectionDivider = lipgloss.NewStyle().
			Foreground(cDim)

	// Panel styles
	panelNormal = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(cGray).
			Padding(0, 1)

	panelFocused = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(cPrimary).
			Padding(0, 1)

	// Status
	statusActive  = lipgloss.NewStyle().Foreground(cSuccess).Bold(true)
	statusPending = lipgloss.NewStyle().Foreground(cWarning)
	statusBlocked = lipgloss.NewStyle().Foreground(cAlert)

	// Text styles
	textPrimary = lipgloss.NewStyle().Foreground(cText)
	textDim     = lipgloss.NewStyle().Foreground(cGray)
	textCyan    = lipgloss.NewStyle().Foreground(cCyan)
	textAlert   = lipgloss.NewStyle().Foreground(cAlert)
	textSuccess = lipgloss.NewStyle().Foreground(cSuccess)
	textWarning = lipgloss.NewStyle().Foreground(cWarning)
	textMagenta = lipgloss.NewStyle().Foreground(cMagenta)

	// Chat
	chatUser      = lipgloss.NewStyle().Foreground(cCyan)
	chatInput     = lipgloss.NewStyle().Foreground(cPrimary).Bold(true)
	chatCursor    = lipgloss.NewStyle().Foreground(cText).Bold(true)
	chatStreaming = lipgloss.NewStyle().Foreground(cPrimary).Bold(true)

	// Tool boxes
	toolBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cMagenta).
		Padding(0, 1)

	toolBoxHeader = lipgloss.NewStyle().
			Foreground(cMagenta).
			Bold(true)

	toolBoxArg = lipgloss.NewStyle().
			Foreground(cWarning)

	toolBoxDim = lipgloss.NewStyle().
			Foreground(cGray)

	// Reasoning
	reasoningText  = lipgloss.NewStyle().Foreground(cGray).Italic(true)
	reasoningLabel = lipgloss.NewStyle().Foreground(cDim).Bold(true)

	// Footer
	footerStyle = lipgloss.NewStyle().Foreground(cGray)

	// Cursor (overlay navigation)
	cursorActive = lipgloss.NewStyle().Foreground(cPrimary).Bold(true)

	// Alignment bar (proposals)
	barFilled = lipgloss.NewStyle().Foreground(cSuccess)
	barEmpty  = lipgloss.NewStyle().Foreground(cDim)

	// Scope indicator
	scopeStyle = lipgloss.NewStyle().Foreground(cPrimary).Bold(true)

	// Model name
	modelStyle = lipgloss.NewStyle().Foreground(cMagenta)

	// Agent tabs
	tabAgentActive = lipgloss.NewStyle().
			Foreground(cText).
			Background(cPrimary).
			Bold(true).
			Padding(0, 1)

	tabAgentInactive = lipgloss.NewStyle().
				Foreground(cGray).
				Background(lipgloss.Color("#2A2B3D")).
				Padding(0, 1)

	tabAgentCompleted = lipgloss.NewStyle().
				Foreground(cSuccess).
				Background(lipgloss.Color("#2A2B3D")).
				Padding(0, 1)

	tabAgentFailed = lipgloss.NewStyle().
			Foreground(cAlert).
			Background(lipgloss.Color("#2A2B3D")).
			Padding(0, 1)
)
