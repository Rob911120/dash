package main

import (
	"context"
	"encoding/json"
	"fmt"

	"dash"

	tea "github.com/charmbracelet/bubbletea"
)

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
