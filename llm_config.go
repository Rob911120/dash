package dash

import (
	"context"
	"encoding/json"
	"fmt"
)

// DefaultRouterConfig returns the hardcoded default router configuration.
func DefaultRouterConfig() RouterConfig {
	return RouterConfig{
		EnvFile: "/dash/.mcp.json",
		Providers: map[string]ProviderConfig{
			"openrouter": {
				Name:          "openrouter",
				Format:        FormatOpenAI,
				BaseURL:       "https://openrouter.ai/api/v1",
				APIKeyEnv:     "OPENROUTER_API_KEY",
				SupportsTools: true,
				ExtraHeaders: map[string]string{
					"HTTP-Referer": "https://dash.local",
					"X-Title":      "Dash",
				},
				Enabled: true,
			},
			"anthropic": {
				Name:          "anthropic",
				Format:        FormatAnthropic,
				BaseURL:       "https://api.anthropic.com/v1",
				APIKeyEnv:     "ANTHROPIC_API_KEY",
				SupportsTools: true,
				Enabled:       true,
			},
			"openai": {
				Name:          "openai",
				Format:        FormatOpenAI,
				BaseURL:       "https://api.openai.com/v1",
				APIKeyEnv:     "OPENAI_API_KEY",
				SupportsTools: true,
				Enabled:       true,
			},
			"xai": {
				Name:          "xai",
				Format:        FormatOpenAI,
				BaseURL:       "https://api.x.ai/v1",
				APIKeyEnv:     "XAI_API_KEY",
				SupportsTools: true,
				Enabled:       true,
			},
			"minimax": {
				Name:          "minimax",
				Format:        FormatAnthropic,
				AuthStyle:     AuthBearer,
				BaseURL:       "https://api.minimax.io/anthropic/v1",
				APIKeyEnv:     "MINIMAX_API_KEY",
				SupportsTools: true,
				Enabled:       true,
			},
			"moonshot": {
				Name:          "moonshot",
				Format:        FormatOpenAI,
				BaseURL:       "https://api.moonshot.cn/v1",
				APIKeyEnv:     "MOONSHOT_API_KEY",
				SupportsTools: true,
				Enabled:       true,
			},
		},
		Roles: map[string]RoleConfig{
			"embed": {
				Role:     "embed",
				Provider: "openrouter",
				Model:    "openai/text-embedding-3-small",
			},
			"summarize": {
				Role:     "summarize",
				Provider: "openrouter",
				Model:    "google/gemini-2.0-flash-001",
			},
			"chat": {
				Role:     "chat",
				Provider: "openrouter",
				Model:    "anthropic/claude-opus-4",
			},
			"plan": {
				Role:     "plan",
				Provider: "openrouter",
				Model:    "anthropic/claude-opus-4",
			},
			"mutator": {
				Role:     "mutator",
				Provider: "openrouter",
				Model:    "anthropic/claude-3.7-sonnet",
			},
			"synthesizer": {
				Role:     "synthesizer",
				Provider: "openrouter",
				Model:    "anthropic/claude-opus-4",
			},
		},
		ModelAliases: map[string]string{},
		Models: map[string]ModelConfig{
			"anthropic/claude-opus-4":            {Name: "anthropic/claude-opus-4", Provider: "openrouter", ContextLength: 200000},
			"anthropic/claude-3.7-sonnet":        {Name: "anthropic/claude-3.7-sonnet", Provider: "openrouter", ContextLength: 200000},
			"anthropic/claude-3.5-sonnet":        {Name: "anthropic/claude-3.5-sonnet", Provider: "openrouter", ContextLength: 200000},
			"openai/gpt-5.2-pro":                {Name: "openai/gpt-5.2-pro", Provider: "openrouter", ContextLength: 256000},
			"openai/gpt-4o":                     {Name: "openai/gpt-4o", Provider: "openrouter", ContextLength: 128000},
			"openai/gpt-4o-mini":                {Name: "openai/gpt-4o-mini", Provider: "openrouter", ContextLength: 128000},
			"google/gemini-2.5-pro":             {Name: "google/gemini-2.5-pro", Provider: "openrouter", ContextLength: 1000000},
			"google/gemini-2.5-flash":           {Name: "google/gemini-2.5-flash", Provider: "openrouter", ContextLength: 1000000},
			"deepseek/deepseek-r1":              {Name: "deepseek/deepseek-r1", Provider: "openrouter", ContextLength: 64000},
			"deepseek/deepseek-chat":            {Name: "deepseek/deepseek-chat", Provider: "openrouter", ContextLength: 64000},
			"deepseek/deepseek-r1-0528":         {Name: "deepseek/deepseek-r1-0528", Provider: "openrouter", ContextLength: 64000},
			"openai/gpt-4.1":                    {Name: "openai/gpt-4.1", Provider: "openrouter", ContextLength: 128000},
			"meta-llama/llama-4-maverick":        {Name: "meta-llama/llama-4-maverick", Provider: "openrouter", ContextLength: 128000},
			"meta-llama/llama-3.3-70b-instruct": {Name: "meta-llama/llama-3.3-70b-instruct", Provider: "openrouter", ContextLength: 128000},
			"moonshotai/kimi-k2.5":              {Name: "moonshotai/kimi-k2.5", Provider: "openrouter", ContextLength: 256000},
			"MiniMax-M2.5":                      {Name: "MiniMax-M2.5", Provider: "minimax", ContextLength: 1000000},
		},
	}
}

