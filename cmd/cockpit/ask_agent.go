package main

import (
	"encoding/json"
	"time"

	"dash"
)

// pendingQuery tracks a cross-agent query from caller to target.
type pendingQuery struct {
	id         string
	callerKey  string
	targetKey  string
	question   string
	toolCallID string // maps back to caller's tool_call_id for result replacement
	startedAt  time.Time
}

// chatToolResultWithAsk is sent when executeTools detects an ask_agent result.
type chatToolResultWithAsk struct {
	results []dash.ChatMessage
	calls   []streamToolCall
	query   pendingQuery
}

// chatToolResultWithAnswer is sent when executeTools detects an answer_query result.
type chatToolResultWithAnswer struct {
	results []dash.ChatMessage
	calls   []streamToolCall
	queryID string
	answer  string
}

// parseAskResult extracts ask_agent dispatch info from a tool result JSON.
func parseAskResult(resultJSON, toolCallID string) *pendingQuery {
	var data map[string]any
	if err := json.Unmarshal([]byte(resultJSON), &data); err != nil {
		return nil
	}
	queryID, _ := data["query_id"].(string)
	target, _ := data["target"].(string)
	if queryID == "" || target == "" {
		return nil
	}
	return &pendingQuery{
		id:         queryID,
		targetKey:  target,
		question:   strOr(data["question"], ""),
		toolCallID: toolCallID,
		startedAt:  time.Now(),
	}
}

// parseAnswerResult extracts answer_query info from a tool result JSON.
func parseAnswerResult(resultJSON string) (queryID, answer string) {
	var data map[string]any
	if err := json.Unmarshal([]byte(resultJSON), &data); err != nil {
		return "", ""
	}
	queryID, _ = data["query_id"].(string)
	return queryID, ""
}

// replaceToolResult replaces the content of a tool result message matching toolCallID.
func replaceToolResult(messages []dash.ChatMessage, toolCallID, newContent string) {
	for i := range messages {
		if messages[i].Role == "tool" && messages[i].ToolCallID == toolCallID {
			messages[i].Content = newContent
			return
		}
	}
}

func strOr(v any, fallback string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return fallback
}

// chatToolResultWithPlanRequest is sent when executeTools detects a give_to_planner result.
type chatToolResultWithPlanRequest struct {
	results   []dash.ChatMessage
	calls     []streamToolCall
	requestID string
	desc      string
	context   string
	priority  string
}

// planRequestInfo holds parsed give_to_planner result data.
type planRequestInfo struct {
	requestID string
	desc      string
	context   string
	priority  string
}

// parsePlanRequestResult extracts plan request info from a give_to_planner tool result JSON.
func parsePlanRequestResult(resultJSON string) *planRequestInfo {
	var data map[string]any
	if err := json.Unmarshal([]byte(resultJSON), &data); err != nil {
		return nil
	}
	reqID, _ := data["request_id"].(string)
	target, _ := data["target"].(string)
	if reqID == "" || target != "planner-agent" {
		return nil
	}
	return &planRequestInfo{
		requestID: reqID,
	}
}
