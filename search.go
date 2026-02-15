package dash

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SearchResult represents a node found via semantic search.
type SearchResult struct {
	ID          uuid.UUID       `json:"id"`
	Name        string          `json:"name"`
	Path        string          `json:"path,omitempty"`
	Layer       string          `json:"layer"`
	Type        string          `json:"type"`
	Data        json.RawMessage `json:"data"`
	Distance    float64         `json:"distance"` // Cosine distance (0 = identical, 2 = opposite)
	EmbeddingAt *time.Time      `json:"embedding_at,omitempty"`
}

// ErrNoEmbedder is returned when semantic search is attempted without an embedder.
var ErrNoEmbedder = errors.New("embedder not configured (no LLM provider available)")

// SearchSimilarFiles performs semantic search over files using vector similarity.
// Returns files ordered by cosine similarity to the query embedding.
func (d *Dash) SearchSimilarFiles(ctx context.Context, query string, limit int) ([]*SearchResult, error) {
	if d.embedder == nil {
		return nil, ErrNoEmbedder
	}

	// Check if using NoOp embedder
	if _, isNoOp := d.embedder.(*NoOpEmbedder); isNoOp {
		return nil, ErrNoEmbedder
	}

	// Generate embedding for query
	queryEmbedding, err := d.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if queryEmbedding == nil {
		return nil, ErrNoEmbedder
	}

	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	// Convert query embedding to pgvector format and search
	return d.SearchSimilarByEmbedding(ctx, queryEmbedding, limit)
}

// SearchSimilar performs semantic search across ALL node types with embeddings.
// Returns a mixed result set: files, tasks, insights, decisions, etc.
func (d *Dash) SearchSimilar(ctx context.Context, query string, limit int) ([]*SearchResult, error) {
	if d.embedder == nil {
		return nil, ErrNoEmbedder
	}
	if _, isNoOp := d.embedder.(*NoOpEmbedder); isNoOp {
		return nil, ErrNoEmbedder
	}

	queryEmbedding, err := d.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if queryEmbedding == nil {
		return nil, ErrNoEmbedder
	}

	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	queryVector := float32SliceToVector(queryEmbedding)

	rows, err := d.db.QueryContext(ctx, `
		SELECT id, layer, type, name, data, embedding <=> $1 as distance, embedding_at
		FROM nodes
		WHERE embedding IS NOT NULL
		  AND deleted_at IS NULL
		ORDER BY embedding <=> $1
		LIMIT $2
	`, queryVector, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*SearchResult
	for rows.Next() {
		var r SearchResult
		var embeddingAt sql.NullTime
		err := rows.Scan(&r.ID, &r.Layer, &r.Type, &r.Name, &r.Data, &r.Distance, &embeddingAt)
		if err != nil {
			return nil, err
		}
		if r.Layer == "SYSTEM" && r.Type == "file" {
			r.Path = r.Name
		}
		if embeddingAt.Valid {
			r.EmbeddingAt = &embeddingAt.Time
		}
		results = append(results, &r)
	}

	return results, rows.Err()
}

// SearchSimilarByEmbedding performs semantic search over files using a pre-computed embedding.
// Kept for backward compatibility.
func (d *Dash) SearchSimilarByEmbedding(ctx context.Context, embedding []float32, limit int) ([]*SearchResult, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	queryVector := float32SliceToVector(embedding)

	rows, err := d.db.QueryContext(ctx, `
		SELECT id, layer, type, name, data, embedding <=> $1 as distance, embedding_at
		FROM nodes
		WHERE layer = 'SYSTEM' AND type = 'file'
		  AND embedding IS NOT NULL
		  AND deleted_at IS NULL
		ORDER BY embedding <=> $1
		LIMIT $2
	`, queryVector, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*SearchResult
	for rows.Next() {
		var r SearchResult
		var embeddingAt sql.NullTime
		err := rows.Scan(&r.ID, &r.Layer, &r.Type, &r.Name, &r.Data, &r.Distance, &embeddingAt)
		if err != nil {
			return nil, err
		}
		r.Path = r.Name
		if embeddingAt.Valid {
			r.EmbeddingAt = &embeddingAt.Time
		}
		results = append(results, &r)
	}

	return results, rows.Err()
}

// GetFilesNeedingEmbedding returns files that have content_hash but no embedding.
func (d *Dash) GetFilesNeedingEmbedding(ctx context.Context, limit int) ([]*Node, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := d.db.QueryContext(ctx, `
		SELECT id, layer, type, name, data, created_at, updated_at, deleted_at
		FROM nodes
		WHERE layer = 'SYSTEM' AND type = 'file'
		  AND content_hash IS NOT NULL
		  AND embedding IS NULL
		  AND deleted_at IS NULL
		ORDER BY updated_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanNodes(rows)
}
