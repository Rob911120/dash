# Dash

> Ett självförbättrande grafsystem som observerar, lär sig och föreslår sin egen utveckling.

## Lär känna systemet

Du har MCP-verktyg (prefix `mcp__d__`) som ger dig direkt tillgång till grafen. **Använd dem.** Denna fil är en karta - grafen är territoriet.

```
mcp__d__working_set()          # Vad är aktivt just nu?
mcp__d__summary(hours: 24)     # Vad har hänt senaste dygnet?
mcp__d__search("prompt pipeline")  # Hitta relevanta filer semantiskt
mcp__d__tasks()                # Vilka tasks finns och vad blockerar?
mcp__d__query("SELECT ...")    # Fråga databasen direkt
```

Om du är osäker på något - fråga grafen innan du antar.

---

## Arkitektur

### Kärnan: allt är noder och relationer

PostgreSQL-databas (`localhost:5432/dash`) med pgvector. Fyra tabeller:

| Tabell | Innehåll |
|--------|----------|
| `nodes` | Alla entiteter. Fyra lager: CONTEXT, SYSTEM, AUTOMATION, OBSERVATION* |
| `edges` | Stabila relationer mellan noder (depends_on, owns, uses, ...) |
| `edge_events` | Händelser mellan noder - kausalitet, lineage (partitionerad per månad) |
| `observations` | Telemetri och tidsserier (partitionerad per månad) |
| `node_versions` | Automatisk versionshistorik för noder |

*OBSERVATION-data ska ALDRIG lagras i `nodes`. En DB-trigger blockerar detta. Använd `observations`-tabellen.

### Semantiska lager

| Lager | Fråga det svarar | Exempel |
|-------|-------------------|---------|
| **CONTEXT** | "Varför?" | mission, tasks, insights, decisions, system_prompt, session |
| **SYSTEM** | "Vad finns?" | file, service, project |
| **AUTOMATION** | "Hur?" | tool, agent, schema, pattern |

### Dataflöde

```
Claude Code session
    │
    ├─ Hook (stdin JSON) ──► dashhook ──► PostgreSQL
    │   SessionStart: skapar session-nod, kör prompt-pipeline, returnerar kontext
    │   PreToolUse:   loggar intention som observation
    │   PostToolUse:  skapar SYSTEM.file nod + edge_event (observed/modified)
    │   SessionEnd:   uppdaterar session-status, beräknar richness score
    │
    └─ MCP (JSON-RPC) ──► dashmcp ──► PostgreSQL
        On-demand: search, query, node CRUD, traverse, promote, gc, ...
```

---

## Go-paketet (`/dash/dash/`)

Allt är ett Go-paket. `*Dash` struct håller DB-anslutning, embedder och summarizer.

### Grafkärna
| Fil | Ansvar |
|-----|--------|
| `types.go` | Node, Edge, EdgeEvent, Observation, Config, Dash struct |
| `nodes.go` | CRUD: GetNode, CreateNode, SoftDeleteNode, SearchNodes, ... |
| `edges.go` | CRUD: CreateEdge, DeprecateEdge, ListEdgesBySource, ... |
| `events.go` | CreateEdgeEvent, ListEdgeEventsBySource/Target/Relation |
| `observations.go` | CreateObservation, ListByNode, Aggregate |
| `versions.go` | GetNodeVersions, GetNodeAtTime, DiffNodeVersions |
| `traverse.go` | GetDependencies, GetDependents, TraceLineage, FindPath |

### Prompt-system
| Fil | Ansvar |
|-----|--------|
| `prompt_pipeline.go`* | Pipeline-ramverk: SourceFunc, Pipeline, 15 source-funktioner (srcTasks, srcFiles, srcInsights, ...) |
| `prompt_build.go`* | PromptConfig, RefreshContext(), RefreshAllContexts() - bygger + sparar system prompts |
| `prompt_task.go`* | RefreshTaskContext(), RefreshSuggestionContext() - scoped prompts |
| `git.go`* | GetGitStatus() - branch, uncommitted count |

*Filerna heter idag `context_pipeline.go`, `context_build.go`, `context_task.go`, `context_git.go` - namnbyte planerat.

Flödet: Pipeline läser grafdata → assemblerar text → sparar som CONTEXT.system_prompt nod → hook returnerar via stdout.

### Hook-system
| Fil | Ansvar |
|-----|--------|
| `hook_handler.go` | ProcessHookEvent() - huvudorkestrering för alla hook-events |
| `hook_types.go` | HookEventName, ClaudeCodeInput, HookOutput, ... |
| `hook_upsert.go` | GetOrCreateNode(), UpdateNodeData() - race-condition-safe |

