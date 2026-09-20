CREATE TABLE IF NOT EXISTS secrets (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- The name an agent refers to, for example 'default' or 'anthropic'.
    name TEXT NOT NULL,
    -- Envelope encryption: the value is sealed with a per-row data key, and the
    -- data key is itself sealed with the master key from the environment.
    -- Moving to a KMS later replaces one component rather than the schema.
    encrypted_value BYTEA NOT NULL,
    encrypted_data_key BYTEA NOT NULL,
    -- The last few characters, so the dashboard can show which key is stored
    -- without ever returning it.
    masked_suffix TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, name)
);

CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions (expires_at);

-- Which stored credential an agent's model calls use.
ALTER TABLE agents ADD COLUMN IF NOT EXISTS secret_name TEXT NOT NULL DEFAULT 'default';
