-- ============================================================================
-- 012_dash_views_and_funcs.sql
-- Dash: Unified Graph Architecture - Views and Functions
-- ============================================================================

-- ============================================================================
-- ACTIVE ENTITY VIEWS
-- ============================================================================

-- Active nodes (not soft deleted)
CREATE OR REPLACE VIEW v_nodes_active AS
SELECT *
FROM nodes
WHERE deleted_at IS NULL;

-- Active edges (not deprecated)
CREATE OR REPLACE VIEW v_edges_active AS
SELECT *
FROM edges
WHERE deprecated_at IS NULL;

-- ============================================================================
-- LAYER-SPECIFIC VIEWS
-- ============================================================================

-- System state: all active SYSTEM layer nodes with their dependencies
CREATE OR REPLACE VIEW v_system_state AS
SELECT
    n.id,
    n.type,
    n.name,
    n.data,
    n.created_at,
    n.updated_at,
    ARRAY_AGG(DISTINCT t.name) FILTER (WHERE t.id IS NOT NULL) AS depends_on
FROM nodes n
LEFT JOIN edges e ON e.source_id = n.id
    AND e.relation = 'depends_on'
    AND e.deprecated_at IS NULL
LEFT JOIN nodes t ON t.id = e.target_id
    AND t.deleted_at IS NULL
WHERE n.layer = 'SYSTEM'
  AND n.deleted_at IS NULL
GROUP BY n.id, n.type, n.name, n.data, n.created_at, n.updated_at;

-- Active intents: CONTEXT layer nodes representing current intentions
CREATE OR REPLACE VIEW v_active_intents AS
SELECT
    n.id,
    n.type,
    n.name,
    n.data,
    n.created_at,
    n.updated_at,
    (n.data->>'status')::TEXT AS status,
    (n.data->>'priority')::TEXT AS priority
FROM nodes n
WHERE n.layer = 'CONTEXT'
  AND n.deleted_at IS NULL
  AND n.type = 'intent'
ORDER BY n.created_at DESC;

-- Tools: AUTOMATION layer tool definitions
CREATE OR REPLACE VIEW v_tools AS
SELECT
    n.id,
    n.name,
    n.data->>'executor' AS executor,
    n.data->>'description' AS description,
    n.data->'args_schema' AS args_schema,
    n.created_at
FROM nodes n
WHERE n.layer = 'AUTOMATION'
  AND n.type = 'tool'
  AND n.deleted_at IS NULL
ORDER BY n.name;

-- ============================================================================
-- OBSERVABILITY VIEWS
-- ============================================================================

-- Development log: recent edge_events for tracking work
CREATE OR REPLACE VIEW v_development_log AS
SELECT
    ee.id,
    ee.relation,
    ee.success,
    ee.duration_ms,
    ee.data,
    ee.occurred_at,
    sn.layer AS source_layer,
    sn.type AS source_type,
    sn.name AS source_name,
    tn.layer AS target_layer,
    tn.type AS target_type,
    tn.name AS target_name
FROM edge_events ee
JOIN nodes sn ON sn.id = ee.source_id
JOIN nodes tn ON tn.id = ee.target_id
WHERE ee.occurred_at > NOW() - INTERVAL '7 days'
ORDER BY ee.occurred_at DESC;

-- Recent observations: last 24 hours of telemetry
CREATE OR REPLACE VIEW v_recent_observations AS
SELECT
    o.id,
    o.type,
    o.value,
    o.data,
    o.observed_at,
    n.layer AS node_layer,
    n.type AS node_type,
    n.name AS node_name
FROM observations o
JOIN nodes n ON n.id = o.node_id
WHERE o.observed_at > NOW() - INTERVAL '24 hours'
ORDER BY o.observed_at DESC;

-- Node history: version history with diffs
CREATE OR REPLACE VIEW v_node_history AS
SELECT
    nv.node_id,
    nv.version,
    nv.layer,
    nv.type,
    nv.name,
    nv.data,
    nv.created_at,
    n.name AS current_name,
    n.deleted_at IS NOT NULL AS is_deleted
FROM node_versions nv
JOIN nodes n ON n.id = nv.node_id
ORDER BY nv.node_id, nv.version DESC;

-- ============================================================================
-- TRAVERSAL FUNCTIONS
-- ============================================================================

