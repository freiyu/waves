-- Migration 0001: initial schema.
-- The service opens the database with foreign_keys=ON, WAL and a busy timeout, and refuses to
-- start if foreign_keys reads 0. One writer connection, short transactions.
-- Timestamps: UTC, RFC 3339 text. Ids: text (prefix + random), never reused.
-- Scopes in actor columns: system, agent, guard, operator, human.

CREATE TABLE schema_migrations (
  version     INTEGER PRIMARY KEY,
  name        TEXT NOT NULL,
  checksum    TEXT NOT NULL,
  applied_at  TEXT NOT NULL
);

CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL            -- instance_id, created_at, binary version at creation
);

-- ---------------------------------------------------------------- projects
CREATE TABLE projects (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL,
  path         TEXT NOT NULL UNIQUE,      -- absolute, symlinks resolved at registration
  is_git       INTEGER NOT NULL CHECK (is_git IN (0,1)),
  base_branch  TEXT,
  is_active    INTEGER NOT NULL DEFAULT 0 CHECK (is_active IN (0,1)),   -- Active Context
  created_at   TEXT NOT NULL,
  removed_at   TEXT                        -- removal never deletes project files
);
CREATE UNIQUE INDEX one_active_context ON projects(is_active) WHERE is_active = 1;

CREATE TABLE project_settings (
  project_id          TEXT PRIMARY KEY REFERENCES projects(id),
  guard_policy        TEXT NOT NULL DEFAULT 'audit' CHECK (guard_policy IN ('off','audit','enforce')),
  unmanaged_sessions  TEXT NOT NULL DEFAULT 'allow' CHECK (unmanaged_sessions IN ('allow','deny')),
  approvals_json      TEXT NOT NULL DEFAULT '{}',   -- per category policy; phase_start/phase_completion cannot be disabled
  validator_defaults  TEXT NOT NULL DEFAULT '{}',
  prompt_template     TEXT,
  context_budget      INTEGER NOT NULL DEFAULT 24000,
  updated_at          TEXT NOT NULL
);

CREATE TABLE settings (          -- global; security-sensitive keys need human scope
  key         TEXT PRIMARY KEY,
  value_json  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);

-- ----------------------------------------------------------- requirements, planning
CREATE TABLE requirements (
  id            TEXT PRIMARY KEY,
  project_id    TEXT NOT NULL REFERENCES projects(id),
  kind          TEXT NOT NULL CHECK (kind IN ('file','inline')),
  path          TEXT,                       -- file kind: referenced, never overwritten
  content_hash  TEXT,                       -- file kind: sha256 at registration
  body          TEXT,                       -- inline kind
  created_at    TEXT NOT NULL,
  CHECK ((kind='file' AND path IS NOT NULL AND content_hash IS NOT NULL) OR (kind='inline' AND body IS NOT NULL))
);

CREATE TABLE planning_requests (
  id          TEXT PRIMARY KEY,
  project_id  TEXT NOT NULL REFERENCES projects(id),
  state       TEXT NOT NULL CHECK (state IN ('open','submitted','cancelled','expired')),
  body_json   TEXT NOT NULL,                -- requirements refs + schema version + instructions
  created_at  TEXT NOT NULL
);

CREATE TABLE proposals (                    -- immutable
  id                   TEXT PRIMARY KEY,
  planning_request_id  TEXT NOT NULL REFERENCES planning_requests(id),
  project_id           TEXT NOT NULL REFERENCES projects(id),
  schema_version       INTEGER NOT NULL,
  raw_json             TEXT NOT NULL,       -- exactly as submitted
  normalized_json      TEXT,                -- null when invalid
  is_valid             INTEGER NOT NULL CHECK (is_valid IN (0,1)),
  errors_json          TEXT NOT NULL DEFAULT '[]',
  submitted_via        TEXT NOT NULL,       -- lease/session reference, not a secret
  created_at           TEXT NOT NULL
);

-- ----------------------------------------------------------- workflow
CREATE TABLE workflows (
  id          TEXT PRIMARY KEY,
  project_id  TEXT NOT NULL REFERENCES projects(id),
  created_at  TEXT NOT NULL
);

