package dash

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// PromptOptions controls prompt generation behavior.
type PromptOptions struct {
	ForceRefresh        bool
	Cwd                 string
	SessionID           string
	TaskName            string
	SuggName            string
	PlanName            string
	AgentKey            string // agent identifier for agent-continuous profile
	AgentMission        string // why this agent was spawned
	ContextPressurePct  int    // 0-100, current context window usage percentage
}

// cacheTTL returns the cache TTL for a profile.
func cacheTTL(profileName string) time.Duration {
	switch profileName {
	case "task", "suggestion", "execution", "agent-continuous":
		return 1 * time.Minute
	default:
		return 5 * time.Minute
	}
}

// promptCacheKey returns the system_prompt node name for a profile+opts combo.
func promptCacheKey(profileName string, opts PromptOptions) string {
	switch profileName {
	case "task":
		if opts.TaskName != "" {
			return "task:" + opts.TaskName
		}
	case "suggestion":
		if opts.SuggName != "" {
			return "suggestion:" + opts.SuggName
		}
	case "execution":
		if opts.PlanName != "" {
			return "plan:" + opts.PlanName
		}
	case "agent-continuous":
		if opts.AgentKey != "" {
			return "agent:" + opts.AgentKey
		}
	}
	return profileName
}

// promptHeader returns a header for the assembled prompt.
func promptHeader(profileName string, opts PromptOptions) string {
	switch profileName {
	case "task":
		if opts.TaskName != "" {
			return fmt.Sprintf("== TASK: %s ==\n\n", opts.TaskName)
		}
	case "suggestion":
		if opts.SuggName != "" {
			return fmt.Sprintf("== SUGGESTION: %s ==\n\n", opts.SuggName)
		}
	case "execution":
		if opts.PlanName != "" {
			return fmt.Sprintf("== EXECUTING PLAN: %s ==\n\n", opts.PlanName)
		}
	case "agent-continuous":
		if opts.AgentKey != "" {
			return fmt.Sprintf("== AGENT: %s ==\n\n", opts.AgentKey)
		}
	}
	return "== DASH CONTEXT ==\n\n"
}

// GetPrompt is the single entry point for prompt generation.
// It loads a profile from the DB, checks cache, builds the pipeline, and returns the assembled text.
func (d *Dash) GetPrompt(ctx context.Context, profileName string, opts PromptOptions) (string, error) {
	// 1. Load profile
	profile, err := d.GetProfile(ctx, profileName)
	if err != nil {
		return "", fmt.Errorf("unknown profile %q: %w", profileName, err)
	}

	cacheKey := promptCacheKey(profileName, opts)

	// 2. Check cache (unless forced refresh)
	if !opts.ForceRefresh {
		if cached, ok := d.getCachedPrompt(ctx, cacheKey, cacheTTL(profileName)); ok {
			return appendContextPressure(cached, opts.ContextPressurePct), nil
		}
	}

	// 3. Build pipeline from profile
	pipeline := profileToPipeline(profile)

	// 4. Build source params
	params := SourceParams{
		Cwd:          opts.Cwd,
		SessionID:    opts.SessionID,
		TaskName:     opts.TaskName,
		SuggName:     opts.SuggName,
		PlanName:     opts.PlanName,
		AgentKey:     opts.AgentKey,
		AgentMission: opts.AgentMission,
	}

	// 5. Run pipeline
	dynamicText := d.RunPipeline(ctx, pipeline, params)

	// 6. Assemble: header + system_prompt + dynamic context + footer
	var b strings.Builder
	b.WriteString(promptHeader(profileName, opts))

	if profile.SystemPrompt != "" {
		b.WriteString(profile.SystemPrompt)
		b.WriteString("\n\n")
	}

	b.WriteString(dynamicText)
	b.WriteString("\n================\n")

	text := b.String()

	// 7. Cache as CONTEXT.system_prompt node (without pressure — that's per-request)
	d.cachePrompt(ctx, cacheKey, text, profile)

	return appendContextPressure(text, opts.ContextPressurePct), nil
}

// appendContextPressure adds a context pressure warning if pct >= 70.
// This is never cached — it's a per-request overlay.
func appendContextPressure(text string, pct int) string {
	if pct < 70 {
		return text
	}
	return text + fmt.Sprintf("\n⚠️ CONTEXT PRESSURE: %d%% av context-window använd. Sammanfatta och avsluta pågående arbete.\n", pct)
}

// profileToPipeline converts a PromptProfile to a Pipeline.
func profileToPipeline(profile *PromptProfile) Pipeline {
	p := Pipeline{}
	for _, srcName := range profile.Sources {
		src := PipelineSource{Name: srcName}
		if override, ok := profile.SourceConfig[srcName]; ok {
			if override.MaxItems > 0 {
				src.MaxItems = override.MaxItems
			}
			if override.Format != "" {
				src.Format = override.Format
			}
		}
		p.Sources = append(p.Sources, src)
	}
	return p
}

// getCachedPrompt checks for a cached system_prompt node within TTL.
func (d *Dash) getCachedPrompt(ctx context.Context, cacheKey string, ttl time.Duration) (string, bool) {
	node, err := d.GetNodeByName(ctx, LayerContext, "system_prompt", cacheKey)
	if err != nil || node == nil {
		return "", false
	}

	data := extractNodeData(node)
	refreshedAt, ok := data["refreshed_at"].(string)
	if !ok {
		return "", false
	}

	t, err := time.Parse(time.RFC3339, refreshedAt)
	if err != nil {
		return "", false
	}

	if time.Since(t) > ttl {
		return "", false
	}

	// Return cached text
	text, ok := data["text"].(string)
	if !ok || text == "" {
		return "", false
	}

	return text, true
}

// cachePrompt saves assembled text to a CONTEXT.system_prompt node.
func (d *Dash) cachePrompt(ctx context.Context, cacheKey, text string, profile *PromptProfile) {
	nodeData := map[string]any{
		"text":         text,
		"status":       "active",
		"refreshed_at": time.Now().Format(time.RFC3339),
		"profile":      profile.Name,
	}
	if profile.SystemPrompt != "" {
		nodeData["base_instruction"] = profile.SystemPrompt
	}

	node, err := d.GetOrCreateNode(ctx, LayerContext, "system_prompt", cacheKey, nodeData)
	if err != nil {
		return
	}
	_ = d.UpdateNodeData(ctx, node, nodeData)
}

