package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dash"

	tea "github.com/charmbracelet/bubbletea"
)

type chatModel struct {
	client    *chatClient
	d         *dash.Dash
	sessionID string
	messages  []chatMessage
	input     []rune
	cursorPos int

	streaming    bool
	streamBuf    string
	reasoningBuf string
	streamCh     <-chan any
	cancelFn     context.CancelFunc

	scrollY int
	lines   int
	width   int
	height  int

	errMsg        string
	toolStatus    string
	toolIter      int // counts consecutive tool call rounds
	maxToolIter   int // 0 = unlimited, default 5
	showReasoning bool
	runMode       bool

	scopedPlan string
	scopedTask string

	scopedAgent  string
	agentMission string
	meter         tokenMeter
	lastMaxScroll int // tracks if user was at bottom last render
}

// chatToolResultReady is sent when tool execution completes.
type chatToolResultReady struct {
	results []chatMessage
	calls   []streamToolCall
}

// chatToolResultWithSpawn is sent when tool execution includes a spawn_agent result.
type chatToolResultWithSpawn struct {
	results []chatMessage
	calls   []streamToolCall
	spawn   agentSpawnInfo
}

func newChatModel(client *chatClient, d *dash.Dash, sessionID string) *chatModel {
	return &chatModel{client: client, d: d, sessionID: sessionID, maxToolIter: 5}
}