CREATE TABLE workflow_versions (
  id            TEXT PRIMARY KEY,
  workflow_id   TEXT NOT NULL REFERENCES workflows(id),
  version_no    INTEGER NOT NULL,
  proposal_id   TEXT REFERENCES proposals(id),
  state         TEXT NOT NULL CHECK (state IN ('draft','awaiting_approval','approved','rejected','superseded')),
  content_hash  TEXT NOT NULL,              -- hash of phases, tasks and validator commands
  stale         INTEGER NOT NULL DEFAULT 0 CHECK (stale IN (0,1)),   -- a source requirement changed
  created_at    TEXT NOT NULL,
  UNIQUE (workflow_id, version_no)
);
CREATE UNIQUE INDEX one_approved_version ON workflow_versions(workflow_id) WHERE state = 'approved';

CREATE TABLE phases (
  id                   TEXT PRIMARY KEY,
  workflow_version_id  TEXT NOT NULL REFERENCES workflow_versions(id),
  key                  TEXT NOT NULL,
  position             INTEGER NOT NULL,
  title                TEXT NOT NULL,
  objective            TEXT NOT NULL,
  deliverables_json    TEXT NOT NULL DEFAULT '[]',
  state                TEXT NOT NULL DEFAULT 'Planned' CHECK (state IN
    ('Planned','Awaiting Approval','Approved','Ready for Execution','In Progress','Validation',
     'Awaiting Phase Review','Completed','Blocked','Failed','Revision Required')),
  updated_at           TEXT NOT NULL,
  UNIQUE (workflow_version_id, key),
  UNIQUE (workflow_version_id, position)
);

CREATE TABLE tasks (
  id                TEXT PRIMARY KEY,
  phase_id          TEXT NOT NULL REFERENCES phases(id),
  parent_task_id    TEXT REFERENCES tasks(id),       -- subtask when set
  key               TEXT NOT NULL,
  position          INTEGER NOT NULL,
  title             TEXT NOT NULL,
  description       TEXT NOT NULL DEFAULT '',
  priority          TEXT NOT NULL DEFAULT 'normal' CHECK (priority IN ('low','normal','high')),
  workspace_mode    TEXT NOT NULL DEFAULT 'code_change' CHECK (workspace_mode IN ('read_only','code_change')),
  acceptance_json   TEXT NOT NULL DEFAULT '[]',
  suggested_harness TEXT,
  state             TEXT NOT NULL DEFAULT 'Backlog' CHECK (state IN
    ('Backlog','Ready','In Progress','Blocked','Waiting Approval','In Review','Done','Cancelled')),
  blocked_reason    TEXT,                             -- e.g. recovery_required, blocker text
  updated_at        TEXT NOT NULL,
  UNIQUE (phase_id, key)
);

CREATE TABLE task_dependencies (
  task_id             TEXT NOT NULL REFERENCES tasks(id),
  depends_on_task_id  TEXT NOT NULL REFERENCES tasks(id),
  PRIMARY KEY (task_id, depends_on_task_id),
  CHECK (task_id <> depends_on_task_id)
);

CREATE TABLE validators (
  id            TEXT PRIMARY KEY,
  phase_id      TEXT NOT NULL REFERENCES phases(id),
  type          TEXT NOT NULL CHECK (type IN ('command','checklist')),
  name          TEXT NOT NULL,
  command       TEXT,                    -- command type: run only from an approved workflow version
  timeout_s     INTEGER,
  items_json    TEXT,                    -- checklist type
  position      INTEGER NOT NULL,
  CHECK ((type='command' AND command IS NOT NULL AND timeout_s IS NOT NULL) OR (type='checklist' AND items_json IS NOT NULL))
);

CREATE TABLE validation_runs (
  id            TEXT PRIMARY KEY,
  validator_id  TEXT NOT NULL REFERENCES validators(id),
  phase_id      TEXT NOT NULL REFERENCES phases(id),
  status        TEXT NOT NULL CHECK (status IN ('pending','pass','fail','timeout','error','confirmed','unconfirmed')),
  exit_code     INTEGER,
  output_path   TEXT,                    -- artifact file, streamed, not held in memory
  started_at    TEXT,
  finished_at   TEXT
);

