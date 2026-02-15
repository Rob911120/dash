-- Migration: 015_embeddings.sql
-- Description: Add pgvector support for semantic search over file content
-- Used for: Embedding file content to enable semantic similarity search

-- Aktivera pgvector extension (kräver superuser eller CREATE på databas)
CREATE EXTENSION IF NOT EXISTS vector;

-- Lägg till embedding-kolumner på nodes-tabellen
-- 1536 dimensioner matchar OpenAI text-embedding-3-small
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS embedding vector(1536);
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS content_hash TEXT;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS embedding_at TIMESTAMPTZ;

-- Comment för dokumentation
COMMENT ON COLUMN nodes.embedding IS 'Vector embedding (1536 dims) for semantic search';
COMMENT ON COLUMN nodes.content_hash IS 'SHA256 hash of content used to generate embedding';
COMMENT ON COLUMN nodes.embedding_at IS 'Timestamp when embedding was last computed';

-- Index för likhetssökning med hnsw
-- hnsw ger bättre recall för små dataset (<1000 rader) och kräver ingen training
-- Vi använder cosine similarity (vanligast för text embeddings)
CREATE INDEX IF NOT EXISTS idx_nodes_embedding_cosine
ON nodes USING hnsw (embedding vector_cosine_ops)
WHERE embedding IS NOT NULL
  AND deleted_at IS NULL;

-- Index för att hitta filer som behöver embedding uppdaterad
-- (har content_hash men embedding är NULL eller gammal)
CREATE INDEX IF NOT EXISTS idx_nodes_needs_embedding
ON nodes (layer, type, content_hash)
WHERE layer = 'SYSTEM'
  AND type = 'file'
  AND content_hash IS NOT NULL
  AND embedding IS NULL
  AND deleted_at IS NULL;
