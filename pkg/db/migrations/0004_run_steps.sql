-- The rendered prompt is stored so a user can see what the model saw, rather
-- than what they think they wrote.
ALTER TABLE runs ADD COLUMN IF NOT EXISTS rendered_prompt TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS run_steps (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    run_id UUID NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    step_index INTEGER NOT NULL,
    -- 'model_call' or 'tool_call'.
    kind TEXT NOT NULL,
    -- Set on model_call steps: what the model said.
    content TEXT NOT NULL DEFAULT '',
    -- Set on tool_call steps: which tool, with what arguments, and what came
    -- back. A step that failed records why without ending the run.
    tool_name TEXT NOT NULL DEFAULT '',
    arguments TEXT NOT NULL DEFAULT '',
    result TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_run_steps_order ON run_steps (run_id, step_index);
