package main

import (
	"context"
	"encoding/json"
	"fmt"

	"dash"

	tea "github.com/charmbracelet/bubbletea"
)

// fileToolNames lists tools that operate on files.
var fileToolNames = map[string]string{
	"read": "file_path", "write": "file_path", "edit": "file_path",
	"grep": "path", "glob": "path", "file": "file_path",
}

// trackFileFromTool extracts and tracks file paths from tool calls.
func (m *chatModel) trackFileFromTool(toolName string, args map[string]any) {
	argName, ok := fileToolNames[toolName]
	if !ok {
		return
	}
	filePath, _ := args[argName].(string)
	if filePath == "" {
		return
	}
	m.lastFile = filePath
	if m.fileCounts == nil {
		m.fileCounts = make(map[string]int)
	}
	m.fileCounts[filePath]++
	// Update topFile
	maxCount := 0
	for f, c := range m.fileCounts {
		if c > maxCount {
			maxCount = c
			m.topFile = f
		}
		_ = f
	}
}

func (m *chatModel) executeTools(calls []streamToolCall) tea.Cmd {
	d := m.d
	sessionID := m.sessionID
	return func() tea.Msg {
		ctx := context.Background()
		var toolResults []chatMessage
		var spawnInfo *agentSpawnInfo
		for _, c := range calls {
			var args map[string]any
			if err := json.Unmarshal([]byte(c.ArgsBuf.String()), &args); err != nil {
				args = map[string]any{}
			}

			var resultText string
			if d != nil {
				result := d.RunTool(ctx, c.Name, args, &dash.ToolOpts{
					SessionID: sessionID,
					CallerID:  "cockpit",
				})
				if result.Success {
					resultJSON, _ := json.Marshal(result.Data)
					resultText = string(resultJSON)

					// Detect spawn_agent results
					if c.Name == "spawn_agent" {
						spawnInfo = parseSpawnResult(resultText)
					}
				} else {
					resultText = fmt.Sprintf("Error: %s", result.Error)
				}
			} else {
				resultText = "Error: Dash client not available"
			}

			if len(resultText) > 4000 {
				resultText = resultText[:4000] + "\n... (truncated)"
			}

			toolResults = append(toolResults, chatMessage{
				Role:       "tool",
				Name:       c.Name,
				Content:    resultText,
				ToolCallID: c.ID,
			})
		}

		if spawnInfo != nil {
			return chatToolResultWithSpawn{
				results: toolResults,
				calls:   calls,
				spawn:   *spawnInfo,
			}
		}
		return chatToolResultReady{results: toolResults, calls: calls}
	}
}
