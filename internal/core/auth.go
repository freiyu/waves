package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"waves/internal/store"
)

// Token prefixes. They only make secrets recognizable in a leaked log line;
// authority always comes from the hash lookup.
const (
	PrefixOperator  = "wop"
	PrefixGuard     = "wgd"
	PrefixMCP       = "wmp"
	PrefixLease     = "wls"
	PrefixSession   = "wss"
	PrefixElevation = "wel"
	PrefixLink      = "wlk"
)

// Principal kinds.
const (
	KindNone     = "none"
	KindSession  = "session"  // dashboard cookie
	KindOperator = "operator" // CLI bearer token
	KindGuard    = "guard"    // hook bearer token
	KindMCP      = "mcp"      // project MCP credential
	KindLease    = "lease"    // a lease token presented as a bearer
)

// Principal is the authenticated caller of one request.
type Principal struct {
	Kind      string
	SessionID string // sessions.id_hash for dashboard sessions
	CredID    string // credentials.id or leases.id
	ProjectID string // mcp_project credentials and leases
	Client    string // dashboard | cli
	Source    string // remote address
	TTY       bool   // the CLI reported an interactive terminal (a speed bump, not a barrier)
}

// IsOperator reports whether p carries operator scope (dashboard session or operator token).
func (p Principal) IsOperator() bool { return p.Kind == KindSession || p.Kind == KindOperator }

// ActorRef is how p appears in audit records: never a secret.
func (p Principal) ActorRef() string {
	switch p.Kind {
	case KindSession:
		if len(p.SessionID) >= 8 {
			return "session:" + p.SessionID[:8]
		}
	case KindOperator, KindGuard, KindMCP, KindLease:
		return p.Kind + ":" + p.CredID
	}
	return p.Kind
}

const (
	loginLinkTTL      = 60 * time.Second
	singleUseTTL      = 2 * time.Minute
	oneShotWindowTTL  = time.Minute
	stepupWindow      = 15 * time.Minute
	stepupAlertAt     = 5
	stepupMaxDelay    = 5 * time.Minute
	defaultSessionTTL = 12 * time.Hour
)

// CreateCredential stores a new operator or guard token and returns the secret once.
func (s *Service) CreateCredential(ctx context.Context, scope string) (string, error) {
	prefix := map[string]string{"operator": PrefixOperator, "guard": PrefixGuard}[scope]
	if prefix == "" {
		return "", fmt.Errorf("unknown credential scope %q", scope)
	}
	secret := NewSecret(prefix)
	id := NewID("crd")
	err := s.tx(ctx, func(t *Tx) error {
		if _, err := t.ExecContext(ctx, `INSERT INTO credentials(id, scope, token_hash, created_at) VALUES (?,?,?,?)`, id, scope, HashSecret(secret), s.ts()); err != nil {
			return wrap(err, "store credential")
		}
		return t.audit(ctx, store.AuditRecord{Event: "credential.created", ActorScope: "system", Subject: id, Detail: map[string]any{"scope": scope}})
	})
	if err != nil {
		return "", err
	}
	return secret, nil
}

// RevokeCredentials revokes every active credential of scope (used when setup rotates tokens).
func (s *Service) RevokeCredentials(ctx context.Context, scope string) error {
	return s.tx(ctx, func(t *Tx) error {
		_, err := t.ExecContext(ctx, `UPDATE credentials SET revoked_at = ? WHERE scope = ? AND revoked_at IS NULL`, s.ts(), scope)
		return wrap(err, "revoke credentials")
	})
}

// Bearer resolves an Authorization bearer value. A lease token resolves to
// KindLease so the API can reject and audit it on approval routes (AH-4).
func (s *Service) Bearer(ctx context.Context, secret, source string) (Principal, bool) {
	h := HashSecret(secret)
	var id, scope string
	var project sql.NullString
	err := s.db.R.QueryRowContext(ctx, `SELECT id, scope, project_id FROM credentials WHERE token_hash = ? AND revoked_at IS NULL`, h).Scan(&id, &scope, &project)
	if err == nil {
		kind := map[string]string{"operator": KindOperator, "guard": KindGuard, "mcp_project": KindMCP}[scope]
		return Principal{Kind: kind, CredID: id, ProjectID: project.String, Client: "cli", Source: source}, kind != ""
	}
	var lid, pid string
	err = s.db.R.QueryRowContext(ctx, `SELECT id, project_id FROM leases WHERE token_hash = ?`, h).Scan(&lid, &pid)
	if err == nil {
		return Principal{Kind: KindLease, CredID: lid, ProjectID: pid, Source: source}, true
	}
	return Principal{Kind: KindNone, Source: source}, false
}

// ---- login links and sessions ----

