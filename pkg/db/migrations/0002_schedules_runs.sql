CREATE TABLE IF NOT EXISTS schedules (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    agent_id UUID NOT NULL UNIQUE REFERENCES agents (id) ON DELETE CASCADE,
    cron_expression TEXT NOT NULL,
    -- An IANA name, not an offset. "7am" is a wall-clock intent, and only the
    -- zone allows the next instant to be recomputed across a DST transition.
    timezone TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    next_fire_at TIMESTAMPTZ,
    last_fired_at TIMESTAMPTZ,
    -- The wall-clock form of the last fire, kept so that the hour repeated by
    -- the autumn DST transition cannot produce two runs of the same schedule.
    last_fired_wall TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_schedules_next_fire_at ON schedules (next_fire_at)
    WHERE enabled;

CREATE TABLE IF NOT EXISTS runs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    schedule_id UUID REFERENCES schedules (id) ON DELETE SET NULL,
    -- 'schedule' or 'manual'.
    trigger TEXT NOT NULL,
    -- pending, running, succeeded, failed, missed, cancelled, budget_exceeded.
    status TEXT NOT NULL,
    scheduled_for TIMESTAMPTZ NOT NULL,
    picked_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    lease_expires_at TIMESTAMPTZ,
    output TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    -- Recorded but not computed in v1: converting tokens to money needs a
    -- price table maintained by hand, which goes stale silently.
    cost NUMERIC,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_runs_agent_created ON runs (agent_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_runs_pending ON runs (scheduled_for) WHERE status = 'pending';