### AI-pipeline
| Fil | Ansvar |
|-----|--------|
| `embedding.go` | OpenRouterEmbedder (text-embedding-3-small, 1536 dims), EmbedNode() |
| `search.go` | SearchSimilarFiles/SearchSimilar med pgvector (hnsw-index) |
| `summarizer.go` | OpenRouterSummarizer - AI-sammanfattningar av filer |

### Intelligence
| Fil | Ansvar |
|-----|--------|
| `intent_matching.go` | MatchTaskToIntents(), GetActiveTasksWithDeps(), GetHierarchyTree() |
| `suggest.go` | GenerateProposals() - förslag från co-editing, stale tasks, mission gaps |
| `patterns.go` | DetectCoEditingPatterns(), DetectFileChurn(), DetectToolSequences() |
| `scoring.go` | CalculateRichnessScore(), SuggestInsights() |
| `failure_check.go` | CheckPastFailures() - lär av tidigare fel |
| `task_linking.go` | LinkActiveTaskToFile() - automatisk task→fil edge |

### Infrastruktur
| Fil | Ansvar |
|-----|--------|
| `mcp.go` | MCPServer - JSON-RPC server, ToolDefinitions, CallTool |
| `tool.go` + `tool_registry.go` | ToolRegistry, ToolDef, ToolFunc |
| `tool_*.go` | Varje MCP-verktyg definierat som separat fil |
| `working_set.go` | AssembleWorkingSet(), GetSystemPrompt() |
| `promote.go` | PromoteSession() - extrahera kunskap från sessioner |
| `gc.go` | RunGC() - garbage collection med soft delete |
| `agent_tools.go` | RecentActivity(), SessionHistory(), FileHistory(), ContextSearch() |
| `project.go` | GetOrCreateProject(), GetProjectSummary() |
| `executor.go` | Tool execution via allowlist (SQL, filesystem, HTTP) |
| `fileconfig.go` | Path validation, traversal protection |
| `validate.go` | Args + node validation |
| `schemalookup.go` | Schema lookup för AUTOMATION.schema noder |
| `ui_settings.go` | UISettings, tone presets för TUI |
| `diagnostics.go` | RunDiagnostic(), system health checks |
| `file_metadata.go` | CaptureFileMetadata() |
| `system_state.go` | CaptureSystemState(), CaptureProcessContext() |

---

## Binärer (`/dash/cmd/`)

| Binär | Syfte | Bygg | Output |
|-------|-------|------|--------|
| `dashhook` | Claude Code hooks | `cd cmd/dashhook && go build -o /dash/.claude/hooks/dashhook .` | `.claude/hooks/dashhook` |
| `dashmcp` | MCP-server | `cd cmd/dashmcp && go build -o /dash/.claude/mcp/dashmcp .` | `.claude/mcp/dashmcp` |

| `dashwatch` | Filewatcher daemon (fsnotify) | `cd cmd/dashwatch && go build -o /dash/bin/dashwatch .` | `bin/dashwatch` |
| `dashquery` | CLI query-verktyg | `cd cmd/dashquery && go build .` | |
| `test_suggest` | Test-harness för suggest | `cd cmd/test_suggest && go build .` | |



### dashwatch
System daemon (OpenRC: `/etc/init.d/dashwatch`). Bevakar `/dash/{dash,cmd,sql,scripts}` med fsnotify. Auto-embeddar ändrade filer (debounce 2s, hash-jämförelse).

---

## SQL

### Migrations (`sql/migrations/`)
| Fil | Innehåll |
|-----|----------|
| `010_dash_schema.sql` | Typer, nodes, edges, triggers |
| `011_dash_partitions.sql` | edge_events + observations partitionering |
| `012_dash_views_and_funcs.sql` | Vyer och funktioner (get_dependencies, etc.) |
| `013_project_scope.sql` | Projekt-scope utökningar |
| `014_tool_use_id_index.sql` | Index för tool_use_id |
| `015_embeddings.sql` | pgvector, embedding-kolumn, hnsw-index |
| `016_lifecycle_enums.sql` | Lifecycle enum-typer |

### Seeds (`sql/seeds/`)
| Fil | Innehåll |
|-----|----------|
| `001_dash_schemas.sql` | Schema-definitioner |
| `002_dash_tools.sql` | Tool-metadata |
| `003_dash_agents.sql` | Agent-konfigurationer |
| `004_project_bootstrap.sql` | Projekt-bootstrap |
| `005_lifecycle_schemas.sql` | Lifecycle schemas |
| `006_lifecycle_seed.sql` | Lifecycle seed data |
| `010_dash_vision.sql` | Mission och intents |

---

## Konfiguration

