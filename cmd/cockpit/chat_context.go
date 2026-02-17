package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"dash"
)

// conversationMessages returns all non-system-marker messages.
func (m *chatModel) conversationMessages() []chatMessage {
	var msgs []chatMessage
	for _, msg := range m.messages {
		if msg.Role != "system-marker" {
			msgs = append(msgs, msg)
		}
	}
	return msgs
}

// compressedConversationMessages returns conversation messages with old tool results compressed.
func (m *chatModel) compressedConversationMessages() []chatMessage {
	msgs := m.conversationMessages()
	if len(msgs) <= 6 {
		return msgs
	}

	// Find the start of the last 3 user turns to protect them
	protectFrom := len(msgs)
	turnsFound := 0
	for i := len(msgs) - 1; i >= 0 && turnsFound < 3; i-- {
		if msgs[i].Role == "user" {
			turnsFound++
		}
		protectFrom = i
	}

	result := make([]chatMessage, len(msgs))
	copy(result, msgs)

	for i := 0; i < protectFrom; i++ {
		if result[i].Role == "tool" && len(result[i].Content) > 300 {
			result[i] = chatMessage{
				Role:       "tool",
				Name:       result[i].Name,
				Content:    summarizeToolResult(result[i].Name, result[i].Content),
				ToolCallID: result[i].ToolCallID,
			}
		}
	}
	return result
}

// clearAndContinue summarizes the conversation and resets for a fresh context window.
func (m *chatModel) clearAndContinue() {
	summary := buildConversationSummary(m.conversationMessages())
	m.messages = []chatMessage{
		{Role: "system-marker", Content: "--- session rotated ---"},
		{Role: "user", Content: "[Sammanfattning av session]\n" + summary},
		{Role: "assistant", Content: "Förstått. Jag fortsätter med denna kontext."},
	}
	m.meter = newTokenMeter(m.meter.limit)
	m.toolIter = 0
	m.errMsg = ""
	m.toolStatus = ""
	m.streamBuf = ""
	m.reasoningBuf = ""
}

// buildConversationSummary creates a compact summary of messages.
func buildConversationSummary(msgs []chatMessage) string {
	var b strings.Builder

	// Session metadata header
	toolNames := uniqueToolNames(msgs)
	turnCount := countUserTurns(msgs)
	b.WriteString(fmt.Sprintf("Session: %d turer, verktyg: %s\n---\n",
		turnCount, strings.Join(toolNames, ", ")))

	// Build per-message summaries
	type entry struct {
		role string
		text string
	}
	var entries []entry

	for _, msg := range msgs {
		switch msg.Role {
		case "user":
			text := msg.Content
			if len(text) > 200 {
				text = text[:200] + "..."
			}
			entries = append(entries, entry{"user", "USER: " + text})
		case "assistant":
			text := msg.Content
			if len(msg.ToolCalls) > 0 {
				var names []string
				for _, tc := range msg.ToolCalls {
					names = append(names, tc.Function.Name)
				}
				text += " [tools: " + strings.Join(names, ", ") + "]"
			}
			if len(text) > 300 {
				text = text[:300] + "..."
			}
			entries = append(entries, entry{"assistant", "ASSISTANT: " + text})
		case "tool":
			text := summarizeToolResult(msg.Name, msg.Content)
			entries = append(entries, entry{"tool", "TOOL(" + msg.Name + "): " + text})
		}
	}

	// Always keep the last 3 user/assistant turns fully
	keepFrom := len(entries)
	turnsKept := 0
	for i := len(entries) - 1; i >= 0 && turnsKept < 3; i-- {
		if entries[i].role == "user" || entries[i].role == "assistant" {
			turnsKept++
		}
		keepFrom = i
	}

	const maxLen = 6000
	headerLen := b.Len()

	// Write entries, respecting total cap but never cutting protected tail
	var body strings.Builder
	for i, e := range entries {
		line := e.text + "\n"
		if i < keepFrom && headerLen+body.Len()+len(line) > maxLen-1500 {
			// Reserve ~1500 chars for the protected tail
			body.WriteString("... (klippt)\n")
			// Skip ahead to protected tail
			for j := i + 1; j < keepFrom; j++ {
				_ = entries[j] // skip
			}
			for j := keepFrom; j < len(entries); j++ {
				body.WriteString(entries[j].text + "\n")
			}
			b.WriteString(body.String())
			return b.String()
		}
		body.WriteString(line)
	}

	b.WriteString(body.String())
	return b.String()
}

