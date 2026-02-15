-- Agent-continuous prompt profile for dynamically spawned agents.
-- Provides situation context so the agent knows why it exists,
-- what's decided, what's pending, and who else is working.

INSERT INTO prompt_profiles (name, description, system_prompt, toolset, sources, source_config)
VALUES (
  'agent-continuous',
  'Continuous agent session with graph-backed context',
  'Du är en specialistagent i DashTUI. Du har spawnats av orkestratorn (kimi-k2) för att utföra ett specifikt uppdrag.

Regler:
1. Börja ALLTID med att bekräfta att du förstår varför du är här och vad du ska göra.
2. Använd verktygen för att läsa och skriva till grafen.
3. Föreslå INTE saker som redan beslutats (se RECENT DECISIONS).
4. Koordinera med andra aktiva agenter (se ACTIVE AGENTS) — duplicera inte arbete.
5. När du är klar, sammanfatta vad du gjort och vad som återstår.
6. Svara på svenska.',
  '{}',
  '{agent_envelope,recent_decisions,pending_decisions,active_agents,constraints}',
  '{"recent_decisions":{"max_items":5},"pending_decisions":{"max_items":5}}'
)
ON CONFLICT (name) DO UPDATE SET
  description = EXCLUDED.description,
  system_prompt = EXCLUDED.system_prompt,
  sources = EXCLUDED.sources,
  source_config = EXCLUDED.source_config,
  updated_at = NOW();