### Hooks (`.claude/settings.json`)
Alla events triggar `.claude/hooks/dashhook` som läser JSON från stdin.

### MCP (`.mcp.json`)
Server `d` kör `/dash/.claude/mcp/dashmcp` med `OPENROUTER_API_KEY` i env.

### Go module
`module dash`, Go 1.22. Beroenden: Bubbletea (TUI), lib/pq (PostgreSQL), google/uuid, fsnotify.

---

## Kritiska regler

1. **Soft delete only.** `UPDATE nodes SET deleted_at = NOW()` - aldrig `DELETE`.
2. **OBSERVATION → observations-tabellen.** Aldrig i `nodes` (trigger blockerar).
3. **Path validation.** Alla filoperationer genom `FileConfig.ValidatePath()`.
4. **Working set max ~25 noder.** Bounded kontext oavsett total grafstorlek.
5. **Embeddings: hnsw, inte ivfflat.** ivfflat kräver 1000+ rader, hnsw fungerar alltid.

---

## MCP-verktyg (prefix: `mcp__d__`)

### Utforska
| Verktyg | Gör |
|---------|-----|
| `working_set()` | Aktuell arbetskontext: mission, tasks, constraints, insights |
| `summary(hours, scope)` | Projektöversikt: tasks, sessioner, filer |
| `tasks()` | Aktiva tasks med dependencies och intent-länkar |
| `search(query, limit)` | Semantisk sökning över filer |
| `query(query)` | Rå SQL (SELECT only) mot databasen |
| `activity(limit)` | Senaste sessioner |
| `session(session_id)` | Filoperationer för en session |
| `file(file_path)` | Historik för en specifik fil |

### Modifiera
| Verktyg | Gör |
|---------|-----|
| `node(op, ...)` | CRUD för noder (get/create/update/delete/list) |
| `link(op, ...)` | Edges: create/list/deprecate |
| `traverse(id, direction, depth)` | Navigera grafen (dependencies/dependents/lineage) |
| `remember(type, text)` | Spara insight/decision/todo |
| `promote(session_id, ...)` | Extrahera kunskap från session |
| `suggest_improvement(title, description, rationale)` | Registrera förbättringsförslag |

### Underhåll
| Verktyg | Gör |
|---------|-----|
| `gc(dry_run, ...)` | Garbage collection på gamla sessioner |
| `embed(op)` | Status/backfill av fil-embeddings |
| `patterns(type)` | Detektera co-editing, file-churn, tool-sequences |

### Filsystem (MCP)
| Verktyg | Gör |
|---------|-----|
| `read(path)` | Läs fil |
| `write(path, content)` | Skriv fil |
| `edit(path, old_text, new_text)` | Ersätt text i fil |
| `grep(pattern, path)` | Sök i filinnehåll |
| `glob(pattern, path)` | Hitta filer med mönster |
| `ls(path)` | Lista katalog |
| `exec(command)` | Kör shell-kommando |

---

## Kunskapslivscykel

```
Session (ephemeral, skapas av hooks)
    │
    ├── promote ──► Insights, Decisions, Tasks (permanent)
    │               Sparas som CONTEXT-noder i grafen
    │
    ├── gc ────────► Soft-deleted (efter retention)
    │               Extraherad kunskap bevaras
    │
    └── working_set ► Bounded kontext (~25 noder) för resonering
```

Sessioner är tillfälliga. Kunskap extraheras via `promote` till permanenta grafnoder. `gc` rensar gamla sessioner utan att förlora insikter.

---

## Bygga

```bash
# Allt
cd /dash/cmd/dashhook && go build -o /dash/.claude/hooks/dashhook .
cd /dash/cmd/dashmcp && go build -o /dash/.claude/mcp/dashmcp .
cd /dash/cmd/dashtui && go build -o /dash/bin/dashtui .
cd /dash/cmd/dashwatch && go build -o /dash/bin/dashwatch .

# Starta om dashwatch efter ändring
rc-service dashwatch restart

# Starta om MCP - starta om Claude Code
```

---

## Utforska vidare

Istället för att läsa mer dokumentation, fråga systemet:

```
# Vad finns i grafen?
mcp__d__query("SELECT layer, type, COUNT(*) FROM nodes WHERE deleted_at IS NULL GROUP BY layer, type ORDER BY count DESC")

# Vilka filer ändras oftast tillsammans?
mcp__d__patterns(type: "co-editing")

# Vad vet systemet om sig självt?
mcp__d__search("self-improvement")

# Vilka intents driver utvecklingen?
mcp__d__query("SELECT name, data->>'description' FROM nodes WHERE layer='CONTEXT' AND type='intent' AND deleted_at IS NULL")
```