// summarizeToolResult extracts meaningful info from tool results instead of blind truncation.
func summarizeToolResult(name, content string) string {
	switch name {
	case "working_set":
		return summarizeWorkingSet(content)
	case "traverse":
		return summarizeTraverse(content)
	case "tasks":
		return summarizeTasks(content)
	case "node":
		return summarizeNode(content)
	case "query":
		return summarizeQuery(content)
	}
	// Default: truncate
	if len(content) > 150 {
		return content[:150] + "..."
	}
	return content
}

func summarizeWorkingSet(content string) string {
	var data map[string]any
	if err := json.Unmarshal([]byte(content), &data); err != nil {
		return truncate(content, 150)
	}
	var parts []string
	if m, ok := data["mission"].(string); ok && m != "" {
		if len(m) > 60 {
			m = m[:60] + "..."
		}
		parts = append(parts, "mission: "+m)
	}
	if tasks, ok := data["tasks"].([]any); ok {
		parts = append(parts, fmt.Sprintf("%d tasks", len(tasks)))
	}
	if cf, ok := data["context_frame"].(map[string]any); ok && cf != nil {
		parts = append(parts, "context_frame")
	}
	if insights, ok := data["insights"].([]any); ok && len(insights) > 0 {
		parts = append(parts, fmt.Sprintf("%d insights", len(insights)))
	}
	if len(parts) == 0 {
		return truncate(content, 150)
	}
	return strings.Join(parts, " + ")
}

func summarizeTraverse(content string) string {
	var data map[string]any
	if err := json.Unmarshal([]byte(content), &data); err != nil {
		return truncate(content, 150)
	}
	if path, ok := data["path"].([]any); ok && len(path) > 0 {
		var names []string
		for _, p := range path {
			if node, ok := p.(map[string]any); ok {
				if n, ok := node["name"].(string); ok {
					names = append(names, n)
				}
			}
		}
		if len(names) > 0 {
			return "path: " + strings.Join(names, " → ")
		}
	}
	return "null (no path found)"
}

func summarizeTasks(content string) string {
	var data any
	if err := json.Unmarshal([]byte(content), &data); err != nil {
		return truncate(content, 150)
	}
	if tasks, ok := data.([]any); ok {
		var names []string
		for _, t := range tasks {
			if task, ok := t.(map[string]any); ok {
				if n, ok := task["name"].(string); ok {
					names = append(names, n)
				}
			}
		}
		result := fmt.Sprintf("%d tasks", len(tasks))
		if len(names) > 0 {
			list := strings.Join(names, ", ")
			if len(list) > 100 {
				list = list[:100] + "..."
			}
			result += " (" + list + ")"
		}
		return result
	}
	// Could be a map wrapper
	if m, ok := data.(map[string]any); ok {
		if tasks, ok := m["tasks"].([]any); ok {
			return fmt.Sprintf("%d tasks", len(tasks))
		}
	}
	return truncate(content, 150)
}

func summarizeNode(content string) string {
	var data map[string]any
	if err := json.Unmarshal([]byte(content), &data); err != nil {
		return truncate(content, 150)
	}
	typ, _ := data["type"].(string)
	name, _ := data["name"].(string)
	if typ != "" || name != "" {
		return fmt.Sprintf("%s.%s", typ, name)
	}
	return truncate(content, 150)
}

func summarizeQuery(content string) string {
	var data any
	if err := json.Unmarshal([]byte(content), &data); err != nil {
		return truncate(content, 150)
	}
	if rows, ok := data.([]any); ok {
		return fmt.Sprintf("%d rows", len(rows))
	}
	if m, ok := data.(map[string]any); ok {
		if rows, ok := m["rows"].([]any); ok {
			return fmt.Sprintf("%d rows", len(rows))
		}
	}
	return truncate(content, 150)
}