// CreateLoginLink returns a single-use code valid for 60 seconds. Only the
// operator token (the CLI) may create one.
func (s *Service) CreateLoginLink(ctx context.Context, p Principal) (string, time.Time, error) {
	if p.Kind != KindOperator {
		return "", time.Time{}, errWrongScope
	}
	code := NewSecret(PrefixLink)
	exp := s.now().Add(loginLinkTTL)
	err := s.tx(ctx, func(t *Tx) error {
		if _, err := t.ExecContext(ctx, `INSERT INTO login_links(code_hash, created_at, expires_at) VALUES (?,?,?)`, HashSecret(code), s.ts(), store.FormatTime(exp)); err != nil {
			return wrap(err, "store login link")
		}
		return t.audit(ctx, store.AuditRecord{Event: "login_link.created", ActorScope: "operator", ActorRef: p.ActorRef()})
	})
	return code, exp, err
}

// LinkStatus tells whether code is still usable without consuming it.
func (s *Service) LinkStatus(ctx context.Context, code string) (bool, int) {
	var exp string
	err := s.db.R.QueryRowContext(ctx, `SELECT expires_at FROM login_links WHERE code_hash = ? AND consumed_at IS NULL`, HashSecret(code)).Scan(&exp)
	if err != nil {
		return false, 0
	}
	left := int(store.ParseTime(exp).Sub(s.now()).Seconds())
	if left <= 0 {
		return false, 0
	}
	return true, left
}

// SessionInfo is what GET /auth/session returns.
type SessionInfo struct {
	CSRF          string
	ExpiresAt     time.Time
	PassphraseSet bool
	Elevation     *ElevationInfo
}

// ElevationInfo describes an active elevation without its secret.
type ElevationInfo struct {
	Kind       string
	ExpiresAt  time.Time
	ApprovalID string
}

// ConsumeLink turns a valid login link into a dashboard session. It returns
// the session secret (the cookie value).
func (s *Service) ConsumeLink(ctx context.Context, code, source string) (string, error) {
	h := HashSecret(code)
	secret := NewSecret(PrefixSession)
	ttl := time.Duration(s.settingInt(ctx, "session_ttl_h")) * time.Hour
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}
	var bad *Error
	err := s.tx(ctx, func(t *Tx) error {
		var exp string
		var consumed sql.NullString
		err := t.QueryRowContext(ctx, `SELECT expires_at, consumed_at FROM login_links WHERE code_hash = ?`, h).Scan(&exp, &consumed)
		if errors.Is(err, sql.ErrNoRows) {
			bad = fail(http.StatusUnauthorized, "unauthenticated", "This link was already used or has expired.", "Create a new one: waves open.")
			return nil
		}
		if err != nil {
			return wrap(err, "read login link")
		}
		if consumed.Valid || store.ParseTime(exp).Before(s.now()) {
			ev := "login_link.expired"
			if consumed.Valid {
				ev = "login_link.replayed"
			}
			bad = fail(http.StatusUnauthorized, "unauthenticated", "This link was already used or has expired.", "Create a new one: waves open.")
			return t.audit(ctx, store.AuditRecord{Event: ev, ActorScope: "operator", Detail: map[string]any{"source": source}})
		}
		if _, err := t.ExecContext(ctx, `UPDATE login_links SET consumed_at = ? WHERE code_hash = ?`, s.ts(), h); err != nil {
			return wrap(err, "consume login link")
		}
		sid := HashSecret(secret)
		if _, err := t.ExecContext(ctx, `INSERT INTO sessions(id_hash, csrf_hash, client, created_at, expires_at) VALUES (?,?,?,?,?)`,
			sid, HashSecret(derive(secret, "csrf")), "dashboard", s.ts(), s.tsIn(ttl)); err != nil {
			return wrap(err, "create session")
		}
		if err := t.audit(ctx, store.AuditRecord{Event: "login_link.consumed", ActorScope: "operator", Detail: map[string]any{"source": source}}); err != nil {
			return err
		}
		return t.audit(ctx, store.AuditRecord{Event: "session.created", ActorScope: "operator", ActorRef: "session:" + sid[:8], Detail: map[string]any{"client": "dashboard", "source": source}})
	})
	if err != nil {
		return "", err
	}
	if bad != nil {
		return "", bad
	}
	return secret, nil
}

// Session resolves a session cookie value. It returns the principal and the
// CSRF token the request must carry on writes.
func (s *Service) Session(ctx context.Context, secret, source string) (Principal, string, bool) {
	sid := HashSecret(secret)
	var exp string
	err := s.db.R.QueryRowContext(ctx, `SELECT expires_at FROM sessions WHERE id_hash = ? AND revoked_at IS NULL`, sid).Scan(&exp)
	if err != nil || store.ParseTime(exp).Before(s.now()) {
		return Principal{Kind: KindNone, Source: source}, "", false
	}
	return Principal{Kind: KindSession, SessionID: sid, Client: "dashboard", Source: source}, derive(secret, "csrf"), true
}