// LoadRouterConfig reads SYSTEM.llm_provider and SYSTEM.llm_role nodes from the graph.
// Falls back to defaults if no nodes exist.
func LoadRouterConfig(ctx context.Context, d *Dash) (RouterConfig, error) {
	cfg := DefaultRouterConfig()

	// Load providers from graph
	providerNodes, err := d.getNodesByLayerType(ctx, LayerSystem, "llm_provider")
	if err != nil {
		return cfg, nil // use defaults on error
	}

	if len(providerNodes) > 0 {
		defaults := DefaultRouterConfig()
		for _, n := range providerNodes {
			pc := parseProviderFromData(n.Name, n.Data)
			if pc.Name != "" {
				// Merge defaults for fields missing in the graph node
				if def, ok := defaults.Providers[pc.Name]; ok {
					if pc.AuthStyle == "" {
						pc.AuthStyle = def.AuthStyle
					}
					if !pc.SupportsTools {
						pc.SupportsTools = def.SupportsTools
					}
				}
				cfg.Providers[pc.Name] = pc
			}
		}
	}

	// Load roles from graph
	roleNodes, err := d.getNodesByLayerType(ctx, LayerSystem, "llm_role")
	if err != nil {
		return cfg, nil
	}

	if len(roleNodes) > 0 {
		for _, n := range roleNodes {
			rc := parseRoleFromData(n.Name, n.Data)
			if rc.Role != "" {
				cfg.Roles[rc.Role] = rc
			}
		}
	}

	// Load models from graph
	modelNodes, err := d.getNodesByLayerType(ctx, LayerSystem, "llm_model")
	if err != nil {
		return cfg, nil
	}

	if len(modelNodes) > 0 {
		for _, n := range modelNodes {
			mc := parseModelFromData(n.Name, n.Data)
			if mc.Name != "" {
				cfg.Models[mc.Name] = mc
			}
		}
	}

	return cfg, nil
}

