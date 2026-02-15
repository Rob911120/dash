-- ============================================================================
-- 004_project_bootstrap.sql
-- Dash: Project Bootstrap Workflow and Templates
-- ============================================================================
--
-- This seed file creates:
--   1. A bootstrap workflow node for creating new projects
--   2. A template for minimal project stubs
--   3. The dash project itself as a reference implementation
--
-- Usage:
--   psql -h soul -U postgres -d dash -f sql/seeds/004_project_bootstrap.sql
--
-- ============================================================================

BEGIN;

-- ============================================================================
-- PROJECT BOOTSTRAP WORKFLOW
-- ============================================================================

-- The workflow definition for bootstrapping new projects
-- Note: Global nodes (no project_id) use NULL which COALESCE converts to the nil UUID
INSERT INTO nodes (layer, type, name, project_id, data) VALUES
('AUTOMATION', 'workflow', 'project_bootstrap', NULL, jsonb_build_object(
    'description', 'Skapa startkapsel för nytt projekt',
    'version', '1.0.0',
    'creates', jsonb_build_object(
        'SYSTEM', jsonb_build_array('project'),
        'CONTEXT', jsonb_build_array('intro', 'intent', 'plan', 'assumptions'),
        'AUTOMATION', jsonb_build_array('tool_permissions')
    ),
    'steps', jsonb_build_array(
        jsonb_build_object(
            'order', 1,
            'action', 'create_project_node',
            'description', 'Skapa SYSTEM/project nod med metadata'
        ),
        jsonb_build_object(
            'order', 2,
            'action', 'create_intro',
            'description', 'Skapa CONTEXT/intro med projektbeskrivning'
        ),
        jsonb_build_object(
            'order', 3,
            'action', 'create_initial_intent',
            'description', 'Skapa CONTEXT/intent för projektsyfte'
        ),
        jsonb_build_object(
            'order', 4,
            'action', 'store_project_id',
            'description', 'Spara projekt-ID i .project fil'
        )
    ),
    'provenance_source', 'user_seed'
))
ON CONFLICT (COALESCE(project_id, '00000000-0000-0000-0000-000000000000'), layer, type, name) WHERE deleted_at IS NULL
DO UPDATE SET data = EXCLUDED.data;

-- ============================================================================
-- PROJECT SCHEMA
-- ============================================================================

-- Schema definition for SYSTEM/project nodes
INSERT INTO nodes (layer, type, name, project_id, data) VALUES
('AUTOMATION', 'schema', 'project_schema', NULL, jsonb_build_object(
    'for_layer', 'SYSTEM',
    'for_type', 'project',
    'version', '1.0.0',
    'fields', jsonb_build_object(
        'description', jsonb_build_object(
            'type', 'string',
            'required', true,
            'description', 'Kort beskrivning av projektet'
        ),
        'stack', jsonb_build_object(
            'type', 'array',
            'items', jsonb_build_object('type', 'string'),
            'description', 'Teknologier som används'
        ),
        'constraints', jsonb_build_object(
            'type', 'array',
            'items', jsonb_build_object('type', 'string'),
            'description', 'Arkitekturella begränsningar'
        ),
        'milestones', jsonb_build_object(
            'type', 'array',
            'items', jsonb_build_object(
                'type', 'object',
                'properties', jsonb_build_object(
                    'name', jsonb_build_object('type', 'string'),
                    'status', jsonb_build_object('type', 'string', 'enum', jsonb_build_array('pending', 'in_progress', 'completed')),
                    'due_date', jsonb_build_object('type', 'string', 'format', 'date')
                )
            ),
            'description', 'Projektmilstolpar'
        ),
        'owner', jsonb_build_object(
            'type', 'string',
            'description', 'Projektägare'
        ),
        'repo_url', jsonb_build_object(
            'type', 'string',
            'format', 'uri',
            'description', 'Git repository URL'
        )
    )
))
ON CONFLICT (COALESCE(project_id, '00000000-0000-0000-0000-000000000000'), layer, type, name) WHERE deleted_at IS NULL
DO UPDATE SET data = EXCLUDED.data;

-- ============================================================================
-- DASH PROJECT (Reference Implementation)
-- ============================================================================

