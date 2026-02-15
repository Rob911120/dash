// Package dash provides a graph-based architecture for modeling systems,
// intentions, automation, and telemetry.
package dash

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Layer represents the semantic layer of a node.
type Layer string

const (
	LayerContext    Layer = "CONTEXT"    // Intentions, plans, decisions (Why?)
	LayerSystem     Layer = "SYSTEM"     // Services, tables, files, containers (What exists?)
	LayerAutomation Layer = "AUTOMATION" // Tools, agents, schemas, patterns (How?)
	// LayerObservation is not valid for nodes - use observations table
)

// Relation represents stable topology relationships between nodes.
type Relation string

const (
	RelationDependsOn    Relation = "depends_on"    // A needs B to function
	RelationOwns         Relation = "owns"          // A owns/is responsible for B
	RelationUses         Relation = "uses"          // A uses B
	RelationGeneratedBy  Relation = "generated_by"  // A was created by B
	RelationInstanceOf   Relation = "instance_of"   // A is an instance of B
	RelationChildOf      Relation = "child_of"      // A is child of B (hierarchy)
	RelationConfiguredBy Relation = "configured_by" // A is configured by B
	RelationImplements   Relation = "implements"    // task → intent/mission
	RelationAffects      Relation = "affects"       // task → file
	RelationDerivedFrom  Relation = "derived_from"  // insight → session
	RelationJustifies    Relation = "justifies"     // decision → task
	RelationBasedOn      Relation = "based_on"      // decision → insight
	RelationPointsTo     Relation = "points_to"     // context_frame → task/summary
	RelationSupersedes   Relation = "supersedes"    // newer insight → older
	RelationAssignedTo   Relation = "assigned_to"   // work_order → agent
	RelationProduces     Relation = "produces"      // work_order → file/commit
	RelationScopedTo     Relation = "scoped_to"     // work_order → file (scope boundary)
)

// EventRelation represents causal/lineage relationships in edge_events.
type EventRelation string

const (
	EventRelationResultedIn EventRelation = "resulted_in" // A led to B
	EventRelationObserved   EventRelation = "observed"    // A observed B
	EventRelationMeasured   EventRelation = "measured"    // A measured B
	EventRelationFailedWith EventRelation = "failed_with" // A failed with B
	EventRelationTriggered  EventRelation = "triggered"   // A triggered B
	EventRelationCompleted  EventRelation = "completed"   // A completed B
	EventRelationStarted    EventRelation = "started"     // A started B
	EventRelationModified   EventRelation = "modified"    // A modified B
)

// Node represents an entity in the graph.
type Node struct {
	ID          uuid.UUID       `json:"id"`
	Layer       Layer           `json:"layer"`
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Data        json.RawMessage `json:"data"`
	Embedding   []float32       `json:"embedding,omitempty"`
	ContentHash string          `json:"content_hash,omitempty"`
	EmbeddingAt *time.Time      `json:"embedding_at,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	DeletedAt   *time.Time      `json:"deleted_at,omitempty"`
}

// Edge represents a stable topology relationship between nodes.
type Edge struct {
	ID           uuid.UUID       `json:"id"`
	SourceID     uuid.UUID       `json:"source_id"`
	TargetID     uuid.UUID       `json:"target_id"`
	Relation     Relation        `json:"relation"`
	Data         json.RawMessage `json:"data"`
	CreatedAt    time.Time       `json:"created_at"`
	DeprecatedAt *time.Time      `json:"deprecated_at,omitempty"`
}

// EdgeEvent represents a causal/lineage event between nodes.
type EdgeEvent struct {
	ID         uuid.UUID       `json:"id"`
	SourceID   uuid.UUID       `json:"source_id"`
	TargetID   uuid.UUID       `json:"target_id"`
	Relation   EventRelation   `json:"relation"`
	Success    bool            `json:"success"`
	DurationMs *int            `json:"duration_ms,omitempty"`
	Data       json.RawMessage `json:"data"`
	OccurredAt time.Time       `json:"occurred_at"`
}

// Observation represents telemetry data for a node.
type Observation struct {
	ID         uuid.UUID       `json:"id"`
	NodeID     uuid.UUID       `json:"node_id"`
	Type       string          `json:"type"`
	Value      *float64        `json:"value,omitempty"`
	Data       json.RawMessage `json:"data"`
	ObservedAt time.Time       `json:"observed_at"`
}

// NodeVersion represents a historical snapshot of a node.
type NodeVersion struct {
	ID        uuid.UUID       `json:"id"`
	NodeID    uuid.UUID       `json:"node_id"`
	Version   int             `json:"version"`
	Layer     Layer           `json:"layer"`
	Type      string          `json:"type"`
	Name      string          `json:"name"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
}

// TimeRange represents a time range for querying time-partitioned data.
type TimeRange struct {
	Start time.Time
	End   time.Time
}

// Dash is the main client for interacting with the Dash graph database.
type Dash struct {
	db         *sql.DB
	fileConfig *FileConfig
	executors  map[string]Executor
	embedder   EmbeddingClient
	summarizer SummaryClient
	registry   *ToolRegistry
	router     *LLMRouter
}

// Config holds configuration for creating a new Dash client.
type Config struct {
	DB              *sql.DB
	FileAllowedRoot string
	Embedder        EmbeddingClient // Optional: if nil, embeddings are disabled
	Summarizer      SummaryClient   // Optional: if nil, summaries are disabled
	Router          *LLMRouter      // Optional: if set, used as embedder + summarizer
}

