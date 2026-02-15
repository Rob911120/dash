-- Migration 019: WorkOrder support
-- Adds relation types for work order lifecycle

ALTER TYPE dash_relation ADD VALUE IF NOT EXISTS 'assigned_to';  -- work_order → agent
ALTER TYPE dash_relation ADD VALUE IF NOT EXISTS 'produces';     -- work_order → file/commit
ALTER TYPE dash_relation ADD VALUE IF NOT EXISTS 'scoped_to';    -- work_order → file (scope boundary)