-- ----------------------------------------------------------- harnesses, workspaces
CREATE TABLE harnesses (
  id                 TEXT PRIMARY KEY,
  kind               TEXT NOT NULL UNIQUE,            -- 'claude-code'
  detected_version   TEXT,
  capabilities_json  TEXT NOT NULL DEFAULT '{}',      -- capability -> supported|partial|unsupported|unknown
  updated_at         TEXT NOT NULL
);

CREATE TABLE adapter_configs (                        -- per project connection
  project_id       TEXT NOT NULL REFERENCES projects(id),
  harness_id       TEXT NOT NULL REFERENCES harnesses(id),
  connected        INTEGER NOT NULL DEFAULT 0 CHECK (connected IN (0,1)),
  changes_json     TEXT NOT NULL DEFAULT '[]',         -- exact edits made to project files, for disconnect
  connected_at     TEXT,
  PRIMARY KEY (project_id, harness_id)
);

CREATE TABLE workspaces (
  id          TEXT PRIMARY KEY,
  project_id  TEXT NOT NULL REFERENCES projects(id),
  path        TEXT NOT NULL,
  mode        TEXT NOT NULL DEFAULT 'in_place' CHECK (mode IN ('in_place','worktree')),
  UNIQUE (project_id, path)
);

-- ----------------------------------------------------------- executions, leases
CREATE TABLE executions (
  id               TEXT PRIMARY KEY,
  task_id          TEXT NOT NULL REFERENCES tasks(id),
  project_id       TEXT NOT NULL REFERENCES projects(id),
  harness_id       TEXT NOT NULL REFERENCES harnesses(id),
  workspace_id     TEXT NOT NULL REFERENCES workspaces(id),
  mode             TEXT NOT NULL CHECK (mode IN ('harness_driven','waves_driven')),
  workspace_mode   TEXT NOT NULL CHECK (workspace_mode IN ('read_only','code_change')),
  state            TEXT NOT NULL DEFAULT 'Queued' CHECK (state IN
    ('Queued','Starting','Running','Waiting Approval','Paused','Completed','Failed','Interrupted','Disconnected','Cancelled')),
  session_id       TEXT,                    -- harness session, identifier only
  start_commit     TEXT,
  end_commit       TEXT,
  diff_path        TEXT,
  log_path         TEXT,
  result_json      TEXT,
  error            TEXT,
  created_at       TEXT NOT NULL,
  started_at       TEXT,
  ended_at         TEXT
);
-- Workspace lock: one code-changing execution that may still touch files per workspace.
CREATE UNIQUE INDEX workspace_lock ON executions(workspace_id)
  WHERE workspace_mode = 'code_change'
    AND state IN ('Starting','Running','Waiting Approval','Paused','Disconnected','Interrupted');

CREATE TABLE leases (
  id                 TEXT PRIMARY KEY,
  execution_id       TEXT NOT NULL REFERENCES executions(id),
  task_id            TEXT NOT NULL REFERENCES tasks(id),
  project_id         TEXT NOT NULL REFERENCES projects(id),
  token_hash         TEXT NOT NULL UNIQUE,            -- sha256 of the secret; the secret is shown once
  session_id         TEXT,                            -- bound from the tool_use_id join
  granted_at         TEXT NOT NULL,
  expires_at         TEXT NOT NULL,
  last_heartbeat_at  TEXT NOT NULL,
  revoked_at         TEXT,
  revoke_reason      TEXT CHECK (revoke_reason IN
    ('phase_exit','task_terminal','cancelled','released','expired','recovery','superseded'))
);
CREATE UNIQUE INDEX one_active_lease_per_task ON leases(task_id) WHERE revoked_at IS NULL;
CREATE INDEX lease_by_session ON leases(session_id) WHERE revoked_at IS NULL;

-- Join of the hook event and the MCP call that carry the same tool_use_id.
CREATE TABLE tool_use_bindings (
  tool_use_id  TEXT PRIMARY KEY,
  session_id   TEXT,                -- from the hook event
  lease_id     TEXT REFERENCES leases(id),   -- from the MCP call
  hook_seen_at TEXT,
  mcp_seen_at  TEXT
);

