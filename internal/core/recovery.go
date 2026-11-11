package core

import (
	"context"

	"waves/internal/store"
)

// recover runs once at startup, before the API serves requests. Nothing runs
// on its own after a restart: every validator run that had not finished is
// recorded as an error and its phase goes to review with that result, where
// the human decides (approve with an override reason, or reject).
func (s *Service) recover(ctx context.Context) error {
	rows, err := s.db.R.QueryContext(ctx, `SELECT DISTINCT r.phase_id FROM validation_runs r JOIN phases p ON p.id = r.phase_id
		WHERE p.state = 'Validation'`)
	if err != nil {
		return wrap(err, "find interrupted validations")
	}
	var phases []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		phases = append(phases, id)
	}
	rows.Close()
	var pending []string
	rows, err = s.db.R.QueryContext(ctx, `SELECT r.id FROM validation_runs r JOIN validators v ON v.id = r.validator_id WHERE r.status = 'pending' AND v.type = 'command'`)
	if err != nil {
		return wrap(err, "find interrupted validator runs")
	}
	for rows.Next() {
		var id string
		rows.Scan(&id)
		pending = append(pending, id)
	}
	rows.Close()
	// A validator the crashed service started must not keep running unsupervised.
	stopped := stopLeftovers(pending)
	err = s.tx(ctx, func(t *Tx) error {
		res, err := t.ExecContext(ctx, `UPDATE validation_runs SET status = 'error', finished_at = ?
			WHERE status = 'pending' AND validator_id IN (SELECT id FROM validators WHERE type = 'command')`, s.ts())
		if err != nil {
			return wrap(err, "mark interrupted validator runs")
		}
		if n, _ := res.RowsAffected(); n > 0 {
			return t.audit(ctx, store.AuditRecord{Event: "recovery.validator_interrupted", ActorScope: "system", Detail: map[string]any{"runs": n, "processes_stopped": stopped}})
		}
		return nil
	})
	if err != nil {
		return err
	}
	// No command is left pending, so this only moves each phase to its review.
	for _, ph := range phases {
		s.queueValidation(ph)
	}
	return s.recoverExecutions(ctx)
}

// loops are the background goroutines Start runs.
func (s *Service) loops() []func(context.Context) {
	return []func(context.Context){s.validationLoop, s.leaseLoop, func(ctx context.Context) { s.detectClaude(ctx) }}
}
