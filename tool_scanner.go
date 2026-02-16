package dash

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// ToolScanner auto-registers tools based on reflection.
type ToolScanner struct {
	dash *Dash
}

// NewToolScanner creates a new scanner.
func NewToolScanner(d *Dash) *ToolScanner {
	return &ToolScanner{dash: d}
}

// AllToolDefs returns a map of all tool definition functions.
// This is the single source of truth for tool registration.
func AllToolDefs() map[string]func() *ToolDef {
	return map[string]func() *ToolDef{
		// Graph & observability
		"defActivity":           defActivity,
		"defSession":           defSession,
		"defFile":               defFile,
		"defSearch":             defSearch,
		"defQuery":              defQuery,
		"defNode":               defNode,
		"defLink":               defLink,
		"defTraverse":           defTraverse,
		"defSummary":            defSummary,
		"defRemember":           defRemember,
		"defForget":             defForget,
		"defWorkingSet":         defWorkingSet,
		"defPromote":            defPromote,
		"defGC":                 defGC,
		"defPatterns":           defPatterns,
		"defEmbed":              defEmbed,
		"defTasks":              defTasks,
		"defSuggestImprovement": defSuggestImprovement,
		"defContextPack":        defContextPack,
		"defUpdateStateCard":    defUpdateStateCard,
		"defPlan":               defPlan,
		"defPlanReview":         defPlanReview,
		// Unified work tool
		"defWork":     defWork,
		// LLM router config
		"defLLMConfig": defLLMConfig,
		// Prompt service
		"defPrompt":        defPrompt,
		"defPromptProfile":  defPromptProfile,
		// Agent management
		"defSpawnAgent":       defSpawnAgent,
		"defAgentStatus":      defAgentStatus,
		"defUpdateAgentStatus": defUpdateAgentStatus,
		// Pipeline tools
		"defWorkOrder":     defWorkOrder,
		"defBuildGateTool": defBuildGateTool,
		"defPipeline":      defPipeline,
		// Filesystem tools
		"defRead":  defRead,
		"defWrite": defWrite,
		"defEdit":  defEdit,
		"defGrep":  defGrep,
		"defGlob":  defGlob,
		"defLs":    defLs,
		"defMkdir": defMkdir,
		"defExec":  defExec,
	}
}

// RegisterAll auto-registers all tools from AllToolDefs().
func (s *ToolScanner) RegisterAll() int {
	registered := 0
	for name, fn := range AllToolDefs() {
		// Skip if already registered
		if _, exists := s.dash.registry.Get(name); exists {
			continue
		}
		
		// Call the def function to get ToolDef
		def := fn()
		if def != nil {
			s.dash.registry.Register(def)
			registered++
			fmt.Printf("Auto-registered tool: %s\n", def.Name)
		}
	}
	return registered
}

// RegisterWithFallback tries to auto-register missing tools.
// Returns the number of newly registered tools.
func (s *ToolScanner) RegisterWithFallback() int {
	before := len(s.dash.registry.All())
	after := s.RegisterAll()
	return after - before
}

// GetToolDefsFromCode returns tool definitions by calling each defXXX function.
// This can be used to sync tools to the database.
func GetToolDefsFromCode() []*ToolDef {
	tools := make([]*ToolDef, 0)
	for _, fn := range AllToolDefs() {
		def := fn()
		if def != nil {
			tools = append(tools, def)
		}
	}
	return tools
}

// EnsureToolRegistry calls all defXXX functions and registers them.
// This replaces the hardcoded registerBuiltinTools for a more maintainable approach.
func EnsureToolRegistry(d *Dash) int {
	registered := 0
	for _, fn := range AllToolDefs() {
		def := fn()
		if def != nil {
			// Only register if not already present
			if _, exists := d.registry.Get(def.Name); !exists {
				d.registry.Register(def)
				registered++
			}
		}
	}
	return registered
}

// GetMissingTools returns tools that are in the code but not in the registry.
func (s *ToolScanner) GetMissingTools() []*ToolDef {
	var missing []*ToolDef
	for name, fn := range AllToolDefs() {
		if _, exists := s.dash.registry.Get(name); !exists {
			def := fn()
			if def != nil {
				missing = append(missing, def)
			}
		}
	}
	return missing
}

// SyncToDatabase syncs tool definitions to the database as AUTOMATION.tool nodes.
func (s *ToolScanner) SyncToDatabase() error {
	ctx := context.Background()
	
	for _, fn := range AllToolDefs() {
		def := fn()
		if def == nil {
			continue
		}

		// Check if tool node already exists
		existing, err := s.dash.GetNodeByName(ctx, "AUTOMATION", "tool", def.Name)
		if err == nil && existing != nil {
			// Tool exists, check if it needs update
			var data map[string]any
			if err := json.Unmarshal(existing.Data, &data); err == nil {
				// Compare and update if different
				needsUpdate := false
				if data["description"] != def.Description {
					needsUpdate = true
				}
				// Add more comparison logic as needed
				
				if needsUpdate {
					existing.Data = mapToJSON(map[string]any{
						"name":         def.Name,
						"description":  def.Description,
						"input_schema": def.InputSchema,
						"tags":         def.Tags,
					})
					if err := s.dash.UpdateNode(ctx, existing); err != nil {
						fmt.Printf("Warning: failed to update tool node %s: %v\n", def.Name, err)
					}
				}
			}
			continue
		}

		// Create new tool node
		toolNode := &Node{
			ID:   uuid.New(),
			Layer: "AUTOMATION",
			Type: "tool",
			Name: def.Name,
			Data: mapToJSON(map[string]any{
				"name":         def.Name,
				"description":  def.Description,
				"input_schema": def.InputSchema,
				"tags":         def.Tags,
			}),
		}

		if err := s.dash.CreateNode(ctx, toolNode); err != nil {
			fmt.Printf("Warning: failed to create tool node %s: %v\n", def.Name, err)
			continue
		}
		fmt.Printf("Created tool node: %s\n", def.Name)
	}

	fmt.Printf("ToolScanner: synced %d tools to database\n", len(AllToolDefs()))
	return nil
}

// mapToJSON converts a map to json.RawMessage
func mapToJSON(m map[string]any) json.RawMessage {
	data, _ := json.Marshal(m)
	return json.RawMessage(data)
}