// EnsureDefaultRouterConfig creates default SYSTEM.llm_provider and SYSTEM.llm_role nodes
// in the graph if they don't already exist.
func (d *Dash) EnsureDefaultRouterConfig(ctx context.Context) error {
	defaults := DefaultRouterConfig()

	for name, pc := range defaults.Providers {
		dataMap := map[string]any{
			"name":           pc.Name,
			"format":         string(pc.Format),
			"auth_style":     string(pc.AuthStyle),
			"base_url":       pc.BaseURL,
			"api_key_env":    pc.APIKeyEnv,
			"enabled":        pc.Enabled,
			"supports_tools": pc.SupportsTools,
		}
		if len(pc.ExtraHeaders) > 0 {
			dataMap["extra_headers"] = pc.ExtraHeaders
		}
		_, err := d.GetOrCreateNode(ctx, LayerSystem, "llm_provider", name, dataMap)
		if err != nil {
			return fmt.Errorf("ensure provider %s: %w", name, err)
		}
	}

	for name, rc := range defaults.Roles {
		dataMap := map[string]any{
			"role":     rc.Role,
			"provider": rc.Provider,
			"model":    rc.Model,
		}
		_, err := d.GetOrCreateNode(ctx, LayerSystem, "llm_role", name, dataMap)
		if err != nil {
			return fmt.Errorf("ensure role %s: %w", name, err)
		}
	}

	for name, mc := range defaults.Models {
		dataMap := map[string]any{
			"name":           mc.Name,
			"provider":       mc.Provider,
			"context_length": mc.ContextLength,
		}
		_, err := d.GetOrCreateNode(ctx, LayerSystem, "llm_model", name, dataMap)
		if err != nil {
			return fmt.Errorf("ensure model %s: %w", name, err)
		}
	}

	return nil
}

// parseProviderFromData extracts a ProviderConfig from node data JSON.
func parseProviderFromData(nodeName string, data json.RawMessage) ProviderConfig {
	var m map[string]any
	if json.Unmarshal(data, &m) != nil {
		return ProviderConfig{}
	}

	getString := func(key string) string {
		if v, ok := m[key].(string); ok {
			return v
		}
		return ""
	}
	getBool := func(key string) bool {
		if v, ok := m[key].(bool); ok {
			return v
		}
		return false
	}

	pc := ProviderConfig{
		Name:          getString("name"),
		Format:        APIFormat(getString("format")),
		AuthStyle:     AuthStyle(getString("auth_style")),
		BaseURL:       getString("base_url"),
		APIKeyEnv:     getString("api_key_env"),
		Enabled:       getBool("enabled"),
		SupportsTools: getBool("supports_tools"),
	}
	if pc.Name == "" {
		pc.Name = nodeName
	}

	// Parse extra_headers
	if eh, ok := m["extra_headers"].(map[string]any); ok {
		pc.ExtraHeaders = make(map[string]string)
		for k, v := range eh {
			if s, ok := v.(string); ok {
				pc.ExtraHeaders[k] = s
			}
		}
	}

	return pc
}

// parseRoleFromData extracts a RoleConfig from node data JSON.
func parseRoleFromData(nodeName string, data json.RawMessage) RoleConfig {
	var m map[string]any
	if json.Unmarshal(data, &m) != nil {
		return RoleConfig{}
	}

	getString := func(key string) string {
		if v, ok := m[key].(string); ok {
			return v
		}
		return ""
	}

	rc := RoleConfig{
		Role:     getString("role"),
		Provider: getString("provider"),
		Model:    getString("model"),
	}
	if rc.Role == "" {
		rc.Role = nodeName
	}
	if mt, ok := m["max_tokens"].(float64); ok {
		rc.MaxTokens = int(mt)
	}

	return rc
}

// parseModelFromData extracts a ModelConfig from node data JSON.
func parseModelFromData(nodeName string, data json.RawMessage) ModelConfig {
	var m map[string]any
	if json.Unmarshal(data, &m) != nil {
		return ModelConfig{}
	}

	getString := func(key string) string {
		if v, ok := m[key].(string); ok {
			return v
		}
		return ""
	}

	mc := ModelConfig{
		Name:     getString("name"),
		Provider: getString("provider"),
	}
	if mc.Name == "" {
		mc.Name = nodeName
	}
	if cl, ok := m["context_length"].(float64); ok {
		mc.ContextLength = int(cl)
	}

	return mc
}

// getNodesByLayerType returns all non-deleted nodes matching layer+type.
func (d *Dash) getNodesByLayerType(ctx context.Context, layer Layer, nodeType string) ([]*Node, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, layer, type, name, data, created_at, updated_at, deleted_at
		FROM nodes
		WHERE layer = $1 AND type = $2 AND deleted_at IS NULL
		ORDER BY name
	`, string(layer), nodeType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []*Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}
