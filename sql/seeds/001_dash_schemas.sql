-- ============================================================================
-- 001_dash_schemas.sql
-- Dash: Schema definitions for node validation
-- ============================================================================

-- ============================================================================
-- CONTEXT LAYER SCHEMAS
-- ============================================================================

-- Intent schema
INSERT INTO nodes (layer, type, name, data) VALUES
('AUTOMATION', 'schema', 'intent_schema', '{
    "for_layer": "CONTEXT",
    "for_type": "intent",
    "description": "Schema for intent nodes representing planned work or goals",
    "fields": {
        "status": {
            "type": "enum",
            "values": ["draft", "active", "completed", "cancelled"],
            "required": true,
            "default": "draft"
        },
        "priority": {
            "type": "enum",
            "values": ["low", "medium", "high", "critical"],
            "required": false,
            "default": "medium"
        },
        "description": {
            "type": "string",
            "required": false
        },
        "due_date": {
            "type": "timestamp",
            "required": false
        },
        "tags": {
            "type": "array",
            "items": {"type": "string"},
            "required": false
        }
    }
}'::jsonb);

-- Decision schema
INSERT INTO nodes (layer, type, name, data) VALUES
('AUTOMATION', 'schema', 'decision_schema', '{
    "for_layer": "CONTEXT",
    "for_type": "decision",
    "description": "Schema for decision nodes capturing architectural choices",
    "fields": {
        "status": {
            "type": "enum",
            "values": ["proposed", "accepted", "rejected", "superseded"],
            "required": true,
            "default": "proposed"
        },
        "context": {
            "type": "string",
            "required": true
        },
        "decision": {
            "type": "string",
            "required": true
        },
        "consequences": {
            "type": "array",
            "items": {"type": "string"},
            "required": false
        },
        "alternatives": {
            "type": "array",
            "items": {"type": "object"},
            "required": false
        }
    }
}'::jsonb);

-- ============================================================================
-- SYSTEM LAYER SCHEMAS
-- ============================================================================

-- Service schema
INSERT INTO nodes (layer, type, name, data) VALUES
('AUTOMATION', 'schema', 'service_schema', '{
    "for_layer": "SYSTEM",
    "for_type": "service",
    "description": "Schema for service nodes representing running services",
    "fields": {
        "status": {
            "type": "enum",
            "values": ["running", "stopped", "error", "starting", "stopping"],
            "required": false,
            "default": "stopped"
        },
        "port": {
            "type": "integer",
            "required": false,
            "min": 1,
            "max": 65535
        },
        "host": {
            "type": "string",
            "required": false
        },
        "version": {
            "type": "string",
            "required": false
        },
        "health_endpoint": {
            "type": "string",
            "required": false
        }
    }
}'::jsonb);

-- Database schema
INSERT INTO nodes (layer, type, name, data) VALUES
('AUTOMATION', 'schema', 'database_schema', '{
    "for_layer": "SYSTEM",
    "for_type": "database",
    "description": "Schema for database nodes",
    "fields": {
        "engine": {
            "type": "enum",
            "values": ["postgresql", "mysql", "sqlite", "mongodb", "redis"],
            "required": true
        },
        "host": {
            "type": "string",
            "required": false
        },
        "port": {
            "type": "integer",
            "required": false
        },
        "database_name": {
            "type": "string",
            "required": false
        },
        "connection_pool_size": {
            "type": "integer",
            "required": false,
            "min": 1,
            "max": 100
        }
    }
}'::jsonb);

-- File schema
INSERT INTO nodes (layer, type, name, data) VALUES
('AUTOMATION', 'schema', 'file_schema', '{
    "for_layer": "SYSTEM",
    "for_type": "file",
    "description": "Schema for file nodes representing files in the system",
    "fields": {
        "path": {
            "type": "string",
            "required": true
        },
        "mime_type": {
            "type": "string",
            "required": false
        },
        "size_bytes": {
            "type": "integer",
            "required": false,
            "min": 0
        },
        "checksum": {
            "type": "string",
            "required": false
        }
    }
}'::jsonb);

-- Table schema (database table)
INSERT INTO nodes (layer, type, name, data) VALUES
('AUTOMATION', 'schema', 'table_schema', '{
    "for_layer": "SYSTEM",
    "for_type": "table",
    "description": "Schema for database table nodes",
    "fields": {
        "database": {
            "type": "string",
            "required": true
        },
        "schema_name": {
            "type": "string",
            "required": false,
            "default": "public"
        },
        "columns": {
            "type": "array",
            "items": {
                "type": "object",
                "properties": {
                    "name": {"type": "string"},
                    "type": {"type": "string"},
                    "nullable": {"type": "boolean"}
                }
            },
            "required": false
        },
        "row_count_estimate": {
            "type": "integer",
            "required": false
        }
    }
}'::jsonb);

-- Container schema
INSERT INTO nodes (layer, type, name, data) VALUES
('AUTOMATION', 'schema', 'container_schema', '{
    "for_layer": "SYSTEM",
    "for_type": "container",
    "description": "Schema for container nodes (Docker/Kubernetes)",
    "fields": {
        "image": {
            "type": "string",
            "required": true
        },
        "tag": {
            "type": "string",
            "required": false,
            "default": "latest"
        },
        "status": {
            "type": "enum",
            "values": ["running", "stopped", "restarting", "paused", "exited"],
            "required": false
        },
        "ports": {
            "type": "array",
            "items": {"type": "string"},
            "required": false
        },
        "environment": {
            "type": "object",
            "required": false
        }
    }
}'::jsonb);

-- ============================================================================
-- AUTOMATION LAYER SCHEMAS
-- ============================================================================

-- Pattern schema
INSERT INTO nodes (layer, type, name, data) VALUES
('AUTOMATION', 'schema', 'pattern_schema', '{
    "for_layer": "AUTOMATION",
    "for_type": "pattern",
    "description": "Schema for pattern nodes representing reusable patterns",
    "fields": {
        "category": {
            "type": "enum",
            "values": ["structural", "behavioral", "creational", "integration"],
            "required": false
        },
        "template": {
            "type": "string",
            "required": false
        },
        "parameters": {
            "type": "object",
            "required": false
        },
        "examples": {
            "type": "array",
            "items": {"type": "string"},
            "required": false
        }
    }
}'::jsonb);

-- Agent schema
INSERT INTO nodes (layer, type, name, data) VALUES
('AUTOMATION', 'schema', 'agent_schema', '{
    "for_layer": "AUTOMATION",
    "for_type": "agent",
    "description": "Schema for agent nodes representing autonomous agents",
    "fields": {
        "status": {
            "type": "enum",
            "values": ["idle", "running", "paused", "error"],
            "required": false,
            "default": "idle"
        },
        "capabilities": {
            "type": "array",
            "items": {"type": "string"},
            "required": false
        },
        "allowed_tools": {
            "type": "array",
            "items": {"type": "string"},
            "required": false
        },
        "config": {
            "type": "object",
            "required": false
        }
    }
}'::jsonb);
