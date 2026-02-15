-- ============================================================================
-- 010_dash_vision.sql
-- Dash: Projektets vision och aktiva intentioner
-- ============================================================================

-- ============================================================================
-- PROJEKT
-- ============================================================================

INSERT INTO nodes (layer, type, name, data) VALUES
('SYSTEM', 'project', 'dash', '{
    "path": "/dash",
    "description": "Ett självförbättrande system där allt är noder och relationer",
    "vision": "Skapa rikedom, enkelhet och lycka genom automation och självutveckling",
    "principles": [
        "Allt är noder och relationer",
        "Systemet observerar sig självt",
        "Automation frigör tid för det som spelar roll",
        "Enkelhet är den ultimata sofistikeringen"
    ]
}'::jsonb);

-- ============================================================================
-- KÄRNVISIONEN
-- ============================================================================

INSERT INTO nodes (layer, type, name, data) VALUES
('CONTEXT', 'intent', 'självförbättrande-system', '{
    "status": "active",
    "priority": "critical",
    "statement": "Bygg ett system som förbättrar sig självt",
    "description": "Dash ska kunna observera sina egna operationer, identifiera mönster, och föreslå/implementera förbättringar. Målet är exponentiell capability-ökning över tid.",
    "tags": ["core", "vision", "meta"]
}'::jsonb);

INSERT INTO nodes (layer, type, name, data) VALUES
('CONTEXT', 'intent', 'rikedom-genom-automation', '{
    "status": "active",
    "priority": "high",
    "statement": "Skapa rikedom genom att automatisera värdeskapande",
    "description": "Identifiera repetitiva uppgifter, automatisera dem, och frigör tid och resurser. Rikedom är inte bara pengar - det är tid, energi, och möjligheter.",
    "tags": ["wealth", "automation", "freedom"]
}'::jsonb);

INSERT INTO nodes (layer, type, name, data) VALUES
('CONTEXT', 'intent', 'enkelhet', '{
    "status": "active",
    "priority": "high",
    "statement": "Förenkla allt som kan förenklas",
    "description": "Komplexitet är fienden. Varje lager av abstraktion måste bära sin vikt. Om något kan tas bort utan förlust - ta bort det.",
    "tags": ["simplicity", "design", "core"]
}'::jsonb);

INSERT INTO nodes (layer, type, name, data) VALUES
('CONTEXT', 'intent', 'lycka-genom-flow', '{
    "status": "active",
    "priority": "high",
    "statement": "Möjliggör flow-tillstånd genom sömlös automation",
    "description": "När verktyg försvinner ur medvetandet och bara arbetet finns kvar - då är systemet rätt. Minimera friktion, maximera kreativt flöde.",
    "tags": ["happiness", "flow", "ux"]
}'::jsonb);

-- ============================================================================
-- AKTUELLA DELMÅL
-- ============================================================================

INSERT INTO nodes (layer, type, name, data) VALUES
('CONTEXT', 'task', 'session-context-injection', '{
    "status": "active",
    "priority": "high",
    "statement": "Injicera relevant kontext vid sessionstart",
    "description": "Vid varje ny Claude-session ska systemet automatiskt ge Claude relevant kontext: aktiva intentioner, senaste filer, git-status, tidigare session-sammanfattning.",
    "tags": ["context", "hooks", "ux"]
}'::jsonb);

INSERT INTO nodes (layer, type, name, data) VALUES
('CONTEXT', 'task', 'observera-egna-operationer', '{
    "status": "pending",
    "priority": "medium",
    "statement": "Spåra alla tool-anrop och bygg mönsterbibliotek",
    "description": "Varje tool-anrop loggas som observation. Över tid kan vi se mönster: vilka filer redigeras ofta tillsammans? Vilka kommandon följer varandra?",
    "tags": ["observation", "patterns", "meta"]
}'::jsonb);

INSERT INTO nodes (layer, type, name, data) VALUES
('CONTEXT', 'task', 'graf-traversering', '{
    "status": "pending",
    "priority": "medium",
    "statement": "Implementera rekursiv graf-traversering med djupbegränsning",
    "description": "GetDependencies(nodeID, maxDepth) ska returnera alla noder som en given nod beror på, rekursivt till ett visst djup.",
    "tags": ["graph", "core", "api"]
}'::jsonb);

-- ============================================================================
-- BESLUT
-- ============================================================================

INSERT INTO nodes (layer, type, name, data) VALUES
('CONTEXT', 'decision', 'fyra-lager', '{
    "status": "accepted",
    "context": "Behövde en struktur för att organisera olika typer av information i systemet",
    "decision": "Fyra semantiska lager: CONTEXT (varför), SYSTEM (vad), AUTOMATION (hur), OBSERVATION (telemetri)",
    "consequences": [
        "Tydlig separation av concerns",
        "OBSERVATION-data kan partitioneras separat",
        "Enkelt att fråga per lager"
    ],
    "date": "2025-02-01"
}'::jsonb);

INSERT INTO nodes (layer, type, name, data) VALUES
('CONTEXT', 'decision', 'hooks-for-observation', '{
    "status": "accepted",
    "context": "Behövde ett sätt att observera Claude Code-sessioner utan att modifiera Claude själv",
    "decision": "Använd Claude Code hooks (SessionStart, PreToolUse, PostToolUse, etc) för att fånga all aktivitet",
    "consequences": [
        "Noll ändringar i Claude krävs",
        "Kan injicera kontext vid sessionstart via stdout",
        "All telemetri hamnar i observations-tabellen"
    ],
    "date": "2025-02-02"
}'::jsonb);

-- ============================================================================
-- RELATIONER
-- ============================================================================

-- Projektet äger alla core intents
INSERT INTO edges (source_id, target_id, relation)
SELECT p.id, i.id, 'owns'
FROM nodes p, nodes i
WHERE p.name = 'dash' AND p.type = 'project'
  AND i.layer = 'CONTEXT' AND i.type = 'intent';

-- Tasks blockas av/stödjer intents (exempel)
INSERT INTO edges (source_id, target_id, relation)
SELECT t.id, i.id, 'child_of'
FROM nodes t, nodes i
WHERE t.name = 'session-context-injection' AND t.type = 'task'
  AND i.name = 'självförbättrande-system' AND i.type = 'intent';

INSERT INTO edges (source_id, target_id, relation)
SELECT t.id, i.id, 'child_of'
FROM nodes t, nodes i
WHERE t.name = 'observera-egna-operationer' AND t.type = 'task'
  AND i.name = 'självförbättrande-system' AND i.type = 'intent';