// SessionInfo returns the session view for p.
func (s *Service) SessionInfo(ctx context.Context, p Principal, csrf string) (SessionInfo, error) {
	info := SessionInfo{CSRF: csrf, PassphraseSet: s.passphraseSet(ctx)}
	if p.Kind == KindSession {
		var exp string
		if err := s.db.R.QueryRowContext(ctx, `SELECT expires_at FROM sessions WHERE id_hash = ?`, p.SessionID).Scan(&exp); err != nil {
			return info, wrap(err, "read session")
		}
		info.ExpiresAt = store.ParseTime(exp)
		var kind, eexp string
		var apr sql.NullString
		err := s.db.R.QueryRowContext(ctx, `SELECT kind, expires_at, approval_id FROM elevations WHERE session_id = ? AND kind = 'window' AND consumed_at IS NULL AND expires_at > ? ORDER BY created_at DESC, rowid DESC LIMIT 1`,
			p.SessionID, s.ts()).Scan(&kind, &eexp, &apr)
		if err == nil {
			info.Elevation = &ElevationInfo{Kind: kind, ExpiresAt: store.ParseTime(eexp), ApprovalID: apr.String}
		}
	} else {
		info.ExpiresAt = s.now().Add(defaultSessionTTL)
	}
	return info, nil
}

// Logout revokes the dashboard session and its elevations.
func (s *Service) Logout(ctx context.Context, p Principal) error {
	if p.Kind != KindSession {
		return nil
	}
	return s.tx(ctx, func(t *Tx) error {
		if _, err := t.ExecContext(ctx, `UPDATE sessions SET revoked_at = ? WHERE id_hash = ? AND revoked_at IS NULL`, s.ts(), p.SessionID); err != nil {
			return wrap(err, "revoke session")
		}
		if _, err := t.ExecContext(ctx, `UPDATE elevations SET consumed_at = ? WHERE session_id = ? AND consumed_at IS NULL`, s.ts(), p.SessionID); err != nil {
			return wrap(err, "revoke elevations")
		}
		return t.audit(ctx, store.AuditRecord{Event: "session.revoked", ActorScope: "operator", ActorRef: p.ActorRef()})
	})
}

// ---- passphrase and step-up ----

func (s *Service) passphraseSet(ctx context.Context) bool {
	var n int
	s.db.R.QueryRowContext(ctx, `SELECT count(*) FROM passphrase`).Scan(&n)
	return n == 1
}

// SetInitialPassphrase stores the first approval passphrase. Only `waves
// setup` calls it, after reading the passphrase twice from a TTY. It refuses
// to replace an existing passphrase.
func (s *Service) SetInitialPassphrase(ctx context.Context, pass string) error {
	if err := CheckPassphraseStrength(pass); err != nil {
		return err
	}
	stored, key := hashPassphrase(s.argon, pass)
	return s.tx(ctx, func(t *Tx) error {
		var n int
		if err := t.QueryRowContext(ctx, `SELECT count(*) FROM passphrase`).Scan(&n); err != nil {
			return wrap(err, "read passphrase")
		}
		if n > 0 {
			return errors.New("an approval passphrase is already set; rotate it with: waves auth passphrase")
		}
		if _, err := t.ExecContext(ctx, `INSERT INTO passphrase(id, hash, created_at) VALUES (1,?,?)`, stored, s.ts()); err != nil {
			return wrap(err, "store passphrase")
		}
		if err := t.audit(ctx, store.AuditRecord{Event: "passphrase.set", ActorScope: "human", Detail: map[string]any{"method": "tty"}}); err != nil {
			return err
		}
		return store.WriteCheckpoint(ctx, t.Tx, key)
	})
}

