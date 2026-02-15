-- Migration 016: Add lifecycle relation types
-- These support the promotion pipeline: ephemeral → canonical → execution

-- New stable relations for data lifecycle
ALTER TYPE dash_relation ADD VALUE IF NOT EXISTS 'implements';    -- task → intent/mission
ALTER TYPE dash_relation ADD VALUE IF NOT EXISTS 'affects';       -- task → file
ALTER TYPE dash_relation ADD VALUE IF NOT EXISTS 'derived_from';  -- insight → session
ALTER TYPE dash_relation ADD VALUE IF NOT EXISTS 'justifies';     -- decision → task
ALTER TYPE dash_relation ADD VALUE IF NOT EXISTS 'based_on';      -- decision → insight
ALTER TYPE dash_relation ADD VALUE IF NOT EXISTS 'points_to';     -- context_frame → task/summary
ALTER TYPE dash_relation ADD VALUE IF NOT EXISTS 'supersedes';    -- newer insight → older
