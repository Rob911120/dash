package main

import (
	"fmt"
	"strings"
)

// renderMessages builds the scrollable chat content string.
func (m *chatModel) renderMessages(width int) string {
	contentWidth := width - 4
	if contentWidth < 20 {
		contentWidth = 20
	}

	// Map of tool results by call ID
	toolResults := make(map[string]string)
	for _, msg := range m.messages {
		if msg.Role == "tool" && msg.ToolCallID != "" {
			toolResults[msg.ToolCallID] = msg.Content
		}
	}

	boxWidth := contentWidth - 2
	if boxWidth < 24 {
		boxWidth = 24
	}

	// Find index of the last assistant message (for collapsible tool logic)
	lastAssistantIdx := -1
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].Role == "assistant" {
			lastAssistantIdx = i
			break
		}
	}

	var content strings.Builder
	for i, msg := range m.messages {
		switch msg.Role {
		case "system-marker":
			content.WriteString(textDim.Render("  \u2500\u2500 "+msg.Content+" \u2500\u2500") + "\n")
		case "control-briefing":
			box := controlBriefingBox.Width(boxWidth).Render(msg.Content)
			for _, line := range strings.Split(box, "\n") {
				content.WriteString("  " + line + "\n")
			}
		case "control-release":
			content.WriteString(textCyan.Render("  ── "+msg.Content+" ──") + "\n")
		case "user":
			content.WriteString(chatUser.Render("  > "))
			content.WriteString(chatUser.Render(wrapText(msg.Content, contentWidth)))
			content.WriteString("\n")
		case "assistant":
			if m.showReasoning && msg.Reasoning != "" {
				wrapped := wrapText(msg.Reasoning, contentWidth-4)
				content.WriteString("  " + reasoningBlock.Render(wrapped) + "\n")
			}
			if msg.Content != "" {
				content.WriteString("  ")
				content.WriteString(wrapText(msg.Content, contentWidth))
				content.WriteString("\n")
			}

			// Collapse completed tool calls for older messages when toolsCollapsed is true
			isLastAssistant := i == lastAssistantIdx
			for _, tc := range msg.ToolCalls {
				if result, ok := toolResults[tc.ID]; ok {
					if m.toolsCollapsed && !isLastAssistant {
						// Collapsed: one-line badge
						content.WriteString("  " + renderToolBadge(tc, result, boxWidth) + "\n")
					} else {
						box := renderToolBox(tc, result, boxWidth)
						for _, line := range strings.Split(box, "\n") {
							content.WriteString("  " + line + "\n")
						}
					}
				} else {
					// Pending: always expanded
					box := renderToolBoxPending(tc, boxWidth)
					for _, line := range strings.Split(box, "\n") {
						content.WriteString("  " + line + "\n")
					}
				}
			}
		}
	}

	// Streaming content
	if m.streaming {
		if m.reasoningBuf != "" {
			if m.showReasoning {
				wrapped := wrapText(m.reasoningBuf, contentWidth-4)
				content.WriteString("  " + reasoningBlock.Render(wrapped) + "\n")
			} else if m.streamBuf == "" {
				lines := strings.Count(m.reasoningBuf, "\n") + 1
				content.WriteString("  " + m.thinkSpinner.View() + textDim.Render(fmt.Sprintf(" thinking (%d lines)", lines)) + "\n")
			}
		}
		content.WriteString("  ")
		if m.streamBuf != "" {
			content.WriteString(wrapText(m.streamBuf, contentWidth))
		}
		content.WriteString(chatStreaming.Render(" ~"))
		content.WriteString("\n")
	}

	if m.toolStatus != "" {
		content.WriteString(textDim.Render("  "+m.toolStatus) + "\n")
	}
	if m.errMsg != "" {
		content.WriteString(textAlert.Render("  Error: "+m.errMsg) + "\n")
	}

	return content.String()
}

func (m *chatModel) View(width, height int) string {
	if m.client == nil || m.client.router == nil {
		return textDim.Render("  No LLM provider configured")
	}

	var out strings.Builder

	// Resize viewport for this frame
	vpH := height - 1 // -1 for input line
	if vpH < 1 {
		vpH = 1
	}
	m.viewport.Width = width
	m.viewport.Height = vpH

	// Build and set content
	content := m.renderMessages(width)
	wasAtBottom := m.viewport.AtBottom()
	m.viewport.SetContent(content)
	if wasAtBottom || m.streaming {
		m.viewport.GotoBottom()
	}

	// Render viewport
	out.WriteString(m.viewport.View())

	// Input line
	out.WriteString("\n")
	var prompt string
	promptLen := 4
	if m.scopedAgent == "orchestrator" {
		prompt = chatInput.Render("  [CMD] > ")
		promptLen = 10
	} else if m.scopedAgent != "" {
		prompt = chatInput.Render("  [" + m.scopedAgent + "] > ")
		promptLen = len(m.scopedAgent) + 6
	} else {
		prompt = chatInput.Render("  > ")
	}
	if m.streaming {
		out.WriteString(prompt + textDim.Render("streaming..."))
	} else {
		inputStr := string(m.input)
		inputWidth := width - promptLen - 1
		if inputWidth < 10 {
			inputWidth = 10
		}
		if len(inputStr) <= inputWidth {
			before := string(m.input[:m.cursorPos])
			after := string(m.input[m.cursorPos:])
			out.WriteString(prompt + before + chatCursor.Render("|") + after)
		} else {
			start := m.cursorPos - inputWidth/2
			if start < 0 {
				start = 0
			}
			end := start + inputWidth
			if end > len(m.input) {
				end = len(m.input)
				start = end - inputWidth
				if start < 0 {
					start = 0
				}
			}
			vis := string(m.input[start:end])
			cursorInWindow := m.cursorPos - start
			before := vis[:cursorInWindow]
			after := vis[cursorInWindow:]
			prefix := ""
			if start > 0 {
				prefix = textDim.Render("<")
			}
			suffix := ""
			if end < len(m.input) {
				suffix = textDim.Render(">")
			}
			out.WriteString(prompt + prefix + before + chatCursor.Render("|") + after + suffix)
		}
	}

	return out.String()
}

func (m *chatModel) FooterHelp() string {
	// Mode-specific help bindings
	var helpStr string
	if m.streaming {
		m.helpModel.ShowAll = false
		helpStr = m.helpModel.ShortHelpView(m.keyMap.StreamingHelp())
	} else {
		m.helpModel.ShowAll = false
		helpStr = m.helpModel.ShortHelpView(m.keyMap.ShortHelp())
	}

	// Model info suffix
	shortModel := m.client.model
	if idx := strings.LastIndex(shortModel, "/"); idx >= 0 {
		shortModel = shortModel[idx+1:]
	}
	toolLimitStr := fmt.Sprintf("tools:%d", m.maxToolIter)
	if m.maxToolIter == 0 {
		toolLimitStr = "tools:∞"
	}
	modelInfo := modelStyle.Render(shortModel) + " " + textDim.Render(toolLimitStr)
	if m.scopedAgent != "" {
		modelInfo = textDim.Render("["+m.scopedAgent+"]") + " " + modelInfo
	}

	meterStr := ""
	if mv := m.meter.View(); mv != "" {
		meterStr = "  " + mv
	}

	return helpStr + "  " + modelInfo + meterStr
}
