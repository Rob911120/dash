-- ============================================================================
-- 010_dash_schema.sql
-- Dash: Unified Graph Architecture - Foundation Schema
-- ============================================================================

-- Extensions
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ============================================================================
-- ENUMS
-- ============================================================================

-- Semantic layers
CREATE TYPE dash_layer AS ENUM (
    'CONTEXT',      -- Intentions, plans, decisions (Why?)
    'SYSTEM',       -- Services, tables, files, containers (What exists?)
    'AUTOMATION',   -- Tools, agents, schemas, patterns (How?)
    'OBSERVATION'   -- Telemetry - metrics/logs/events/traces (What happened?)
);

-- Stable topology relations
CREATE TYPE dash_relation AS ENUM (
    'depends_on',       -- A needs B to function
    'owns',             -- A owns/is responsible for B
    'uses',             -- A uses B
    'generated_by',     -- A was created by B
    'instance_of',      -- A is an instance of B
    'child_of',         -- A is child of B (hierarchy)
    'configured_by'     -- A is configured by B
);

-- Event relations (causal/lineage)
CREATE TYPE dash_event_relation AS ENUM (
    'resulted_in',      -- A led to B
    'observed',         -- A observed B
    'measured',         -- A measured B
    'failed_with',      -- A failed with B
    'triggered',        -- A triggered B
    'completed',        -- A completed B
    'started',          -- A started B
    'modified'          -- A modified B
);

-- ============================================================================
-- TABLES
-- ============================================================================

-- nodes: All entities (graph core)
CREATE TABLE nodes (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    layer           dash_layer NOT NULL,
    type            TEXT NOT NULL,
    name            TEXT NOT NULL,
    data            JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ,

    CONSTRAINT chk_nodes_type_not_empty CHECK (type <> ''),
    CONSTRAINT chk_nodes_name_not_empty CHECK (name <> '')
);

-- edges: Stable topology between nodes
CREATE TABLE edges (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id       UUID NOT NULL REFERENCES nodes(id),
    target_id       UUID NOT NULL REFERENCES nodes(id),
    relation        dash_relation NOT NULL,
    data            JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deprecated_at   TIMESTAMPTZ,

    CONSTRAINT chk_edges_no_self_loop CHECK (source_id <> target_id)
);

-- node_versions: Version history for nodes
CREATE TABLE node_versions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    node_id         UUID NOT NULL REFERENCES nodes(id),
    version         INTEGER NOT NULL,
    layer           dash_layer NOT NULL,
    type            TEXT NOT NULL,
    name            TEXT NOT NULL,
    data            JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT ux_node_versions_node_version UNIQUE (node_id, version)
);

-- ============================================================================
-- INDEXES
-- ============================================================================

-- nodes indexes
CREATE INDEX idx_nodes_layer ON nodes(layer) WHERE deleted_at IS NULL;
CREATE INDEX idx_nodes_type ON nodes(type) WHERE deleted_at IS NULL;
CREATE INDEX idx_nodes_layer_type ON nodes(layer, type) WHERE deleted_at IS NULL;
CREATE INDEX idx_nodes_name ON nodes(name) WHERE deleted_at IS NULL;
CREATE INDEX idx_nodes_data ON nodes USING GIN(data) WHERE deleted_at IS NULL;
CREATE INDEX idx_nodes_created_at ON nodes(created_at) WHERE deleted_at IS NULL;
CREATE INDEX idx_nodes_deleted_at ON nodes(deleted_at) WHERE deleted_at IS NOT NULL;

-- Partial unique index: unique name per layer+type for active nodes
CREATE UNIQUE INDEX ux_nodes_unique_active
    ON nodes(layer, type, name)
    WHERE deleted_at IS NULL;

