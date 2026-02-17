package dash

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// MCP JSON-RPC 2.0 types

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// MCP Protocol types

type mcpInitializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type mcpToolsListResult struct {
	Tools []mcpTool `json:"tools"`
}

type mcpToolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type mcpToolResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// MCPServer handles MCP protocol communication
type MCPServer struct {
	dash   *Dash
	reader *bufio.Reader
	writer io.Writer
}

// NewMCPServer creates a new MCP server
func NewMCPServer(d *Dash) *MCPServer {
	return &MCPServer{
		dash:   d,
		reader: bufio.NewReader(os.Stdin),
		writer: os.Stdout,
	}
}

// Run starts the MCP server loop
func (s *MCPServer) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := s.reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		if len(line) == 0 {
			continue
		}

		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.sendError(nil, -32700, "Parse error", err.Error())
			continue
		}

		s.handleRequest(ctx, &req)
	}
}

func (s *MCPServer) handleRequest(ctx context.Context, req *jsonRPCRequest) {
	switch req.Method {
	case "initialize":
		s.handleInitialize(req)
	case "initialized":
		// No response needed
	case "tools/list":
		s.handleToolsList(req)
	case "tools/call":
		s.handleToolsCall(ctx, req)
	default:
		s.sendError(req.ID, -32601, "Method not found", req.Method)
	}
}

func (s *MCPServer) handleInitialize(req *jsonRPCRequest) {
	result := mcpInitializeResult{
		ProtocolVersion: "2024-11-05",
		Capabilities: map[string]any{
			"tools": map[string]any{},
		},
	}
	result.ServerInfo.Name = "dash-mcp"
	result.ServerInfo.Version = "1.0.0"

	s.sendResult(req.ID, result)
}

func (s *MCPServer) handleToolsList(req *jsonRPCRequest) {
	defs := s.dash.registry.All()
	tools := make([]mcpTool, len(defs))
	for i, d := range defs {
		tools[i] = mcpTool{
			Name:        d.Name,
			Description: d.Description,
			InputSchema: d.InputSchema,
		}
	}
	s.sendResult(req.ID, mcpToolsListResult{Tools: tools})
}

func (s *MCPServer) handleToolsCall(ctx context.Context, req *jsonRPCRequest) {
	var params mcpToolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.sendError(req.ID, -32602, "Invalid params", err.Error())
		return
	}

	result := s.dash.RunTool(ctx, params.Name, params.Arguments, &ToolOpts{CallerID: "mcp"})

	if !result.Success {
		s.sendResult(req.ID, mcpToolResult{
			Content: []mcpContent{{Type: "text", Text: fmt.Sprintf("Error: %s", result.Error)}},
			IsError: true,
		})
		return
	}

	resultJSON, _ := json.MarshalIndent(result.Data, "", "  ")
	s.sendResult(req.ID, mcpToolResult{
		Content: []mcpContent{{Type: "text", Text: string(resultJSON)}},
	})
}

// CallTool executes a tool by name with arguments via RunTool.
// Returns the JSON result string.
func (s *MCPServer) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	result := s.dash.RunTool(ctx, name, args, &ToolOpts{CallerID: "mcp"})
	if !result.Success {
		return "", fmt.Errorf("%s", result.Error)
	}
	resultJSON, err := json.Marshal(result.Data)
	if err != nil {
		return "", fmt.Errorf("marshal result: %w", err)
	}
	return string(resultJSON), nil
}

// ToolDefinitions returns OpenRouter/OpenAI-formatted tool definitions.
func (s *MCPServer) ToolDefinitions() []map[string]any {
	defs := s.dash.registry.All()
	out := make([]map[string]any, len(defs))
	for i, t := range defs {
		out[i] = map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.InputSchema,
			},
		}
	}
	return out
}

// Helper functions

func (s *MCPServer) sendResult(id any, result any) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	s.send(resp)
}

func (s *MCPServer) sendError(id any, code int, message, data string) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &rpcError{
			Code:    code,
			Message: message,
			Data:    data,
		},
	}
	s.send(resp)
}

func (s *MCPServer) send(resp jsonRPCResponse) {
	data, _ := json.Marshal(resp)
	fmt.Fprintf(s.writer, "%s\n", data)
}

// isSelectQuery checks if a SQL query starts with SELECT (case-insensitive).
func isSelectQuery(q string) bool {
	for i := 0; i < len(q); i++ {
		c := q[i]
		if c == ' ' || c == '\t' || c == '\n' {
			continue
		}
		if i+6 <= len(q) {
			prefix := q[i : i+6]
			if prefix == "SELECT" || prefix == "select" || prefix == "Select" {
				return true
			}
		}
		return false
	}
	return false
}

// hashContent computes SHA256 hash of content string.
func hashContent(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// registerBuiltinTools registers all built-in tools in the registry.
// Uses tool_scanner for automatic registration, with manual fallbacks.
func registerBuiltinTools(d *Dash) {
	// First, try auto-registration from tool_scanner
	// This ensures any new tools are automatically registered
	autoCount := EnsureToolRegistry(d)
	if autoCount > 0 {
		fmt.Printf("Auto-registered %d tools from scanner\n", autoCount)
	}

	// Note: Tools registered above are now the source of truth.
	// The manual registrations below are kept for compatibility
	// but will be skipped if tool already exists (handled by EnsureToolRegistry).
	
	// If no tools registered at all, fall back to manual registration
	if len(d.registry.All()) == 0 {
		// Graph & observability
		d.registry.Register(defActivity())
		d.registry.Register(defSession())
		d.registry.Register(defFile())
		d.registry.Register(defSearch())
		d.registry.Register(defQuery())
		d.registry.Register(defNode())
		d.registry.Register(defLink())
		d.registry.Register(defTraverse())
		d.registry.Register(defSummary())
		d.registry.Register(defRemember())
		d.registry.Register(defForget())
		d.registry.Register(defWorkingSet())
		d.registry.Register(defPromote())
		d.registry.Register(defGC())
		d.registry.Register(defPatterns())
		d.registry.Register(defEmbed())
		d.registry.Register(defTasks())
		d.registry.Register(defSuggestImprovement())
		d.registry.Register(defContextPack())
		d.registry.Register(defUpdateStateCard())
		d.registry.Register(defPlan())
		d.registry.Register(defPlanReview())
		// Unified work tool
		d.registry.Register(defWork())
		// LLM router config
		d.registry.Register(defLLMConfig())
		// Prompt service
		d.registry.Register(defPrompt())
		d.registry.Register(defPromptProfile())
		// Agent management
		d.registry.Register(defSpawnAgent())
		d.registry.Register(defAgentStatus())
		d.registry.Register(defUpdateAgentStatus())
		// Cross-agent communication
		d.registry.Register(defAskAgent())
		d.registry.Register(defAnswerQuery())
		// Planner delegation
		d.registry.Register(defGiveToPlanner())
		// Filesystem
		d.registry.Register(defRead())
		d.registry.Register(defWrite())
		d.registry.Register(defEdit())
		d.registry.Register(defGrep())
		d.registry.Register(defGlob())
		d.registry.Register(defLs())
		d.registry.Register(defMkdir())
		d.registry.Register(defExec())
	}
}