func (m *chatModel) Update(msg tea.Msg, width, height int) tea.Cmd {
	m.width = width
	m.height = height

	switch msg := msg.(type) {
	case tea.MouseMsg:
		return m.handleMouse(tea.MouseEvent(msg))

	case chatReasoningMsg:
		m.reasoningBuf += msg.chunk
		return waitForChatMsg(m.streamCh)

	case chatChunkMsg:
		m.streamBuf += msg.chunk
		return waitForChatMsg(m.streamCh)

	case chatToolCallMsg:
		m.streaming = false
		m.toolIter++
		var names []string
		for _, c := range msg.calls {
			names = append(names, c.Name)
		}
		limitStr := fmt.Sprintf("%d", m.maxToolIter)
		if m.maxToolIter == 0 {
			limitStr = "∞"
		}
		m.toolStatus = fmt.Sprintf("calling %s... (%d/%s)", strings.Join(names, ", "), m.toolIter, limitStr)
		if m.cancelFn != nil {
			m.cancelFn()
			m.cancelFn = nil
		}

		var tcs []toolCall
		for _, c := range msg.calls {
			tcs = append(tcs, toolCall{
				ID:   c.ID,
				Type: "function",
				Function: toolFunction{
					Name:      c.Name,
					Arguments: c.ArgsBuf.String(),
				},
			})
		}

		assistantMsg := chatMessage{
			Role:      "assistant",
			Content:   m.streamBuf,
			ToolCalls: tcs,
			Reasoning: m.reasoningBuf,
		}
		m.messages = append(m.messages, assistantMsg)
		m.storeReasoning(m.reasoningBuf, m.streamBuf)
		m.streamBuf = ""
		m.reasoningBuf = ""
		m.streamCh = nil

		// Prevent infinite tool-call loops (0 = unlimited)
		if m.maxToolIter > 0 && m.toolIter >= m.maxToolIter {
			// Replace last assistant message (with ToolCalls) with text-only
			// to avoid orphaned tool_calls in the API sequence
			if len(m.messages) > 0 {
				last := &m.messages[len(m.messages)-1]
				last.ToolCalls = nil
				if last.Content == "" {
					last.Content = "[Tool calls avbrutna — limit nådd]"
				} else {
					last.Content += "\n[Tool calls avbrutna — limit nådd]"
				}
			}
			m.toolStatus = ""
			m.errMsg = fmt.Sprintf("Stoppade efter %d tool-iterationer — skriv 'fortsätt' för fler", m.maxToolIter)
			m.toolIter = 0
			return nil
		}

		return m.executeTools(msg.calls)

	case chatDoneMsg:
		if m.streaming {
			m.streaming = false
			m.toolStatus = ""
			if m.streamBuf != "" {
				assistantMsg := chatMessage{Role: "assistant", Content: m.streamBuf, Reasoning: m.reasoningBuf}
				m.messages = append(m.messages, assistantMsg)
				m.storeReasoning(m.reasoningBuf, m.streamBuf)
				m.meter.addExchange()
			} else if m.reasoningBuf == "" {
				m.errMsg = fmt.Sprintf("Inget svar från %s — testa en annan modell (tab+å/ä)", m.client.model)
			}
			if msg.usage != nil {
				m.meter.set(msg.usage.PromptTokens, msg.usage.CompletionTokens)
			}
			m.streamBuf = ""
			m.reasoningBuf = ""
			m.streamCh = nil
			if m.cancelFn != nil {
				m.cancelFn()
				m.cancelFn = nil
			}

			// Auto-rotate at 85% context usage
			if pct := m.meter.pct(); pct >= 85 {
				m.clearAndContinue()
				m.addSystemMessage(fmt.Sprintf("⚡ Context auto-roterad vid %d%% — konversationen fortsätter.", pct))
			}

			m.scrollToBottom()
		}
		return nil

	case chatErrorMsg:
		m.streaming = false
		m.toolStatus = ""
		m.errMsg = msg.err.Error()
		if m.streamBuf != "" {
			m.messages = append(m.messages, chatMessage{Role: "assistant", Content: m.streamBuf, Reasoning: m.reasoningBuf})
			m.streamBuf = ""
			m.reasoningBuf = ""
		}
		m.streamCh = nil
		if m.cancelFn != nil {
			m.cancelFn()
			m.cancelFn = nil
		}
		return nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return nil
}

func (m *chatModel) handleKey(msg tea.KeyMsg) tea.Cmd {
	// Determine mode string for keybinding resolution
	mode := "normal"
	if m.streaming {
		mode = "streaming"
	} else if m.runMode {
		mode = "runMode"
	}

	action := resolveChatKey(msg, mode)
	switch action {
	case ActionScrollUp:
		m.scrollUp(m.height / 2)
		return func() tea.Msg { return tickMsg{} }
	case ActionScrollDown:
		m.scrollDown(m.height / 2)
		return func() tea.Msg { return tickMsg{} }
	case ActionCancelStream:
		if m.cancelFn != nil {
			m.cancelFn()
		}
	case ActionToggleReasoning:
		m.showReasoning = !m.showReasoning
	case ActionRunModeContinue:
		m.messages = append(m.messages, chatMessage{
			Role: "user", Content: "Fortsätt med nästa steg.",
		})
		return m.startStream()
	case ActionExitRunMode:
		m.runMode = false
		m.scopedPlan = ""
	case ActionSendMessage:
		return m.sendMessage()
	case ActionDeleteCharBack:
		if m.cursorPos > 0 {
			m.input = append(m.input[:m.cursorPos-1], m.input[m.cursorPos:]...)
			m.cursorPos--
		}
	case ActionDeleteCharForward:
		if m.cursorPos < len(m.input) {
			m.input = append(m.input[:m.cursorPos], m.input[m.cursorPos+1:]...)
		}
	case ActionCursorLeft:
		if m.cursorPos > 0 {
			m.cursorPos--
		}
	case ActionCursorRight:
		if m.cursorPos < len(m.input) {
			m.cursorPos++
		}
	case ActionCursorHome:
		m.cursorPos = 0
	case ActionCursorEnd:
		m.cursorPos = len(m.input)
	case ActionDeleteToStart:
		m.input = m.input[m.cursorPos:]
		m.cursorPos = 0
	case ActionDeleteToEnd:
		m.input = m.input[:m.cursorPos]
	case ActionInsertSpace:
		newInput := make([]rune, 0, len(m.input)+1)
		newInput = append(newInput, m.input[:m.cursorPos]...)
		newInput = append(newInput, ' ')
		newInput = append(newInput, m.input[m.cursorPos:]...)
		m.input = newInput
		m.cursorPos++
	case ActionInsertRunes:
		runes := msg.Runes
		newInput := make([]rune, 0, len(m.input)+len(runes))
		newInput = append(newInput, m.input[:m.cursorPos]...)
		newInput = append(newInput, runes...)
		newInput = append(newInput, m.input[m.cursorPos:]...)
		m.input = newInput
		m.cursorPos += len(runes)
	case ActionClearChat:
		m.messages = nil
		m.errMsg = ""
		m.scrollY = 0
		m.runMode = false
		m.scopedPlan = ""
		m.scopedTask = ""
	case ActionExitScope:
		if m.scopedTask != "" || m.scopedPlan != "" {
			m.scopedTask = ""
			m.scopedPlan = ""
			m.messages = nil
			m.errMsg = ""
			m.scrollY = 0
		}
	}
	return nil
}

func (m *chatModel) sendMessage() tea.Cmd {
	text := strings.TrimSpace(string(m.input))
	if text == "" || m.client == nil || m.client.router == nil {
		return nil
	}
	m.input = nil
	m.cursorPos = 0
	m.errMsg = ""
	m.toolIter = 0
	m.scrollToBottom()
	m.messages = append(m.messages, chatMessage{Role: "user", Content: text})
	return m.startStream()
}

func (m *chatModel) startStream() tea.Cmd {
	var sysPrompt string
	if m.meter.exchanges == 0 {
		sysPrompt = m.systemPrompt()       // Full profil (mission, tasks, constraints, etc.)
	} else {
		sysPrompt = m.continuationPrompt() // Kort nudge (~100 chars)
	}
	apiMsgs := []chatMessage{{Role: "system", Content: sysPrompt}}

	// Compress old tool results to save context
	apiMsgs = append(apiMsgs, m.compressedConversationMessages()...)

	// Sync meter limit with model's actual context window
	if m.client != nil {
		m.meter.limit = m.client.contextLimit()
	}

	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan any, 64)
	m.streaming = true
	m.streamBuf = ""
	m.reasoningBuf = ""
	m.streamCh = ch
	m.cancelFn = cancel
	m.scrollToBottom()

	go m.client.Stream(ctx, apiMsgs, ch)
	return waitForChatMsg(ch)
}



