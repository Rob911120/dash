package main

import (
	"context"
	"os"
	"strings"

	"dash"

	tea "github.com/charmbracelet/bubbletea"
)

type chatClient struct {
	model    string
	models   []string
	modelIdx int
	tools    []map[string]any
	router   *dash.LLMRouter
}

type chatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Reasoning  string     `json:"-"`
}

type toolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type streamToolCall struct {
	Index   int
	ID      string
	Name    string
	ArgsBuf strings.Builder
}

// Chat streaming message types — every type carries owner for routing.
type chatChunkMsg struct {
	owner string
	chunk string
}
type chatReasoningMsg struct {
	owner string
	chunk string
}
type chatDoneMsg struct {
	owner string
	usage *apiUsage
}
type chatErrorMsg struct {
	owner string
	err   error
}
type chatToolCallMsg struct {
	owner string
	calls []streamToolCall
}

type apiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func newChatClient(router *dash.LLMRouter) *chatClient {
	// Get only models that have a working API key
	all := router.AvailableModels()
	var models []string
	for _, m := range all {
		if m.Available {
			models = append(models, m.Name)
		}
	}

	// If no models available, use a sensible default
	if len(models) == 0 {
		models = []string{"anthropic/claude-opus-4"}
	}

	// Determine default model: env > chat role > first available
	model := os.Getenv("DASH_CHAT_MODEL")
	idx := 0
	if model != "" {
		found := false
		for i, m := range models {
			if m == model {
				idx = i
				found = true
				break
			}
		}
		if !found {
			models = append([]string{model}, models...)
		}
	} else {
		// Prefer the model configured for the "chat" role
		if rc, ok := router.Config().Roles["chat"]; ok && rc.Model != "" {
			for i, m := range models {
				if m == rc.Model {
					idx = i
					break
				}
			}
		}
		model = models[idx]
	}

	return &chatClient{
		model:    model,
		models:   models,
		modelIdx: idx,
		router:   router,
	}
}

func (c *chatClient) cycleModel() string {
	c.modelIdx = (c.modelIdx + 1) % len(c.models)
	c.model = c.models[c.modelIdx]
	return c.model
}

func (c *chatClient) cycleModelBack() string {
	c.modelIdx--
	if c.modelIdx < 0 {
		c.modelIdx = len(c.models) - 1
	}
	c.model = c.models[c.modelIdx]
	return c.model
}

// contextLimit returns the context window size for the current model.
func (c *chatClient) contextLimit() int {
	return c.router.ContextLimit(c.model)
}

func waitForChatMsg(ch <-chan any, owner string) tea.Cmd {
	return func() tea.Msg {
		raw, ok := <-ch
		if !ok {
			return chatDoneMsg{owner: owner}
		}
		switch m := raw.(type) {
		case chatChunkMsg:
			m.owner = owner
			return m
		case chatReasoningMsg:
			m.owner = owner
			return m
		case chatDoneMsg:
			m.owner = owner
			return m
		case chatErrorMsg:
			m.owner = owner
			return m
		case chatToolCallMsg:
			m.owner = owner
			return m
		default:
			return raw.(tea.Msg)
		}
	}
}

// Stream sends messages via the LLM router using all registered tools.
func (c *chatClient) Stream(ctx context.Context, messages []chatMessage, ch chan<- any) {
	c.StreamWithTools(ctx, messages, c.tools, ch)
}

// StreamWithTools sends messages via the LLM router with a specific set of tools.
func (c *chatClient) StreamWithTools(ctx context.Context, messages []chatMessage, tools []map[string]any, ch chan<- any) {
	defer close(ch)

	// Convert cockpit chatMessages to router ChatMessages
	routerMsgs := make([]dash.ChatMessage, len(messages))
	for i, m := range messages {
		routerMsgs[i] = dash.ChatMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		}
		for _, tc := range m.ToolCalls {
			routerMsgs[i].ToolCalls = append(routerMsgs[i].ToolCalls, dash.ToolCallRef{
				ID:   tc.ID,
				Type: tc.Type,
				Function: dash.ToolCallFunc{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
	}

	events := c.router.StreamWithModel(ctx, c.model, routerMsgs, tools)

	// Collect usage separately — EventUsage arrives BEFORE EventToolCall
	// in the Anthropic SSE stream, so emitting chatDoneMsg from EventUsage
	// would stop channel reads before the tool call message is processed.
	var lastUsage *apiUsage

	for ev := range events {
		switch ev.Type {
		case dash.EventContent:
			ch <- chatChunkMsg{chunk: ev.Content}
		case dash.EventReasoning:
			ch <- chatReasoningMsg{chunk: ev.Reasoning}
		case dash.EventToolCall:
			var calls []streamToolCall
			for i, tc := range ev.ToolCalls {
				calls = append(calls, streamToolCall{
					Index: i,
					ID:    tc.ID,
					Name:  tc.Name,
				})
				calls[i].ArgsBuf.WriteString(tc.Arguments)
			}
			ch <- chatToolCallMsg{calls: calls}
		case dash.EventUsage:
			if ev.Usage != nil {
				lastUsage = &apiUsage{
					PromptTokens:     ev.Usage.PromptTokens,
					CompletionTokens: ev.Usage.CompletionTokens,
					TotalTokens:      ev.Usage.TotalTokens,
				}
			}
		case dash.EventError:
			ch <- chatErrorMsg{err: ev.Error}
			return
		case dash.EventDone:
			ch <- chatDoneMsg{usage: lastUsage}
			return
		}
	}
}
