CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- v1 is single-user. The owner row exists from the start so that every table can
-- carry an owner column without a signup flow.
INSERT INTO users (id, email)
VALUES ('00000000-0000-0000-0000-000000000001', 'owner@localhost')
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS agents (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL,
    base_url TEXT NOT NULL DEFAULT '',
    system_prompt TEXT NOT NULL DEFAULT '',
    user_prompt TEXT NOT NULL DEFAULT '',
    -- Tool grants. A capability is a grant, not a setting: an agent has memory
    -- if and only if the memory tools appear here.
    tools JSONB NOT NULL DEFAULT '[]'::jsonb,
    max_steps INTEGER NOT NULL DEFAULT 20,
    max_tokens INTEGER NOT NULL DEFAULT 100000,
    max_duration_seconds INTEGER NOT NULL DEFAULT 600,
    context_mode TEXT NOT NULL DEFAULT 'fresh',
    context_runs INTEGER NOT NULL DEFAULT 0,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_agents_user_id ON agents (user_id);
