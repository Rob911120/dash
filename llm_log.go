package dash

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const llmLogDir = "logs/llm"

var llmLogOnce sync.Once

// ensureLogDir creates the log directory once.
func ensureLogDir() {
	llmLogOnce.Do(func() {
		os.MkdirAll(llmLogDir, 0755)
	})
}

// llmLogFile returns the per-agent log file path.
func llmLogFile(agent string) string {
	// Sanitize agent name for filename
	safe := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, agent)
	if safe == "" {
		safe = "default"
	}
	return filepath.Join(llmLogDir, safe+".log")
}

// llmLog writes a timestamped log entry to the agent's log file.
func llmLog(agent, format string, args ...any) {
	ensureLogDir()
	f, err := os.OpenFile(llmLogFile(agent), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	ts := time.Now().Format("15:04:05.000")
	fmt.Fprintf(f, "[%s] %s\n", ts, fmt.Sprintf(format, args...))
}

// LLMLogRequest logs an outgoing API request with message details.
func LLMLogRequest(ctx context.Context, prov ProviderConfig, model string, messages []ChatMessage, tools []map[string]any, payloadBytes int) {
	agent := LLMAgentFromContext(ctx)

	llmLog(agent, ">>> REQUEST provider=%s model=%s format=%s tools=%d msgs=%d payload=%d bytes",
		prov.Name, model, prov.Format, len(tools), len(messages), payloadBytes)

	for i, msg := range messages {
		preview := msg.Content
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		toolInfo := ""
		if len(msg.ToolCalls) > 0 {
			var names []string
			for _, tc := range msg.ToolCalls {
				names = append(names, tc.Function.Name)
			}
			toolInfo = fmt.Sprintf(" tools:[%s]", strings.Join(names, ","))
		}
		llmLog(agent, "  msg[%d] role=%s%s content=%q", i, msg.Role, toolInfo, preview)
	}
}

// LLMLogResponse logs a completed API response.
func LLMLogResponse(ctx context.Context, model string, status int, contentLen int, toolCalls int, err error) {
	agent := LLMAgentFromContext(ctx)

	if err != nil {
		llmLog(agent, "<<< ERROR model=%s status=%d err=%v", model, status, err)
		return
	}
	llmLog(agent, "<<< RESPONSE model=%s status=%d content=%d chars tool_calls=%d",
		model, status, contentLen, toolCalls)
}

// LLMLogStreamEnd logs the end of a streaming response with summary.
func LLMLogStreamEnd(ctx context.Context, model string, contentLen int, toolCalls int, usage *TokenUsage, err error) {
	agent := LLMAgentFromContext(ctx)

	if err != nil {
		llmLog(agent, "<<< STREAM-ERROR model=%s err=%v", model, err)
		return
	}

	usageStr := ""
	if usage != nil {
		usageStr = fmt.Sprintf(" prompt=%d completion=%d total=%d",
			usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
	}
	llmLog(agent, "<<< STREAM-END model=%s content=%d chars tool_calls=%d%s",
		model, contentLen, toolCalls, usageStr)
}
