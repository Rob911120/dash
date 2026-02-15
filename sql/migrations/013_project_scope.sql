-- ============================================================================
-- 013_project_scope.sql
-- Dash: Project as First-Class Scope
-- ============================================================================
--
-- Adds project_id to nodes table to enable project-scoped queries and
-- organization. Projects are SYSTEM/project nodes that group related work.
--
-- Usage:
--   psql -h soul -U postgres -d dash -f sql/migrations/013_project_scope.sql
--
-- ============================================================================

BEGIN;

-- ============================================================================
-- ADD PROJECT_ID COLUMN
-- ============================================================================

-- Add project_id column (nullable - not all nodes belong to a project)
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS project_id UUID REFERENCES nodes(id);

COMMENT ON COLUMN nodes.project_id IS 'Optional reference to a SYSTEM/project node for scoping';

-- ============================================================================
-- UPDATE UNIQUE INDEX FOR PROJECT SCOPE
-- ============================================================================

-- Drop existing unique index
DROP INDEX IF EXISTS ux_nodes_unique_active;

-- Create simple unique index (layer, type, name) for active nodes
-- NOTE: This means node names must be globally unique per layer+type
-- Project scoping is handled at application level, not DB constraint
CREATE UNIQUE INDEX ux_nodes_unique_active
ON nodes (layer, type, name)
WHERE deleted_at IS NULL;

COMMENT ON INDEX ux_nodes_unique_active IS 'Unique name per layer+type for active nodes (global scope)';

-- ============================================================================
-- INDEXES FOR PROJECT QUERIES
-- ============================================================================

-- Index for finding all nodes in a project
CREATE INDEX IF NOT EXISTS idx_nodes_project
ON nodes(project_id)
WHERE project_id IS NOT NULL AND deleted_at IS NULL;

-- Index for project-scoped layer queries
CREATE INDEX IF NOT EXISTS idx_nodes_project_layer
ON nodes(project_id, layer)
WHERE project_id IS NOT NULL AND deleted_at IS NULL;

-- GIN index on observations for project_id lookups
CREATE INDEX IF NOT EXISTS idx_observations_project
ON observations((data->>'project_id'))
WHERE data->>'project_id' IS NOT NULL;

-- ============================================================================
-- VALIDATION TRIGGER
-- ============================================================================

-- Ensure project_id references a valid SYSTEM/project node
CREATE OR REPLACE FUNCTION validate_project_reference()
RETURNS TRIGGER AS $$
BEGIN
    -- Skip validation if project_id is NULL
    IF NEW.project_id IS NULL THEN
        RETURN NEW;
    END IF;

    -- Check that referenced node is a project
    IF NOT EXISTS (
        SELECT 1 FROM nodes
        WHERE id = NEW.project_id
          AND layer = 'SYSTEM'
          AND type = 'project'
          AND deleted_at IS NULL
    ) THEN
        RAISE EXCEPTION 'project_id must reference an active SYSTEM/project node, got: %', NEW.project_id;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Create trigger for project reference validation
DROP TRIGGER IF EXISTS trg_nodes_validate_project ON nodes;
CREATE TRIGGER trg_nodes_validate_project
    BEFORE INSERT OR UPDATE ON nodes
    FOR EACH ROW
    WHEN (NEW.project_id IS NOT NULL)
    EXECUTE FUNCTION validate_project_reference();

-- ============================================================================
-- HELPER FUNCTIONS
-- ============================================================================

-- Get all nodes in a project (recursive, includes sub-projects)
CREATE OR REPLACE FUNCTION get_project_nodes(p_project_id UUID, p_include_subprojects BOOLEAN DEFAULT FALSE)
RETURNS TABLE (
    id UUID,
    layer dash_layer,
    type TEXT,
    name TEXT,
    data JSONB,
    created_at TIMESTAMPTZ
) AS $$
BEGIN
    IF p_include_subprojects THEN
        -- Recursive: include nodes from sub-projects
        RETURN QUERY
        WITH RECURSIVE project_tree AS (
            -- Base case: the project itself
            SELECT n.id as project_id
            FROM nodes n
            WHERE n.id = p_project_id
              AND n.deleted_at IS NULL

            UNION ALL

            -- Recursive: sub-projects
            SELECT n.id
            FROM nodes n
            JOIN project_tree pt ON n.project_id = pt.project_id
            WHERE n.type = 'project'
              AND n.layer = 'SYSTEM'
              AND n.deleted_at IS NULL
        )
        SELECT n.id, n.layer, n.type, n.name, n.data, n.created_at
        FROM nodes n
        JOIN project_tree pt ON n.project_id = pt.project_id
        WHERE n.deleted_at IS NULL
        ORDER BY n.created_at;
    ELSE
        -- Non-recursive: just direct children
        RETURN QUERY
        SELECT n.id, n.layer, n.type, n.name, n.data, n.created_at
        FROM nodes n
        WHERE n.project_id = p_project_id
          AND n.deleted_at IS NULL
        ORDER BY n.created_at;
    END IF;
END;
$$ LANGUAGE plpgsql STABLE;

-- Get project statistics
CREATE OR REPLACE FUNCTION get_project_stats(p_project_id UUID)
RETURNS TABLE (
    layer dash_layer,
    type TEXT,
    count BIGINT
) AS $$
BEGIN
    RETURN QUERY
    SELECT n.layer, n.type, COUNT(*)::BIGINT
    FROM nodes n
    WHERE n.project_id = p_project_id
      AND n.deleted_at IS NULL
    GROUP BY n.layer, n.type
    ORDER BY n.layer, n.type;
END;
$$ LANGUAGE plpgsql STABLE;

-- ============================================================================
-- COMMENTS
-- ============================================================================

COMMENT ON FUNCTION get_project_nodes IS 'Get all nodes belonging to a project, optionally including sub-projects';
COMMENT ON FUNCTION get_project_stats IS 'Get node counts per layer/type for a project';

COMMIT;
