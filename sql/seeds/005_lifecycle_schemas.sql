-- Seed 005: Schema definitions for lifecycle node types

-- CONTEXT.mission schema
INSERT INTO nodes (layer, type, name, data)
VALUES ('AUTOMATION', 'schema', 'mission_schema', '{
    "for_layer": "CONTEXT",
    "for_type": "mission",
    "fields": {
        "statement": {"type": "string", "required": true},
        "status": {"type": "enum", "values": ["active", "paused", "completed", "abandoned"], "required": false},
        "success_criteria": {"type": "array", "items": {"type": "string"}, "required": false}
    }
}'::jsonb)
ON CONFLICT DO NOTHING;

-- CONTEXT.context_frame schema
INSERT INTO nodes (layer, type, name, data)
VALUES ('AUTOMATION', 'schema', 'context_frame_schema', '{
    "for_layer": "CONTEXT",
    "for_type": "context_frame",
    "fields": {
        "summary": {"type": "string", "required": true},
        "current_focus": {"type": "string", "required": false},
        "next_steps": {"type": "array", "items": {"type": "string"}, "required": false},
        "blockers": {"type": "array", "items": {"type": "string"}, "required": false}
    }
}'::jsonb)
ON CONFLICT DO NOTHING;

-- CONTEXT.constraint schema
INSERT INTO nodes (layer, type, name, data)
VALUES ('AUTOMATION', 'schema', 'constraint_schema', '{
    "for_layer": "CONTEXT",
    "for_type": "constraint",
    "fields": {
        "text": {"type": "string", "required": true},
        "scope": {"type": "string", "required": false},
        "enforcement": {"type": "enum", "values": ["hard", "soft"], "required": false},
        "rationale": {"type": "string", "required": false}
    }
}'::jsonb)
ON CONFLICT DO NOTHING;

-- CONTEXT.summary schema
INSERT INTO nodes (layer, type, name, data)
VALUES ('AUTOMATION', 'schema', 'summary_schema', '{
    "for_layer": "CONTEXT",
    "for_type": "summary",
    "fields": {
        "text": {"type": "string", "required": true},
        "covers_sessions": {"type": "array", "items": {"type": "string"}, "required": false},
        "period_start": {"type": "string", "required": false},
        "period_end": {"type": "string", "required": false}
    }
}'::jsonb)
ON CONFLICT DO NOTHING;
