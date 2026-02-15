package dash

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// UISettings holds the user's UI configuration including tone preset and tab labels.
type UISettings struct {
	TonePreset   string            `json:"tone_preset"`
	TabLabels    map[string]string `json:"tab_labels"`
	TabOverrides map[string]string `json:"tab_overrides,omitempty"`
	LastChanged  *time.Time        `json:"last_changed,omitempty"`
}

const queryGetUISettings = `
	SELECT id, layer, type, name, data, created_at, updated_at, deleted_at
	FROM nodes
	WHERE layer = 'CONTEXT' AND type = 'settings' AND name = 'ui'
	  AND deleted_at IS NULL
	LIMIT 1`

// GetUISettings retrieves the UI settings node and parses it into UISettings.
// Returns nil, nil if the node doesn't exist yet.
func (d *Dash) GetUISettings(ctx context.Context) (*UISettings, error) {
	qCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	row := d.db.QueryRowContext(qCtx, queryGetUISettings)
	node, err := scanNode(row)
	if err != nil {
		// Node doesn't exist yet - not an error
		if err == ErrNodeNotFound {
			return nil, nil
		}
		return nil, err
	}

	var settings UISettings
	if err := json.Unmarshal(node.Data, &settings); err != nil {
		return nil, fmt.Errorf("parse ui settings: %w", err)
	}

	return &settings, nil
}

// EnsureUISettings creates the CONTEXT.settings.ui node if it doesn't exist,
// using the given default preset. Returns the node.
func (d *Dash) EnsureUISettings(ctx context.Context, defaultPreset string) (*Node, error) {
	if defaultPreset == "" {
		defaultPreset = "action"
	}

	data := map[string]any{
		"tone_preset":   defaultPreset,
		"tab_labels":    defaultTabLabels(defaultPreset),
		"tab_overrides": map[string]string{},
		"last_changed":  nil,
	}

	return d.GetOrCreateNode(ctx, LayerContext, "settings", "ui", data)
}

// UpdateTonePreset changes the tone preset with a 30-day cooldown.
// Set force=true to bypass the cooldown.
func (d *Dash) UpdateTonePreset(ctx context.Context, newPreset string, force bool) error {
	node, err := d.GetNodeByName(ctx, LayerContext, "settings", "ui")
	if err != nil {
		return fmt.Errorf("ui settings not found: %w", err)
	}

	var data map[string]any
	if err := json.Unmarshal(node.Data, &data); err != nil {
		data = make(map[string]any)
	}

	// Check cooldown
	if !force {
		if lastStr, ok := data["last_changed"].(string); ok && lastStr != "" {
			lastChanged, err := time.Parse(time.RFC3339, lastStr)
			if err == nil && time.Since(lastChanged) < 30*24*time.Hour {
				return fmt.Errorf("tone preset was changed %s ago (30-day cooldown)", time.Since(lastChanged).Round(time.Hour))
			}
		}
	}

	labels := defaultTabLabels(newPreset)

	return d.UpdateNodeData(ctx, node, map[string]any{
		"tone_preset":   newPreset,
		"tab_labels":    labels,
		"tab_overrides": data["tab_overrides"],
		"last_changed":  time.Now().Format(time.RFC3339),
	})
}

// defaultTabLabels returns the built-in tab labels for a preset name.
func defaultTabLabels(preset string) map[string]string {
	switch preset {
	case "professional":
		return map[string]string{
			"create": "Kontext",
			"idea":   "Förslag",
			"plan":   "Plan",
			"work":   "Pågående",
		}
	case "agent-os":
		return map[string]string{
			"create": "Context",
			"idea":   "Proposals",
			"plan":   "Plan",
			"work":   "Run",
		}
	default: // "action"
		return map[string]string{
			"create": "Skapa",
			"idea":   "Spåna",
			"plan":   "Planera",
			"work":   "Kör",
		}
	}
}