func (m *chatModel) switchModel() tea.Cmd {
	if m.streaming || m.client == nil {
		return nil
	}
	oldModel := m.client.model
	newModel := m.client.cycleModel()
	m.messages = append(m.messages, chatMessage{
		Role:    "system-marker",
		Content: "\u2192 " + newModel,
	})
	m.scrollToBottom()
	m.logModelSwitch(oldModel, newModel)
	return nil
}

func (m *chatModel) switchModelBack() tea.Cmd {
	if m.streaming || m.client == nil {
		return nil
	}
	oldModel := m.client.model
	newModel := m.client.cycleModelBack()
	m.messages = append(m.messages, chatMessage{
		Role:    "system-marker",
		Content: "\u2192 " + newModel,
	})
	m.scrollToBottom()
	m.logModelSwitch(oldModel, newModel)
	return nil
}

func (m *chatModel) logModelSwitch(from, to string) {
	if m.d == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		m.d.StoreObservation(ctx, m.sessionID, "model_switch", map[string]any{
			"from":       from,
			"to":         to,
			"exchanges":  m.meter.exchanges,
			"task":       m.scopedTask,
			"plan":       m.scopedPlan,
			"agent":      m.scopedAgent,
		})
	}()
}

func (m *chatModel) storeReasoning(reasoning, response string) {
	if reasoning == "" || m.d == nil {
		return
	}
	if len(reasoning) > 2000 {
		reasoning = reasoning[:2000] + "..."
	}
	summary := response
	if len(summary) > 200 {
		summary = summary[:200] + "..."
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		m.d.StoreObservation(ctx, m.sessionID, "agent_reasoning", map[string]any{
			"reasoning": reasoning,
			"response":  summary,
			"model":     m.client.model,
		})
	}()
}

func (m *chatModel) handleMouse(me tea.MouseEvent) tea.Cmd {
	switch me.Button {
	case tea.MouseButtonWheelUp:
		m.scrollY--
		if m.scrollY < 0 {
			m.scrollY = 0
		}
	case tea.MouseButtonWheelDown:
		m.scrollY++
		if m.lines > 0 && m.scrollY > m.lines {
			m.scrollY = m.lines
		}
	}
	return nil
}

func (m *chatModel) scrollUp(n int) {
	m.scrollY -= n
	if m.scrollY < 0 {
		m.scrollY = 0
	}
}

func (m *chatModel) scrollDown(n int) {
	m.scrollY += n
	if m.lines > 0 && m.scrollY > m.lines {
		m.scrollY = m.lines
	}
}

func (m *chatModel) scrollToBottom() {
	m.scrollY = 999999 // Will be clamped in View()
}


// cycleToolLimit cycles maxToolIter: 5 → 10 → 20 → 0(∞) → 5
func (m *chatModel) cycleToolLimit() {
	switch m.maxToolIter {
	case 5:
		m.maxToolIter = 10
	case 10:
		m.maxToolIter = 20
	case 20:
		m.maxToolIter = 0
	default:
		m.maxToolIter = 5
	}
}

// addSystemMessage adds a system message to the chat (e.g., from observation agent)
func (m *chatModel) addSystemMessage(content string) {
	m.messages = append(m.messages, chatMessage{
		Role:    "system-marker",
		Content: content,
	})
	m.scrollToBottom()
}