func uniqueToolNames(msgs []chatMessage) []string {
	seen := make(map[string]bool)
	var names []string
	for _, msg := range msgs {
		if msg.Role == "tool" && msg.Name != "" && !seen[msg.Name] {
			seen[msg.Name] = true
			names = append(names, msg.Name)
		}
	}
	return names
}

func countUserTurns(msgs []chatMessage) int {
	count := 0
	for _, msg := range msgs {
		if msg.Role == "user" {
			count++
		}
	}
	return count
}

func (m *chatModel) systemPrompt() string {
	if m.d == nil {
		return "Du är en AI-agent med åtkomst till en grafdatabas. Svara på svenska."
	}

	var profileName string
	var opts dash.PromptOptions

	switch {
	case m.scopedAgent == "orchestrator":
		profileName = "orchestrator"
		opts.AgentKey = m.scopedAgent
		opts.AgentMission = m.agentMission
	case m.scopedAgent != "":
		profileName = "agent-continuous"
		opts.AgentKey = m.scopedAgent
		opts.AgentMission = m.agentMission
	default:
		profileName = "orchestrator" // fallback
	}

	opts.ContextPressurePct = m.meter.pct()

	text, err := m.d.GetPrompt(context.Background(), profileName, opts)
	if err != nil {
		return "Du är en AI-agent med åtkomst till en grafdatabas. Svara på svenska."
	}

	// Cross-agent query injection
	if m.answeringQueryInfo != nil {
		text += fmt.Sprintf("\n\nDu svarar just nu på en fråga (query_id: %s) från %s.\nNär du har ett svar, AVSLUTA med: answer_query(query_id=\"%s\", answer=\"ditt svar här\")\n", m.answeringQueryInfo.id, m.answeringQueryInfo.callerKey, m.answeringQueryInfo.id)
	}

	return text
}

// continuationPrompt returns a short nudge for non-first exchanges (after full system prompt was sent).
func (m *chatModel) continuationPrompt() string {
	var b strings.Builder
	b.WriteString("== DASH ==\n")
	b.WriteString("VERKTYG: working_set, query, remember, node, tasks\n")

	// Active work order nudge for agents
	if m.scopedAgent != "" && m.d != nil {
		wo, _ := m.d.GetActiveWorkOrderForAgent(context.Background(), m.scopedAgent)
		if wo != nil {
			b.WriteString(fmt.Sprintf("WORK ORDER: %s [%s]", wo.Node.Name, wo.Status))
			if wo.BranchName != "" {
				b.WriteString(fmt.Sprintf(" branch:%s", wo.BranchName))
			}
			b.WriteString("\n")
		}
	}

	// Planner-specific nudge
	if m.scopedAgent == "planner-agent" {
		b.WriteString("PLAN-REQUESTS: kolla plan(op=\"list\") för aktiva planer\n")
	}

	// File nudge: most recent + most frequent
	if m.lastFile != "" {
		b.WriteString(fmt.Sprintf("SENASTE FIL: %s\n", m.lastFile))
	}
	if m.topFile != "" && m.topFile != m.lastFile {
		b.WriteString(fmt.Sprintf("VANLIGASTE FIL: %s\n", m.topFile))
	}

	if pct := m.meter.pct(); pct >= 70 {
		b.WriteString(fmt.Sprintf("CONTEXT: %d%% — sammanfatta snart.\n", pct))
	}

	// Cross-agent query injection
	if m.answeringQueryInfo != nil {
		b.WriteString(fmt.Sprintf("\nDu svarar just nu på en fråga (query_id: %s) från %s.\n", m.answeringQueryInfo.id, m.answeringQueryInfo.callerKey))
		b.WriteString(fmt.Sprintf("När du har ett svar, AVSLUTA med: answer_query(query_id=\"%s\", answer=\"ditt svar här\")\n", m.answeringQueryInfo.id))
	}

	return b.String()
}
