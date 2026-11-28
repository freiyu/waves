package core

import (
	"context"
	"strings"
	"sync"
	"time"

	"waves/internal/store"
)

// A lease is held by a connection, not by a clock. The bridge that Claude Code starts keeps one MCP stream open to the
// service for as long as the session lives, also while one tool call runs for many minutes and nothing else is said. Waves
// counts those streams per MCP session: while a session has one, the leases claimed on it have no deadline, and silence is not
// a reason to end them. When the last stream closes (Claude Code exited or was killed, the bridge died) the execution is
// Disconnected at once and the lease ends after presenceGrace unless a stream comes back. Nothing is rerun either way.
// A lease whose session has no stream (a client that does not open one) keeps the heartbeat rules.
//
// presenceGrace is a variable only so tests can shorten it; there is no flag or environment variable for it.
var presenceGrace = 30 * time.Second

// heldFor is the deadline a connected session's lease carries: no deadline for any practical purpose. The checks that read
// the deadline (a lease must be unexpired) keep working unchanged.
const heldFor = 100 * 365 * 24 * time.Hour

type presence struct {
	mu   sync.Mutex
	open map[string]int
}

// The key names the project as well as the session: a credential of another project cannot hold this one's leases even if it
// somehow named the session.
func presenceKey(project, session string) string { return project + "\x00" + session }

func (p *presence) has(project, session string) bool {
	if session == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.open[presenceKey(project, session)] > 0
}

// add counts one more stream of the session; done counts one less and reports how many are still open.
func (p *presence) add(project, session string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.open[presenceKey(project, session)]++
}

// done counts one stream less and reports how many are open and whether the session was counted at all.
func (p *presence) done(project, session string) (open int, known bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	k := presenceKey(project, session)
	if p.open[k] == 0 {
		return 0, false
	}
	p.open[k]--
	if p.open[k] == 0 {
		delete(p.open, k)
	}
	return p.open[k], true
}

// sessionsOf lists the sessions of a project that have a stream open.
func (p *presence) sessionsOf(project string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	prefix := presenceKey(project, "")
	for k := range p.open {
		if strings.HasPrefix(k, prefix) {
			out = append(out, strings.TrimPrefix(k, prefix))
		}
	}
	return out
}

// dropProject forgets every session of a project and returns them: used when the project's credential is revoked, because a
// stream opened with it must not hold leases any longer.
func (p *presence) dropProject(project string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	prefix := presenceKey(project, "")
	for k := range p.open {
		if strings.HasPrefix(k, prefix) {
			out = append(out, strings.TrimPrefix(k, prefix))
			delete(p.open, k)
		}
	}
	return out
}

func (s *Service) heldWhileConnected() time.Time { return s.now().Add(heldFor) }

// leaseExpiry is the deadline to store for a lease that is being renewed: the normal one, or none while its session is connected.
func (s *Service) leaseExpiry(ctx context.Context, t *Tx, lease string, normal time.Time) time.Time {
	var project, session string
	t.QueryRowContext(ctx, `SELECT project_id, COALESCE(mcp_session,'') FROM leases WHERE id = ?`, lease).Scan(&project, &session)
	if s.pres.has(project, session) {
		return s.heldWhileConnected()
	}
	return normal
}

type heldLease struct{ id, exe, state string }

