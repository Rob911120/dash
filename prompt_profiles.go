package dash

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// PromptProfile represents an agent prompt profile stored in the DB.
type PromptProfile struct {
	Name         string                    `json:"name"`
	Description  string                    `json:"description"`
	SystemPrompt string                    `json:"system_prompt"`
	Toolset      []string                  `json:"toolset"`
	Sources      []string                  `json:"sources"`
	SourceConfig map[string]SourceOverride `json:"source_config"`
	Active       bool                      `json:"active"`
	CreatedAt    time.Time                 `json:"created_at"`
	UpdatedAt    time.Time                 `json:"updated_at"`
}

// SourceOverride allows per-source configuration in a profile.
type SourceOverride struct {
	MaxItems int    `json:"max_items,omitempty"`
	Format   string `json:"format,omitempty"`
}

// GetProfile retrieves a prompt profile by name.
func (d *Dash) GetProfile(ctx context.Context, name string) (*PromptProfile, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT name, description, system_prompt, toolset, sources, source_config, active, created_at, updated_at
		FROM prompt_profiles
		WHERE name = $1 AND active = true`, name)

	return scanProfile(row)
}

// ListProfiles retrieves all active prompt profiles.
func (d *Dash) ListProfiles(ctx context.Context) ([]*PromptProfile, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT name, description, system_prompt, toolset, sources, source_config, active, created_at, updated_at
		FROM prompt_profiles
		WHERE active = true
		ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var profiles []*PromptProfile
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			continue
		}
		profiles = append(profiles, p)
	}
	return profiles, rows.Err()
}

// CreateProfile creates a new prompt profile.
func (d *Dash) CreateProfile(ctx context.Context, profile *PromptProfile) error {
	if profile.Name == "" {
		return fmt.Errorf("profile name is required")
	}
	if err := d.validateProfileSources(profile.Sources); err != nil {
		return err
	}
	if err := d.validateProfileToolset(profile.Toolset); err != nil {
		return err
	}

	scJSON, err := json.Marshal(profile.SourceConfig)
	if err != nil {
		return fmt.Errorf("marshal source_config: %w", err)
	}

	_, err = d.db.ExecContext(ctx, `
		INSERT INTO prompt_profiles (name, description, system_prompt, toolset, sources, source_config)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		profile.Name, profile.Description, profile.SystemPrompt,
		pq.Array(profile.Toolset), pq.Array(profile.Sources), scJSON)
	return err
}

// UpdateProfile applies a partial update to a prompt profile.
func (d *Dash) UpdateProfile(ctx context.Context, name string, patch map[string]any) error {
	// Validate sources if being updated
	if sourcesRaw, ok := patch["sources"]; ok {
		sources, err := toStringSlice(sourcesRaw)
		if err != nil {
			return fmt.Errorf("invalid sources: %w", err)
		}
		if err := d.validateProfileSources(sources); err != nil {
			return err
		}
	}

	// Validate toolset if being updated
	if toolsetRaw, ok := patch["toolset"]; ok {
		toolset, err := toStringSlice(toolsetRaw)
		if err != nil {
			return fmt.Errorf("invalid toolset: %w", err)
		}
		if err := d.validateProfileToolset(toolset); err != nil {
			return err
		}
	}

	// Build dynamic UPDATE
	setClauses := []string{"updated_at = NOW()"}
	args := []any{name}
	argIdx := 2

	for _, field := range []string{"description", "system_prompt"} {
		if v, ok := patch[field]; ok {
			s, _ := v.(string)
			setClauses = append(setClauses, fmt.Sprintf("%s = $%d", field, argIdx))
			args = append(args, s)
			argIdx++
		}
	}

	if v, ok := patch["toolset"]; ok {
		ts, _ := toStringSlice(v)
		setClauses = append(setClauses, fmt.Sprintf("toolset = $%d", argIdx))
		args = append(args, pq.Array(ts))
		argIdx++
	}

	if v, ok := patch["sources"]; ok {
		ss, _ := toStringSlice(v)
		setClauses = append(setClauses, fmt.Sprintf("sources = $%d", argIdx))
		args = append(args, pq.Array(ss))
		argIdx++
	}

	if v, ok := patch["source_config"]; ok {
		scJSON, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("marshal source_config: %w", err)
		}
		setClauses = append(setClauses, fmt.Sprintf("source_config = $%d", argIdx))
		args = append(args, scJSON)
		argIdx++
	}

	if v, ok := patch["active"]; ok {
		b, _ := v.(bool)
		setClauses = append(setClauses, fmt.Sprintf("active = $%d", argIdx))
		args = append(args, b)
		argIdx++
	}

	if len(setClauses) == 1 {
		return fmt.Errorf("no valid fields to update")
	}

	query := fmt.Sprintf("UPDATE prompt_profiles SET %s WHERE name = $1",
		joinStrings(setClauses, ", "))

	result, err := d.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("profile %q not found", name)
	}
	return nil
}

// validateProfileSources checks all source names exist in the registry.
func (d *Dash) validateProfileSources(sources []string) error {
	for _, s := range sources {
		if _, ok := sourceRegistry[s]; !ok {
			return fmt.Errorf("unknown source: %q (available: %s)", s, sourceRegistryKeys())
		}
	}
	return nil
}

// validateProfileToolset checks all tool names exist in the registry.
// Empty toolset means "all tools" and is always valid.
func (d *Dash) validateProfileToolset(toolset []string) error {
	for _, t := range toolset {
		if _, ok := d.registry.Get(t); !ok {
			return fmt.Errorf("unknown tool: %q", t)
		}
	}
	return nil
}

// sourceRegistryKeys returns a comma-separated list of source names.
func sourceRegistryKeys() string {
	keys := make([]string, 0, len(sourceRegistry))
	for k := range sourceRegistry {
		keys = append(keys, k)
	}
	return joinStrings(keys, ", ")
}

func joinStrings(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	out := ss[0]
	for _, s := range ss[1:] {
		out += sep + s
	}
	return out
}

// scanProfile scans a profile from a row.
func scanProfile(scanner interface {
	Scan(dest ...any) error
}) (*PromptProfile, error) {
	var p PromptProfile
	var scJSON []byte

	err := scanner.Scan(
		&p.Name, &p.Description, &p.SystemPrompt,
		pq.Array(&p.Toolset), pq.Array(&p.Sources),
		&scJSON, &p.Active, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	p.SourceConfig = make(map[string]SourceOverride)
	if len(scJSON) > 0 {
		json.Unmarshal(scJSON, &p.SourceConfig)
	}

	return &p, nil
}

// toStringSlice converts an interface{} to []string.
func toStringSlice(v any) ([]string, error) {
	switch val := v.(type) {
	case []string:
		return val, nil
	case []any:
		out := make([]string, len(val))
		for i, item := range val {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("element %d is not a string", i)
			}
			out[i] = s
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected []string or []any, got %T", v)
	}
}
