package dash

import (
	"context"
	"fmt"
)

func defPatterns() *ToolDef {
	return &ToolDef{
		Name:        "patterns",
		Description: "Detect patterns: co-editing (files modified together), file-churn (frequently modified files), tool-sequences (common tool pairs).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"type":      map[string]any{"type": "string", "enum": []string{"co-editing", "file-churn", "tool-sequences", "all"}, "description": "Pattern type to detect (default: all)"},
				"min_count": map[string]any{"type": "integer", "description": "Minimum frequency threshold (default: 2)"},
				"store":     map[string]any{"type": "boolean", "description": "Store co-editing patterns as AUTOMATION.pattern nodes (default: false)"},
			},
		},
		Tags: []string{"read", "analysis"},
		Fn:   toolPatterns,
	}
}

func toolPatterns(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	patternType := "all"
	if t, ok := args["type"].(string); ok && t != "" {
		patternType = t
	}
	minCount := 2
	if m, ok := args["min_count"].(float64); ok {
		minCount = int(m)
	}

	result := map[string]any{}

	if patternType == "co-editing" || patternType == "all" {
		patterns, err := d.DetectCoEditingPatterns(ctx, minCount)
		if err != nil {
			return nil, err
		}
		result["co_editing"] = patterns
		if store, ok := args["store"].(bool); ok && store {
			if err := d.StorePatterns(ctx, patterns); err != nil {
				return nil, fmt.Errorf("store patterns: %w", err)
			}
			result["stored"] = true
		}
	}

	if patternType == "file-churn" || patternType == "all" {
		churn, err := d.DetectFileChurn(ctx, minCount)
		if err != nil {
			return nil, err
		}
		result["file_churn"] = churn
	}

	if patternType == "tool-sequences" || patternType == "all" {
		seqs, err := d.DetectToolSequences(ctx, minCount)
		if err != nil {
			return nil, err
		}
		result["tool_sequences"] = seqs
	}

	return result, nil
}
