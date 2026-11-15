-- Migration 0005: the harness waits for the human, project forks, the graph arrangement.
--
-- A wait is a record beside the execution, not a state: the execution stays Running, so the
-- state machine and its transition table do not change. At most one wait is open per execution.
-- Nothing here grants a power: a wait does not release the lease, the workspace lock or the
-- concurrency slot, and the lease still ends when the longest wait the settings allow has passed.
CREATE TABLE execution_waits (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  execution_id TEXT NOT NULL REFERENCES executions(id),
  since        TEXT NOT NULL,
  source       TEXT NOT NULL CHECK (source IN ('hook','agent')),
  ended_at     TEXT,
  ended_by     TEXT CHECK (ended_by IN ('resumed','expired','state_changed')),
  CHECK ((ended_at IS NULL) = (ended_by IS NULL))
);
CREATE UNIQUE INDEX execution_waits_one_open ON execution_waits(execution_id) WHERE ended_at IS NULL;

-- A project registered as a fork of another registered project (a label, Waves never creates forks).
ALTER TABLE projects ADD COLUMN fork_of TEXT REFERENCES projects(id);
CREATE TRIGGER projects_fork_not_self_insert BEFORE INSERT ON projects
  WHEN NEW.fork_of IS NOT NULL AND NEW.fork_of = NEW.id
  BEGIN SELECT RAISE(ABORT, 'a project cannot be a fork of itself'); END;
CREATE TRIGGER projects_fork_not_self_update BEFORE UPDATE OF fork_of ON projects
  WHEN NEW.fork_of IS NOT NULL AND NEW.fork_of = NEW.id
  BEGIN SELECT RAISE(ABORT, 'a project cannot be a fork of itself'); END;

-- Where the human put nodes and which side of a node each line uses. One arrangement, validated in code.
CREATE TABLE graph_layout (
  id          INTEGER PRIMARY KEY CHECK (id = 1),
  layout_json TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);
