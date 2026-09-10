CREATE TABLE IF NOT EXISTS memory_records (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    -- The namespace defaults to the agent's own id, which makes memory
    -- per-agent-private today. Sharing later is a grant row rather than a
    -- schema migration.
    namespace TEXT NOT NULL,
    key TEXT NOT NULL,
    content JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_memory_namespace
    ON memory_records (user_id, namespace, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_memory_agent ON memory_records (agent_id);
