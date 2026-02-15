-- Migration: 014_tool_use_id_index.sql
-- Description: Create index for tool_use_id lookup in observations
-- Used for: Calculating tool execution duration by matching pre/post events
--
-- Note: observations is partitioned, so we create the index on the parent table
-- without CONCURRENTLY. The index definition is inherited by all partitions.

-- Index on tool_use_id for fast lookup of PreToolUse events
-- This enables duration calculation by finding the pre-event timestamp
CREATE INDEX IF NOT EXISTS idx_observations_tool_use_id
ON observations((data->'claude_code'->>'tool_use_id'))
WHERE type = 'tool_event'
  AND data->'normalized'->>'event' = 'tool.pre';
