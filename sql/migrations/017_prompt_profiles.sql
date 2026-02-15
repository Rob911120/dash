-- 017_prompt_profiles.sql
-- Agent prompt profiles as first-class DB objects.
-- Code owns mechanics (sources, rendering). DB owns policy (system_prompt, sources, toolset).

CREATE TABLE IF NOT EXISTS prompt_profiles (
    name            TEXT PRIMARY KEY,
    description     TEXT,
    system_prompt   TEXT NOT NULL DEFAULT '',
    toolset         TEXT[] DEFAULT '{}',
    sources         TEXT[] DEFAULT '{}',
    source_config   JSONB DEFAULT '{}',
    active          BOOLEAN DEFAULT true,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);

-- Seed profiles (replaces hardcoded pipelines)

INSERT INTO prompt_profiles (name, description, system_prompt, toolset, sources, source_config) VALUES
(
    'default',
    'Full session context for Claude Code hooks',
    '',
    '{}',
    '{header,mission,now,tasks,constraints,insights,files,context_pack,suggestions,promote,session}',
    '{}'
),
(
    'compact',
    'Minimal context for TUI chat',
    '',
    '{}',
    '{mission,tasks,constraints,files}',
    '{"tasks":{"max_items":5,"format":"compact"}}'
),
(
    'planner',
    'Broad context for planning agents',
    '',
    '{search,query,tasks,plan,plan_review}',
    '{mission,now,tasks,constraints,insights}',
    '{}'
),
(
    'task',
    'Task-scoped reasoning context',
    '',
    '{search,query,node,link,remember}',
    '{mission,task_detail,sibling_tasks,context_pack,insights,decisions,constraints}',
    '{"insights":{"max_items":5},"decisions":{"max_items":3}}'
),
(
    'suggestion',
    'Suggestion evaluation context',
    E'Du utvärderar ett förslag från systemets analysmotor.\n\nUnderlaget nedan är redan verifierat — du behöver inte bekräfta det med queries.\n\nBedöm:\n1. Är detta värt att göra? Tjänar det missionen?\n2. Överlappar det med befintliga tasks?\n3. Är insatsen värd resultatet?\n\nVar ärlig. Säg vad du tycker rakt ut. Kör inte queries för att verifiera saker som redan står i kontexten.',
    '{search,query}',
    '{mission,suggestion_detail,sibling_tasks,context_pack,insights,decisions,constraints}',
    '{"insights":{"max_items":5},"decisions":{"max_items":3}}'
),
(
    'execution',
    'Plan execution context',
    E'Du implementerar en godkänd plan steg för steg.\nRegler:\n1. Följ stegen i ordning\n2. Använd write/edit för ändringar\n3. Verifiera kompilering efter varje steg (go build)\n4. Kör teststrategin efter alla steg\n5. Rapportera status tydligt',
    '{read,write,edit,exec,grep,glob}',
    '{plan_execution,constraints,insights}',
    '{"insights":{"max_items":3}}'
)
ON CONFLICT (name) DO NOTHING;