// leasesOfStream lists the active leases claimed on an MCP session of this project.
func (s *Service) leasesOfStream(ctx context.Context, t *Tx, project, session string) ([]heldLease, error) {
	rows, err := t.QueryContext(ctx, `SELECT l.id, l.execution_id, e.state FROM leases l JOIN executions e ON e.id = l.execution_id
		WHERE l.mcp_session = ? AND l.project_id = ? AND l.revoked_at IS NULL AND l.expires_at > ? ORDER BY l.rowid`, session, project, s.ts())
	if err != nil {
		return nil, wrap(err, "find the leases of the stream")
	}
	defer rows.Close()
	var out []heldLease
	for rows.Next() {
		var l heldLease
		if err := rows.Scan(&l.id, &l.exe, &l.state); err != nil {
			return nil, wrap(err, "read lease")
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// StreamOpen records that an MCP session of the project opened a stream. Only the project's MCP credential can: the lease
// lookup is by that project and the session id, which the service assigned to one credential, so another session's lease is
// never held by it. Leases of the session lose their deadline, and a Disconnected execution is Running again.
func (s *Service) StreamOpen(ctx context.Context, p Principal, session string) error {
	if p.Kind != KindMCP || session == "" {
		return errWrongScope
	}
	s.pres.add(p.ProjectID, session)
	err := s.tx(ctx, func(t *Tx) error {
		// Checked again here: a request authenticated just before the project was disconnected must hold nothing after it.
		var live int
		if err := t.QueryRowContext(ctx, `SELECT count(*) FROM credentials WHERE id = ? AND revoked_at IS NULL`, p.CredID).Scan(&live); err != nil {
			return wrap(err, "check the credential")
		}
		if live == 0 {
			return errWrongScope
		}
		ls, err := s.leasesOfStream(ctx, t, p.ProjectID, session)
		if err != nil {
			return err
		}
		for _, l := range ls {
			if _, err := t.ExecContext(ctx, `UPDATE leases SET last_heartbeat_at = ?, expires_at = ? WHERE id = ?`, s.ts(), store.FormatTime(s.heldWhileConnected()), l.id); err != nil {
				return wrap(err, "hold lease")
			}
			if l.state == "Disconnected" {
				if _, err := s.transition(ctx, t, "execution", l.exe, "Running", Actor{Scope: "agent", Ref: p.ActorRef()}, "session reconnected", ""); err != nil {
					return err
				}
			}
		}
		t.afterCommit(s.pokeLeases)
		return nil
	})
	if err != nil {
		s.pres.done(p.ProjectID, session) // the handler registers no close for a stream that failed to open: do not count it
	}
	return err
}

// StreamClose records that a stream of the session ended. When it was the last one, the leases of the session get presenceGrace
// to come back: the execution is Disconnected now (a wait, if one was open, ends) and the lease ends when the time is up.
func (s *Service) StreamClose(ctx context.Context, p Principal, session string) error {
	if p.Kind != KindMCP || session == "" {
		return errWrongScope
	}
	if open, known := s.pres.done(p.ProjectID, session); open > 0 || !known {
		return nil // another stream is open, or the credential was revoked and the session already let go
	}
	err := s.tx(ctx, func(t *Tx) error {
		// A stream may have opened between the count reaching zero and this transaction: then the session is connected.
		if s.pres.has(p.ProjectID, session) {
			return nil
		}
		return s.leaveSession(ctx, t, p.ProjectID, session)
	})
	if err != nil {
		s.pokeLeases() // the loop notices a lease that is held by a connection that is gone (see expireLeases)
	}
	return err
}

// leaveSession starts the grace period of the session's leases.
func (s *Service) leaveSession(ctx context.Context, t *Tx, project, session string) error {
	ls, err := s.leasesOfStream(ctx, t, project, session)
	if err != nil {
		return err
	}
	for _, l := range ls {
		if _, err := endWait(ctx, t, s.ts(), l.exe, "state_changed"); err != nil {
			return err
		}
		if _, err := t.ExecContext(ctx, `UPDATE leases SET last_heartbeat_at = ?, expires_at = ? WHERE id = ?`, s.ts(), store.FormatTime(s.now().Add(presenceGrace)), l.id); err != nil {
			return wrap(err, "start the grace period")
		}
		if l.state == "Running" {
			if _, err := s.transition(ctx, t, "execution", l.exe, "Disconnected", sysActor, "session left", ""); err != nil {
				return err
			}
		}
	}
	t.afterCommit(s.pokeLeases)
	return nil
}

// endPresence lets go of every connected session of a project, in the transaction that revokes its credential: the leases
// get the grace period and the old streams hold nothing from here on.
func (s *Service) endPresence(ctx context.Context, t *Tx, project string) error {
	// The counts are forgotten only once the transaction committed, and before the loop is woken for the new deadlines.
	t.afterCommit(func() { s.pres.dropProject(project) })
	for _, session := range s.pres.sessionsOf(project) {
		if err := s.leaveSession(ctx, t, project, session); err != nil {
			return err
		}
	}
	return nil
}

// SessionConnected reports whether an MCP session of the project has a stream open.
func (s *Service) SessionConnected(project, session string) bool { return s.pres.has(project, session) }
