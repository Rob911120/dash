-- ============================================================================
-- smoketest.sql
-- Dash: Verification script to test all components
-- ============================================================================
--
-- Run after setup: psql -d dash -f scripts/smoketest.sql
--
-- Expected output: All tests should show 'PASS'
-- ============================================================================

\set ON_ERROR_STOP on
\pset tuples_only on
\pset format unaligned

\echo '============================================'
\echo 'Dash Smoketest'
\echo '============================================'
\echo ''

-- ============================================================================
-- Test 1: Create nodes in each layer
-- ============================================================================
\echo 'Test 1: Create nodes in CONTEXT, SYSTEM, AUTOMATION layers'

DO $$
DECLARE
    context_id UUID;
    system_id UUID;
    automation_id UUID;
BEGIN
    -- Create CONTEXT node (intent)
    INSERT INTO nodes (layer, type, name, data)
    VALUES ('CONTEXT', 'intent', 'test_intent', '{"status": "active", "description": "Test intent"}'::jsonb)
    RETURNING id INTO context_id;

    -- Create SYSTEM node (service)
    INSERT INTO nodes (layer, type, name, data)
    VALUES ('SYSTEM', 'service', 'test_service', '{"status": "running", "port": 8080}'::jsonb)
    RETURNING id INTO system_id;

    -- Create AUTOMATION node (pattern)
    INSERT INTO nodes (layer, type, name, data)
    VALUES ('AUTOMATION', 'pattern', 'test_pattern', '{"category": "structural"}'::jsonb)
    RETURNING id INTO automation_id;

    -- Verify all created
    IF context_id IS NOT NULL AND system_id IS NOT NULL AND automation_id IS NOT NULL THEN
        RAISE NOTICE 'PASS: Created nodes in all layers';
    ELSE
        RAISE EXCEPTION 'FAIL: Could not create nodes';
    END IF;

    -- Store IDs for later tests
    PERFORM set_config('test.context_id', context_id::text, false);
    PERFORM set_config('test.system_id', system_id::text, false);
    PERFORM set_config('test.automation_id', automation_id::text, false);
END;
$$;

-- ============================================================================
-- Test 2: OBSERVATION guardrail (should fail)
-- ============================================================================
\echo 'Test 2: OBSERVATION guardrail prevents nodes in OBSERVATION layer'

DO $$
BEGIN
    -- Attempt to create OBSERVATION node (should fail)
    INSERT INTO nodes (layer, type, name, data)
    VALUES ('OBSERVATION', 'metric', 'test_metric', '{"value": 42}'::jsonb);

    RAISE EXCEPTION 'FAIL: OBSERVATION node was created (should have been blocked)';
EXCEPTION
    WHEN raise_exception THEN
        IF SQLERRM LIKE '%OBSERVATION data must be stored in observations table%' THEN
            RAISE NOTICE 'PASS: OBSERVATION guardrail working correctly';
        ELSE
            RAISE;
        END IF;
END;
$$;

-- ============================================================================
-- Test 3: Create edges
-- ============================================================================
\echo 'Test 3: Create edges between nodes'

DO $$
DECLARE
    context_id UUID := current_setting('test.context_id')::uuid;
    system_id UUID := current_setting('test.system_id')::uuid;
    edge_id UUID;
BEGIN
    -- Create edge: intent -> service (resulted_in relationship via edge_events)
    INSERT INTO edges (source_id, target_id, relation, data)
    VALUES (context_id, system_id, 'owns', '{"reason": "test"}'::jsonb)
    RETURNING id INTO edge_id;

    IF edge_id IS NOT NULL THEN
        RAISE NOTICE 'PASS: Created edge between nodes';
    ELSE
        RAISE EXCEPTION 'FAIL: Could not create edge';
    END IF;

    PERFORM set_config('test.edge_id', edge_id::text, false);
END;
$$;

-- ============================================================================
-- Test 4: Create edge_events
-- ============================================================================
\echo 'Test 4: Create edge_events (lineage)'

DO $$
DECLARE
    context_id UUID := current_setting('test.context_id')::uuid;
    system_id UUID := current_setting('test.system_id')::uuid;
    event_id UUID;
BEGIN
    INSERT INTO edge_events (source_id, target_id, relation, success, duration_ms, data)
    VALUES (context_id, system_id, 'resulted_in', true, 1234, '{"action": "create"}'::jsonb)
    RETURNING id INTO event_id;

    IF event_id IS NOT NULL THEN
        RAISE NOTICE 'PASS: Created edge_event';
    ELSE
        RAISE EXCEPTION 'FAIL: Could not create edge_event';
    END IF;
END;
$$;

-- ============================================================================
-- Test 5: Create observations
-- ============================================================================
\echo 'Test 5: Create observations (telemetry)'

DO $$
DECLARE
    system_id UUID := current_setting('test.system_id')::uuid;
    obs_id UUID;
BEGIN
    INSERT INTO observations (node_id, type, value, data)
    VALUES (system_id, 'cpu_usage', 45.5, '{"unit": "percent"}'::jsonb)
    RETURNING id INTO obs_id;

    IF obs_id IS NOT NULL THEN
        RAISE NOTICE 'PASS: Created observation';
    ELSE
        RAISE EXCEPTION 'FAIL: Could not create observation';
    END IF;
END;
$$;

-- ============================================================================
-- Test 6: Soft delete cascade
-- ============================================================================
\echo 'Test 6: Soft delete cascades to edges'

DO $$
DECLARE
    system_id UUID := current_setting('test.system_id')::uuid;
    edge_deprecated BOOLEAN;
