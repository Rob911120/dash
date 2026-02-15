# Dash

A self-improving graph system that observes, learns, and suggests its own evolution — with human control as safety net.

## What it does

Dash wraps around [Claude Code](https://claude.ai) sessions via **hooks** and **MCP tools**, building a persistent knowledge graph in PostgreSQL. It tracks files, decisions, tasks, and insights across sessions — then uses that context to improve future interactions.

```
Claude Code session
    ├── Hook (dashhook)  → observes sessions, files, tool use → PostgreSQL
    └── MCP  (dashmcp)   → on-demand: search, query, traverse, promote → PostgreSQL
```

## Architecture

**PostgreSQL + pgvector.** Four tables:

| Table | Purpose |
|-------|---------|
| `nodes` | All entities across 3 layers: CONTEXT (why), SYSTEM (what), AUTOMATION (how) |
| `edges` | Stable relationships (depends_on, owns, uses, ...) |
| `edge_events` | Causal events between nodes (partitioned monthly) |
| `observations` | Telemetry and time series (partitioned monthly) |

**Go package** (`module dash`) — graph engine, prompt pipeline, embedding, semantic search, pattern detection, garbage collection.

## Components

| Binary | Purpose |
|--------|---------|
| `cmd/dashhook` | Claude Code hook handler — processes session/tool events |
| `cmd/dashmcp` | MCP server — exposes graph as tools (search, query, node CRUD, traverse, ...) |
| `cmd/cockpit` | TUI dashboard (Bubbletea) — agent monitoring, chat, overlays |
| `cmd/dashwatch` | File watcher daemon — auto-embeds changed files via fsnotify |
| `cmd/dashquery` | CLI query tool |

## Setup

Requires: Go 1.22+, PostgreSQL with pgvector.

```bash
# Run migrations
psql -f sql/migrations/010_dash_schema.sql
# ... through 019_work_order.sql

# Build
cd cmd/dashhook && go build -o /path/to/hooks/dashhook .
cd cmd/dashmcp  && go build -o /path/to/mcp/dashmcp .
cd cmd/cockpit  && go build -o cockpit .
```

Configure Claude Code hooks to point at `dashhook`, and MCP config to point at `dashmcp`.

## Key constraints

- **Soft delete only** — never hard-delete data
- **Observations go in `observations` table** — never in `nodes` (enforced by trigger)
- **Working set bounded at ~25 nodes** — keeps context focused regardless of graph size

## License

Private.
