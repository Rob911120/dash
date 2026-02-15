-- ============================================================================
-- 011_dash_partitions.sql
-- Dash: Unified Graph Architecture - Partitioned Tables
-- ============================================================================

-- ============================================================================
-- PARTITIONED TABLES
-- ============================================================================

-- edge_events: Events/lineage between nodes (partitioned by month)
CREATE TABLE edge_events (
    id              UUID NOT NULL DEFAULT gen_random_uuid(),
    source_id       UUID NOT NULL,
    target_id       UUID NOT NULL,
    relation        dash_event_relation NOT NULL,
    success         BOOLEAN NOT NULL DEFAULT TRUE,
    duration_ms     INTEGER,
    data            JSONB NOT NULL DEFAULT '{}',
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (id, occurred_at),
    CONSTRAINT chk_edge_events_duration_positive CHECK (duration_ms IS NULL OR duration_ms >= 0)
) PARTITION BY RANGE (occurred_at);

-- observations: Telemetry and time series (partitioned by month)
CREATE TABLE observations (
    id              UUID NOT NULL DEFAULT gen_random_uuid(),
    node_id         UUID NOT NULL,
    type            TEXT NOT NULL,
    value           DOUBLE PRECISION,
    data            JSONB NOT NULL DEFAULT '{}',
    observed_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (id, observed_at),
    CONSTRAINT chk_observations_type_not_empty CHECK (type <> '')
) PARTITION BY RANGE (observed_at);

-- ============================================================================
-- DEFAULT PARTITIONS (fallback for out-of-range data)
-- ============================================================================

CREATE TABLE edge_events_default PARTITION OF edge_events DEFAULT;
CREATE TABLE observations_default PARTITION OF observations DEFAULT;

-- ============================================================================
-- INDEXES ON PARTITIONED TABLES
-- ============================================================================

-- edge_events indexes
CREATE INDEX idx_edge_events_source ON edge_events(source_id);
CREATE INDEX idx_edge_events_target ON edge_events(target_id);
CREATE INDEX idx_edge_events_relation ON edge_events(relation);
CREATE INDEX idx_edge_events_occurred ON edge_events(occurred_at);
CREATE INDEX idx_edge_events_source_occurred ON edge_events(source_id, occurred_at);

-- observations indexes
CREATE INDEX idx_observations_node ON observations(node_id);
CREATE INDEX idx_observations_type ON observations(type);
CREATE INDEX idx_observations_observed ON observations(observed_at);
CREATE INDEX idx_observations_node_observed ON observations(node_id, observed_at);
CREATE INDEX idx_observations_data ON observations USING GIN(data);

-- ============================================================================
-- PARTITION MANAGEMENT FUNCTIONS
-- ============================================================================

-- Create a monthly partition for a given table
CREATE OR REPLACE FUNCTION create_monthly_partition(
    p_table_name TEXT,
    p_year INTEGER,
    p_month INTEGER
) RETURNS TEXT AS $$
DECLARE
    partition_name TEXT;
    start_date DATE;
    end_date DATE;
    sql_stmt TEXT;
BEGIN
    -- Build partition name: table_YYYYMM
    partition_name := p_table_name || '_' || TO_CHAR(MAKE_DATE(p_year, p_month, 1), 'YYYYMM');
    start_date := MAKE_DATE(p_year, p_month, 1);
    end_date := start_date + INTERVAL '1 month';

    -- Check if partition already exists
    IF EXISTS (
        SELECT 1 FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE c.relname = partition_name
          AND n.nspname = 'public'
    ) THEN
        RETURN partition_name || ' (already exists)';
    END IF;

    -- Create the partition
    sql_stmt := FORMAT(
        'CREATE TABLE %I PARTITION OF %I FOR VALUES FROM (%L) TO (%L)',
        partition_name,
        p_table_name,
        start_date,
        end_date
    );
    EXECUTE sql_stmt;

    RETURN partition_name;
END;
$$ LANGUAGE plpgsql;

-- Ensure partitions exist for N months ahead
CREATE OR REPLACE FUNCTION ensure_future_partitions(
    p_months_ahead INTEGER DEFAULT 6
) RETURNS TABLE(table_name TEXT, partition_name TEXT) AS $$
DECLARE
    current_date DATE := CURRENT_DATE;
    target_date DATE;
    y INTEGER;
    m INTEGER;
    result TEXT;
