#!/bin/bash
# ============================================================================
# partition_maintenance.sh
# Dash: Partition maintenance script for cron
# ============================================================================
#
# Usage: Run daily via cron
#   0 2 * * * /path/to/partition_maintenance.sh >> /var/log/dash_maintenance.log 2>&1
#
# Environment variables:
#   PGHOST     - PostgreSQL host (default: localhost)
#   PGPORT     - PostgreSQL port (default: 5432)
#   PGDATABASE - Database name (default: dash)
#   PGUSER     - Database user
#   PGPASSWORD - Database password (or use .pgpass)
#
# ============================================================================

set -euo pipefail

# Configuration
BATCH_SIZE="${BATCH_SIZE:-5000}"
MONTHS_AHEAD="${MONTHS_AHEAD:-6}"
LOG_PREFIX="[$(date '+%Y-%m-%d %H:%M:%S')]"

# Database connection (uses standard PG* environment variables)
PSQL_CMD="psql -v ON_ERROR_STOP=1 -q"

log_info() {
    echo "${LOG_PREFIX} INFO: $*"
}

log_error() {
    echo "${LOG_PREFIX} ERROR: $*" >&2
}

# ============================================================================
# Step 1: Ensure future partitions exist
# ============================================================================
log_info "Creating future partitions (${MONTHS_AHEAD} months ahead)..."

$PSQL_CMD <<EOF
SELECT table_name, partition_name
FROM ensure_future_partitions(${MONTHS_AHEAD});
EOF

if [ $? -eq 0 ]; then
    log_info "Future partitions created successfully"
else
    log_error "Failed to create future partitions"
    exit 1
fi

# ============================================================================
# Step 2: Relocate data from default partitions
# ============================================================================
log_info "Relocating edge_events from default partition..."

$PSQL_CMD <<EOF
CALL relocate_edge_events_default(${BATCH_SIZE});
EOF

if [ $? -eq 0 ]; then
    log_info "edge_events relocation completed"
else
    log_error "Failed to relocate edge_events"
    exit 1
fi

log_info "Relocating observations from default partition..."

$PSQL_CMD <<EOF
CALL relocate_observations_default(${BATCH_SIZE});
EOF

if [ $? -eq 0 ]; then
    log_info "observations relocation completed"
else
    log_error "Failed to relocate observations"
    exit 1
fi

# ============================================================================
# Step 3: Report default partition status
# ============================================================================
log_info "Checking default partition sizes..."

$PSQL_CMD <<EOF
SELECT 'edge_events_default' AS partition, COUNT(*) AS row_count
FROM edge_events_default
UNION ALL
SELECT 'observations_default' AS partition, COUNT(*) AS row_count
FROM observations_default;
EOF

# ============================================================================
# Step 4: Report overall statistics
# ============================================================================
log_info "Partition statistics:"

$PSQL_CMD <<EOF
SELECT
    schemaname,
    tablename,
    pg_size_pretty(pg_total_relation_size(schemaname || '.' || tablename)) AS total_size
FROM pg_tables
WHERE tablename LIKE 'edge_events_%'
   OR tablename LIKE 'observations_%'
ORDER BY tablename;
EOF

log_info "Maintenance completed successfully"