CREATE TABLE execution_events (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  execution_id  TEXT NOT NULL REFERENCES executions(id),
  ts            TEXT NOT NULL,
  kind          TEXT NOT NULL,
  payload_json  TEXT NOT NULL               -- summaries only; raw logs live in files
);
CREATE INDEX execution_events_by_exec ON execution_events(execution_id, id);

CREATE TABLE context_packages (
  id                TEXT PRIMARY KEY,
  execution_id      TEXT NOT NULL UNIQUE REFERENCES executions(id),
  template_version  TEXT NOT NULL,
  content_hash      TEXT NOT NULL,
  content_path      TEXT NOT NULL,          -- exact delivered context, stored for audit
  size_bytes        INTEGER NOT NULL
);

CREATE TABLE usage_records (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  execution_id  TEXT NOT NULL REFERENCES executions(id),
  input_tokens  INTEGER, output_tokens INTEGER, cost_usd REAL,
  recorded_at   TEXT NOT NULL
);

CREATE TABLE guard_decisions (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  ts           TEXT NOT NULL,
  project_id   TEXT REFERENCES projects(id),
  session_id   TEXT NOT NULL,
  lease_id     TEXT REFERENCES leases(id),
  tool_name    TEXT NOT NULL,
  tool_use_id  TEXT NOT NULL,
  target_path  TEXT,
  policy       TEXT NOT NULL CHECK (policy IN ('off','audit','enforce')),
  decision     TEXT NOT NULL CHECK (decision IN ('allow','deny')),
  would_deny   INTEGER NOT NULL CHECK (would_deny IN (0,1)),   -- audit policy: allowed but would have been denied
  reason       TEXT NOT NULL
);
CREATE INDEX guard_decisions_by_ts ON guard_decisions(ts);

-- ----------------------------------------------------------- approvals and transitions
CREATE TABLE approvals (
  id                  TEXT PRIMARY KEY,
  category            TEXT NOT NULL CHECK (category IN
    ('workflow','phase_start','phase_completion','task_execution','risky_action','result','merge')),
  subject_type        TEXT NOT NULL CHECK (subject_type IN ('workflow_version','phase','task','execution')),
  subject_id          TEXT NOT NULL,
  project_id          TEXT NOT NULL REFERENCES projects(id),
  state               TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','approved','rejected','cancelled')),
  requested_by        TEXT NOT NULL,             -- scope: system | agent | operator | human
  impact_json         TEXT NOT NULL DEFAULT '{}',
  requested_at        TEXT NOT NULL,
  decided_at          TEXT,
  decided_by_scope    TEXT CHECK (decided_by_scope IN ('operator','human')),
  stepup_method       TEXT,                      -- 'passphrase'
  elevation_id        TEXT,
  tty                 INTEGER CHECK (tty IN (0,1)),
  client              TEXT CHECK (client IN ('cli','dashboard')),
  source_addr         TEXT,
  feedback            TEXT,
  override_reason     TEXT,                      -- approve with failed validators
  CHECK (state = 'pending' OR decided_at IS NOT NULL),
  -- every approval (any category) needs human scope + passphrase step-up; operator can never approve
  CHECK (state <> 'approved'
         OR (decided_by_scope = 'human' AND stepup_method = 'passphrase' AND elevation_id IS NOT NULL)),
  -- rejecting a gate or a workflow is also a human decision
  CHECK (category NOT IN ('workflow','phase_start','phase_completion') OR state <> 'rejected' OR decided_by_scope = 'human')
);
CREATE INDEX approvals_pending ON approvals(state, project_id);

CREATE TABLE allowed_transitions (               -- legal moves; seeded below, changed only by a migration
  entity_type       TEXT NOT NULL CHECK (entity_type IN ('workflow_version','phase','task','execution')),
  from_state        TEXT NOT NULL,
  to_state          TEXT NOT NULL,
  actor_scope       TEXT NOT NULL CHECK (actor_scope IN ('system','agent','operator','human')),
  approval_category TEXT,                        -- when set, a decided approval of this category is required
  PRIMARY KEY (entity_type, from_state, to_state, actor_scope)
);

