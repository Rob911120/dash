# Dash: Unified Graph Architecture

> Ett självförbättrande system där allt är noder och relationer.

## Projektöversikt

Dash är en grafbaserad arkitektur för att modellera och spåra system, intentioner, automation och telemetri. Kärnan är en PostgreSQL-databas med fyra semantiska lager representerade i fyra fysiska tabeller.

## Snabbstart

```bash
# 1. Kör migrations
psql -d dash -f sql/migrations/010_dash_schema.sql
psql -d dash -f sql/migrations/011_dash_partitions.sql
psql -d dash -f sql/migrations/012_dash_views_and_funcs.sql

# 2. Kör seeds
psql -d dash -f sql/seeds/001_dash_schemas.sql
psql -d dash -f sql/seeds/002_dash_tools.sql
psql -d dash -f sql/seeds/003_dash_agents.sql

# 3. Skapa partitioner
psql -d dash -c "SELECT ensure_future_partitions(6);"
```

---

## Arkitektur

### Semantiska lager

| Lager | Syfte | Fråga |
|-------|-------|-------|
| **CONTEXT** | Intentioner, planer, beslut | "Varför?" |
| **SYSTEM** | Services, tabeller, filer, containers | "Vad finns?" |
| **AUTOMATION** | Tools, agents, schemas, patterns | "Hur gör vi?" |
| **OBSERVATION** | Telemetri (metrics/logs/events/traces) | "Vad hände?" |

### Fysiska tabeller

| Tabell | Innehåll | Partitionerad |
|--------|----------|---------------|
| `nodes` | Alla entiteter (grafkärna) | Nej |
| `edges` | Stabil topologi mellan noder | Nej |
| `edge_events` | Händelser/lineage mellan noder | Ja (månad) |
| `observations` | Telemetri och tidsserier | Ja (månad) |
| `node_versions` | Versionshistorik för noder | Nej |

### Dataflödesregel

```
Vad ska sparas var?
├── Entitet/ting → nodes
├── Stabil relation → edges
├── Kausalitet/utfall/lineage → edge_events
└── Telemetri om en nod → observations
```

**VIKTIGT:** OBSERVATION-data ska ALDRIG ligga i `nodes`-tabellen. En DB-trigger blockerar detta.

---

## Mappstruktur

```
/dash
├── claude.md                 # Denna fil
├── README.md                 # Publik dokumentation
│
├── sql/
│   ├── migrations/
│   │   ├── 010_dash_schema.sql        # Types, nodes, edges, triggers
│   │   ├── 011_dash_partitions.sql    # Partitionerade tabeller
│   │   └── 012_dash_views_and_funcs.sql # Vyer och funktioner
│   │
│   └── seeds/
│       ├── 001_dash_schemas.sql       # Schema-definitioner
│       ├── 002_dash_tools.sql         # Tool-metadata
│       └── 003_dash_agents.sql        # Agent-konfigurationer
│
├── dash/                     # Go-paket
│   ├── types.go              # Node, Edge, EdgeEvent, Observation
│   ├── nodes.go              # CRUD för nodes
│   ├── edges.go              # CRUD för edges
│   ├── events.go             # EdgeEvent insert/query
│   ├── observations.go       # Observation insert/query
│   ├── versions.go           # Versionshistorik-queries
│   ├── traverse.go           # Graf-traversering
│   ├── executor.go           # Tool execution (allowlist)
│   ├── fileconfig.go         # File root policy
│   ├── validate.go           # Args + node validering
│   └── schemalookup.go       # Schema lookup
│
└── scripts/
    ├── partition_maintenance.sh  # Cron-script för partitioner
    └── smoketest.sql             # Verifieringsscript
```

---

## Kodkonventioner

### Go

```go
// Alla funktioner returnerar (result, error)
func (d *Dash) GetNode(id uuid.UUID) (*Node, error)

// Context ska alltid vara första parameter
func (d *Dash) CreateNode(ctx context.Context, node *Node) error

// Använd prepared statements för alla queries
const queryGetNode = `SELECT ... FROM nodes WHERE id = $1`
```

### SQL

```sql
-- Använd ALLTID deleted_at IS NULL för aktiva noder
SELECT * FROM nodes WHERE id = $1 AND deleted_at IS NULL;

-- Använd v_nodes_active / v_edges_active i vyer
SELECT * FROM v_nodes_active WHERE layer = 'SYSTEM';

-- Inkludera deprecated_at check för edges
SELECT * FROM edges WHERE source_id = $1 AND deprecated_at IS NULL;
```

### Namngivning

- **Tabeller:** snake_case plural (`nodes`, `edge_events`)
- **Kolumner:** snake_case (`created_at`, `source_id`)
- **Go types:** PascalCase (`Node`, `EdgeEvent`)
- **Go functions:** PascalCase public, camelCase private
- **Index:** `idx_<tabell>_<kolumner>` (`idx_nodes_layer_type`)

---

## Kritiska regler

### 1. Soft Delete Only

```sql
-- ALDRIG gör detta:
DELETE FROM nodes WHERE id = '...';

-- GÖR detta:
UPDATE nodes SET deleted_at = NOW() WHERE id = '...';
```

Edges markeras automatiskt som `deprecated_at` via trigger.

### 2. Fil-operationer

```go
// ALLTID validera path innan fil-operationer
path, err := fileConfig.ValidatePath(requestedPath)
if err != nil {
    return nil, err // Path traversal attempt
}
```

Allowed root: `/body/`

