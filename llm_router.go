package dash

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"
)

// LLMRouter dispatches LLM requests to the right provider based on role configuration.
// It implements both EmbeddingClient and SummaryClient.
type LLMRouter struct {
	config     RouterConfig
	httpClient *http.Client
	mu         sync.RWMutex
}

// NewLLMRouter creates a router from the given config.
// If cfg.EnvFile is set, API keys are loaded from that file automatically.
func NewLLMRouter(cfg RouterConfig) *LLMRouter {
	if cfg.EnvFile != "" {
		LoadEnvFromMCPConfig(cfg.EnvFile)
	}
	return &LLMRouter{
		config: cfg,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// UpdateConfig replaces the router's configuration (hot-reload).
func (r *LLMRouter) UpdateConfig(cfg RouterConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.config = cfg
}

// Config returns a snapshot of the current configuration.
func (r *LLMRouter) Config() RouterConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.config
}

// resolve returns the provider and role config for a given role name.
func (r *LLMRouter) resolve(role string) (ProviderConfig, RoleConfig, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	rc, ok := r.config.Roles[role]
	if !ok {
		return ProviderConfig{}, RoleConfig{}, fmt.Errorf("unknown role: %s", role)
	}
	pc, ok := r.config.Providers[rc.Provider]
	if !ok {
		return ProviderConfig{}, RoleConfig{}, fmt.Errorf("unknown provider %s for role %s", rc.Provider, role)
	}
	if !pc.Enabled {
		return ProviderConfig{}, RoleConfig{}, fmt.Errorf("provider %s is disabled", pc.Name)
	}
	return pc, rc, nil
}

// --- EmbeddingClient implementation ---

// Embed generates an embedding for the given text using the "embed" role.
func (r *LLMRouter) Embed(ctx context.Context, text string) ([]float32, error) {
	if len(text) > MaxEmbeddingTextSize {
		text = text[:MaxEmbeddingTextSize]
	}

	prov, role, err := r.resolve("embed")
	if err != nil {
		return nil, err
	}

	switch prov.Format {
	case FormatOpenAI:
		return doOpenAIEmbed(ctx, r.httpClient, prov, role.Model, text)
	default:
		return nil, fmt.Errorf("embeddings not supported for format %s", prov.Format)
	}
}

// --- SummaryClient implementation ---

const defaultSummaryPrompt = "Summarize this file in 1-2 sentences in English. Focus on what the file does, not implementation details."

// Summarize generates a summary of file content using the "summarize" role.
func (r *LLMRouter) Summarize(ctx context.Context, content, filePath string) (string, error) {
	if len(content) > MaxEmbeddingTextSize {
		content = content[:MaxEmbeddingTextSize]
	}
	userMsg := fmt.Sprintf("File: %s\n\n%s", filePath, content)
	return r.Complete(ctx, defaultSummaryPrompt, userMsg)
}

// Complete sends a completion request using the "summarize" role.
func (r *LLMRouter) Complete(ctx context.Context, systemPrompt, userMsg string) (string, error) {
	prov, role, err := r.resolve("summarize")
	if err != nil {
		return "", err
	}

	messages := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userMsg},
	}

	opts := CompleteOpts{
		MaxTokens:   role.MaxTokens,
		Temperature: role.Temperature,
	}

	switch prov.Format {
	case FormatOpenAI:
		return doOpenAIComplete(ctx, r.httpClient, prov, role.Model, messages, opts)
	case FormatAnthropic:
		return doAnthropicComplete(ctx, r.httpClient, prov, role.Model, messages, opts)
	default:
		return "", fmt.Errorf("unknown format: %s", prov.Format)
	}
}

// CompleteWithRole sends a completion request using the specified role.
func (r *LLMRouter) CompleteWithRole(ctx context.Context, role, systemPrompt, userMsg string) (string, error) {
	prov, rc, err := r.resolve(role)
	if err != nil {
		return "", err
	}

	messages := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userMsg},
	}

	opts := CompleteOpts{
		MaxTokens:   rc.MaxTokens,
		Temperature: rc.Temperature,
	}

	switch prov.Format {
	case FormatOpenAI:
		return doOpenAIComplete(ctx, r.httpClient, prov, rc.Model, messages, opts)
	case FormatAnthropic:
		return doAnthropicComplete(ctx, r.httpClient, prov, rc.Model, messages, opts)
	default:
		return "", fmt.Errorf("unknown format: %s", prov.Format)
	}
}

// --- Streaming ---

