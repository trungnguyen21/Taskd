-- The inherited tasks table is superseded by runs. Every task there was a single
-- instant retired once dispatched, which cannot express recurrence.
DROP TABLE IF EXISTS tasks;
