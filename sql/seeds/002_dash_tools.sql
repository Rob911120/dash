-- ============================================================================
-- 002_dash_tools.sql
-- Dash: Tool metadata for execution
-- ============================================================================

-- ============================================================================
-- SQL TOOLS
-- ============================================================================

INSERT INTO nodes (layer, type, name, data) VALUES
('AUTOMATION', 'tool', 'postgres_query', '{
    "executor": "sql",
    "description": "Execute a read-only SQL query against PostgreSQL",
    "args_schema": {
        "type": "object",
        "required": ["query"],
        "properties": {
            "query": {
                "type": "string",
                "minLength": 1,
                "description": "SQL query to execute (SELECT only)"
            },
            "params": {
                "type": "array",
                "items": {},
                "description": "Query parameters for prepared statement"
            },
            "timeout_ms": {
                "type": "integer",
                "minimum": 100,
                "maximum": 30000,
                "default": 5000,
                "description": "Query timeout in milliseconds"
            }
        }
    },
    "constraints": {
        "read_only": true,
        "max_rows": 1000
    }
}'::jsonb);

INSERT INTO nodes (layer, type, name, data) VALUES
('AUTOMATION', 'tool', 'postgres_execute', '{
    "executor": "sql",
    "description": "Execute a write SQL statement against PostgreSQL",
    "args_schema": {
        "type": "object",
        "required": ["statement"],
        "properties": {
            "statement": {
                "type": "string",
                "minLength": 1,
                "description": "SQL statement to execute (INSERT/UPDATE/DELETE)"
            },
            "params": {
                "type": "array",
                "items": {},
                "description": "Statement parameters"
            },
            "timeout_ms": {
                "type": "integer",
                "minimum": 100,
                "maximum": 30000,
                "default": 5000,
                "description": "Statement timeout in milliseconds"
            }
        }
    },
    "constraints": {
        "read_only": false
    }
}'::jsonb);

-- ============================================================================
-- FILESYSTEM TOOLS
-- ============================================================================

INSERT INTO nodes (layer, type, name, data) VALUES
('AUTOMATION', 'tool', 'read_file', '{
    "executor": "filesystem_read",
    "description": "Read the contents of a file",
    "args_schema": {
        "type": "object",
        "required": ["path"],
        "properties": {
            "path": {
                "type": "string",
                "minLength": 1,
                "description": "Path to the file (relative to allowed root)"
            },
            "encoding": {
                "type": "string",
                "enum": ["utf-8", "base64"],
                "default": "utf-8",
                "description": "File encoding"
            },
            "max_bytes": {
                "type": "integer",
                "minimum": 1,
                "maximum": 10485760,
                "default": 1048576,
                "description": "Maximum bytes to read (default 1MB)"
            }
        }
    },
    "constraints": {
        "allowed_root": "/body/"
    }
}'::jsonb);

INSERT INTO nodes (layer, type, name, data) VALUES
('AUTOMATION', 'tool', 'write_file', '{
    "executor": "filesystem_write",
    "description": "Write content to a file",
    "args_schema": {
        "type": "object",
        "required": ["path", "content"],
        "properties": {
            "path": {
                "type": "string",
                "minLength": 1,
                "description": "Path to the file (relative to allowed root)"
            },
            "content": {
                "type": "string",
                "description": "Content to write"
            },
            "encoding": {
                "type": "string",
                "enum": ["utf-8", "base64"],
                "default": "utf-8",
                "description": "Content encoding"
            },
            "create_dirs": {
                "type": "boolean",
                "default": false,
                "description": "Create parent directories if they dont exist"
            },
            "overwrite": {
                "type": "boolean",
                "default": false,
                "description": "Overwrite existing file"
            }
        }
    },
    "constraints": {
        "allowed_root": "/body/",
        "max_size_bytes": 10485760
    }
}'::jsonb);

INSERT INTO nodes (layer, type, name, data) VALUES
('AUTOMATION', 'tool', 'list_directory', '{
    "executor": "filesystem_read",
    "description": "List contents of a directory",
    "args_schema": {
        "type": "object",
        "required": ["path"],
        "properties": {
            "path": {
                "type": "string",
                "minLength": 1,
                "description": "Path to the directory"
            },
            "recursive": {
                "type": "boolean",
                "default": false,
                "description": "List recursively"
            },
            "include_hidden": {
                "type": "boolean",
                "default": false,
                "description": "Include hidden files"
            },
            "pattern": {
                "type": "string",
                "description": "Glob pattern to filter results"
            }
        }
    },
    "constraints": {
        "allowed_root": "/body/",
        "max_entries": 1000
    }
}'::jsonb);