CREATE TABLE state_transitions (                 -- append-only
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  entity_type  TEXT NOT NULL,
  entity_id    TEXT NOT NULL,
  from_state   TEXT NOT NULL,
  to_state     TEXT NOT NULL,
  trigger      TEXT NOT NULL,
  actor_scope  TEXT NOT NULL,
  actor_ref    TEXT,
  approval_id  TEXT REFERENCES approvals(id),
  ts           TEXT NOT NULL
);
CREATE INDEX state_transitions_by_entity ON state_transitions(entity_type, entity_id, id);

-- ----------------------------------------------------------- audit chain
CREATE TABLE audit (
  seq          INTEGER PRIMARY KEY AUTOINCREMENT,
  ts           TEXT NOT NULL,
  event        TEXT NOT NULL,
  actor_scope  TEXT NOT NULL,
  actor_ref    TEXT,
  project_id   TEXT,
  subject      TEXT,
  detail_json  TEXT NOT NULL DEFAULT '{}',        -- never secrets
  prev_hash    TEXT NOT NULL UNIQUE,              -- UNIQUE: the chain cannot fork
  hash         TEXT NOT NULL UNIQUE               -- sha256(prev_hash || canonical record)
);

CREATE TABLE audit_checkpoints (                  -- HMAC under a key derived at human step-up, never stored
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  seq         INTEGER NOT NULL REFERENCES audit(seq),
  hash        TEXT NOT NULL,
  hmac        TEXT NOT NULL,
  created_at  TEXT NOT NULL
);

-- ----------------------------------------------------------- credentials and sessions
CREATE TABLE credentials (                        -- operator and guard tokens, hashes only
  id          TEXT PRIMARY KEY,
  scope       TEXT NOT NULL CHECK (scope IN ('operator','guard')),
  token_hash  TEXT NOT NULL UNIQUE,
  created_at  TEXT NOT NULL,
  revoked_at  TEXT
);

CREATE TABLE passphrase (
  id          INTEGER PRIMARY KEY CHECK (id = 1),
  hash        TEXT NOT NULL,                      -- argon2id, parameters embedded
  created_at  TEXT NOT NULL,
  rotated_at  TEXT
);

CREATE TABLE login_links (
  code_hash    TEXT PRIMARY KEY,
  created_at   TEXT NOT NULL,
  expires_at   TEXT NOT NULL,                     -- 60 seconds
  consumed_at  TEXT
);

CREATE TABLE sessions (
  id_hash     TEXT PRIMARY KEY,
  csrf_hash   TEXT NOT NULL,
  client      TEXT NOT NULL,
  created_at  TEXT NOT NULL,
  expires_at  TEXT NOT NULL,
  revoked_at  TEXT
);

CREATE TABLE elevations (                         -- scope 'human', short-lived, created by step-up only
  id           TEXT PRIMARY KEY,
  session_id   TEXT REFERENCES sessions(id_hash),
  kind         TEXT NOT NULL CHECK (kind IN ('window','single_use')),
  approval_id  TEXT REFERENCES approvals(id),     -- single_use is bound to one approval
  created_at   TEXT NOT NULL,
  expires_at   TEXT NOT NULL,
  consumed_at  TEXT,
  CHECK (kind = 'window' OR approval_id IS NOT NULL)
);

CREATE TABLE stepup_failures (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  ts          TEXT NOT NULL,
  session_id  TEXT,
  source      TEXT NOT NULL
);

