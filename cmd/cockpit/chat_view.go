package main

import (
	"fmt"
	"strings"
)

func (m *chatModel) View(width, height int) string {
	if m.client == nil || m.client.router == nil {
		return textDim.Render("  No LLM provider configured")
	}

	var out strings.Builder

	// Scope header
	if m.scopedPlan != "" {
		out.WriteString(scopeStyle.Render("  EXECUTING: "+m.scopedPlan) + "\n")
		height--
	} else if m.scopedTask != "" {
		out.WriteString(scopeStyle.Render("  TASK: "+m.scopedTask) + "\n")
		height--
	}

	// Build message content
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

	var content strings.Builder
	for _, msg := range m.messages {
		switch msg.Role {
		case "system-marker":
			content.WriteString(textDim.Render("  \u2500\u2500 "+msg.Content+" \u2500\u2500") + "\n")
		case "user":
			content.WriteString(chatUser.Render("  > "))
			content.WriteString(chatUser.Render(wrapText(msg.Content, contentWidth)))
			content.WriteString("\n")
		case "assistant":
			if m.showReasoning && msg.Reasoning != "" {
				content.WriteString(reasoningLabel.Render("  [thinking]") + "\n")
				for _, line := range strings.Split(wrapText(msg.Reasoning, contentWidth-2), "\n") {
					content.WriteString(reasoningText.Render("  "+line) + "\n")
				}
				content.WriteString(reasoningLabel.Render("  [/thinking]") + "\n")
			}
			if msg.Content != "" {
				content.WriteString("  ")
				content.WriteString(wrapText(msg.Content, contentWidth))
				content.WriteString("\n")
			}
			for _, tc := range msg.ToolCalls {
				if result, ok := toolResults[tc.ID]; ok {
					box := renderToolBox(tc, result, boxWidth)
					for _, line := range strings.Split(box, "\n") {
						content.WriteString("  " + line + "\n")
					}
				} else {
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
				content.WriteString(reasoningLabel.Render("  [thinking]") + "\n")
				for _, line := range strings.Split(wrapText(m.reasoningBuf, contentWidth-2), "\n") {
					content.WriteString(reasoningText.Render("  "+line) + "\n")
				}
			} else if m.streamBuf == "" {
				lines := strings.Count(m.reasoningBuf, "\n") + 1
				content.WriteString(textDim.Render(fmt.Sprintf("  thinking... (%d lines, ctrl+o to show)", lines)) + "\n")
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

	// Viewport scrolling
	allLines := strings.Split(content.String(), "\n")
	m.lines = len(allLines)

	viewHeight := height - 5 // header + divider + input + footer + margin
	if viewHeight < 1 {
		viewHeight = 1
	}

	// Clamp scroll position
	maxScroll := len(allLines) - viewHeight
	if maxScroll < 0 {
		maxScroll = 0
	}

	// Auto-follow: if user was at bottom last render, stay at bottom
	if m.scrollY >= m.lastMaxScroll {
		m.scrollY = maxScroll
	}
	m.lastMaxScroll = maxScroll

	if m.scrollY > maxScroll {
		m.scrollY = maxScroll
	}
	if m.scrollY < 0 {
		m.scrollY = 0
	}

	start := m.scrollY
	end := start + viewHeight
	if end > len(allLines) {
		end = len(allLines)
	}
	visible := allLines[start:end]

	// Scroll indicators
	if m.scrollY > 0 {
		out.WriteString(textDim.Render("  ↑ more ↑") + "\n")
	}
	// Top-aligned rendering - consistent scroll experience
	out.WriteString(strings.Join(visible, "\n"))

	if m.scrollY < maxScroll {
		out.WriteString("\n" + textDim.Render("  ↓ more ↓"))
	}

	// Input line
	prompt := chatInput.Render("  > ")
	promptLen := 4
	if m.runMode {
		if m.streaming {
			out.WriteString(textDim.Render("  executing..."))
		} else {
			out.WriteString(textDim.Render("  esc: back  enter: continue"))
		}
	} else if m.streaming {
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
	reasoningHint := "ctrl+o: thinking"
	if m.showReasoning {
		reasoningHint = "ctrl+o: hide thinking"
	}
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
	} else if m.scopedPlan != "" {
		modelInfo = textDim.Render("["+m.scopedPlan+"]") + " " + modelInfo
	} else if m.scopedTask != "" {
		modelInfo = textDim.Render("["+m.scopedTask+"]") + " " + modelInfo
	}

	meterStr := ""
	if mv := m.meter.View(); mv != "" {
		meterStr = "  " + mv
	}

	if m.runMode {
		return "esc: back  " + reasoningHint + "  " + modelInfo + meterStr
	}
	if m.streaming {
		return "esc: stop  " + reasoningHint + "  " + modelInfo + meterStr
	}
	return "enter: send  ctrl+l: clear  tab+å/ä: model  " + reasoningHint + "  pgup/dn  " + modelInfo + meterStr
}
