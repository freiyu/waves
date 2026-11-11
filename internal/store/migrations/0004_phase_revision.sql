-- Migration 0004: revision after a rejected phase review (PRD 7.6).
-- A human, with a passphrase step-up, sends finished tasks of the rejected
-- phase back to Ready; the phase returns to In Progress (rows exist since
-- 0001), the agent gets the review feedback, validators run again and a new
-- review is requested. No task is added, so the approved workflow is unchanged.
INSERT INTO allowed_transitions(entity_type, from_state, to_state, actor_scope, approval_category) VALUES
('task','Done','Ready','human',NULL);