// New creates a new Dash client with the given configuration.
func New(cfg Config) (*Dash, error) {
	fc, err := NewFileConfig(cfg.FileAllowedRoot)
	if err != nil {
		return nil, err
	}

	d := &Dash{
		db:         cfg.DB,
		fileConfig: fc,
		executors:  make(map[string]Executor),
		embedder:   cfg.Embedder,
		summarizer: cfg.Summarizer,
		registry:   NewToolRegistry(),
		router:     cfg.Router,
	}

	// If router is provided, use it as embedder and summarizer
	if d.router != nil {
		if d.embedder == nil {
			d.embedder = d.router
		}
		if d.summarizer == nil {
			d.summarizer = d.router
		}
	}

	// If no embedder provided, use NoOp
	if d.embedder == nil {
		d.embedder = &NoOpEmbedder{}
	}

	// If no summarizer provided, use NoOp
	if d.summarizer == nil {
		d.summarizer = &NoOpSummarizer{}
	}

	// Register default executors
	d.RegisterExecutor("sql", &SQLExecutor{db: cfg.DB})
	d.RegisterExecutor("filesystem_read", &FileReadExecutor{config: fc})
	d.RegisterExecutor("filesystem_write", &FileWriteExecutor{config: fc})
	d.RegisterExecutor("http", &HTTPExecutor{})

	// Register all builtin tools
	registerBuiltinTools(d)

	return d, nil
}

// Embedder returns the configured embedding client.
func (d *Dash) Embedder() EmbeddingClient {
	return d.embedder
}

// EmbedText generates an embedding vector for the given text.
func (d *Dash) EmbedText(ctx context.Context, text string) ([]float32, error) {
	return d.embedder.Embed(ctx, text)
}

// HasRealEmbedder returns true if a real (non-NoOp) embedder is configured.
func (d *Dash) HasRealEmbedder() bool {
	if d.embedder == nil {
		return false
	}
	_, isNoOp := d.embedder.(*NoOpEmbedder)
	return !isNoOp
}

// HasRealSummarizer returns true if a real (non-NoOp) summarizer is configured.
func (d *Dash) HasRealSummarizer() bool {
	if d.summarizer == nil {
		return false
	}
	_, isNoOp := d.summarizer.(*NoOpSummarizer)
	return !isNoOp
}

// Router returns the configured LLM router, or nil.
func (d *Dash) Router() *LLMRouter {
	return d.router
}

// DB returns the underlying database connection.
func (d *Dash) DB() *sql.DB {
	return d.db
}

// Close closes the Dash client.
func (d *Dash) Close() error {
	return d.db.Close()
}

// WithTx executes a function within a database transaction.
func (d *Dash) WithTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit()
}

// scanNode scans a node from a row.
func scanNode(scanner interface {
	Scan(dest ...any) error
}) (*Node, error) {
	var n Node
	var deletedAt sql.NullTime

	err := scanner.Scan(
		&n.ID,
		&n.Layer,
		&n.Type,
		&n.Name,
		&n.Data,
		&n.CreatedAt,
		&n.UpdatedAt,
		&deletedAt,
	)
	if err != nil {
		return nil, err
	}

	if deletedAt.Valid {
		n.DeletedAt = &deletedAt.Time
	}

	return &n, nil
}

// scanEdge scans an edge from a row.
func scanEdge(scanner interface {
	Scan(dest ...any) error
}) (*Edge, error) {
	var e Edge
	var deprecatedAt sql.NullTime

	err := scanner.Scan(
		&e.ID,
		&e.SourceID,
		&e.TargetID,
		&e.Relation,
		&e.Data,
		&e.CreatedAt,
		&deprecatedAt,
	)
	if err != nil {
		return nil, err
	}

	if deprecatedAt.Valid {
		e.DeprecatedAt = &deprecatedAt.Time
	}

	return &e, nil
}

// scanEdgeEvent scans an edge event from a row.
func scanEdgeEvent(scanner interface {
	Scan(dest ...any) error
}) (*EdgeEvent, error) {
	var e EdgeEvent
	var durationMs sql.NullInt32

	err := scanner.Scan(
		&e.ID,
		&e.SourceID,
		&e.TargetID,
		&e.Relation,
		&e.Success,
		&durationMs,
		&e.Data,
		&e.OccurredAt,
	)
	if err != nil {
		return nil, err
	}

	if durationMs.Valid {
		d := int(durationMs.Int32)
		e.DurationMs = &d
	}

	return &e, nil
}

// scanObservation scans an observation from a row.
func scanObservation(scanner interface {
	Scan(dest ...any) error
}) (*Observation, error) {
	var o Observation
	var value sql.NullFloat64

	err := scanner.Scan(
		&o.ID,
		&o.NodeID,
		&o.Type,
		&value,
		&o.Data,
		&o.ObservedAt,
	)
	if err != nil {
		return nil, err
	}

	if value.Valid {
		o.Value = &value.Float64
	}

	return &o, nil
}

// scanNodeVersion scans a node version from a row.
func scanNodeVersion(scanner interface {
	Scan(dest ...any) error
}) (*NodeVersion, error) {
	var v NodeVersion

	err := scanner.Scan(
		&v.ID,
		&v.NodeID,
		&v.Version,
		&v.Layer,
		&v.Type,
		&v.Name,
		&v.Data,
		&v.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	return &v, nil
}