-- Get all dependencies of a node (recursive, up to max_depth)
CREATE OR REPLACE FUNCTION get_dependencies(
    p_node_id UUID,
    p_max_depth INTEGER DEFAULT 10
) RETURNS TABLE(
    id UUID,
    layer dash_layer,
    type TEXT,
    name TEXT,
    data JSONB,
    depth INTEGER,
    path UUID[]
) AS $$
BEGIN
    RETURN QUERY
    WITH RECURSIVE deps AS (
        -- Base case: direct dependencies
        SELECT
            n.id,
            n.layer,
            n.type,
            n.name,
            n.data,
            1 AS depth,
            ARRAY[p_node_id, n.id] AS path
        FROM edges e
        JOIN nodes n ON n.id = e.target_id
        WHERE e.source_id = p_node_id
          AND e.relation = 'depends_on'
          AND e.deprecated_at IS NULL
          AND n.deleted_at IS NULL

        UNION ALL

        -- Recursive case
        SELECT
            n.id,
            n.layer,
            n.type,
            n.name,
            n.data,
            d.depth + 1,
            d.path || n.id
        FROM deps d
        JOIN edges e ON e.source_id = d.id
        JOIN nodes n ON n.id = e.target_id
        WHERE e.relation = 'depends_on'
          AND e.deprecated_at IS NULL
          AND n.deleted_at IS NULL
          AND d.depth < p_max_depth
          AND NOT (n.id = ANY(d.path))  -- Prevent cycles
    )
    SELECT DISTINCT ON (deps.id)
        deps.id,
        deps.layer,
        deps.type,
        deps.name,
        deps.data,
        deps.depth,
        deps.path
    FROM deps
    ORDER BY deps.id, deps.depth;
END;
$$ LANGUAGE plpgsql STABLE;

-- Get all dependents of a node (reverse traversal)
CREATE OR REPLACE FUNCTION get_dependents(
    p_node_id UUID,
    p_max_depth INTEGER DEFAULT 10
) RETURNS TABLE(
    id UUID,
    layer dash_layer,
    type TEXT,
    name TEXT,
    data JSONB,
    depth INTEGER,
    path UUID[]
) AS $$
BEGIN
    RETURN QUERY
    WITH RECURSIVE deps AS (
        -- Base case: direct dependents
        SELECT
            n.id,
            n.layer,
            n.type,
            n.name,
            n.data,
            1 AS depth,
            ARRAY[p_node_id, n.id] AS path
        FROM edges e
        JOIN nodes n ON n.id = e.source_id
        WHERE e.target_id = p_node_id
          AND e.relation = 'depends_on'
          AND e.deprecated_at IS NULL
          AND n.deleted_at IS NULL

        UNION ALL

        -- Recursive case
        SELECT
            n.id,
            n.layer,
            n.type,
            n.name,
            n.data,
            d.depth + 1,
            d.path || n.id
        FROM deps d
        JOIN edges e ON e.target_id = d.id
        JOIN nodes n ON n.id = e.source_id
        WHERE e.relation = 'depends_on'
          AND e.deprecated_at IS NULL
          AND n.deleted_at IS NULL
          AND d.depth < p_max_depth
          AND NOT (n.id = ANY(d.path))  -- Prevent cycles
    )
    SELECT DISTINCT ON (deps.id)
        deps.id,
        deps.layer,
        deps.type,
        deps.name,
        deps.data,
        deps.depth,
        deps.path
    FROM deps
    ORDER BY deps.id, deps.depth;
END;
$$ LANGUAGE plpgsql STABLE;

-- Trace lineage: follow edge_events to find causal chain
CREATE OR REPLACE FUNCTION trace_lineage(
    p_node_id UUID,
    p_max_depth INTEGER DEFAULT 20
) RETURNS TABLE(
    event_id UUID,
    source_id UUID,
    source_name TEXT,
    target_id UUID,
    target_name TEXT,
    relation dash_event_relation,
    success BOOLEAN,
    duration_ms INTEGER,
    occurred_at TIMESTAMPTZ,
    depth INTEGER
) AS $$
BEGIN
    RETURN QUERY
    WITH RECURSIVE lineage AS (
        -- Base case: events where this node is the source
        SELECT
            ee.id AS event_id,
            ee.source_id,
            sn.name AS source_name,
            ee.target_id,
            tn.name AS target_name,
            ee.relation,
            ee.success,
            ee.duration_ms,
            ee.occurred_at,
            1 AS depth
        FROM edge_events ee
        JOIN nodes sn ON sn.id = ee.source_id
        JOIN nodes tn ON tn.id = ee.target_id
        WHERE ee.source_id = p_node_id

        UNION ALL

        -- Recursive case: follow the chain
        SELECT
            ee.id,
            ee.source_id,
            sn.name,
            ee.target_id,
            tn.name,
            ee.relation,
            ee.success,
            ee.duration_ms,
            ee.occurred_at,
            l.depth + 1
        FROM lineage l
        JOIN edge_events ee ON ee.source_id = l.target_id
        JOIN nodes sn ON sn.id = ee.source_id
        JOIN nodes tn ON tn.id = ee.target_id
        WHERE l.depth < p_max_depth
    )
    SELECT * FROM lineage
    ORDER BY lineage.occurred_at;