// RotatePassphrase replaces the passphrase after checking the current one.
// The elevation is checked first, and the current passphrase goes through the
// same limited, audited check as a step-up, so this is no guessing oracle.
func (s *Service) RotatePassphrase(ctx context.Context, p Principal, elevation, current, next string) error {
	if !p.IsOperator() {
		return errWrongScope
	}
	if err := s.tx(ctx, func(t *Tx) error { return s.RequireElevation(ctx, t, p, elevation) }); err != nil {
		return err
	}
	if err := CheckPassphraseStrength(next); err != nil {
		return errValidation(err.Error(), "Choose a longer passphrase.")
	}
	oldKey, err := s.checkPassphrase(ctx, p, current)
	if err != nil {
		return err
	}
	newStored, newKey := hashPassphrase(s.argon, next)
	return s.tx(ctx, func(t *Tx) error {
		if id, err := store.VerifyCheckpoints(ctx, t.Tx, oldKey); err != nil {
			return err
		} else if id != 0 {
			return fail(http.StatusConflict, "audit_chain_broken", "An audit checkpoint does not verify.", "Step up once so the break is recorded, then acknowledge it.")
		}
		if _, err := t.ExecContext(ctx, `UPDATE passphrase SET hash = ?, rotated_at = ? WHERE id = 1`, newStored, s.ts()); err != nil {
			return wrap(err, "store passphrase")
		}
		// Elevations from the old passphrase end now.
		if _, err := t.ExecContext(ctx, `UPDATE elevations SET consumed_at = ? WHERE consumed_at IS NULL`, s.ts()); err != nil {
			return wrap(err, "end elevations")
		}
		// Old checkpoints were made with the old key; sign the current head under the new one.
		if _, err := t.ExecContext(ctx, `DELETE FROM audit_checkpoints`); err != nil {
			return wrap(err, "reset checkpoints")
		}
		if err := t.audit(ctx, store.AuditRecord{Event: "passphrase.rotated", ActorScope: "human", ActorRef: p.ActorRef(), Detail: map[string]any{"client": p.Client}}); err != nil {
			return err
		}
		return store.WriteCheckpoint(ctx, t.Tx, newKey)
	})
}

// ResetPassphrase replaces the passphrase without the current one. Only the
// offline `waves auth passphrase --reset` calls it (service stopped, TTY,
// typed confirmation). Every session and elevation is invalidated.
func (s *Service) ResetPassphrase(ctx context.Context, next string) error {
	if err := CheckPassphraseStrength(next); err != nil {
		return err
	}
	stored, key := hashPassphrase(s.argon, next)
	return s.tx(ctx, func(t *Tx) error {
		if _, err := t.ExecContext(ctx, `INSERT INTO passphrase(id, hash, created_at) VALUES (1,?,?) ON CONFLICT(id) DO UPDATE SET hash = excluded.hash, rotated_at = excluded.created_at`, stored, s.ts()); err != nil {
			return wrap(err, "store passphrase")
		}
		for _, q := range []string{
			`UPDATE sessions SET revoked_at = ? WHERE revoked_at IS NULL`,
			`UPDATE elevations SET consumed_at = ? WHERE consumed_at IS NULL`,
		} {
			if _, err := t.ExecContext(ctx, q, s.ts()); err != nil {
				return wrap(err, "invalidate sessions")
			}
		}
		// The old checkpoints were signed with a key the old passphrase yields, so they cannot
		// verify any more. The reset record names the head they covered instead of dropping them silently.
		var headSeq int64
		var headHash string
		if err := t.QueryRowContext(ctx, `SELECT seq, hash FROM audit ORDER BY seq DESC LIMIT 1`).Scan(&headSeq, &headHash); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return wrap(err, "read audit head")
		}
		var replaced int
		if err := t.QueryRowContext(ctx, `SELECT count(*) FROM audit_checkpoints`).Scan(&replaced); err != nil {
			return wrap(err, "count checkpoints")
		}
		if _, err := t.ExecContext(ctx, `DELETE FROM audit_checkpoints`); err != nil {
			return wrap(err, "reset checkpoints")
		}
		// Scope local_offline: a terminal on this machine started it and nothing proved who was typing, so it is not a human decision.
		if err := t.audit(ctx, store.AuditRecord{Event: "passphrase.reset", ActorScope: "local_offline", Detail: map[string]any{
			"method": "tty", "sessions": "invalidated", "previous_head_seq": headSeq, "previous_head_hash": headHash, "checkpoints_replaced": replaced}}); err != nil {
			return err
		}
		return store.WriteCheckpoint(ctx, t.Tx, key)
	})
}

// StepUpResult is a fresh elevation. Secret is shown once.
type StepUpResult struct {
	Secret string
	ElevationInfo
}

