-- Migration 0002: project-scoped MCP credentials, one-shot window elevations,
-- MCP session binding on leases, subject hashes on approvals, checklist confirmations.

-- credentials gains the mcp_project scope, which is bound to one project.
CREATE TABLE credentials_new (
  id          TEXT PRIMARY KEY,
  scope       TEXT NOT NULL CHECK (scope IN ('operator','guard','mcp_project')),
  project_id  TEXT REFERENCES projects(id),
  token_hash  TEXT NOT NULL UNIQUE,
  created_at  TEXT NOT NULL,
  revoked_at  TEXT,
  CHECK ((scope = 'mcp_project') = (project_id IS NOT NULL))
);
INSERT INTO credentials_new(id, scope, token_hash, created_at, revoked_at)
  SELECT id, scope, token_hash, created_at, revoked_at FROM credentials;
DROP TABLE credentials;
ALTER TABLE credentials_new RENAME TO credentials;

-- A window elevation issued while the window setting is 0 works for one action only.
ALTER TABLE elevations ADD COLUMN one_shot INTEGER NOT NULL DEFAULT 0 CHECK (one_shot IN (0,1));

-- The MCP session that claimed the lease; every lease call must arrive on it.
ALTER TABLE leases ADD COLUMN mcp_session TEXT;

-- Hash of what the human sees; a decision with another hash fails as stale.
ALTER TABLE approvals ADD COLUMN subject_hash TEXT;

-- Confirmed checklist item indexes of a checklist validation run, as a JSON array.
ALTER TABLE validation_runs ADD COLUMN confirmed_json TEXT NOT NULL DEFAULT '[]';