-- =========================================================== integrity triggers
-- Immutable tables.
CREATE TRIGGER proposals_no_update BEFORE UPDATE ON proposals BEGIN SELECT RAISE(ABORT,'proposals are immutable'); END;
CREATE TRIGGER proposals_no_delete BEFORE DELETE ON proposals BEGIN SELECT RAISE(ABORT,'proposals are immutable'); END;
CREATE TRIGGER audit_no_update BEFORE UPDATE ON audit BEGIN SELECT RAISE(ABORT,'audit is append-only'); END;
CREATE TRIGGER audit_no_delete BEFORE DELETE ON audit BEGIN SELECT RAISE(ABORT,'audit is append-only'); END;
CREATE TRIGGER transitions_no_update BEFORE UPDATE ON state_transitions BEGIN SELECT RAISE(ABORT,'transitions are append-only'); END;
CREATE TRIGGER transitions_no_delete BEFORE DELETE ON state_transitions BEGIN SELECT RAISE(ABORT,'transitions are append-only'); END;
CREATE TRIGGER allowed_no_update BEFORE UPDATE ON allowed_transitions BEGIN SELECT RAISE(ABORT,'transition table changes only by migration'); END;
CREATE TRIGGER allowed_no_delete BEFORE DELETE ON allowed_transitions BEGIN SELECT RAISE(ABORT,'transition table changes only by migration'); END;
CREATE TRIGGER approvals_no_delete BEFORE DELETE ON approvals BEGIN SELECT RAISE(ABORT,'approvals are kept'); END;
-- A decided approval cannot change. Only pending -> approved|rejected|cancelled is allowed.
CREATE TRIGGER approvals_decided_immutable BEFORE UPDATE ON approvals
WHEN OLD.state <> 'pending' BEGIN SELECT RAISE(ABORT,'a decided approval cannot change'); END;
-- The audit chain must link to the previous record.
CREATE TRIGGER audit_chain_link BEFORE INSERT ON audit
WHEN NEW.prev_hash <> COALESCE((SELECT hash FROM audit ORDER BY seq DESC LIMIT 1), '0000000000000000000000000000000000000000000000000000000000000000')
BEGIN SELECT RAISE(ABORT,'audit chain link broken'); END;

-- A transition row is legal only if the (entity, from, to, actor) move exists, and, when the move
-- needs an approval, a decided approval of that category by human scope with step-up is attached.
CREATE TRIGGER transitions_must_be_allowed BEFORE INSERT ON state_transitions
BEGIN
  SELECT RAISE(ABORT,'illegal transition')
  WHERE NOT EXISTS (SELECT 1 FROM allowed_transitions a
    WHERE a.entity_type = NEW.entity_type AND a.from_state = NEW.from_state
      AND a.to_state = NEW.to_state AND a.actor_scope = NEW.actor_scope);
  SELECT RAISE(ABORT,'transition needs a decided human approval')
  WHERE EXISTS (SELECT 1 FROM allowed_transitions a
      WHERE a.entity_type = NEW.entity_type AND a.from_state = NEW.from_state
        AND a.to_state = NEW.to_state AND a.actor_scope = NEW.actor_scope AND a.approval_category IS NOT NULL)
    AND NOT EXISTS (SELECT 1 FROM approvals p
      WHERE p.id = NEW.approval_id AND p.state = 'approved' AND p.decided_by_scope = 'human'
        AND p.stepup_method = 'passphrase' AND p.subject_id = NEW.entity_id
        AND p.category = (SELECT a.approval_category FROM allowed_transitions a
          WHERE a.entity_type = NEW.entity_type AND a.from_state = NEW.from_state
            AND a.to_state = NEW.to_state AND a.actor_scope = NEW.actor_scope));
END;

-- A phase can resume work only if it was once approved to start. A start-gate rejection
-- (Awaiting Approval to Revision Required) must go back through Awaiting Approval and a new human approval.
CREATE TRIGGER phase_resume_needs_prior_approval BEFORE INSERT ON state_transitions
WHEN NEW.entity_type = 'phase' AND NEW.from_state = 'Revision Required' AND NEW.to_state = 'In Progress'
BEGIN
  SELECT RAISE(ABORT,'a phase that was never approved cannot start')
  WHERE NOT EXISTS (SELECT 1 FROM state_transitions t
    WHERE t.entity_type = 'phase' AND t.entity_id = NEW.entity_id AND t.to_state = 'Approved');
END;

-- State columns change only together with a matching newest transition row.
CREATE TRIGGER phases_state_needs_transition BEFORE UPDATE OF state ON phases WHEN NEW.state <> OLD.state
BEGIN SELECT RAISE(ABORT,'phase state change needs a transition record')
  WHERE NOT EXISTS (SELECT 1 FROM state_transitions t WHERE t.entity_type='phase' AND t.entity_id=NEW.id
    AND t.from_state=OLD.state AND t.to_state=NEW.state
    AND t.id = (SELECT max(id) FROM state_transitions WHERE entity_type='phase' AND entity_id=NEW.id)); END;