// StepUp checks the approval passphrase and creates an elevation: single_use
// and bound to approvalID when one is given, otherwise a window elevation.
func (s *Service) StepUp(ctx context.Context, p Principal, pass, approvalID string) (StepUpResult, error) {
	if !p.IsOperator() {
		s.auditBypass(ctx, p, "stepup")
		return StepUpResult{}, errWrongScope
	}
	if approvalID != "" {
		var state string
		if err := s.db.R.QueryRowContext(ctx, `SELECT state FROM approvals WHERE id = ?`, approvalID).Scan(&state); err != nil {
			return StepUpResult{}, errNotFound("approval", "Open the approval inbox.")
		}
		if state != "pending" {
			return StepUpResult{}, errInvalidState("This approval is already "+state+".", "Nothing more to do.")
		}
		// A broken chain blocks decisions, not the window step-up that acknowledging it needs.
		if st := s.AuditStatus(ctx); st.State == "broken" {
			return StepUpResult{}, fail(http.StatusConflict, "audit_chain_broken", "Approvals are blocked: the audit chain does not verify.", "A human must acknowledge the break on the Activity page.").with("record", int(st.BrokenAt))
		}
	}
	ckKey, err := s.checkPassphrase(ctx, p, pass)
	if err != nil {
		return StepUpResult{}, err
	}
	secret := NewSecret(PrefixElevation)
	res := StepUpResult{Secret: secret}
	err = s.tx(ctx, func(t *Tx) error {
		// The checkpoints were signed with a key only the passphrase yields: a
		// rewrite of the chain before the last checkpoint shows up here.
		bad, err := store.VerifyCheckpoints(ctx, t.Tx, ckKey)
		if err != nil {
			return err
		}
		var nck int
		if err := t.QueryRowContext(ctx, `SELECT count(*) FROM audit_checkpoints`).Scan(&nck); err != nil {
			return wrap(err, "count checkpoints")
		}
		// Setting the passphrase writes the first checkpoint, so none at all means they were removed.
		if bad != 0 || nck == 0 {
			if err := t.audit(ctx, store.AuditRecord{Event: "audit.chain_broken", ActorScope: "system", Detail: map[string]any{"checkpoint": bad, "checkpoints": nck}}); err != nil {
				return err
			}
			// The failed checkpoints are recorded in the break; drop them so the
			// fresh checkpoint below is the new anchor and one acknowledgement suffices.
			if _, err := t.ExecContext(ctx, `DELETE FROM audit_checkpoints`); err != nil {
				return wrap(err, "retire failed checkpoints")
			}
			t.publish("audit.alert", map[string]string{"kind": "chain_broken"})
		}
		kind, ttl, oneShot := "window", time.Duration(s.settingInt(ctx, "elevation_window_min"))*time.Minute, 0
		if approvalID != "" {
			kind, ttl = "single_use", singleUseTTL
		} else if ttl <= 0 {
			ttl, oneShot = oneShotWindowTTL, 1
		}
		exp := s.now().Add(ttl)
		res.Kind, res.ExpiresAt, res.ApprovalID = kind, exp, approvalID
		if _, err := t.ExecContext(ctx, `INSERT INTO elevations(id, session_id, kind, approval_id, created_at, expires_at, one_shot) VALUES (?,?,?,?,?,?,?)`,
			HashSecret(secret), store.NullStr(p.SessionID), kind, store.NullStr(approvalID), s.ts(), store.FormatTime(exp), oneShot); err != nil {
			return wrap(err, "store elevation")
		}
		if err := t.audit(ctx, store.AuditRecord{Event: "stepup.succeeded", ActorScope: "human", ActorRef: p.ActorRef(),
			Detail: map[string]any{"method": "passphrase", "kind": kind, "approval_id": approvalID, "credential": p.Kind, "client": p.Client, "tty": p.TTY, "source": p.Source}}); err != nil {
			return err
		}
		return store.WriteCheckpoint(ctx, t.Tx, ckKey)
	})
	if err != nil {
		return StepUpResult{}, err
	}
	return res, nil
}