BEGIN
    FOR i IN 0..p_months_ahead LOOP
        target_date := current_date + (i || ' months')::INTERVAL;
        y := EXTRACT(YEAR FROM target_date)::INTEGER;
        m := EXTRACT(MONTH FROM target_date)::INTEGER;

        -- Create edge_events partition
        result := create_monthly_partition('edge_events', y, m);
        table_name := 'edge_events';
        partition_name := result;
        RETURN NEXT;

        -- Create observations partition
        result := create_monthly_partition('observations', y, m);
        table_name := 'observations';
        partition_name := result;
        RETURN NEXT;
    END LOOP;
END;
$$ LANGUAGE plpgsql;

-- ============================================================================
-- DATA RELOCATION PROCEDURES
-- ============================================================================

-- Relocate data from edge_events_default to proper partitions
CREATE OR REPLACE PROCEDURE relocate_edge_events_default(
    p_batch_size INTEGER DEFAULT 5000
)
LANGUAGE plpgsql AS $$
DECLARE
    rows_moved INTEGER := 0;
    total_moved INTEGER := 0;
    rec RECORD;
BEGIN
    LOOP
        -- Process one batch
        WITH to_move AS (
            SELECT id, occurred_at
            FROM edge_events_default
            LIMIT p_batch_size
            FOR UPDATE SKIP LOCKED
        ),
        deleted AS (
            DELETE FROM edge_events_default e
            USING to_move t
            WHERE e.id = t.id AND e.occurred_at = t.occurred_at
            RETURNING e.*
        )
        INSERT INTO edge_events
        SELECT * FROM deleted;

        GET DIAGNOSTICS rows_moved = ROW_COUNT;
        total_moved := total_moved + rows_moved;

        -- Commit the batch
        COMMIT;

        -- Exit if no more rows
        EXIT WHEN rows_moved = 0;

        RAISE NOTICE 'Relocated % edge_events rows (total: %)', rows_moved, total_moved;
    END LOOP;

    RAISE NOTICE 'Completed: relocated % total edge_events from default partition', total_moved;
END;
$$;

-- Relocate data from observations_default to proper partitions
CREATE OR REPLACE PROCEDURE relocate_observations_default(
    p_batch_size INTEGER DEFAULT 5000
)
LANGUAGE plpgsql AS $$
DECLARE
    rows_moved INTEGER := 0;
    total_moved INTEGER := 0;
    rec RECORD;
BEGIN
    LOOP
        -- Process one batch
        WITH to_move AS (
            SELECT id, observed_at
            FROM observations_default
            LIMIT p_batch_size
            FOR UPDATE SKIP LOCKED
        ),
        deleted AS (
            DELETE FROM observations_default o
            USING to_move t
            WHERE o.id = t.id AND o.observed_at = t.observed_at
            RETURNING o.*
        )
        INSERT INTO observations
        SELECT * FROM deleted;

        GET DIAGNOSTICS rows_moved = ROW_COUNT;
        total_moved := total_moved + rows_moved;

        -- Commit the batch
        COMMIT;

        -- Exit if no more rows
        EXIT WHEN rows_moved = 0;

        RAISE NOTICE 'Relocated % observations rows (total: %)', rows_moved, total_moved;
    END LOOP;

    RAISE NOTICE 'Completed: relocated % total observations from default partition', total_moved;
END;
$$;

-- ============================================================================
-- CREATE INITIAL PARTITIONS
-- ============================================================================

-- Create partitions for current month and 6 months ahead
SELECT * FROM ensure_future_partitions(6);

-- ============================================================================
-- COMMENTS
-- ============================================================================

COMMENT ON TABLE edge_events IS 'Event/lineage records between nodes (partitioned by month)';
COMMENT ON TABLE observations IS 'Telemetry data for nodes (partitioned by month)';
COMMENT ON TABLE edge_events_default IS 'Default partition for edge_events - should be empty';
COMMENT ON TABLE observations_default IS 'Default partition for observations - should be empty';

COMMENT ON FUNCTION create_monthly_partition IS 'Create a monthly partition for a given table';
COMMENT ON FUNCTION ensure_future_partitions IS 'Ensure partitions exist for N months ahead';
COMMENT ON PROCEDURE relocate_edge_events_default IS 'Move data from default partition to proper partitions';
COMMENT ON PROCEDURE relocate_observations_default IS 'Move data from default partition to proper partitions';