END;
$$ LANGUAGE plpgsql STABLE;

-- ============================================================================
-- VERSION FUNCTIONS
-- ============================================================================

-- Get a specific version of a node
CREATE OR REPLACE FUNCTION get_node_version(
    p_node_id UUID,
    p_version INTEGER
) RETURNS TABLE(
    node_id UUID,
    version INTEGER,
    layer dash_layer,
    type TEXT,
    name TEXT,
    data JSONB,
    created_at TIMESTAMPTZ
) AS $$
BEGIN
    RETURN QUERY
    SELECT
        nv.node_id,
        nv.version,
        nv.layer,
        nv.type,
        nv.name,
        nv.data,
        nv.created_at
    FROM node_versions nv
    WHERE nv.node_id = p_node_id
      AND nv.version = p_version;
END;
$$ LANGUAGE plpgsql STABLE;

-- Get node state at a specific point in time
CREATE OR REPLACE FUNCTION get_node_at_time(
    p_node_id UUID,
    p_timestamp TIMESTAMPTZ
) RETURNS TABLE(
    node_id UUID,
    version INTEGER,
    layer dash_layer,
    type TEXT,
    name TEXT,
    data JSONB,
    as_of TIMESTAMPTZ
) AS $$
DECLARE
    node_created TIMESTAMPTZ;
BEGIN
    -- Get node creation time
    SELECT created_at INTO node_created
    FROM nodes
    WHERE id = p_node_id;

    -- If timestamp is before node was created, return nothing
    IF p_timestamp < node_created THEN
        RETURN;
    END IF;

    -- Find the version that was active at p_timestamp
    RETURN QUERY
    SELECT
        nv.node_id,
        nv.version,
        nv.layer,
        nv.type,
        nv.name,
        nv.data,
        p_timestamp AS as_of
    FROM node_versions nv
    WHERE nv.node_id = p_node_id
      AND nv.created_at <= p_timestamp
    ORDER BY nv.version DESC
    LIMIT 1;

    -- If no version found, the current state was valid at that time
    IF NOT FOUND THEN
        RETURN QUERY
        SELECT
            n.id AS node_id,
            0 AS version,
            n.layer,
            n.type,
            n.name,
            n.data,
            p_timestamp AS as_of
        FROM nodes n
        WHERE n.id = p_node_id
          AND n.created_at <= p_timestamp;
    END IF;
END;
$$ LANGUAGE plpgsql STABLE;

-- ============================================================================
-- COMMENTS
-- ============================================================================

COMMENT ON VIEW v_nodes_active IS 'All nodes that have not been soft deleted';
COMMENT ON VIEW v_edges_active IS 'All edges that have not been deprecated';
COMMENT ON VIEW v_system_state IS 'SYSTEM layer nodes with their dependencies';
COMMENT ON VIEW v_active_intents IS 'Active intent nodes from CONTEXT layer';
COMMENT ON VIEW v_tools IS 'Tool definitions from AUTOMATION layer';
COMMENT ON VIEW v_development_log IS 'Recent edge_events for development tracking';
COMMENT ON VIEW v_recent_observations IS 'Last 24 hours of telemetry';
COMMENT ON VIEW v_node_history IS 'Version history for all nodes';

COMMENT ON FUNCTION get_dependencies IS 'Recursive dependency traversal up to max_depth';
COMMENT ON FUNCTION get_dependents IS 'Reverse dependency traversal (who depends on this?)';
COMMENT ON FUNCTION trace_lineage IS 'Follow edge_events causal chain';
COMMENT ON FUNCTION get_node_version IS 'Get a specific version of a node';
COMMENT ON FUNCTION get_node_at_time IS 'Get node state at a point in time';