// Stream sends a streaming chat completion for the given role and returns a channel of events.
func (r *LLMRouter) Stream(ctx context.Context, role string, messages []ChatMessage, tools []map[string]any) <-chan StreamEvent {
	ch := make(chan StreamEvent, 64)
	go func() {
		defer close(ch)

		prov, rc, err := r.resolve(role)
		if err != nil {
			ch <- StreamEvent{Type: EventError, Error: err}
			ch <- StreamEvent{Type: EventDone}
			return
		}

		switch prov.Format {
		case FormatOpenAI:
			streamOpenAI(ctx, r.httpClient, prov, rc.Model, messages, tools, ch)
		case FormatAnthropic:
			streamAnthropic(ctx, r.httpClient, prov, rc.Model, messages, tools, ch)
		default:
			ch <- StreamEvent{Type: EventError, Error: fmt.Errorf("unknown format: %s", prov.Format)}
			ch <- StreamEvent{Type: EventDone}
		}
	}()
	return ch
}

// StreamWithModel streams using a specific model, resolving provider from the model prefix or from a fallback role.
func (r *LLMRouter) StreamWithModel(ctx context.Context, model string, messages []ChatMessage, tools []map[string]any) <-chan StreamEvent {
	ch := make(chan StreamEvent, 64)
	go func() {
		defer close(ch)

		prov, found := r.findProviderForModel(model)
		if !found {
			ch <- StreamEvent{Type: EventError, Error: fmt.Errorf("no provider for model: %s", model)}
			ch <- StreamEvent{Type: EventDone}
			return
		}

		// Strip tools for providers that don't support them
		if !prov.SupportsTools {
			tools = nil
		}

		switch prov.Format {
		case FormatOpenAI:
			streamOpenAI(ctx, r.httpClient, prov, model, messages, tools, ch)
		case FormatAnthropic:
			streamAnthropic(ctx, r.httpClient, prov, model, messages, tools, ch)
		default:
			ch <- StreamEvent{Type: EventError, Error: fmt.Errorf("unknown format: %s", prov.Format)}
			ch <- StreamEvent{Type: EventDone}
		}
	}()
	return ch
}

// findProviderForModel finds the best provider for a given model string.
// Strategy: check if any role already uses this model, or match by provider name prefix.
func (r *LLMRouter) findProviderForModel(model string) (ProviderConfig, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Check explicit model aliases first
	if provName, ok := r.config.ModelAliases[model]; ok {
		if prov, ok := r.config.Providers[provName]; ok && prov.Enabled {
			return prov, true
		}
	}

	// Check Models config (centralized model→provider mapping)
	if mc, ok := r.config.Models[model]; ok && mc.Provider != "" {
		if prov, ok := r.config.Providers[mc.Provider]; ok && prov.Enabled {
			return prov, true
		}
	}

	// Check roles
	for _, rc := range r.config.Roles {
		if rc.Model == model {
			if prov, ok := r.config.Providers[rc.Provider]; ok && prov.Enabled {
				return prov, true
			}
		}
	}

	// Match by provider name prefix (e.g. "anthropic/claude-..." → "anthropic" provider,
	// but also handle OpenRouter-style model names like "anthropic/claude-opus-4")
	// If a model has a prefix matching a provider name, use that provider.
	// But first check if it matches a non-openrouter provider for direct access.
	for name, prov := range r.config.Providers {
		if !prov.Enabled {
			continue
		}
		if name == "openrouter" {
			continue // check openrouter last
		}
		// Direct provider match: model starts with provider name
		if len(model) > len(name)+1 && model[:len(name)+1] == name+"/" {
			return prov, true
		}
	}

	// Fall back to openrouter (handles most prefixed models)
	if prov, ok := r.config.Providers["openrouter"]; ok && prov.Enabled {
		return prov, true
	}

	// Last resort: return first enabled provider
	for _, prov := range r.config.Providers {
		if prov.Enabled {
			return prov, true
		}
	}
	return ProviderConfig{}, false
}

// HasProvider returns true if a provider with the given name is configured and enabled.
func (r *LLMRouter) HasProvider(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	prov, ok := r.config.Providers[name]
	return ok && prov.Enabled
}

// ModelInfo is returned by AvailableModels with runtime status.
type ModelInfo struct {
	Name          string `json:"name"`
	Provider      string `json:"provider"`
	ContextLength int    `json:"context_length"`
	Available     bool   `json:"available"` // true if provider enabled + has API key
}

// AvailableModels returns all configured models with their availability status.
func (r *LLMRouter) AvailableModels() []ModelInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	models := make([]ModelInfo, 0, len(r.config.Models))
	for _, mc := range r.config.Models {
		available := false
		if prov, ok := r.config.Providers[mc.Provider]; ok && prov.Enabled {
			available = resolveAPIKey(prov) != ""
		}
		models = append(models, ModelInfo{
			Name:          mc.Name,
			Provider:      mc.Provider,
			ContextLength: mc.ContextLength,
			Available:     available,
		})
	}

	sort.Slice(models, func(i, j int) bool {
		return models[i].Name < models[j].Name
	})
	return models
}

// ContextLimit returns the context window size for a model, falling back to 128000.
func (r *LLMRouter) ContextLimit(model string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if mc, ok := r.config.Models[model]; ok && mc.ContextLength > 0 {
		return mc.ContextLength
	}
	return 128000
}
