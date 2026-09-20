-- Telegram delivery is per-user rather than per-installation. The bot token is
-- a credential and lives in the secrets table under a reserved name; the chat id
-- is not secret.
ALTER TABLE users ADD COLUMN IF NOT EXISTS telegram_chat_id TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS failure_alerts_enabled BOOLEAN NOT NULL DEFAULT TRUE;

-- The inbox. Every run's output is recorded whether or not the agent chose to
-- notify, so a confused agent that forgets to send still leaves something to
-- read, and Taskd is useful before a bot is configured.
ALTER TABLE runs ADD COLUMN IF NOT EXISTS read_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_runs_unread ON runs (user_id, created_at DESC)
    WHERE read_at IS NULL;

-- Consecutive failures are counted so the platform can report an agent that has
-- stopped working. A failing agent cannot report its own failure: if its model
-- credential is wrong, the agent is exactly the thing that cannot run.
ALTER TABLE agents ADD COLUMN IF NOT EXISTS consecutive_failures INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agents ADD COLUMN IF NOT EXISTS failure_notified_at TIMESTAMPTZ;
