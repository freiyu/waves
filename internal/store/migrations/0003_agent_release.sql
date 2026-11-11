-- Migration 0003: an agent may give its task back (waves_release_task).
-- The execution ends as Cancelled by the agent; the task returns to Ready only
-- when the workspace is unchanged since the start commit (checked in code),
-- otherwise it goes to Blocked with reason released_with_changes.
-- A cancelled execution request also lets the agent stop on its own.
INSERT INTO allowed_transitions(entity_type, from_state, to_state, actor_scope, approval_category) VALUES
('execution','Running','Cancelled','agent',NULL),
('execution','Waiting Approval','Cancelled','agent',NULL),
('task','In Progress','Ready','system',NULL);

-- Set when the human interrupts; the next heartbeat tells the agent to stop.
ALTER TABLE executions ADD COLUMN cancel_requested INTEGER NOT NULL DEFAULT 0 CHECK (cancel_requested IN (0,1));