// checkPassphrase is the only place a passphrase is verified for an online
// caller. One check runs at a time (it bounds argon2 memory and closes the
// check-then-record race), and failures count globally: every caller is on
// loopback, so neither a new connection nor a new session resets the limit.
func (s *Service) checkPassphrase(ctx context.Context, p Principal, pass string) ([]byte, error) {
	s.passMu.Lock()
	defer s.passMu.Unlock()
	var stored string
	if err := s.db.R.QueryRowContext(ctx, `SELECT hash FROM passphrase WHERE id = 1`).Scan(&stored); err != nil {
		return nil, fail(http.StatusConflict, "passphrase_not_set", "No approval passphrase is set.", "Run waves setup in a terminal.")
	}
	if wait := s.stepupDelay(ctx); wait > 0 {
		// Capped like other refusals: a loop of rate-limited calls must not grow the chain without bound.
		if ok, dropped := s.refusals.take(s.now()); ok {
			d := map[string]any{"source": p.Source}
			if dropped > 0 {
				d["refusals_not_recorded"] = dropped
			}
			s.Audit(ctx, store.AuditRecord{Event: "stepup.rate_limited", ActorScope: "operator", ActorRef: p.ActorRef(), Detail: d})
		}
		secs := int(wait.Seconds() + 0.999)
		return nil, fail(http.StatusTooManyRequests, "rate_limited", "Too many wrong passphrases.", fmt.Sprintf("Try again in %d s.", secs)).with("retry_after_s", secs)
	}
	ok, key, err := verifyPassphrase(stored, pass)
	if err != nil {
		return nil, err
	}
	if ok {
		if _, err := s.db.W.ExecContext(ctx, `DELETE FROM stepup_failures`); err != nil {
			return nil, wrap(err, "clear step-up failures")
		}
		return key, nil
	}
	var n int
	err = s.tx(ctx, func(t *Tx) error {
		if _, err := t.ExecContext(ctx, `INSERT INTO stepup_failures(ts, session_id, source) VALUES (?,?,?)`, s.ts(), store.NullStr(p.SessionID), p.Source); err != nil {
			return wrap(err, "record step-up failure")
		}
		n = s.countFailures(ctx, t)
		if err := t.audit(ctx, store.AuditRecord{Event: "stepup.failed", ActorScope: "operator", ActorRef: p.ActorRef(), Detail: map[string]any{"source": p.Source, "credential": p.Kind, "client": p.Client, "failures": n}}); err != nil {
			return err
		}
		if n >= stepupAlertAt {
			t.publish("audit.alert", map[string]string{"kind": "stepup_failures"})
			t.publish("attention.changed", func() any { return map[string]int{"count": s.AttentionCount(ctx)} })
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return nil, fail(http.StatusForbidden, "stepup_failed", "Wrong passphrase.", "Nothing was changed. Enter the approval passphrase again.").with("attempts_left", max(0, stepupAlertAt-n))
}

func (s *Service) countFailures(ctx context.Context, q store.Execer) int {
	var n int
	since := store.FormatTime(s.now().Add(-stepupWindow))
	q.QueryRowContext(ctx, `SELECT count(*) FROM stepup_failures WHERE ts > ?`, since).Scan(&n)
	return n
}

// stepupDelay is the time left before another attempt: 1 s after the first
// failure, doubling, at most 5 minutes.
func (s *Service) stepupDelay(ctx context.Context) time.Duration {
	n := s.countFailures(ctx, s.db.R)
	if n == 0 {
		return 0
	}
	var last string
	s.db.R.QueryRowContext(ctx, `SELECT max(ts) FROM stepup_failures`).Scan(&last)
	delay := min(time.Second<<min(n-1, 20), stepupMaxDelay)
	return max(store.ParseTime(last).Add(delay).Sub(s.now()), 0)
}

// RevokeElevation forgets an elevation now.
func (s *Service) RevokeElevation(ctx context.Context, p Principal, secret string) error {
	if secret == "" {
		return nil
	}
	return s.tx(ctx, func(t *Tx) error {
		_, err := t.ExecContext(ctx, `UPDATE elevations SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL AND (session_id IS NULL OR session_id = ?)`, s.ts(), HashSecret(secret), p.SessionID)
		return wrap(err, "revoke elevation")
	})
}

// useElevation checks the elevation secret inside t and consumes it when it
// is single-use or one-shot. approvalID binds single-use elevations;
// allowWindow says whether a window elevation is enough. It returns the
// elevation id for the approval row.
func (s *Service) useElevation(ctx context.Context, t *Tx, p Principal, secret, approvalID string, allowWindow bool) (string, error) {
	if !p.IsOperator() {
		return "", errWrongScope
	}
	if secret == "" {
		return "", errStepUp("This action needs your approval passphrase.")
	}
	id := HashSecret(secret)
	var kind, exp string
	var sess, apr, consumed sql.NullString
	var oneShot int
	err := t.QueryRowContext(ctx, `SELECT kind, expires_at, session_id, approval_id, consumed_at, one_shot FROM elevations WHERE id = ?`, id).
		Scan(&kind, &exp, &sess, &apr, &consumed, &oneShot)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errStepUp("This passphrase step-up is not valid.")
	}
	if err != nil {
		return "", wrap(err, "read elevation")
	}
	switch {
	case consumed.Valid:
		return "", errStepUp("This passphrase step-up was already used.")
	case !store.ParseTime(exp).After(s.now()):
		return "", errStepUp("This passphrase step-up has expired.")
	case sess.Valid && sess.String != p.SessionID:
		return "", errStepUp("This passphrase step-up belongs to another session.")
	case !sess.Valid && p.Kind != KindOperator:
		return "", errStepUp("This passphrase step-up belongs to another client.")
	case kind == "single_use" && (approvalID == "" || apr.String != approvalID):
		return "", errStepUp("This passphrase step-up does not cover this action.")
	case kind == "window" && !allowWindow:
		return "", errStepUp("This decision needs a fresh passphrase confirmation.")
	}
	if kind == "single_use" || oneShot == 1 {
		res, err := t.ExecContext(ctx, `UPDATE elevations SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL`, s.ts(), id)
		if err != nil {
			return "", wrap(err, "consume elevation")
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return "", errStepUp("This passphrase step-up was already used.")
		}
	}
	return id, nil
}

// RequireElevation checks and (for one-shot elevations) consumes an elevation
// for a human-scope action that is not an approval decision.
func (s *Service) RequireElevation(ctx context.Context, t *Tx, p Principal, secret string) error {
	_, err := s.useElevation(ctx, t, p, secret, "", true)
	return err
}

// auditBypass records a lease, guard or MCP credential used where only a
// human may act (AH-1, AH-4).
func (s *Service) auditBypass(ctx context.Context, p Principal, what string) {
	ev := "auth.wrong_scope"
	if p.Kind == KindLease || p.Kind == KindGuard || p.Kind == KindMCP {
		ev = "auth.bypass_attempt"
	}
	lim := &s.refusals
	if ev == "auth.bypass_attempt" {
		lim = &s.bypasses
	}
	ok, dropped := lim.take(s.now())
	if !ok {
		return
	}
	err := s.tx(ctx, func(t *Tx) error {
		if ev == "auth.bypass_attempt" {
			t.publish("audit.alert", map[string]string{"kind": "bypass_attempt"})
		}
		d := map[string]any{"source": p.Source}
		if dropped > 0 {
			d["refusals_not_recorded"] = dropped
		}
		return t.audit(ctx, store.AuditRecord{Event: ev, ActorScope: scopeOf(p), ActorRef: p.ActorRef(), ProjectID: p.ProjectID, Subject: what, Detail: d})
	})
	if err != nil {
		s.log.Error("audit bypass attempt", "err", err)
	}
}

// refusalLimit caps refused-request records at refusalsPerMinute, so a
// looping agent cannot grow the audit chain without bound. Refusals over the
// cap are counted and the count goes into the next record.
type refusalLimit struct {
	mu      sync.Mutex
	start   time.Time
	n       int
	dropped int
}

const refusalsPerMinute = 60

func (l *refusalLimit) take(now time.Time) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.start) >= time.Minute {
		l.start, l.n = now, 0
	}
	if l.n >= refusalsPerMinute {
		l.dropped++
		return false, 0
	}
	l.n++
	d := l.dropped
	l.dropped = 0
	return true, d
}

