package dash

import "sync"

// ToolRegistry holds all registered tool definitions.
type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]*ToolDef
	order []string
}

// NewToolRegistry creates an empty registry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]*ToolDef),
	}
}

// Register adds a tool to the registry. Overwrites if name exists.
func (r *ToolRegistry) Register(def *ToolDef) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[def.Name]; !exists {
		r.order = append(r.order, def.Name)
	}
	r.tools[def.Name] = def
}

// Get returns a tool by name.
func (r *ToolRegistry) Get(name string) (*ToolDef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// All returns all tools in registration order.
func (r *ToolRegistry) All() []*ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*ToolDef, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.tools[name])
	}
	return out
}