### 3. OBSERVATION-data

```sql
-- OBSERVATION-data går ALLTID till observations-tabellen
INSERT INTO observations (node_id, type, data) VALUES (...);

-- ALDRIG till nodes (trigger kastar exception)
INSERT INTO nodes (layer, ...) VALUES ('OBSERVATION', ...); -- FEL!
```

### 4. Executor Allowlist

```go
// Endast registrerade executors tillåts
var executors = map[string]Executor{
    "sql":              &SQLExecutor{},
    "filesystem_read":  &FileReadExecutor{},
    "filesystem_write": &FileWriteExecutor{},
    "http":             &HTTPExecutor{},
}
```

---

## Vanliga operationer

### Skapa en nod

```go
node := &Node{
    Layer: LayerSystem,
    Type:  "service",
    Name:  "api-gateway",
    Data: map[string]any{
        "port":   8080,
        "status": "running",
    },
}
err := dash.CreateNode(ctx, node)
```

### Skapa en edge

```go
edge := &Edge{
    SourceID: serviceNode.ID,
    TargetID: dbNode.ID,
    Relation: RelationDependsOn,
}
err := dash.CreateEdge(ctx, edge)
```

### Logga ett event

```go
event := &EdgeEvent{
    SourceID:   intentNode.ID,
    TargetID:   serviceNode.ID,
    Relation:   EventRelationResultedIn,
    Success:    true,
    DurationMs: 1234,
}
err := dash.CreateEdgeEvent(ctx, event)
```

### Hämta dependencies

```go
deps, err := dash.GetDependencies(ctx, nodeID, 10) // max depth 10
```

### Hämta versionshistorik

```go
versions, err := dash.GetNodeVersions(ctx, nodeID)
// eller
oldData, err := dash.GetNodeAtTime(ctx, nodeID, timestamp)
```

---

## Underhåll

### Dagligen

```bash
# Flytta data från default partitioner
psql -d dash -c "CALL relocate_edge_events_default(5000);"
psql -d dash -c "CALL relocate_observations_default(5000);"
```

### Månatligen

```bash
# Skapa framtida partitioner
psql -d dash -c "SELECT ensure_future_partitions(6);"
```

### Övervaka

```sql
-- Kolla default partition storlek (ska vara ~0)
SELECT COUNT(*) FROM edge_events_default;
SELECT COUNT(*) FROM observations_default;

-- Kolla aktiva noder per lager
SELECT layer, COUNT(*) FROM v_nodes_active GROUP BY layer;
```

---

## Felsökning

### "OBSERVATION data must be stored in observations table"

Du försöker skapa en nod med `layer = 'OBSERVATION'`. Använd `observations`-tabellen istället.

### "path traversal detected"

Fil-path försöker gå utanför allowed root. Kontrollera att path inte innehåller `..` eller absoluta paths utanför `/body/`.

### "unknown executor"

Tool refererar till en executor som inte finns i allowlist. Lägg till i `executors` map eller fixa tool-metadata.

### Långsamma queries på edge_events/observations

1. Kontrollera att du filtrerar på `occurred_at`/`observed_at`
2. Verifiera att rätt partition finns: `\d+ edge_events`
3. Kör `CALL relocate_*_default()` om default partition är stor

---

## Relationer

### dash_relation (stabil topologi)

| Relation | Betydelse |
|----------|-----------|
| `depends_on` | A behöver B för att fungera |
| `owns` | A äger/ansvarar för B |
| `uses` | A använder B |
| `generated_by` | A skapades av B |
| `instance_of` | A är en instans av B |
| `child_of` | A är barn till B (hierarki) |
| `configured_by` | A konfigureras av B |

### dash_event_relation (händelser)

| Relation | Betydelse |
|----------|-----------|
| `resulted_in` | A ledde till B |
| `observed` | A observerade B |
| `measured` | A mätte B |
| `failed_with` | A misslyckades med B |
| `triggered` | A triggade B |
| `completed` | A slutförde B |
| `started` | A startade B |
| `modified` | A modifierade B |

---

## Schema-validering

Schemas definieras som noder i AUTOMATION-lagret:

```sql
SELECT * FROM nodes
WHERE layer = 'AUTOMATION'
  AND type = 'schema'
  AND data->>'for_layer' = 'SYSTEM'
  AND data->>'for_type' = 'service';
```

Schema-format:

```json
{
  "for_layer": "SYSTEM",
  "for_type": "service",
  "fields": {
    "port": {"type": "integer", "required": false},
    "status": {"type": "enum", "values": ["running", "stopped", "error"]}
  }
}
```

---

## Tool-execution

Tools är metadata i databasen, execution sker via allowlist i Go.

```sql
-- Tool-metadata
SELECT data FROM nodes
WHERE layer = 'AUTOMATION' AND type = 'tool' AND name = 'read_file';
```

```json
{
  "executor": "filesystem_read",
  "description": "Läs innehållet i en fil",
  "args_schema": {
    "type": "object",
    "required": ["path"],
    "properties": {
      "path": {"type": "string", "minLength": 1}
    }
  }
}
```

Execution:

```go
result, err := dash.ExecuteTool("read_file", map[string]any{
    "path": "/body/config.yaml",
})
```

---

## Tester

### Smoketest

```sql
-- Kör efter setup
\i scripts/smoketest.sql
```

### Go-tester

```bash
cd dash
go test ./...
```

---

## Kontakt

Vid frågor om arkitekturen, se detta dokument först. För implementation, se respektive Go-fil.