CREATE TRIGGER tasks_state_needs_transition BEFORE UPDATE OF state ON tasks WHEN NEW.state <> OLD.state
BEGIN SELECT RAISE(ABORT,'task state change needs a transition record')
  WHERE NOT EXISTS (SELECT 1 FROM state_transitions t WHERE t.entity_type='task' AND t.entity_id=NEW.id
    AND t.from_state=OLD.state AND t.to_state=NEW.state
    AND t.id = (SELECT max(id) FROM state_transitions WHERE entity_type='task' AND entity_id=NEW.id)); END;
CREATE TRIGGER executions_state_needs_transition BEFORE UPDATE OF state ON executions WHEN NEW.state <> OLD.state
BEGIN SELECT RAISE(ABORT,'execution state change needs a transition record')
  WHERE NOT EXISTS (SELECT 1 FROM state_transitions t WHERE t.entity_type='execution' AND t.entity_id=NEW.id
    AND t.from_state=OLD.state AND t.to_state=NEW.state
    AND t.id = (SELECT max(id) FROM state_transitions WHERE entity_type='execution' AND entity_id=NEW.id)); END;
CREATE TRIGGER wfversions_state_needs_transition BEFORE UPDATE OF state ON workflow_versions WHEN NEW.state <> OLD.state
BEGIN SELECT RAISE(ABORT,'workflow version state change needs a transition record')
  WHERE NOT EXISTS (SELECT 1 FROM state_transitions t WHERE t.entity_type='workflow_version' AND t.entity_id=NEW.id
    AND t.from_state=OLD.state AND t.to_state=NEW.state
    AND t.id = (SELECT max(id) FROM state_transitions WHERE entity_type='workflow_version' AND entity_id=NEW.id)); END;

-- Rows start in their initial state. Anything else needs a transition path.
CREATE TRIGGER phases_initial_state BEFORE INSERT ON phases WHEN NEW.state <> 'Planned'
BEGIN SELECT RAISE(ABORT,'a phase starts as Planned'); END;
CREATE TRIGGER tasks_initial_state BEFORE INSERT ON tasks WHEN NEW.state <> 'Backlog'
BEGIN SELECT RAISE(ABORT,'a task starts as Backlog'); END;
CREATE TRIGGER executions_initial_state BEFORE INSERT ON executions WHEN NEW.state <> 'Queued'
BEGIN SELECT RAISE(ABORT,'an execution starts as Queued'); END;

-- A lease can only be granted while the phase is In Progress or Ready for Execution (first lease moves it).
CREATE TRIGGER lease_needs_open_phase BEFORE INSERT ON leases
WHEN NOT EXISTS (SELECT 1 FROM tasks t JOIN phases p ON p.id = t.phase_id
                 WHERE t.id = NEW.task_id AND p.state IN ('Ready for Execution','In Progress'))
BEGIN SELECT RAISE(ABORT,'lease needs an approved, open phase'); END;

