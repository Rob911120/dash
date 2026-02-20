package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dash"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// uiMessage holds TUI-internal messages that must not be sent to LLM APIs.
type uiMessage struct {
	Kind    string // "system-marker", "control-briefing", "control-release"
	Content string
}

// renderEntry tracks interleaving order of messages and uiMessages for TUI rendering.
type renderEntry struct {
	IsUI bool // true = uiMessages[Idx], false = messages[Idx]
	Idx  int
}

type chatModel struct {
	client    *chatClient
	d         *dash.Dash
	sessionID string
	messages   []dash.ChatMessage // Only valid API roles (user/assistant/tool/system)
	uiMessages []uiMessage       // TUI-internal markers, briefings
	renderLog  []renderEntry     // Interleaving order for TUI rendering
	input     []rune
	cursorPos int

	streaming    bool
	streamBuf    string
	reasoningBuf string
	streamCh     <-chan any
	cancelFn     context.CancelFunc

	viewport     viewport.Model
	thinkSpinner spinner.Model
	width        int
	height       int

	errMsg              string
	toolStatus          string
	toolIter            int // counts consecutive tool call rounds
	maxToolIter         int // 0 = unlimited, default 20
	consecutiveFailures int // counts rounds where ALL tool calls failed
	showReasoning       bool
	toolsCollapsed      bool

	scopedAgent  string
	agentMission string
	meter        tokenMeter
	helpModel    help.Model
	keyMap       chatKeyMap

	lastFile string // most recently touched file (from tool results)
	topFile  string // most frequently touched file
	fileCounts map[string]int // file frequency tracker

	answeringQueryInfo *pendingQuery // non-nil when this chat is answering a cross-agent query
}

// chatToolResultReady is sent when tool execution completes.
type chatToolResultReady struct {
	results []dash.ChatMessage
	calls   []streamToolCall
}

// chatToolResultWithSpawn is sent when tool execution includes a spawn_agent result.
type chatToolResultWithSpawn struct {
	results []dash.ChatMessage
	calls   []streamToolCall
	spawn   agentSpawnInfo
}

// appendMsg adds an API message and tracks it in the render log.
func (m *chatModel) appendMsg(msg dash.ChatMessage) {
	m.messages = append(m.messages, msg)
	m.renderLog = append(m.renderLog, renderEntry{IsUI: false, Idx: len(m.messages) - 1})
}

// appendMsgs adds multiple API messages and tracks them in the render log.
func (m *chatModel) appendMsgs(msgs []dash.ChatMessage) {
	for _, msg := range msgs {
		m.appendMsg(msg)
	}
}

// appendUI adds a UI message and tracks it in the render log.
func (m *chatModel) appendUI(kind, content string) {
	m.uiMessages = append(m.uiMessages, uiMessage{Kind: kind, Content: content})
	m.renderLog = append(m.renderLog, renderEntry{IsUI: true, Idx: len(m.uiMessages) - 1})
}

const maxConsecutiveFailures = 3

// handleToolResults appends tool results and counts consecutive failures.
// Returns true if the agent should continue (stream next turn), false if stopped.
func (m *chatModel) handleToolResults(results []dash.ChatMessage) bool {
	m.appendMsgs(results)
	m.toolStatus = ""
	m.scrollToBottom()

	// Count failures in this batch
	failCount := 0
	for _, r := range results {
		if r.ToolError {
			failCount++
		}
	}
	if failCount == len(results) && len(results) > 0 {
		m.consecutiveFailures++
	} else {
		m.consecutiveFailures = 0
	}

	if m.consecutiveFailures >= maxConsecutiveFailures {
		// Strip tool calls from last assistant to avoid orphaned tool_calls
		if len(m.messages) > 0 {
			for i := len(m.messages) - 1; i >= 0; i-- {
				if m.messages[i].Role == "assistant" {
					m.messages[i].ToolCalls = nil
					if m.messages[i].Content == "" {
						m.messages[i].Content = "[Stoppade — alla tool calls misslyckades 3 gånger i rad]"
					}
					break
				}
			}
		}
		m.errMsg = "Stoppade: alla tool calls misslyckades 3 iterationer i rad"
		m.consecutiveFailures = 0
		m.toolIter = 0
		return false
	}
	return true
}

func newChatModel(client *chatClient, d *dash.Dash, sessionID string) *chatModel {
	vp := viewport.New(0, 0)
	vp.MouseWheelEnabled = true
	vp.KeyMap = viewport.KeyMap{} // Disable built-in keys — we handle scroll explicitly
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = reasoningLabel
	h := help.New()
	h.Styles.ShortKey = textDim
	h.Styles.ShortDesc = textDim
	h.Styles.ShortSeparator = textDim
	return &chatModel{client: client, d: d, sessionID: sessionID, maxToolIter: 20, viewport: vp, thinkSpinner: sp, helpModel: h, keyMap: newChatKeyMap()}
}