BEGIN
    -- Soft delete the system node
    UPDATE nodes SET deleted_at = NOW() WHERE id = system_id;

    -- Check if edge was deprecated
    SELECT deprecated_at IS NOT NULL INTO edge_deprecated
    FROM edges
    WHERE id = current_setting('test.edge_id')::uuid;

    IF edge_deprecated THEN
        RAISE NOTICE 'PASS: Edge deprecated on node soft delete';
    ELSE
        RAISE EXCEPTION 'FAIL: Edge was not deprecated';
    END IF;
END;
$$;

-- ============================================================================
-- Test 7: Version history
-- ============================================================================
\echo 'Test 7: Auto-versioning on node update'

DO $$
DECLARE
    context_id UUID := current_setting('test.context_id')::uuid;
    version_count INTEGER;
BEGIN
    -- Update the node (should create version)
    UPDATE nodes
    SET data = data || '{"status": "completed"}'::jsonb
    WHERE id = context_id;

    -- Check version was created
    SELECT COUNT(*) INTO version_count
    FROM node_versions
    WHERE node_id = context_id;

    IF version_count > 0 THEN
        RAISE NOTICE 'PASS: Version history created (% versions)', version_count;
    ELSE
        RAISE EXCEPTION 'FAIL: No version history created';
    END IF;
END;
$$;

-- ============================================================================
-- Test 8: Views work correctly
-- ============================================================================
\echo 'Test 8: Views return correct data'

DO $$
DECLARE
    active_count INTEGER;
    tool_count INTEGER;
BEGIN
    -- Check v_nodes_active excludes deleted
    SELECT COUNT(*) INTO active_count
    FROM v_nodes_active
    WHERE id = current_setting('test.system_id')::uuid;

    IF active_count = 0 THEN
        RAISE NOTICE 'PASS: v_nodes_active excludes soft-deleted nodes';
    ELSE
        RAISE EXCEPTION 'FAIL: v_nodes_active includes soft-deleted nodes';
    END IF;

    -- Check v_tools
    SELECT COUNT(*) INTO tool_count FROM v_tools;

    IF tool_count > 0 THEN
        RAISE NOTICE 'PASS: v_tools returns % tools', tool_count;
    ELSE
        RAISE NOTICE 'WARN: v_tools returned 0 tools (seeds may not be loaded)';
    END IF;
END;
$$;

-- ============================================================================
-- Test 9: Traversal functions
-- ============================================================================
\echo 'Test 9: Traversal functions work'

DO $$
DECLARE
    dep_count INTEGER;
BEGIN
    -- Create a dependency chain for testing
    WITH new_nodes AS (
        INSERT INTO nodes (layer, type, name, data)
        VALUES
            ('SYSTEM', 'service', 'service_a', '{"status": "running"}'::jsonb),
            ('SYSTEM', 'service', 'service_b', '{"status": "running"}'::jsonb),
            ('SYSTEM', 'database', 'db_main', '{"engine": "postgresql"}'::jsonb)
        RETURNING id, name
    ),
    node_ids AS (
        SELECT
            (SELECT id FROM new_nodes WHERE name = 'service_a') AS a_id,
            (SELECT id FROM new_nodes WHERE name = 'service_b') AS b_id,
            (SELECT id FROM new_nodes WHERE name = 'db_main') AS db_id
    )
    INSERT INTO edges (source_id, target_id, relation)
    SELECT a_id, b_id, 'depends_on'::dash_relation FROM node_ids
    UNION ALL
    SELECT b_id, db_id, 'depends_on'::dash_relation FROM node_ids;

    -- Test get_dependencies
    SELECT COUNT(*) INTO dep_count
    FROM get_dependencies(
        (SELECT id FROM nodes WHERE name = 'service_a' AND deleted_at IS NULL),
        10
    );

    IF dep_count >= 2 THEN
        RAISE NOTICE 'PASS: get_dependencies found % dependencies', dep_count;
    ELSE
        RAISE EXCEPTION 'FAIL: get_dependencies found only % dependencies (expected 2)', dep_count;
    END IF;
END;
$$;

-- ============================================================================
-- Test 10: Partition functions
-- ============================================================================
\echo 'Test 10: Partition management functions'

DO $$
DECLARE
    partition_count INTEGER;
BEGIN
    -- Check partitions exist
    SELECT COUNT(*) INTO partition_count
    FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE c.relname LIKE 'edge_events_2%'
      AND n.nspname = 'public';

    IF partition_count > 0 THEN
        RAISE NOTICE 'PASS: Found % edge_events partitions', partition_count;
    ELSE
        RAISE NOTICE 'WARN: No date-based partitions found (only default exists)';
    END IF;
END;
$$;

-- ============================================================================
-- Cleanup test data
-- ============================================================================
\echo ''
\echo 'Cleaning up test data...'

DELETE FROM edge_events WHERE source_id IN (
    SELECT id FROM nodes WHERE name LIKE 'test_%' OR name LIKE 'service_%' OR name = 'db_main'
);

DELETE FROM observations WHERE node_id IN (
    SELECT id FROM nodes WHERE name LIKE 'test_%' OR name LIKE 'service_%' OR name = 'db_main'
);

DELETE FROM edges WHERE source_id IN (
    SELECT id FROM nodes WHERE name LIKE 'test_%' OR name LIKE 'service_%' OR name = 'db_main'
) OR target_id IN (
    SELECT id FROM nodes WHERE name LIKE 'test_%' OR name LIKE 'service_%' OR name = 'db_main'
);

DELETE FROM node_versions WHERE node_id IN (
    SELECT id FROM nodes WHERE name LIKE 'test_%' OR name LIKE 'service_%' OR name = 'db_main'
);

DELETE FROM nodes WHERE name LIKE 'test_%' OR name LIKE 'service_%' OR name = 'db_main';

\echo ''
\echo '============================================'
\echo 'Smoketest completed!'
\echo '============================================'
