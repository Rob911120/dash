package dash

import (
	"context"
	"fmt"
	"time"
)

func defLLMConfig() *ToolDef {
	return &ToolDef{
		Name:        "llm_config",
		Description: "Manage LLM providers, roles, and models. Operations: list, set_provider, set_role, remove_provider, remove_role, list_models, set_model.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"operation"},
			"properties": map[string]any{
				"operation": map[string]any{
					"type":        "string",
					"enum":        []string{"list", "set_provider", "set_role", "remove_provider", "remove_role", "list_models", "set_model"},
					"description": "Operation to perform",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Provider, role, or model name",
				},
				"format": map[string]any{
					"type":        "string",
					"enum":        []string{"openai", "anthropic"},
					"description": "API format (for set_provider)",
				},
				"base_url": map[string]any{
					"type":        "string",
					"description": "Base URL (for set_provider)",
				},
				"api_key_env": map[string]any{
					"type":        "string",
					"description": "Environment variable name for API key (for set_provider)",
				},
				"provider": map[string]any{
					"type":        "string",
					"description": "Provider name (for set_role, set_model)",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "Model name (for set_role)",
				},
				"max_tokens": map[string]any{
					"type":        "integer",
					"description": "Max tokens (for set_role)",
				},
				"context_length": map[string]any{
					"type":        "integer",
					"description": "Context window size in tokens (for set_model)",
				},
			},
		},
		Tags: []string{"admin"},
		Fn:   toolLLMConfig,
	}
}

func toolLLMConfig(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	op, _ := args["operation"].(string)
	name, _ := args["name"].(string)

	switch op {
	case "list":
		return toolLLMConfigList(ctx, d)
	case "set_provider":
		return toolLLMConfigSetProvider(ctx, d, name, args)
	case "set_role":
		return toolLLMConfigSetRole(ctx, d, name, args)
	case "remove_provider":
		return toolLLMConfigRemoveNode(ctx, d, "llm_provider", name)
	case "remove_role":
		return toolLLMConfigRemoveNode(ctx, d, "llm_role", name)
	case "list_models":
		return toolLLMConfigListModels(d)
	case "set_model":
		return toolLLMConfigSetModel(ctx, d, name, args)
	default:
		return nil, fmt.Errorf("unknown operation: %s", op)
	}
}

func toolLLMConfigList(ctx context.Context, d *Dash) (any, error) {
	cfg, err := LoadRouterConfig(ctx, d)
	if err != nil {
		return nil, err
	}

	// Build provider list with key availability
	providers := make([]map[string]any, 0, len(cfg.Providers))
	for _, pc := range cfg.Providers {
		hasKey := resolveAPIKey(pc) != ""
		providers = append(providers, map[string]any{
			"name":        pc.Name,
			"format":      pc.Format,
			"base_url":    pc.BaseURL,
			"api_key_env": pc.APIKeyEnv,
			"has_key":     hasKey,
			"enabled":     pc.Enabled,
		})
	}

	roles := make([]map[string]any, 0, len(cfg.Roles))
	for _, rc := range cfg.Roles {
		roles = append(roles, map[string]any{
			"role":       rc.Role,
			"provider":   rc.Provider,
			"model":      rc.Model,
			"max_tokens": rc.MaxTokens,
		})
	}

	return map[string]any{
		"providers": providers,
		"roles":     roles,
	}, nil
}

func toolLLMConfigListModels(d *Dash) (any, error) {
	if d.router == nil {
		return nil, fmt.Errorf("router not initialized")
	}

	models := d.router.AvailableModels()
	result := make([]map[string]any, len(models))
	for i, m := range models {
		result[i] = map[string]any{
			"name":           m.Name,
			"provider":       m.Provider,
			"context_length": m.ContextLength,
			"available":      m.Available,
		}
	}

	return map[string]any{
		"models": result,
		"count":  len(result),
	}, nil
}

func toolLLMConfigSetModel(ctx context.Context, d *Dash, name string, args map[string]any) (any, error) {
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	provider, _ := args["provider"].(string)
	if provider == "" {
		return nil, fmt.Errorf("provider is required")
	}

	contextLength := 128000 // default
	if cl, ok := args["context_length"].(float64); ok && cl > 0 {
		contextLength = int(cl)
	}

	dataMap := map[string]any{
		"name":           name,
		"provider":       provider,
		"context_length": contextLength,
	}
	node, err := d.GetOrCreateNode(ctx, LayerSystem, "llm_model", name, dataMap)
	if err != nil {
		return nil, fmt.Errorf("save model: %w", err)
	}

	// Update existing node data
	if err := d.UpdateNodeData(ctx, node, dataMap); err != nil {
		return nil, fmt.Errorf("update model: %w", err)
	}

	// Hot-reload router if available
	if d.router != nil {
		cfg, _ := LoadRouterConfig(ctx, d)
		d.router.UpdateConfig(cfg)
	}

	return map[string]any{
		"status":  "ok",
		"model":   name,
		"message": fmt.Sprintf("Model %s registered (provider=%s, context=%d)", name, provider, contextLength),
	}, nil
}

