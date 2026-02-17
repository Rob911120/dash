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
	callerKey := m.scopedAgent
	return func() tea.Msg {
		ctx := context.Background()
		var toolResults []chatMessage
		var spawnInfo *agentSpawnInfo
		var askQuery *pendingQuery
		var planReqInfo *planRequestInfo
		var answerQueryID, answerText string
		for _, c := range calls {
			var args map[string]any
			if err := json.Unmarshal([]byte(c.ArgsBuf.String()), &args); err != nil {
				args = map[string]any{}
			}

			var resultText string
			if d != nil {
				// Self-ask guard
				if c.Name == "ask_agent" {
					target, _ := args["target"].(string)
					if target == callerKey {
						resultText = `{"error": "cannot ask yourself"}`
						toolResults = append(toolResults, chatMessage{
							Role: "tool", Name: c.Name, Content: resultText, ToolCallID: c.ID,
						})
						continue
					}
				}

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
					// Detect give_to_planner results
					if c.Name == "give_to_planner" {
						if info := parsePlanRequestResult(resultText); info != nil {
							desc, _ := args["description"].(string)
							info.desc = desc
							info.context, _ = args["context"].(string)
							info.priority, _ = args["priority"].(string)
							planReqInfo = info
						}
					}
					// Detect ask_agent results
					if c.Name == "ask_agent" {
						if q := parseAskResult(resultText, c.ID); q != nil {
							q.callerKey = callerKey
							askQuery = q
						}
					}
					// Detect answer_query results
					if c.Name == "answer_query" {
						answerQueryID, _ = parseAnswerResult(resultText)
						answerRaw, _ := args["answer"].(string)
						answerText = answerRaw
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

		// Priority: answer > ask > planRequest > spawn > normal
		if answerQueryID != "" {
			return chatToolResultWithAnswer{
				results: toolResults,
				calls:   calls,
				queryID: answerQueryID,
				answer:  answerText,
			}
		}
		if askQuery != nil {
			return chatToolResultWithAsk{
				results: toolResults,
				calls:   calls,
				query:   *askQuery,
			}
		}
		if planReqInfo != nil {
			return chatToolResultWithPlanRequest{
				results:   toolResults,
				calls:     calls,
				requestID: planReqInfo.requestID,
				desc:      planReqInfo.desc,
				context:   planReqInfo.context,
				priority:  planReqInfo.priority,
			}
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