func (m *chatModel) Update(msg tea.Msg, width, height int) tea.Cmd {
	m.width = width
	m.height = height

	// Resize viewport
	vpH := height - 2 // room for scope header + input line
	if vpH < 1 {
		vpH = 1
	}
	m.viewport.Width = width
	m.viewport.Height = vpH

	switch msg := msg.(type) {
	case tea.MouseMsg:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return cmd

	case spinner.TickMsg:
		// Spinner ticks are broadcast globally by model — just update state.
		m.tickSpinner(msg)
		return nil

	case chatReasoningMsg:
		m.reasoningBuf += msg.chunk
		return waitForChatMsg(m.streamCh, m.scopedAgent)

	case chatChunkMsg:
		m.streamBuf += msg.chunk
		return waitForChatMsg(m.streamCh, m.scopedAgent)

	case chatToolCallMsg:
		m.streaming = false
		m.toolIter++
		var names []string
		for _, c := range msg.calls {
			names = append(names, c.Name)
			// Track file paths from tool call args
			var args map[string]any
			if json.Unmarshal([]byte(c.ArgsBuf.String()), &args) == nil {
				m.trackFileFromTool(c.Name, args)
			}
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

		var tcs []dash.ToolCallRef
		for _, c := range msg.calls {
			tcs = append(tcs, dash.ToolCallRef{
				ID:   c.ID,
				Type: "function",
				Function: dash.ToolCallFunc{
					Name:      c.Name,
					Arguments: c.ArgsBuf.String(),
				},
			})
		}

		assistantMsg := dash.ChatMessage{
			Role:      "assistant",
			Content:   m.streamBuf,
			ToolCalls: tcs,
			Reasoning: m.reasoningBuf,
		}
		m.appendMsg(assistantMsg)
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
				assistantMsg := dash.ChatMessage{Role: "assistant", Content: m.streamBuf, Reasoning: m.reasoningBuf}
				m.appendMsg(assistantMsg)
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
			m.appendMsg(dash.ChatMessage{Role: "assistant", Content: m.streamBuf, Reasoning: m.reasoningBuf})
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
	}

	action := resolveChatKey(msg, mode)
	switch action {
	case ActionScrollUp:
		m.viewport.HalfViewUp()
		return nil
	case ActionScrollDown:
		m.viewport.HalfViewDown()
		return nil
	case ActionCancelStream:
		if m.cancelFn != nil {
			m.cancelFn()
		}
	case ActionToggleReasoning:
		m.showReasoning = !m.showReasoning
	case ActionToggleToolCollapse:
		m.toolsCollapsed = !m.toolsCollapsed
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
		m.uiMessages = nil
		m.renderLog = nil
		m.errMsg = ""
		m.viewport.GotoTop()
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
	m.appendMsg(dash.ChatMessage{Role: "user", Content: text})
	return m.startStream()
}

func (m *chatModel) startStream() tea.Cmd {
	var sysPrompt string
	if m.meter.exchanges == 0 {
		sysPrompt = m.systemPrompt()       // Full profil (mission, tasks, constraints, etc.)
	} else {
		sysPrompt = m.continuationPrompt() // Kort nudge (~100 chars)
	}
	apiMsgs := []dash.ChatMessage{{Role: "system", Content: sysPrompt}}

	// Compress old tool results to save context
	apiMsgs = append(apiMsgs, m.compressedConversationMessages()...)

	// Sync meter limit with model's actual context window
	if m.client != nil {
		m.meter.limit = m.client.contextLimit()
	}

	// Filter tools per profile toolset
	tools := m.client.tools
	if filtered := m.filteredTools(); filtered != nil {
		tools = filtered
	}

	ctx, cancel := context.WithCancel(dash.WithLLMAgent(context.Background(), m.scopedAgent))
	ch := make(chan any, 64)
	m.streaming = true
	m.streamBuf = ""
	m.reasoningBuf = ""
	m.streamCh = ch
	m.cancelFn = cancel
	m.scrollToBottom()

	go m.client.StreamWithTools(ctx, apiMsgs, tools, ch)
	return waitForChatMsg(ch, m.scopedAgent)
}

// filteredTools returns tools filtered by the current profile's toolset,
// or nil if no filtering should be applied (empty toolset = all tools).
func (m *chatModel) filteredTools() []map[string]any {
	if m.d == nil || m.client == nil {
		return nil
	}

	var profileName string
	switch {
	case m.scopedAgent == "orchestrator":
		profileName = "orchestrator"
	case m.scopedAgent != "":
		profileName = "agent-continuous"
	default:
		return nil // default/compact profiles use all tools
	}

	profile, err := m.d.GetProfile(context.Background(), profileName)
	if err != nil || profile == nil || len(profile.Toolset) == 0 {
		return nil
	}

	// Build allowed tool set
	allowed := make(map[string]bool, len(profile.Toolset))
	for _, t := range profile.Toolset {
		allowed[t] = true
	}

	// Filter tools
	var filtered []map[string]any
	for _, tool := range m.client.tools {
		fn, ok := tool["function"].(map[string]any)
		if !ok {
			continue
		}
		name, _ := fn["name"].(string)
		if allowed[name] {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}



func (m *chatModel) switchModel() tea.Cmd {
	if m.streaming || m.client == nil {
		return nil
	}
	oldModel := m.client.model
	newModel := m.client.cycleModel()
	m.appendUI("system-marker", "\u2192 "+newModel)
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
	m.appendUI("system-marker", "\u2192 "+newModel)
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
			"from":      from,
			"to":        to,
			"exchanges": m.meter.exchanges,
			"agent":     m.scopedAgent,
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

// tickSpinner advances the spinner animation if streaming.
func (m *chatModel) tickSpinner(msg spinner.TickMsg) {
	if m.streaming {
		m.thinkSpinner, _ = m.thinkSpinner.Update(msg)
	}
}

func (m *chatModel) scrollToBottom() {
	m.viewport.GotoBottom()
}


// cycleToolLimit toggles maxToolIter: 0(∞) ↔ 20 (safety cap)
func (m *chatModel) cycleToolLimit() {
	if m.maxToolIter == 0 {
		m.maxToolIter = 20
	} else {
		m.maxToolIter = 0
	}
}

// addSystemMessage adds a UI message to the chat (e.g., from observation agent)
func (m *chatModel) addSystemMessage(content string) {
	m.appendUI("system-marker", content)
	m.scrollToBottom()
}