-- edges indexes
CREATE INDEX idx_edges_source ON edges(source_id) WHERE deprecated_at IS NULL;
CREATE INDEX idx_edges_target ON edges(target_id) WHERE deprecated_at IS NULL;
CREATE INDEX idx_edges_relation ON edges(relation) WHERE deprecated_at IS NULL;
CREATE INDEX idx_edges_source_relation ON edges(source_id, relation) WHERE deprecated_at IS NULL;

-- node_versions indexes
CREATE INDEX idx_node_versions_node ON node_versions(node_id);
CREATE INDEX idx_node_versions_created ON node_versions(created_at);

-- ============================================================================
-- TRIGGER FUNCTIONS
-- ============================================================================

-- Auto-update timestamp on row modification
CREATE OR REPLACE FUNCTION update_timestamp()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Forbid OBSERVATION layer in nodes table (must use observations table)
CREATE OR REPLACE FUNCTION forbid_observation_nodes()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.layer = 'OBSERVATION' THEN
        RAISE EXCEPTION 'OBSERVATION data must be stored in observations table, not nodes';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Cascade soft delete to edges when a node is soft deleted
CREATE OR REPLACE FUNCTION cascade_soft_delete_edges()
RETURNS TRIGGER AS $$
BEGIN
    -- Only act when deleted_at changes from NULL to a value
    IF OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL THEN
        UPDATE edges
        SET deprecated_at = NEW.deleted_at
        WHERE (source_id = NEW.id OR target_id = NEW.id)
          AND deprecated_at IS NULL;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Auto-create version on node update
CREATE OR REPLACE FUNCTION create_node_version()
RETURNS TRIGGER AS $$
DECLARE
    next_version INTEGER;
BEGIN
    -- Only create version if data actually changed (not just timestamps)
    IF OLD.layer = NEW.layer
       AND OLD.type = NEW.type
       AND OLD.name = NEW.name
       AND OLD.data = NEW.data THEN
        RETURN NEW;
    END IF;

    -- Get next version number
    SELECT COALESCE(MAX(version), 0) + 1 INTO next_version
    FROM node_versions
    WHERE node_id = NEW.id;

    -- Insert version record with OLD values (snapshot before change)
    INSERT INTO node_versions (node_id, version, layer, type, name, data, created_at)
    VALUES (NEW.id, next_version, OLD.layer, OLD.type, OLD.name, OLD.data, NOW());

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- ============================================================================
-- TRIGGERS
-- ============================================================================

-- Update timestamp trigger
CREATE TRIGGER trg_nodes_update_timestamp
    BEFORE UPDATE ON nodes
    FOR EACH ROW
    EXECUTE FUNCTION update_timestamp();

-- Forbid OBSERVATION nodes trigger
CREATE TRIGGER trg_nodes_forbid_observation
    BEFORE INSERT OR UPDATE ON nodes
    FOR EACH ROW
    EXECUTE FUNCTION forbid_observation_nodes();

-- Cascade soft delete trigger
CREATE TRIGGER trg_nodes_cascade_soft_delete
    AFTER UPDATE ON nodes
    FOR EACH ROW
    EXECUTE FUNCTION cascade_soft_delete_edges();

-- Auto-versioning trigger
CREATE TRIGGER trg_nodes_create_version
    AFTER UPDATE ON nodes
    FOR EACH ROW
    EXECUTE FUNCTION create_node_version();

-- ============================================================================
-- COMMENTS
-- ============================================================================

COMMENT ON TABLE nodes IS 'All entities in the graph (except OBSERVATION telemetry)';
COMMENT ON TABLE edges IS 'Stable topology relationships between nodes';
COMMENT ON TABLE node_versions IS 'Version history snapshots for nodes';

COMMENT ON COLUMN nodes.layer IS 'Semantic layer: CONTEXT, SYSTEM, AUTOMATION (not OBSERVATION)';
COMMENT ON COLUMN nodes.deleted_at IS 'Soft delete timestamp - never hard delete';
COMMENT ON COLUMN edges.deprecated_at IS 'Edge deprecation timestamp - set automatically on node soft delete';