-- Create the dash project itself (project nodes don't have a project_id - they ARE projects)
INSERT INTO nodes (layer, type, name, project_id, data) VALUES
('SYSTEM', 'project', 'dash', NULL, jsonb_build_object(
    'description', 'Grafbaserad arkitektur för system-modeling och telemetri',
    'stack', jsonb_build_array('go', 'postgresql', 'shell'),
    'constraints', jsonb_build_array(
        'soft-delete',
        'allowlist-tools',
        'verification-first',
        'observation-layer-separation'
    ),
    'milestones', jsonb_build_array(
        jsonb_build_object(
            'name', 'Core Schema',
            'status', 'completed',
            'description', 'nodes, edges, observations, edge_events'
        ),
        jsonb_build_object(
            'name', 'Hook Integration',
            'status', 'completed',
            'description', 'Claude Code hooks för auto-capture'
        ),
        jsonb_build_object(
            'name', 'Project Scope',
            'status', 'completed',
            'description', 'project_id och context envelope'
        ),
        jsonb_build_object(
            'name', 'Go Package',
            'status', 'pending',
            'description', 'CRUD och traversering i Go'
        )
    ),
    'owner', 'robin',
    'workspace_path', '/dash',
    'created_at', NOW()
))
ON CONFLICT (COALESCE(project_id, '00000000-0000-0000-0000-000000000000'), layer, type, name) WHERE deleted_at IS NULL
DO UPDATE SET data = nodes.data || jsonb_build_object('updated_at', NOW());

-- ============================================================================
-- PROJECT INTRO (CONTEXT layer)
-- ============================================================================

-- Get dash project ID for linking
DO $$
DECLARE
    dash_project_id UUID;
BEGIN
    SELECT id INTO dash_project_id FROM nodes
    WHERE layer = 'SYSTEM' AND type = 'project' AND name = 'dash'
    AND deleted_at IS NULL;

    -- Create intro node linked to project
    INSERT INTO nodes (layer, type, name, project_id, data) VALUES
    ('CONTEXT', 'intro', 'dash_intro', dash_project_id, jsonb_build_object(
        'title', 'Dash: Unified Graph Architecture',
        'tagline', 'Ett självförbättrande system där allt är noder och relationer',
        'key_concepts', jsonb_build_array(
            'Fyra semantiska lager: CONTEXT, SYSTEM, AUTOMATION, OBSERVATION',
            'Grafen är källan till sanning',
            'Telemetri separeras i observations-tabellen',
            'Hooks fångar automatiskt alla tool-anrop'
        ),
        'provenance', jsonb_build_object(
            'source', 'user_seed',
            'confidence', 1.0
        )
    ))
    ON CONFLICT (COALESCE(project_id, '00000000-0000-0000-0000-000000000000'), layer, type, name) WHERE deleted_at IS NULL
    DO UPDATE SET data = EXCLUDED.data;

    -- Create initial intent
    INSERT INTO nodes (layer, type, name, project_id, data) VALUES
    ('CONTEXT', 'intent', 'dash_core_intent', dash_project_id, jsonb_build_object(
        'statement', 'Bygga ett grafbaserat system för att modellera och spåra allt som händer i utvecklingsmiljön',
        'motivation', 'Fullständig observabilitet utan manuell loggning',
        'success_criteria', jsonb_build_array(
            'Alla tool-anrop loggas automatiskt',
            'Kausalitet kan spåras via edge_events',
            'Projekt kan scopes och filtreras',
            'Ingen OBSERVATION-data i nodes-tabellen'
        ),
        'status', 'active',
        'provenance', jsonb_build_object(
            'source', 'user_seed',
            'confidence', 1.0
        )
    ))
    ON CONFLICT (COALESCE(project_id, '00000000-0000-0000-0000-000000000000'), layer, type, name) WHERE deleted_at IS NULL
    DO UPDATE SET data = EXCLUDED.data;

    -- Create assumptions node
    INSERT INTO nodes (layer, type, name, project_id, data) VALUES
    ('CONTEXT', 'assumptions', 'dash_assumptions', dash_project_id, jsonb_build_object(
        'items', jsonb_build_array(
            jsonb_build_object(
                'assumption', 'PostgreSQL är tillgänglig på host "soul"',
                'validated', true
            ),
            jsonb_build_object(
                'assumption', 'Claude Code hooks körs synkront',
                'validated', true
            ),
            jsonb_build_object(
                'assumption', 'jq finns installerat i miljön',
                'validated', true
            ),
            jsonb_build_object(
                'assumption', 'Git repository finns i arbetskatalogen',
                'validated', false,
                'note', 'Inte alla projekt är git repos'
            )
        ),
        'provenance', jsonb_build_object(
            'source', 'user_seed',
            'confidence', 1.0
        )
    ))
    ON CONFLICT (COALESCE(project_id, '00000000-0000-0000-0000-000000000000'), layer, type, name) WHERE deleted_at IS NULL
    DO UPDATE SET data = EXCLUDED.data;

END $$;

-- ============================================================================
-- BOOTSTRAP HELPER FUNCTION
-- ============================================================================

-- Function to bootstrap a new project
CREATE OR REPLACE FUNCTION bootstrap_project(
    p_name TEXT,
    p_description TEXT,
    p_owner TEXT DEFAULT NULL,
    p_workspace_path TEXT DEFAULT NULL,
    p_stack TEXT[] DEFAULT ARRAY[]::TEXT[],
    p_constraints TEXT[] DEFAULT ARRAY[]::TEXT[]
) RETURNS UUID AS $$
DECLARE
    project_id UUID;
BEGIN
    -- Create project node
    INSERT INTO nodes (layer, type, name, data)
    VALUES (
        'SYSTEM',
        'project',
        p_name,
        jsonb_build_object(
            'description', p_description,
            'owner', COALESCE(p_owner, current_user),
            'workspace_path', p_workspace_path,
            'stack', to_jsonb(p_stack),
            'constraints', to_jsonb(p_constraints),
            'milestones', '[]'::jsonb,
            'created_at', NOW()
        )
    )
    RETURNING id INTO project_id;

    -- Create intro node
    INSERT INTO nodes (layer, type, name, project_id, data)
    VALUES (
        'CONTEXT',
        'intro',
        p_name || '_intro',
        project_id,
        jsonb_build_object(
            'title', p_name,
            'description', p_description,
            'provenance', jsonb_build_object(
                'source', 'bootstrap_function',
                'confidence', 1.0,
                'created_at', NOW()
            )
        )
    );

    -- Create initial intent
    INSERT INTO nodes (layer, type, name, project_id, data)
    VALUES (
        'CONTEXT',
        'intent',
        p_name || '_initial_intent',
        project_id,
        jsonb_build_object(
            'statement', 'Initial project intent - to be refined',
            'status', 'draft',
            'provenance', jsonb_build_object(
                'source', 'bootstrap_function',
                'confidence', 0.5,
                'created_at', NOW()
            )
        )
    );

    RETURN project_id;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION bootstrap_project IS 'Bootstrap a new project with standard structure (project, intro, intent nodes)';

COMMIT;
