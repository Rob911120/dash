package dash

import (
	"context"
	"fmt"
)

func defPromptProfile() *ToolDef {
	return &ToolDef{
		Name:        "prompt_profile",
		Description: "Manage prompt profiles. Profiles define agent personas: system prompt, tool access, and context sources.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"op"},
			"properties": map[string]any{
				"op":   map[string]any{"type": "string", "enum": []string{"list", "get", "create", "update"}, "description": "Operation to perform"},
				"name": map[string]any{"type": "string", "description": "Profile name (required for get/create/update)"},
				"data": map[string]any{
					"type":        "object",
					"description": "Profile data (for create/update)",
					"properties": map[string]any{
						"description":   map[string]any{"type": "string"},
						"system_prompt": map[string]any{"type": "string"},
						"toolset":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"sources":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"source_config": map[string]any{"type": "object"},
						"active":        map[string]any{"type": "boolean"},
					},
				},
			},
		},
		Tags: []string{"write"},
		Fn:   toolPromptProfile,
	}
}

func toolPromptProfile(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	op, _ := args["op"].(string)

	switch op {
	case "list":
		profiles, err := d.ListProfiles(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, len(profiles))
		for i, p := range profiles {
			out[i] = map[string]any{
				"name":          p.Name,
				"description":   p.Description,
				"sources":       p.Sources,
				"toolset":       p.Toolset,
				"has_prompt":    p.SystemPrompt != "",
				"source_config": p.SourceConfig,
			}
		}
		return map[string]any{"profiles": out}, nil

	case "get":
		name, _ := args["name"].(string)
		if name == "" {
			return nil, fmt.Errorf("name is required for get")
		}
		p, err := d.GetProfile(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("profile %q not found", name)
		}
		return map[string]any{
			"name":          p.Name,
			"description":   p.Description,
			"system_prompt": p.SystemPrompt,
			"toolset":       p.Toolset,
			"sources":       p.Sources,
			"source_config": p.SourceConfig,
			"active":        p.Active,
			"created_at":    p.CreatedAt,
			"updated_at":    p.UpdatedAt,
		}, nil

	case "create":
		name, _ := args["name"].(string)
		if name == "" {
			return nil, fmt.Errorf("name is required for create")
		}
		data, _ := args["data"].(map[string]any)
		if data == nil {
			return nil, fmt.Errorf("data is required for create")
		}

		profile := &PromptProfile{Name: name}
		if v, ok := data["description"].(string); ok {
			profile.Description = v
		}
		if v, ok := data["system_prompt"].(string); ok {
			profile.SystemPrompt = v
		}
		if v, ok := data["toolset"]; ok {
			ts, err := toStringSlice(v)
			if err != nil {
				return nil, fmt.Errorf("invalid toolset: %w", err)
			}
			profile.Toolset = ts
		}
		if v, ok := data["sources"]; ok {
			ss, err := toStringSlice(v)
			if err != nil {
				return nil, fmt.Errorf("invalid sources: %w", err)
			}
			profile.Sources = ss
		}
		if v, ok := data["source_config"].(map[string]any); ok {
			profile.SourceConfig = parseSourceConfig(v)
		}

		if err := d.CreateProfile(ctx, profile); err != nil {
			return nil, err
		}
		return map[string]any{
			"created": true,
			"name":    name,
		}, nil

	case "update":
		name, _ := args["name"].(string)
		if name == "" {
			return nil, fmt.Errorf("name is required for update")
		}
		data, _ := args["data"].(map[string]any)
		if data == nil {
			return nil, fmt.Errorf("data is required for update")
		}

		if err := d.UpdateProfile(ctx, name, data); err != nil {
			return nil, err
		}
		return map[string]any{
			"updated": true,
			"name":    name,
		}, nil

	default:
		return nil, fmt.Errorf("unknown op: %q (use list, get, create, update)", op)
	}
}

// parseSourceConfig converts raw map to typed SourceOverride map.
func parseSourceConfig(raw map[string]any) map[string]SourceOverride {
	result := make(map[string]SourceOverride)
	for key, val := range raw {
		if m, ok := val.(map[string]any); ok {
			var so SourceOverride
			if v, ok := m["max_items"].(float64); ok {
				so.MaxItems = int(v)
			}
			if v, ok := m["format"].(string); ok {
				so.Format = v
			}
			result[key] = so
		}
	}
	return result
}
