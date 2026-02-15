package dash

import (
	"context"
	"fmt"
)

// EmbeddingClient generates vector embeddings from text.
type EmbeddingClient interface {
	// Embed generates a vector embedding for the given text.
	// Returns a 1536-dimensional float32 vector.
	Embed(ctx context.Context, text string) ([]float32, error)
}

// MaxEmbeddingTextSize is the maximum text size for embedding (in bytes).
// OpenAI embeddings have max 8191 tokens (~32KB text).
// We use 32KB as a safe limit.
const MaxEmbeddingTextSize = 32 * 1024

// NoOpEmbedder is a no-op embedder that returns nil (used when no API key is configured).
type NoOpEmbedder struct{}

// Embed returns nil for NoOpEmbedder.
func (n *NoOpEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return nil, nil
}

// extractEmbeddableText returns text suitable for embedding based on node type.
// For CONTEXT nodes (tasks, insights, decisions, todos), it combines relevant fields.
// For SYSTEM.file nodes, returns empty (file content should be read separately).
func extractEmbeddableText(node *Node) string {
	if node == nil {
		return ""
	}

	data := extractNodeData(node)
	getString := func(key string) string {
		if v, ok := data[key].(string); ok {
			return v
		}
		return ""
	}

	var parts []string

	switch node.Type {
	case "task":
		if node.Name != "" {
			parts = append(parts, node.Name)
		}
		if desc := getString("description"); desc != "" {
			parts = append(parts, desc)
		}
		if status := getString("status"); status != "" {
			parts = append(parts, "status: "+status)
		}
	case "insight":
		if text := getString("text"); text != "" {
			parts = append(parts, text)
		}
		if ctx := getString("context"); ctx != "" {
			parts = append(parts, ctx)
		}
	case "decision":
		// Try "text" first (new format), fall back to "decision" (old format)
		if text := getString("text"); text != "" {
			parts = append(parts, text)
		} else if dec := getString("decision"); dec != "" {
			parts = append(parts, dec)
		}
		if rationale := getString("rationale"); rationale != "" {
			parts = append(parts, rationale)
		}
		if ctx := getString("context"); ctx != "" {
			parts = append(parts, ctx)
		}
	case "todo":
		if text := getString("text"); text != "" {
			parts = append(parts, text)
		}
		if ctx := getString("context"); ctx != "" {
			parts = append(parts, ctx)
		}
	case "context_frame":
		if card := getString("card_text"); card != "" {
			parts = append(parts, card)
		} else if focus := getString("current_focus"); focus != "" {
			parts = append(parts, focus)
		}
	default:
		if node.Name != "" {
			parts = append(parts, node.Name)
		}
	}

	// Fallback to node name if no text was extracted from data
	if len(parts) == 0 && node.Name != "" {
		parts = append(parts, node.Name)
	}

	result := ""
	for i, p := range parts {
		if i > 0 {
			result += "\n"
		}
		result += p
	}
	return result
}

// EmbedNode generates and stores an embedding for a CONTEXT node.
// Safe to call async. No-op if embedder is not configured or text is empty.
func (d *Dash) EmbedNode(ctx context.Context, node *Node) error {
	if !d.HasRealEmbedder() {
		return nil
	}
	if node == nil {
		return nil
	}

	text := extractEmbeddableText(node)
	if text == "" {
		return nil
	}

	embedding, err := d.embedder.Embed(ctx, text)
	if err != nil {
		return fmt.Errorf("embed node %s: %w", node.ID, err)
	}
	if embedding == nil {
		return nil
	}

	hash := hashContent(text)
	return d.UpdateNodeEmbedding(ctx, node.ID, embedding, hash)
}