// AuditStepUpRefused records a human-only action refused for a missing,
// invalid, expired or misused passphrase step-up.
func (s *Service) AuditStepUpRefused(ctx context.Context, p Principal, what, reason string) {
	ok, dropped := s.refusals.take(s.now())
	if !ok {
		return
	}
	err := s.tx(ctx, func(t *Tx) error {
		d := map[string]any{"reason": reason, "credential": p.Kind, "client": p.Client, "source": p.Source}
		if dropped > 0 {
			d["refusals_not_recorded"] = dropped
		}
		return t.audit(ctx, store.AuditRecord{Event: "stepup.refused", ActorScope: scopeOf(p), ActorRef: p.ActorRef(), ProjectID: p.ProjectID, Subject: what, Detail: d})
	})
	if err != nil {
		s.log.Error("audit refused step-up", "err", err)
	}
}

// AuditRejected records a request refused for its scope.
func (s *Service) AuditRejected(ctx context.Context, p Principal, what string) {
	s.auditBypass(ctx, p, what)
}

func scopeOf(p Principal) string {
	switch p.Kind {
	case KindSession, KindOperator:
		return "operator"
	case KindGuard:
		return "guard"
	case KindLease, KindMCP:
		return "agent"
	}
	return "system"
}

// ---- audit status ----

// AuditState is the verified chain state.
type AuditState struct {
	State       string // verified | broken
	Records     int
	BrokenAt    int64
	CheckedAt   time.Time
	LastCheckpt *time.Time
}

// AuditStatus verifies the whole chain now. Breaks a human acknowledged do
// not block; a checkpoint failure recorded at step-up blocks until acknowledged.
func (s *Service) AuditStatus(ctx context.Context) AuditState { return s.auditStatusOn(ctx, s.db.R) }

// auditStatusOn verifies through q, so a decision can check inside its own
// transaction. Any read error reports the chain as broken (fail closed).
func (s *Service) auditStatusOn(ctx context.Context, q store.Execer) AuditState {
	st, err := store.VerifyAudit(ctx, q)
	out := AuditState{State: "verified", Records: st.Records, CheckedAt: s.now()}
	acked, aerr := s.acknowledged(ctx, q)
	if err != nil || aerr != nil {
		out.State, out.BrokenAt = "broken", -1
		return out
	}
	for _, b := range st.Breaks {
		if !acked["seq:"+itoa(b)] {
			out.State, out.BrokenAt = "broken", b
			break
		}
	}
	if out.State == "verified" {
		rows, err := q.QueryContext(ctx, `SELECT seq FROM audit WHERE event = 'audit.chain_broken'`)
		if err != nil {
			out.State, out.BrokenAt = "broken", -1
			return out
		}
		for rows.Next() {
			var seq int64
			if err := rows.Scan(&seq); err != nil || !acked["event:"+itoa(seq)] {
				out.State, out.BrokenAt = "broken", seq
			}
		}
		if rows.Close(); rows.Err() != nil {
			out.State, out.BrokenAt = "broken", -1
		}
	}
	var last sql.NullString
	q.QueryRowContext(ctx, `SELECT max(created_at) FROM audit_checkpoints`).Scan(&last)
	if last.Valid {
		t := store.ParseTime(last.String)
		out.LastCheckpt = &t
	}
	return out
}

