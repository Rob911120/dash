-- ============================================================================
-- 003_dash_agents.sql
-- Dash: Agent configurations
-- ============================================================================

-- ============================================================================
-- CORE AGENTS
-- ============================================================================

-- System observer agent - monitors system state
INSERT INTO nodes (layer, type, name, data) VALUES
('AUTOMATION', 'agent', 'system_observer', '{
    "description": "Monitors system state and creates observations",
    "status": "idle",
    "capabilities": [
        "monitor_services",
        "collect_metrics",
        "detect_anomalies"
    ],
    "allowed_tools": [
        "postgres_query",
        "http_get",
        "query_graph"
    ],
    "config": {
        "poll_interval_seconds": 60,
        "observation_retention_days": 30,
        "alert_thresholds": {
            "cpu_percent": 80,
            "memory_percent": 85,
            "disk_percent": 90
        }
    },
    "schedule": "*/1 * * * *"
}'::jsonb);

-- File watcher agent - monitors file changes
INSERT INTO nodes (layer, type, name, data) VALUES
('AUTOMATION', 'agent', 'file_watcher', '{
    "description": "Watches for file changes and updates graph",
    "status": "idle",
    "capabilities": [
        "watch_directories",
        "detect_changes",
        "update_nodes"
    ],
    "allowed_tools": [
        "read_file",
        "list_directory",
        "create_node",
        "query_graph"
    ],
    "config": {
        "watch_paths": ["/body/"],
        "ignore_patterns": ["*.tmp", "*.log", ".git/**"],
        "debounce_ms": 500
    }
}'::jsonb);

-- Intent executor agent - executes planned work
INSERT INTO nodes (layer, type, name, data) VALUES
('AUTOMATION', 'agent', 'intent_executor', '{
    "description": "Picks up active intents and executes them",
    "status": "idle",
    "capabilities": [
        "parse_intent",
        "plan_execution",
        "execute_steps",
        "report_progress"
    ],
    "allowed_tools": [
        "postgres_query",
        "postgres_execute",
        "read_file",
        "write_file",
        "http_request",
        "create_node",
        "create_edge",
        "query_graph"
    ],
    "config": {
        "max_concurrent_intents": 3,
        "step_timeout_seconds": 300,
        "retry_attempts": 3
    }
}'::jsonb);

-- Schema validator agent - validates nodes against schemas
INSERT INTO nodes (layer, type, name, data) VALUES
('AUTOMATION', 'agent', 'schema_validator', '{
    "description": "Validates nodes against their schemas on creation/update",
    "status": "idle",
    "capabilities": [
        "lookup_schema",
        "validate_data",
        "report_violations"
    ],
    "allowed_tools": [
        "postgres_query",
        "query_graph"
    ],
    "config": {
        "strict_mode": false,
        "log_warnings": true
    }
}'::jsonb);

-- Lineage tracker agent - tracks causal relationships
INSERT INTO nodes (layer, type, name, data) VALUES
('AUTOMATION', 'agent', 'lineage_tracker', '{
    "description": "Tracks and records causal relationships between events",
    "status": "idle",
    "capabilities": [
        "capture_events",
        "correlate_events",
        "build_lineage"
    ],
    "allowed_tools": [
        "postgres_query",
        "postgres_execute",
        "query_graph"
    ],
    "config": {
        "correlation_window_seconds": 300,
        "max_chain_depth": 20
    }
}'::jsonb);

-- ============================================================================
-- MAINTENANCE AGENTS
-- ============================================================================

-- Partition manager agent - manages table partitions
INSERT INTO nodes (layer, type, name, data) VALUES
('AUTOMATION', 'agent', 'partition_manager', '{
    "description": "Manages partition creation and data relocation",
    "status": "idle",
    "capabilities": [
        "create_partitions",
        "relocate_data",
        "drop_old_partitions"
    ],
    "allowed_tools": [
        "postgres_query",
        "postgres_execute"
    ],
    "config": {
        "months_ahead": 6,
        "retention_months": 24,
        "batch_size": 5000
    },
    "schedule": "0 2 * * *"
}'::jsonb);

-- Garbage collector agent - cleans up stale data
INSERT INTO nodes (layer, type, name, data) VALUES
('AUTOMATION', 'agent', 'garbage_collector', '{
    "description": "Cleans up old soft-deleted nodes and stale data",
    "status": "idle",
    "capabilities": [
        "find_stale_nodes",
        "archive_data",
        "purge_old_versions"
    ],
    "allowed_tools": [
        "postgres_query",
        "postgres_execute"
    ],
    "config": {
        "soft_delete_retention_days": 90,
        "version_retention_count": 100,
        "batch_size": 1000
    },
    "schedule": "0 3 * * 0"
}'::jsonb);

-- ============================================================================
-- EDGES: Agent Tool Permissions
-- ============================================================================

-- Create edges linking agents to their allowed tools
-- (These will be created after both agents and tools exist)

DO $$
DECLARE
    agent_rec RECORD;
    tool_name TEXT;
    tool_id UUID;
BEGIN
    -- For each agent
    FOR agent_rec IN
        SELECT id, name, data->'allowed_tools' AS allowed_tools
        FROM nodes
        WHERE layer = 'AUTOMATION'
          AND type = 'agent'
          AND deleted_at IS NULL
    LOOP
        -- For each allowed tool
        FOR tool_name IN SELECT jsonb_array_elements_text(agent_rec.allowed_tools)
        LOOP
            -- Find the tool
            SELECT id INTO tool_id
            FROM nodes
            WHERE layer = 'AUTOMATION'
              AND type = 'tool'
              AND name = tool_name
              AND deleted_at IS NULL;

            -- Create edge if tool exists
            IF tool_id IS NOT NULL THEN
                INSERT INTO edges (source_id, target_id, relation, data)
                VALUES (agent_rec.id, tool_id, 'uses', '{"permission": "execute"}'::jsonb)
                ON CONFLICT DO NOTHING;
            END IF;
        END LOOP;
    END LOOP;
END;
$$;
