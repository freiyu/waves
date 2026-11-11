package core

import (
	"context"
	"database/sql"
	"errors"
)

// claudeHarnessID returns the id of the Claude Code harness row, creating it.
func (s *Service) claudeHarnessID(ctx context.Context, t *Tx) (string, error) {
	var id string
	err := t.QueryRowContext(ctx, `SELECT id FROM harnesses WHERE kind = 'claude-code'`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		id = NewID("hrn")
		_, err = t.ExecContext(ctx, `INSERT INTO harnesses(id, kind, updated_at) VALUES (?, 'claude-code', ?)`, id, s.ts())
	}
	return id, wrap(err, "harness row")
}
