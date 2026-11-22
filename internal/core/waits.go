package core

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"waves/internal/store"
)

// A wait is the harness telling Waves that its turn ended and it waits for the human. It sits beside the
// execution (state stays Running), keeps the lease, the workspace lock and the slot, and ends on the first
// sign of life from the same session, on any state change, or when the lease finally expires.

// The default and bounds of the longest a harness may wait before the lease ends as usual.
func (s *Service) waitingMax(ctx context.Context) time.Duration {
	return time.Duration(max(s.settingInt(ctx, "waiting_max_h"), 1)) * time.Hour
}

// HookSignal is what the Stop and UserPromptSubmit hooks send: only the session and the event.
type HookSignal struct {
	SessionID string `json:"session_id"`
	Event     string `json:"event"` // stop | prompt | end
}

// endWait closes the open wait of an execution, if any.
func endWait(ctx context.Context, t *Tx, now, execution, by string) (bool, error) {
	res, err := t.ExecContext(ctx, `UPDATE execution_waits SET ended_at = ?, ended_by = ? WHERE execution_id = ? AND ended_at IS NULL`, now, by, execution)
	if err != nil {
		return false, wrap(err, "end wait")
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// maxWaitAudits is how many wait records one execution may add to the audit chain per minute. The state still changes;
// only the record is skipped, so a loop of signals cannot grow the chain the approvals verify.
const maxWaitAudits = 10

func (s *Service) waitAudit(ctx context.Context, t *Tx, exe, event, scope string, detail map[string]any) error {
	var n int
	t.QueryRowContext(ctx, `SELECT count(*) FROM audit WHERE subject = ? AND event IN ('execution.waiting','execution.resumed') AND ts > ?`,
		exe, store.FormatTime(s.now().Add(-time.Minute))).Scan(&n)
	if n >= maxWaitAudits {
		return nil
	}
	if detail == nil {
		detail = map[string]any{}
	}
	detail["credential"] = scope
	return t.audit(ctx, store.AuditRecord{Event: event, ActorScope: scope, Subject: exe, Detail: detail})
}

// renewWindow gives a lease its normal window again from now: after a wait ended it must not keep the long window the
// wait stretched it to, and its last sign of life is now.
func (s *Service) renewWindow(ctx context.Context, t *Tx, lease string) error {
	e := s.leaseExpiry(ctx, t, lease, s.now().Add(expireAfter*s.heartbeatInterval(ctx)))
	if _, err := t.ExecContext(ctx, `UPDATE leases SET last_heartbeat_at = ?, expires_at = ? WHERE id = ? AND revoked_at IS NULL`, s.ts(), store.FormatTime(e), lease); err != nil {
		return wrap(err, "renew lease")
	}
	t.afterCommit(s.pokeLeases)
	return nil
}

type sessionLease struct{ lease, exe, state string }

// leasesOfSession lists every active lease bound to a session: a session may hold more than one, and a signal is about
// the session, so it applies to all of them rather than to an arbitrary one.
func (s *Service) leasesOfSession(ctx context.Context, t *Tx, session string) ([]sessionLease, error) {
	rows, err := t.QueryContext(ctx, `SELECT l.id, l.execution_id, e.state FROM leases l JOIN executions e ON e.id = l.execution_id
		WHERE l.session_id = ? AND l.revoked_at IS NULL AND l.expires_at > ? ORDER BY l.rowid`, session, s.ts())
	if err != nil {
		return nil, wrap(err, "find the leases of the session")
	}
	defer rows.Close()
	var out []sessionLease
	for rows.Next() {
		var l sessionLease
		if err := rows.Scan(&l.lease, &l.exe, &l.state); err != nil {
			return nil, wrap(err, "read lease")
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// SignalGuard records that the session that holds a lease stopped or got a prompt. The guard token is the only
// credential that may send it. A session without an active lease is ignored: nothing is created for it, and the
// caller cannot tell it apart from a session that has one.
func (s *Service) SignalGuard(ctx context.Context, p Principal, sig HookSignal) error {
	if p.Kind != KindGuard {
		return errWrongScope
	}
	if sig.SessionID == "" || (sig.Event != "stop" && sig.Event != "prompt" && sig.Event != "end") {
		return errValidation("The hook signal needs a session and an event.", "")
	}
	return s.tx(ctx, func(t *Tx) error {
		ls, err := s.leasesOfSession(ctx, t, sig.SessionID)
		if err != nil {
			return err
		}
		for _, l := range ls {
			if l.state != "Running" && l.state != "Disconnected" {
				continue // Paused, waiting for approval, ended: a wait means nothing there
			}
			if sig.Event != "end" { // a session that ended is not a sign of life
				if err := s.revive(ctx, t, l.lease, l.exe, l.state, sig.SessionID); err != nil {
					return err
				}
			}
			if sig.Event == "prompt" {
				if err := s.resume(ctx, t, l.lease, l.exe, "prompt"); err != nil {
					return err
				}
				continue
			}
			if sig.Event == "end" {
				if err := s.sessionEnded(ctx, t, l.lease, l.exe); err != nil {
					return err
				}
				continue
			}
			// stop: the longest the harness may wait is counted from now; the lease is stretched to it.
			now := s.now()
			res, err := t.ExecContext(ctx, `INSERT INTO execution_waits(execution_id, since, source) SELECT ?, ?, 'hook' WHERE NOT EXISTS
				(SELECT 1 FROM execution_waits WHERE execution_id = ? AND ended_at IS NULL)`, l.exe, store.FormatTime(now), l.exe)
			if err != nil {
				return wrap(err, "record wait")
			}
			if n, _ := res.RowsAffected(); n == 0 {
				continue // already waiting
			}
			if _, err := t.ExecContext(ctx, `UPDATE leases SET last_heartbeat_at = ?, expires_at = ? WHERE id = ?`, store.FormatTime(now), store.FormatTime(s.leaseExpiry(ctx, t, l.lease, now.Add(s.waitingMax(ctx)))), l.lease); err != nil {
				return wrap(err, "stretch lease for the wait")
			}
			t.publish("execution.changed", map[string]string{"execution_id": l.exe})
			t.afterCommit(s.pokeLeases)
			if err := s.waitAudit(ctx, t, l.exe, "execution.waiting", "guard", map[string]any{"source": "hook", "max_h": int(s.waitingMax(ctx).Hours())}); err != nil {
				return err
			}
		}
		return nil
	})
}

// sessionEnded: the harness session is over (SessionEnd hook). A wait that was open is closed and the lease gets its
// normal window, not the long one: nobody is left to answer, so a dead session or a finished `claude -p` is flagged
// within minutes (Interrupted, recovery_required), not after the longest wait. Without an open wait nothing changes:
// the lease was already on its normal window.
func (s *Service) sessionEnded(ctx context.Context, t *Tx, lease, exe string) error {
	ended, err := endWait(ctx, t, s.ts(), exe, "state_changed")
	if err != nil || !ended {
		return err
	}
	if err := s.renewWindow(ctx, t, lease); err != nil {
		return err
	}
	t.publish("execution.changed", map[string]string{"execution_id": exe})
	return s.waitAudit(ctx, t, exe, "execution.resumed", "guard", map[string]any{"by": "session_end"})
}

// resume ends an open wait because the harness is active again, and gives the lease its normal window back.
func (s *Service) resume(ctx context.Context, t *Tx, lease, exe, by string) error {
	ended, err := endWait(ctx, t, s.ts(), exe, "resumed")
	if err != nil || !ended {
		return err
	}
	if err := s.renewWindow(ctx, t, lease); err != nil {
		return err
	}
	t.publish("execution.changed", map[string]string{"execution_id": exe})
	return s.waitAudit(ctx, t, exe, "execution.resumed", "guard", map[string]any{"by": by})
}

// revive is the sign of life from a hook: a Disconnected execution is Running again. The audit record says the
// credential was the guard token, whatever session id the hook event named.
func (s *Service) revive(ctx context.Context, t *Tx, lease, exe, state, session string) error {
	if state != "Disconnected" {
		return nil
	}
	if err := s.renewWindow(ctx, t, lease); err != nil {
		return err
	}
	_, err := s.transition(ctx, t, "execution", exe, "Running", Actor{Scope: "agent", Ref: "session:" + session}, "hook signal (guard credential)", "")
	return err
}

// noteLiveness is called from the guard decision for a call by a session that holds an active lease: the lease is
// renewed like a heartbeat (at most once per minHeartbeat), and an open wait ends, because the harness is working.
// It changes no decision and grants nothing a heartbeat could not.
func (s *Service) noteLiveness(ctx context.Context, t *Tx, lease, session string) error {
	var exe, state, last string
	if err := t.QueryRowContext(ctx, `SELECT l.execution_id, e.state, l.last_heartbeat_at FROM leases l JOIN executions e ON e.id = l.execution_id
		WHERE l.id = ? AND l.revoked_at IS NULL AND l.expires_at > ?`, lease, s.ts()).Scan(&exe, &state, &last); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return wrap(err, "read lease for liveness")
	}
	if state == "Disconnected" {
		if err := s.revive(ctx, t, lease, exe, state, session); err != nil {
			return err
		}
	} else if s.now().Sub(store.ParseTime(last)) >= minHeartbeat {
		if err := s.renewWindow(ctx, t, lease); err != nil {
			return err
		}
	}
	return s.resume(ctx, t, lease, exe, "tool_call")
}

var errBadSignal = fail(http.StatusBadRequest, "validation_failed", "The hook signal is not valid JSON.", "")