-- =========================================================== seed: allowed transitions
INSERT INTO allowed_transitions(entity_type, from_state, to_state, actor_scope, approval_category) VALUES
-- workflow_version
('workflow_version','draft','awaiting_approval','operator',NULL),
('workflow_version','draft','awaiting_approval','human',NULL),
('workflow_version','awaiting_approval','approved','human','workflow'),
('workflow_version','awaiting_approval','rejected','human',NULL),
('workflow_version','awaiting_approval','draft','operator',NULL),
('workflow_version','awaiting_approval','draft','human',NULL),
('workflow_version','approved','superseded','system',NULL),
-- phase (human-only gate moves carry an approval category)
('phase','Planned','Awaiting Approval','system',NULL),
('phase','Awaiting Approval','Approved','human','phase_start'),
('phase','Awaiting Approval','Revision Required','human',NULL),
('phase','Approved','Ready for Execution','system',NULL),
('phase','Ready for Execution','In Progress','system',NULL),
('phase','In Progress','Blocked','system',NULL),
('phase','In Progress','Blocked','agent',NULL),
('phase','Blocked','In Progress','system',NULL),
('phase','Blocked','In Progress','operator',NULL),
('phase','Blocked','In Progress','human',NULL),
('phase','In Progress','Validation','system',NULL),
('phase','In Progress','Failed','operator',NULL),
('phase','In Progress','Failed','human',NULL),
('phase','Validation','Awaiting Phase Review','system',NULL),
('phase','Validation','In Progress','system',NULL),
('phase','Validation','In Progress','operator',NULL),
('phase','Validation','In Progress','human',NULL),
('phase','Awaiting Phase Review','Completed','human','phase_completion'),
('phase','Awaiting Phase Review','Revision Required','human',NULL),
('phase','Revision Required','In Progress','system',NULL),
('phase','Revision Required','In Progress','operator',NULL),
('phase','Revision Required','In Progress','human',NULL),
('phase','Revision Required','Awaiting Approval','system',NULL),
-- task
('task','Backlog','Ready','system',NULL),
('task','Ready','In Progress','system',NULL),
('task','In Progress','Waiting Approval','agent',NULL),
('task','Waiting Approval','In Progress','human','risky_action'),
('task','In Progress','In Review','system',NULL),
('task','In Progress','Done','system',NULL),
('task','In Review','Done','human','result'),
('task','In Review','Ready','operator',NULL),
('task','In Review','Ready','human',NULL),
('task','In Progress','Blocked','system',NULL),
('task','In Progress','Blocked','agent',NULL),
('task','Blocked','Ready','operator',NULL),
('task','Blocked','Ready','human',NULL),
('task','Blocked','In Progress','operator',NULL),
('task','Blocked','In Progress','human',NULL),
('task','Backlog','Cancelled','operator',NULL),('task','Backlog','Cancelled','human',NULL),
('task','Ready','Cancelled','operator',NULL),('task','Ready','Cancelled','human',NULL),
('task','In Progress','Cancelled','operator',NULL),('task','In Progress','Cancelled','human',NULL),
('task','Waiting Approval','Cancelled','operator',NULL),('task','Waiting Approval','Cancelled','human',NULL),
('task','In Review','Cancelled','operator',NULL),('task','In Review','Cancelled','human',NULL),
('task','Blocked','Cancelled','operator',NULL),('task','Blocked','Cancelled','human',NULL),
-- execution
('execution','Queued','Starting','system',NULL),
('execution','Starting','Running','system',NULL),
('execution','Starting','Running','agent',NULL),
('execution','Running','Waiting Approval','agent',NULL),
('execution','Waiting Approval','Running','human','risky_action'),
('execution','Running','Paused','operator',NULL),
('execution','Running','Paused','human',NULL),
('execution','Paused','Running','operator',NULL),
('execution','Paused','Running','human',NULL),
('execution','Running','Completed','system',NULL),
('execution','Running','Failed','system',NULL),
('execution','Running','Disconnected','system',NULL),
('execution','Disconnected','Running','agent',NULL),
('execution','Running','Interrupted','system',NULL),
('execution','Starting','Interrupted','system',NULL),
('execution','Waiting Approval','Interrupted','system',NULL),
('execution','Paused','Interrupted','system',NULL),
('execution','Disconnected','Interrupted','system',NULL),
('execution','Interrupted','Running','operator',NULL),('execution','Interrupted','Running','human',NULL),
('execution','Disconnected','Running','operator',NULL),('execution','Disconnected','Running','human',NULL),
('execution','Interrupted','Failed','operator',NULL),('execution','Interrupted','Failed','human',NULL),
('execution','Disconnected','Failed','operator',NULL),('execution','Disconnected','Failed','human',NULL),
('execution','Interrupted','Completed','operator',NULL),('execution','Interrupted','Completed','human',NULL),
('execution','Disconnected','Completed','operator',NULL),('execution','Disconnected','Completed','human',NULL),
('execution','Queued','Cancelled','operator',NULL),('execution','Queued','Cancelled','human',NULL),
('execution','Starting','Cancelled','operator',NULL),('execution','Starting','Cancelled','human',NULL),
('execution','Running','Cancelled','operator',NULL),('execution','Running','Cancelled','human',NULL),
('execution','Waiting Approval','Cancelled','operator',NULL),('execution','Waiting Approval','Cancelled','human',NULL),
('execution','Paused','Cancelled','operator',NULL),('execution','Paused','Cancelled','human',NULL),
('execution','Interrupted','Cancelled','operator',NULL),('execution','Interrupted','Cancelled','human',NULL),
('execution','Disconnected','Cancelled','operator',NULL),('execution','Disconnected','Cancelled','human',NULL);
