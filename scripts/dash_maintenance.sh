#!/bin/bash
# ============================================================================
# dash_maintenance.sh
# Complete Dash graph maintenance: partitions, GC, embeddings, patterns
# ============================================================================
#
# Usage: Run daily via cron
#   0 3 * * * /dash/scripts/dash_maintenance.sh >> /var/log/dash_maintenance.log 2>&1
#
# Environment variables:
#   PGHOST     - PostgreSQL host (default: localhost)
#   PGDATABASE - Database name (default: dash)
#   PGUSER     - Database user (default: postgres)
#   OPENROUTER_API_KEY - Required for embedding backfill
#
# ============================================================================

set -euo pipefail

export PGHOST="${PGHOST:-localhost}"
export PGDATABASE="${PGDATABASE:-dash}"
export PGUSER="${PGUSER:-postgres}"

BATCH_SIZE="${BATCH_SIZE:-5000}"
MONTHS_AHEAD="${MONTHS_AHEAD:-6}"
MCP_BIN="/dash/.claude/mcp/dashmcp"
LOG_PREFIX="[$(date '+%Y-%m-%d %H:%M:%S')]"

log() { echo "${LOG_PREFIX} $*"; }
err() { echo "${LOG_PREFIX} ERROR: $*" >&2; }

PSQL="psql -v ON_ERROR_STOP=1 -q"

# ============================================================================
# 1. Partition maintenance
# ============================================================================
log "=== PARTITIONS ==="
log "Creating future partitions (${MONTHS_AHEAD} months)..."
$PSQL -c "SELECT table_name, partition_name FROM ensure_future_partitions(${MONTHS_AHEAD});" 2>&1 || err "partition creation failed"

log "Relocating default partition data..."
$PSQL -c "CALL relocate_edge_events_default(${BATCH_SIZE});" 2>&1 || err "edge_events relocation failed"
$PSQL -c "CALL relocate_observations_default(${BATCH_SIZE});" 2>&1 || err "observations relocation failed"

# Report
$PSQL -c "SELECT 'edge_events_default' as tbl, COUNT(*) FROM edge_events_default UNION ALL SELECT 'observations_default', COUNT(*) FROM observations_default;"

# ============================================================================
# 2. Garbage collection (via MCP)
# ============================================================================
log "=== GC ==="
if [ -x "$MCP_BIN" ]; then
    echo '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"gc","arguments":{"session_retention_days":14,"compressed_retention_days":30}}}' \
        | timeout 30 "$MCP_BIN" 2>/dev/null | python3 -c "
import sys, json
for line in sys.stdin:
    try:
        r = json.loads(line)
        if 'result' in r:
            d = r['result'].get('content',[{}])[0].get('text','{}')
            data = json.loads(d)
            print(f'  expired_sessions: {data.get(\"expired_sessions\",0)}')
            print(f'  expired_compressed: {data.get(\"expired_compressed\",0)}')
            print(f'  total_soft_deleted: {data.get(\"total_soft_deleted\",0)}')
    except: pass
" 2>/dev/null || log "GC: no sessions expired"
else
    err "MCP binary not found at $MCP_BIN"
fi

# ============================================================================
# 3. Embedding backfill (via MCP)
# ============================================================================
log "=== EMBEDDINGS ==="
if [ -x "$MCP_BIN" ] && [ -n "${OPENROUTER_API_KEY:-}" ]; then
    echo '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"embed","arguments":{"op":"backfill","limit":20}}}' \
        | timeout 60 "$MCP_BIN" 2>/dev/null | python3 -c "
import sys, json
for line in sys.stdin:
    try:
        r = json.loads(line)
        if 'result' in r:
            d = r['result'].get('content',[{}])[0].get('text','{}')
            data = json.loads(d)
            print(f'  processed: {data.get(\"count\",0)} files')
            print(f'  errors: {len(data.get(\"errors\",[]))}')
    except: pass
" 2>/dev/null || log "Embeddings: nothing to backfill"
else
    log "Embeddings: skipped (no MCP binary or OPENROUTER_API_KEY)"
fi

# ============================================================================
# 4. Pattern detection (via MCP)
# ============================================================================
log "=== PATTERNS ==="
if [ -x "$MCP_BIN" ]; then
    echo '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"patterns","arguments":{"type":"co-editing","min_count":2,"store":true}}}' \
        | timeout 30 "$MCP_BIN" 2>/dev/null | python3 -c "
import sys, json
for line in sys.stdin:
    try:
        r = json.loads(line)
        if 'result' in r:
            d = r['result'].get('content',[{}])[0].get('text','{}')
            data = json.loads(d)
            co = data.get('co_editing',[])
            print(f'  co-editing patterns: {len(co)}')
            if data.get('stored'): print('  stored to graph')
    except: pass
" 2>/dev/null || log "Patterns: none detected"
else
    err "MCP binary not found"
fi

# ============================================================================
# 5. Health summary
# ============================================================================
log "=== HEALTH ==="
$PSQL -t -c "
SELECT json_build_object(
    'nodes', (SELECT COUNT(*) FROM nodes WHERE deleted_at IS NULL),
    'edges', (SELECT COUNT(*) FROM edges WHERE deprecated_at IS NULL),
    'sessions_active', (SELECT COUNT(*) FROM nodes WHERE layer='CONTEXT' AND type='session' AND data->>'status'='active' AND deleted_at IS NULL),
    'observations_30d', (SELECT COUNT(*) FROM observations WHERE observed_at > NOW() - INTERVAL '30 days'),
    'files_with_embedding', (SELECT COUNT(*) FROM nodes WHERE layer='SYSTEM' AND type='file' AND embedding IS NOT NULL AND deleted_at IS NULL)
);" 2>/dev/null | python3 -c "
import sys, json
for line in sys.stdin:
    line = line.strip()
    if line:
        try:
            d = json.loads(line)
            for k,v in d.items(): print(f'  {k}: {v}')
        except: print(line)
" 2>/dev/null

log "=== DONE ==="