INSERT INTO nodes (layer, type, name, data) VALUES
('AUTOMATION', 'tool', 'delete_file', '{
    "executor": "filesystem_write",
    "description": "Delete a file",
    "args_schema": {
        "type": "object",
        "required": ["path"],
        "properties": {
            "path": {
                "type": "string",
                "minLength": 1,
                "description": "Path to the file to delete"
            }
        }
    },
    "constraints": {
        "allowed_root": "/body/"
    }
}'::jsonb);

-- ============================================================================
-- HTTP TOOLS
-- ============================================================================

INSERT INTO nodes (layer, type, name, data) VALUES
('AUTOMATION', 'tool', 'http_request', '{
    "executor": "http",
    "description": "Make an HTTP request",
    "args_schema": {
        "type": "object",
        "required": ["url"],
        "properties": {
            "url": {
                "type": "string",
                "format": "uri",
                "description": "URL to request"
            },
            "method": {
                "type": "string",
                "enum": ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"],
                "default": "GET",
                "description": "HTTP method"
            },
            "headers": {
                "type": "object",
                "additionalProperties": {"type": "string"},
                "description": "Request headers"
            },
            "body": {
                "type": "string",
                "description": "Request body"
            },
            "timeout_ms": {
                "type": "integer",
                "minimum": 100,
                "maximum": 60000,
                "default": 10000,
                "description": "Request timeout in milliseconds"
            },
            "follow_redirects": {
                "type": "boolean",
                "default": true,
                "description": "Follow redirects"
            }
        }
    },
    "constraints": {
        "max_response_size": 10485760,
        "allowed_schemes": ["http", "https"]
    }
}'::jsonb);

INSERT INTO nodes (layer, type, name, data) VALUES
('AUTOMATION', 'tool', 'http_get', '{
    "executor": "http",
    "description": "Make an HTTP GET request (simplified)",
    "args_schema": {
        "type": "object",
        "required": ["url"],
        "properties": {
            "url": {
                "type": "string",
                "format": "uri",
                "description": "URL to request"
            },
            "headers": {
                "type": "object",
                "additionalProperties": {"type": "string"},
                "description": "Request headers"
            }
        }
    },
    "constraints": {
        "max_response_size": 10485760
    }
}'::jsonb);

-- ============================================================================
-- GRAPH TOOLS
-- ============================================================================

INSERT INTO nodes (layer, type, name, data) VALUES
('AUTOMATION', 'tool', 'create_node', '{
    "executor": "sql",
    "description": "Create a new node in the graph",
    "args_schema": {
        "type": "object",
        "required": ["layer", "type", "name"],
        "properties": {
            "layer": {
                "type": "string",
                "enum": ["CONTEXT", "SYSTEM", "AUTOMATION"],
                "description": "Node layer"
            },
            "type": {
                "type": "string",
                "minLength": 1,
                "description": "Node type"
            },
            "name": {
                "type": "string",
                "minLength": 1,
                "description": "Node name"
            },
            "data": {
                "type": "object",
                "description": "Node data"
            }
        }
    }
}'::jsonb);

INSERT INTO nodes (layer, type, name, data) VALUES
('AUTOMATION', 'tool', 'create_edge', '{
    "executor": "sql",
    "description": "Create an edge between two nodes",
    "args_schema": {
        "type": "object",
        "required": ["source_id", "target_id", "relation"],
        "properties": {
            "source_id": {
                "type": "string",
                "format": "uuid",
                "description": "Source node ID"
            },
            "target_id": {
                "type": "string",
                "format": "uuid",
                "description": "Target node ID"
            },
            "relation": {
                "type": "string",
                "enum": ["depends_on", "owns", "uses", "generated_by", "instance_of", "child_of", "configured_by"],
                "description": "Edge relation type"
            },
            "data": {
                "type": "object",
                "description": "Edge metadata"
            }
        }
    }
}'::jsonb);

INSERT INTO nodes (layer, type, name, data) VALUES
('AUTOMATION', 'tool', 'query_graph', '{
    "executor": "sql",
    "description": "Query the graph with filters",
    "args_schema": {
        "type": "object",
        "properties": {
            "layer": {
                "type": "string",
                "enum": ["CONTEXT", "SYSTEM", "AUTOMATION"],
                "description": "Filter by layer"
            },
            "type": {
                "type": "string",
                "description": "Filter by type"
            },
            "name_pattern": {
                "type": "string",
                "description": "Filter by name pattern (LIKE)"
            },
            "data_filter": {
                "type": "object",
                "description": "Filter by data fields (JSONB containment)"
            },
            "limit": {
                "type": "integer",
                "minimum": 1,
                "maximum": 1000,
                "default": 100,
                "description": "Maximum results"
            }
        }
    }
}'::jsonb);
