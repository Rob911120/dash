-- Add active_work_order source to agent-continuous profile.
-- This lets agents see their assigned work order in the system prompt.

UPDATE prompt_profiles
SET sources = '{agent_envelope,active_work_order,recent_decisions,pending_decisions,active_agents,constraints}',
    updated_at = NOW()
WHERE name = 'agent-continuous';