// acknowledged returns the breaks a human acknowledged, from the audit chain itself.
func (s *Service) acknowledged(ctx context.Context, q store.Execer) (map[string]bool, error) {
	out := map[string]bool{}
	rows, err := q.QueryContext(ctx, `SELECT detail_json FROM audit WHERE event = 'audit.acknowledged'`)
	if err != nil {
		return nil, wrap(err, "read acknowledgements")
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, wrap(err, "scan acknowledgement")
		}
		var v struct {
			Seqs   []int64 `json:"broken_seqs"`
			Events []int64 `json:"break_events"`
		}
		if json.Unmarshal([]byte(d), &v) == nil {
			for _, x := range v.Seqs {
				out["seq:"+itoa(x)] = true
			}
			for _, x := range v.Events {
				out["event:"+itoa(x)] = true
			}
		}
	}
	return out, rows.Err()
}

// AcknowledgeAudit records that a human saw the current breaks, so approvals
// resume. Needs a human elevation.
func (s *Service) AcknowledgeAudit(ctx context.Context, p Principal, elevation string) (AuditState, error) {
	st, err := store.VerifyAudit(ctx, s.db.R)
	if err != nil {
		return AuditState{}, err
	}
	var events []int64
	rows, err := s.db.R.QueryContext(ctx, `SELECT seq FROM audit WHERE event = 'audit.chain_broken'`)
	if err != nil {
		return AuditState{}, wrap(err, "read chain breaks")
	}
	for rows.Next() {
		var seq int64
		rows.Scan(&seq)
		events = append(events, seq)
	}
	rows.Close()
	err = s.tx(ctx, func(t *Tx) error {
		if err := s.RequireElevation(ctx, t, p, elevation); err != nil {
			return err
		}
		t.publish("attention.changed", func() any { return map[string]int{"count": s.AttentionCount(ctx)} })
		return t.audit(ctx, store.AuditRecord{Event: "audit.acknowledged", ActorScope: "human", ActorRef: p.ActorRef(),
			Detail: map[string]any{"broken_seqs": st.Breaks, "break_events": events, "client": p.Client}})
	})
	if err != nil {
		return AuditState{}, err
	}
	return s.AuditStatus(ctx), nil
}

func itoa(n int64) string { return fmt.Sprintf("%d", n) }

// PassphraseSet reports whether the approval passphrase exists.
func (s *Service) PassphraseSet(ctx context.Context) bool { return s.passphraseSet(ctx) }

// AuditRow is one stored audit record.
type AuditRow struct {
	Seq        int64
	TS         string
	Event      string
	ActorScope string
	Subject    string
	Detail     string
}

// AuditPage returns up to limit records older than before (0: newest), newest
// first, and the cursor for the next page (0 when there is none).
func (s *Service) AuditPage(ctx context.Context, limit int, before int64) ([]AuditRow, int64, error) {
	if before <= 0 {
		before = 1 << 62
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT seq, ts, event, actor_scope, COALESCE(subject,''), detail_json FROM audit WHERE seq < ? ORDER BY seq DESC LIMIT ?`, before, limit+1)
	if err != nil {
		return nil, 0, wrap(err, "read audit")
	}
	defer rows.Close()
	out := []AuditRow{}
	for rows.Next() {
		var a AuditRow
		if err := rows.Scan(&a.Seq, &a.TS, &a.Event, &a.ActorScope, &a.Subject, &a.Detail); err != nil {
			return nil, 0, wrap(err, "scan audit")
		}
		out = append(out, a)
	}
	var next int64
	if len(out) > limit {
		out = out[:limit]
		next = out[limit-1].Seq
	}
	return out, next, rows.Err()
}

// ClaudeCodeVersion is the detected Claude Code version, or "".
func (s *Service) ClaudeCodeVersion(ctx context.Context) string {
	var v sql.NullString
	s.db.R.QueryRowContext(ctx, `SELECT detected_version FROM harnesses WHERE kind = 'claude-code'`).Scan(&v)
	return v.String
}
