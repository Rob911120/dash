-- Seed 006: Lifecycle bootstrap data - mission, context_frame, constraints

-- CONTEXT.mission: the project mission
INSERT INTO nodes (layer, type, name, data)
VALUES ('CONTEXT', 'mission', 'dash-mission', '{
    "statement": "Build a self-improving graph system that models systems, intentions, automation and telemetry - where every session makes the graph smarter.",
    "status": "active",
    "success_criteria": [
        "Graph can answer: What is the mission, where are we, what is next, what blocks us?",
        "Sessions produce canonical knowledge (insights, decisions) not just ephemeral logs",
        "Working set stays bounded (~25 nodes) regardless of total graph size",
        "Old sessions get garbage collected without losing extracted knowledge"
    ]
}'::jsonb)
ON CONFLICT DO NOTHING;

-- CONTEXT.context_frame: the current focus singleton
INSERT INTO nodes (layer, type, name, data)
VALUES ('CONTEXT', 'context_frame', 'current', '{
    "summary": "Implementing data lifecycle pipeline with promotion, working set, and GC.",
    "current_focus": "Data lifecycle: schema, working set, promotion, GC",
    "next_steps": [
        "Verify lifecycle implementation end-to-end",
        "Run promotion on first real session",
        "Tune GC retention policy"
    ],
    "blockers": []
}'::jsonb)
ON CONFLICT DO NOTHING;

-- CONTEXT.constraint: soft-delete-only
INSERT INTO nodes (layer, type, name, data)
VALUES ('CONTEXT', 'constraint', 'soft-delete-only', '{
    "text": "Never hard-delete data. Always use soft-delete (deleted_at/deprecated_at).",
    "scope": "global",
    "enforcement": "hard",
    "rationale": "Audit trail and recovery capability"
}'::jsonb)
ON CONFLICT DO NOTHING;

-- CONTEXT.constraint: observation-not-in-nodes
INSERT INTO nodes (layer, type, name, data)
VALUES ('CONTEXT', 'constraint', 'observation-not-in-nodes', '{
    "text": "OBSERVATION data must go to the observations table, never to nodes.",
    "scope": "data-model",
    "enforcement": "hard",
    "rationale": "DB trigger enforces this - nodes layer=OBSERVATION is rejected"
}'::jsonb)
ON CONFLICT DO NOTHING;

-- CONTEXT.constraint: bounded-working-set
INSERT INTO nodes (layer, type, name, data)
VALUES ('CONTEXT', 'constraint', 'bounded-working-set', '{
    "text": "Working set must stay bounded at ~25 nodes max regardless of total graph size.",
    "scope": "working-set",
    "enforcement": "soft",
    "rationale": "Context injection must stay small enough for LLM context windows"
}'::jsonb)
ON CONFLICT DO NOTHING;

-- Link context_frame to mission
DO $$
DECLARE
    v_frame_id uuid;
    v_mission_id uuid;
BEGIN
    SELECT id INTO v_frame_id FROM nodes WHERE layer = 'CONTEXT' AND type = 'context_frame' AND name = 'current' AND deleted_at IS NULL LIMIT 1;
    SELECT id INTO v_mission_id FROM nodes WHERE layer = 'CONTEXT' AND type = 'mission' AND name = 'dash-mission' AND deleted_at IS NULL LIMIT 1;

    IF v_frame_id IS NOT NULL AND v_mission_id IS NOT NULL THEN
        INSERT INTO edges (source_id, target_id, relation)
        VALUES (v_frame_id, v_mission_id, 'points_to')
        ON CONFLICT DO NOTHING;
    END IF;
END $$;
