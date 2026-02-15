package dash

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

func defEmbed() *ToolDef {
	return &ToolDef{
		Name:        "embed",
		Description: "Manage file embeddings for semantic search. Operations: status (show stats), backfill (generate missing).",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"op"},
			"properties": map[string]any{
				"op":    map[string]any{"type": "string", "enum": []string{"status", "backfill"}, "description": "Operation: status or backfill"},
				"limit": map[string]any{"type": "integer", "description": "Max files to process for backfill (default: 10)"},
			},
		},
		Tags: []string{"admin"},
		Fn:   toolEmbed,
	}
}

func toolEmbed(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	op, _ := args["op"].(string)
	if op == "" {
		return nil, fmt.Errorf("op is required")
	}

	switch op {
	case "status":
		// File stats
		fileRow := d.db.QueryRowContext(ctx, `
			SELECT
				COUNT(*) as total,
				COUNT(embedding) as with_embedding,
				COUNT(content_hash) as with_hash,
				COUNT(*) FILTER (WHERE content_hash IS NOT NULL AND embedding IS NULL) as needs_embedding
			FROM nodes
			WHERE layer = 'SYSTEM' AND type = 'file' AND deleted_at IS NULL
		`)
		var fileTotal, fileEmbed, fileHash, fileNeeds int
		if err := fileRow.Scan(&fileTotal, &fileEmbed, &fileHash, &fileNeeds); err != nil {
			return nil, err
		}

		// Context node stats
		ctxRow := d.db.QueryRowContext(ctx, `
			SELECT
				COUNT(*) as total,
				COUNT(embedding) as with_embedding,
				COUNT(*) FILTER (WHERE embedding IS NULL) as needs_embedding
			FROM nodes
			WHERE layer = 'CONTEXT' AND type IN ('task','insight','decision','todo')
			  AND deleted_at IS NULL
		`)
		var ctxTotal, ctxEmbed, ctxNeeds int
		if err := ctxRow.Scan(&ctxTotal, &ctxEmbed, &ctxNeeds); err != nil {
			return nil, err
		}

		return map[string]any{
			"files": map[string]any{
				"total":           fileTotal,
				"with_embedding":  fileEmbed,
				"with_hash":       fileHash,
				"needs_embedding": fileNeeds,
			},
			"context_nodes": map[string]any{
				"total":           ctxTotal,
				"with_embedding":  ctxEmbed,
				"needs_embedding": ctxNeeds,
			},
			"total_with_embedding": fileEmbed + ctxEmbed,
			"total_needs":          fileNeeds + ctxNeeds,
			"embedder_ready":       d.HasRealEmbedder(),
		}, nil

	case "backfill":
		if !d.HasRealEmbedder() {
			return nil, fmt.Errorf("embedder not configured (no LLM provider with embedding support)")
		}

		limit := 10
		if l, ok := args["limit"].(float64); ok {
			limit = int(l)
		}
		if limit > 50 {
			limit = 50
		}

		var processed []map[string]any
		var errors []map[string]any

		// 1. Backfill CONTEXT nodes (tasks, insights, decisions, todos)
		ctxRows, err := d.db.QueryContext(ctx, `
			SELECT id, layer, type, name, data
			FROM nodes
			WHERE layer = 'CONTEXT' AND type IN ('task','insight','decision','todo')
			  AND embedding IS NULL
			  AND deleted_at IS NULL
			ORDER BY updated_at DESC
			LIMIT $1
		`, limit)
		if err != nil {
			return nil, err
		}

		var ctxNodes []struct {
			id    uuid.UUID
			node  Node
		}
		for ctxRows.Next() {
			var n Node
			if err := ctxRows.Scan(&n.ID, &n.Layer, &n.Type, &n.Name, &n.Data); err != nil {
				continue
			}
			ctxNodes = append(ctxNodes, struct {
				id   uuid.UUID
				node Node
			}{n.ID, n})
		}
		ctxRows.Close()

		for _, cn := range ctxNodes {
			text := extractEmbeddableText(&cn.node)
			if text == "" {
				continue
			}

			embedding, err := d.Embedder().Embed(ctx, text)
			if err != nil {
				errors = append(errors, map[string]any{
					"name":  cn.node.Name,
					"type":  cn.node.Type,
					"error": err.Error(),
				})
				continue
			}

			hash := hashContent(text)
			if err := d.UpdateNodeEmbedding(ctx, cn.id, embedding, hash); err != nil {
				errors = append(errors, map[string]any{
					"name":  cn.node.Name,
					"type":  cn.node.Type,
					"error": err.Error(),
				})
				continue
			}

			processed = append(processed, map[string]any{
				"name":  cn.node.Name,
				"layer": cn.node.Layer,
				"type":  cn.node.Type,
			})
		}

		// 2. Backfill SYSTEM.file nodes (remaining budget)
		fileLimit := limit - len(processed)
		if fileLimit > 0 {
			fileRows, err := d.db.QueryContext(ctx, `
				SELECT id, name
				FROM nodes
				WHERE layer = 'SYSTEM' AND type = 'file'
				  AND embedding IS NULL
				  AND deleted_at IS NULL
				ORDER BY updated_at DESC
				LIMIT $1
			`, fileLimit)
			if err != nil {
				return nil, err
			}

			var fileNodes []struct {
				id   uuid.UUID
				path string
			}
			for fileRows.Next() {
				var id uuid.UUID
				var filePath string
				if err := fileRows.Scan(&id, &filePath); err != nil {
					continue
				}
				fileNodes = append(fileNodes, struct {
					id   uuid.UUID
					path string
				}{id, filePath})
			}
			fileRows.Close()

			for _, fn := range fileNodes {
				content, err := readFileForEmbedding(fn.path)
				if err != nil || content == "" {
					errors = append(errors, map[string]any{
						"path":   fn.path,
						"reason": "unreadable or binary",
					})
					continue
				}

				embedding, err := d.Embedder().Embed(ctx, content)
				if err != nil {
					errors = append(errors, map[string]any{
						"path":  fn.path,
						"error": err.Error(),
					})
					continue
				}

				hash := hashContent(content)
				if err := d.UpdateNodeEmbedding(ctx, fn.id, embedding, hash); err != nil {
					errors = append(errors, map[string]any{
						"path":  fn.path,
						"error": err.Error(),
					})
					continue
				}

				processed = append(processed, map[string]any{
					"path":  fn.path,
					"layer": "SYSTEM",
					"type":  "file",
				})
			}
		}

		return map[string]any{
			"processed": processed,
			"errors":    errors,
			"count":     len(processed),
		}, nil

	default:
		return nil, fmt.Errorf("unknown operation: %s (use: status, backfill)", op)
	}
}