func toolLLMConfigSetProvider(ctx context.Context, d *Dash, name string, args map[string]any) (any, error) {
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	format, _ := args["format"].(string)
	baseURL, _ := args["base_url"].(string)
	apiKeyEnv, _ := args["api_key_env"].(string)

	if format == "" || baseURL == "" || apiKeyEnv == "" {
		return nil, fmt.Errorf("format, base_url, and api_key_env are required")
	}

	pc := ProviderConfig{
		Name:      name,
		Format:    APIFormat(format),
		BaseURL:   baseURL,
		APIKeyEnv: apiKeyEnv,
		Enabled:   true,
	}

	dataMap := map[string]any{
		"name":        pc.Name,
		"format":      string(pc.Format),
		"base_url":    pc.BaseURL,
		"api_key_env": pc.APIKeyEnv,
		"enabled":     pc.Enabled,
	}
	node, err := d.GetOrCreateNode(ctx, LayerSystem, "llm_provider", name, dataMap)
	if err != nil {
		return nil, fmt.Errorf("save provider: %w", err)
	}

	// Update existing node data
	if err := d.UpdateNodeData(ctx, node, dataMap); err != nil {
		return nil, fmt.Errorf("update provider: %w", err)
	}

	// Hot-reload router if available
	if d.router != nil {
		cfg, _ := LoadRouterConfig(ctx, d)
		d.router.UpdateConfig(cfg)
	}

	return map[string]any{
		"status":   "ok",
		"provider": name,
		"message":  fmt.Sprintf("Provider %s configured", name),
	}, nil
}

func toolLLMConfigSetRole(ctx context.Context, d *Dash, name string, args map[string]any) (any, error) {
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	provider, _ := args["provider"].(string)
	model, _ := args["model"].(string)

	if provider == "" || model == "" {
		return nil, fmt.Errorf("provider and model are required")
	}

	rc := RoleConfig{
		Role:     name,
		Provider: provider,
		Model:    model,
	}
	if mt, ok := args["max_tokens"].(float64); ok {
		rc.MaxTokens = int(mt)
	}

	dataMap := map[string]any{
		"role":     rc.Role,
		"provider": rc.Provider,
		"model":    rc.Model,
	}
	if rc.MaxTokens > 0 {
		dataMap["max_tokens"] = rc.MaxTokens
	}
	node, err := d.GetOrCreateNode(ctx, LayerSystem, "llm_role", name, dataMap)
	if err != nil {
		return nil, fmt.Errorf("save role: %w", err)
	}

	// Update existing node data
	if err := d.UpdateNodeData(ctx, node, dataMap); err != nil {
		return nil, fmt.Errorf("update role: %w", err)
	}

	// Hot-reload router if available
	if d.router != nil {
		cfg, _ := LoadRouterConfig(ctx, d)
		d.router.UpdateConfig(cfg)
	}

	return map[string]any{
		"status":  "ok",
		"role":    name,
		"message": fmt.Sprintf("Role %s → %s/%s", name, provider, model),
	}, nil
}

func toolLLMConfigRemoveNode(ctx context.Context, d *Dash, nodeType, name string) (any, error) {
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	// Find the node
	var nodeID string
	err := d.db.QueryRowContext(ctx, `
		SELECT id FROM nodes
		WHERE layer = $1 AND type = $2 AND name = $3 AND deleted_at IS NULL
	`, string(LayerSystem), nodeType, name).Scan(&nodeID)
	if err != nil {
		return nil, fmt.Errorf("not found: %s/%s", nodeType, name)
	}

	// Soft-delete
	now := time.Now()
	_, err = d.db.ExecContext(ctx, `
		UPDATE nodes SET deleted_at = $1 WHERE id = $2
	`, now, nodeID)
	if err != nil {
		return nil, fmt.Errorf("soft-delete: %w", err)
	}

	// Hot-reload router if available
	if d.router != nil {
		cfg, _ := LoadRouterConfig(ctx, d)
		d.router.UpdateConfig(cfg)
	}

	return map[string]any{
		"status":  "ok",
		"message": fmt.Sprintf("Removed %s/%s (soft-deleted)", nodeType, name),
	}, nil
}
