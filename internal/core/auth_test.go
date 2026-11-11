package core

import (
	"strings"
	"testing"
	"time"

	"waves/internal/store"
)

func TestLoginLinkIsSingleUseAndShortLived(t *testing.T) {
	e := newEnv(t)
	op := e.operator(t)
	if _, _, err := e.s.CreateLoginLink(e.ctx, Principal{Kind: KindSession, SessionID: "x"}); code(err) != "wrong_scope" {
		t.Fatalf("session created a login link: %v", err)
	}
	c, _, err := e.s.CreateLoginLink(e.ctx, op)
	if err != nil {
		t.Fatal(err)
	}
	if ok, left := e.s.LinkStatus(e.ctx, c); !ok || left != 60 {
		t.Fatalf("status = %v %d, want valid 60", ok, left)
	}
	if _, err := e.s.ConsumeLink(e.ctx, c, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.ConsumeLink(e.ctx, c, "a"); code(err) != "unauthenticated" {
		t.Fatalf("replayed link: %v", err)
	}
	if e.auditCount(t, "login_link.replayed") != 1 {
		t.Fatal("replay not audited")
	}
	c2, _, _ := e.s.CreateLoginLink(e.ctx, op)
	e.clock.Add(61 * time.Second)
	if ok, _ := e.s.LinkStatus(e.ctx, c2); ok {
		t.Fatal("expired link reported valid")
	}
	if _, err := e.s.ConsumeLink(e.ctx, c2, "a"); code(err) != "unauthenticated" {
		t.Fatalf("expired link: %v", err)
	}
}

func TestSessionExpiresAndLogsOut(t *testing.T) {
	e := newEnv(t)
	op := e.operator(t)
	c, _, _ := e.s.CreateLoginLink(e.ctx, op)
	sec, _ := e.s.ConsumeLink(e.ctx, c, "a")
	p, csrf, ok := e.s.Session(e.ctx, sec, "a")
	if !ok || csrf == "" || csrf == sec {
		t.Fatalf("session: ok=%v csrf=%q", ok, csrf)
	}
	if err := e.s.Logout(e.ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := e.s.Session(e.ctx, sec, "a"); ok {
		t.Fatal("session valid after logout")
	}
	c, _, _ = e.s.CreateLoginLink(e.ctx, op)
	sec, _ = e.s.ConsumeLink(e.ctx, c, "a")
	e.clock.Add(12*time.Hour + time.Second)
	if _, _, ok := e.s.Session(e.ctx, sec, "a"); ok {
		t.Fatal("session valid after 12 h")
	}
}

func TestPassphraseStrengthAndSetOnce(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"", "short", "aaaaaaaaaaaaaaa"} {
		if err := e.s.SetInitialPassphrase(e.ctx, p); err == nil {
			t.Fatalf("weak passphrase %q accepted", p)
		}
	}
	e.withPassphrase(t)
	if err := e.s.SetInitialPassphrase(e.ctx, "another long passphrase"); err == nil {
		t.Fatal("second initial passphrase accepted")
	}
	var h string
	e.db.R.QueryRow(`SELECT hash FROM passphrase`).Scan(&h)
	if h == "" || h == testPass || len(h) < 40 {
		t.Fatalf("stored hash looks wrong: %q", h)
	}
}

// Gate-bypass: only an operator-scope caller with the passphrase gets an elevation.
func TestStepUpRefusesNonHumanCredentials(t *testing.T) {
	e := newEnv(t).withPassphrase(t)
	for _, p := range []Principal{
		{Kind: KindGuard, CredID: "crd_g"},
		{Kind: KindLease, CredID: "lse_1"},
		{Kind: KindMCP, CredID: "crd_m"},
		{Kind: KindNone},
	} {
		if _, err := e.s.StepUp(e.ctx, p, testPass, ""); code(err) != "wrong_scope" {
			t.Fatalf("%s got an elevation: %v", p.Kind, err)
		}
	}
	if n := e.auditCount(t, "auth.bypass_attempt"); n != 3 {
		t.Fatalf("bypass attempts audited = %d, want 3", n)
	}
}

func TestStepUpWrongPassphraseIsLimited(t *testing.T) {
	e := newEnv(t).withPassphrase(t)
	p := e.dashboard(t)
	_, err := e.s.StepUp(e.ctx, p, "wrong passphrase!!", "")
	ae := AsError(err)
	if ae == nil || ae.Code != "stepup_failed" || ae.Extra["attempts_left"] != 4 {
		t.Fatalf("first failure: %v", err)
	}
	// Right away again: the 1 s delay applies, even with the right passphrase.
	if _, err := e.s.StepUp(e.ctx, p, testPass, ""); code(err) != "rate_limited" {
		t.Fatalf("no delay after a failure: %v", err)
	}
	for i := 0; i < 4; i++ {
		e.clock.Add(time.Duration(1<<i)*time.Second + time.Millisecond)
		if _, err := e.s.StepUp(e.ctx, p, "wrong passphrase!!", ""); code(err) != "stepup_failed" {
			t.Fatalf("failure %d: %v", i+2, err)
		}
	}
	if e.auditCount(t, "stepup.failed") != 5 {
		t.Fatal("failures not audited")
	}
	_, err = e.s.StepUp(e.ctx, p, testPass, "")
	ae = AsError(err)
	if ae == nil || ae.Code != "rate_limited" || ae.Extra["retry_after_s"] != 16 {
		t.Fatalf("after 5 failures: %v %+v, want 16 s delay", err, ae)
	}
	e.clock.Add(16*time.Second + time.Millisecond)
	if _, err := e.s.StepUp(e.ctx, p, testPass, ""); err != nil {
		t.Fatalf("after the delay: %v", err)
	}
	if e.count(t, `SELECT count(*) FROM stepup_failures`) != 0 {
		t.Fatal("success did not clear failures")
	}
}

func TestElevationRules(t *testing.T) {
	e := newEnv(t).withPassphrase(t)
	p := e.dashboard(t)
	other := e.dashboard(t)
	cli := e.operator(t)
	use := func(p Principal, secret, approval string, window bool) error {
		return e.s.tx(e.ctx, func(t *Tx) error { _, err := e.s.useElevation(e.ctx, t, p, secret, approval, window); return err })
	}
	win, err := e.s.StepUp(e.ctx, p, testPass, "")
	if err != nil || win.Kind != "window" || !win.ExpiresAt.Equal(e.clock.Now().Add(5*time.Minute)) {
		t.Fatalf("window: %+v %v", win, err)
	}
	if err := use(p, win.Secret, "", true); err != nil {
		t.Fatalf("window use: %v", err)
	}
	if err := use(p, win.Secret, "", true); err != nil {
		t.Fatalf("window reuse inside the window: %v", err)
	}
	cases := []struct {
		name   string
		p      Principal
		secret string
		window bool
	}{
		{"window where a fresh step-up is needed", p, win.Secret, false},
		{"another session", other, win.Secret, true},
		{"a CLI token", cli, win.Secret, true},
		{"a guard token", Principal{Kind: KindGuard}, win.Secret, true},
		{"a lease", Principal{Kind: KindLease}, win.Secret, true},
		{"empty", p, "", true},
		{"forged", p, NewSecret(PrefixElevation), true},
	}
	for _, c := range cases {
		if err := use(c.p, c.secret, "", c.window); err == nil {
			t.Fatalf("%s: accepted", c.name)
		}
	}
	e.clock.Add(5*time.Minute + time.Second)
	if err := use(p, win.Secret, "", true); code(err) != "stepup_required" {
		t.Fatalf("expired window: %v", err)
	}
	// Revoke ends a window early.
	w2, _ := e.s.StepUp(e.ctx, p, testPass, "")
	e.s.RevokeElevation(e.ctx, p, w2.Secret)
	if err := use(p, w2.Secret, "", true); code(err) != "stepup_required" {
		t.Fatalf("revoked window: %v", err)
	}
	// A window set to 0 minutes gives a one-shot elevation.
	if err := e.s.PatchSettings(e.ctx, p, mustStepUp(t, e, p), map[string]int{"elevation_window_min": 0}); err != nil {
		t.Fatal(err)
	}
	e.clock.Add(2 * time.Second)
	one, _ := e.s.StepUp(e.ctx, p, testPass, "")
	if err := use(p, one.Secret, "", true); err != nil {
		t.Fatal(err)
	}
	if err := use(p, one.Secret, "", true); code(err) != "stepup_required" {
		t.Fatalf("one-shot reused: %v", err)
	}
}

func mustStepUp(t *testing.T, e *env, p Principal) string {
	t.Helper()
	r, err := e.s.StepUp(e.ctx, p, testPass, "")
	if err != nil {
		t.Fatal(err)
	}
	return r.Secret
}

func TestSecuritySettingsNeedElevation(t *testing.T) {
	e := newEnv(t).withPassphrase(t)
	p := e.dashboard(t)
	if err := e.s.PatchSettings(e.ctx, p, "", map[string]int{"max_concurrent": 3}); code(err) != "stepup_required" {
		t.Fatalf("security key without elevation: %v", err)
	}
	if err := e.s.PatchSettings(e.ctx, p, "", map[string]int{"log_retention_days": 30}); err != nil {
		t.Fatalf("plain key: %v", err)
	}
	if err := e.s.PatchSettings(e.ctx, p, "", map[string]int{"hard_cap": 99}); code(err) != "validation_failed" {
		t.Fatalf("hard cap changed: %v", err)
	}
	if err := e.s.PatchSettings(e.ctx, p, mustStepUp(t, e, p), map[string]int{"max_concurrent": 9}); code(err) != "validation_failed" {
		t.Fatalf("over the hard cap: %v", err)
	}
}

func TestAuditRewriteIsCaughtByCheckpoint(t *testing.T) {
	e := newEnv(t).withPassphrase(t)
	p := e.dashboard(t)
	mustStepUp(t, e, p)
	// Someone with file access rewrites the whole chain consistently and drops the checkpoints.
	for _, q := range []string{`DROP TRIGGER audit_no_delete`, `DROP TRIGGER audit_chain_link`, `DELETE FROM audit_checkpoints`, `DELETE FROM audit`} {
		if _, err := e.db.W.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	e.db.Tx(e.ctx, func(tx *sqlTx) error {
		_, err := store.AppendAudit(e.ctx, tx, store.AuditRecord{Event: "forged.history", ActorScope: "system"})
		return err
	})
	if st := e.s.AuditStatus(e.ctx); st.State != "verified" {
		t.Fatalf("hash chain alone should look fine here, got %+v", st)
	}
	e.clock.Add(time.Second)
	sec := mustStepUp(t, e, p)
	if st := e.s.AuditStatus(e.ctx); st.State != "broken" {
		t.Fatalf("checkpoint mismatch not reported: %+v", st)
	}
	st, err := e.s.AcknowledgeAudit(e.ctx, p, sec)
	if err != nil || st.State != "verified" {
		t.Fatalf("acknowledge: %+v %v", st, err)
	}
}

// Gate-bypass (review H1): new connections, new sessions and the CLI token
// share one limit, so guessing cannot reset it.
func TestStepUpLimitIsGlobal(t *testing.T) {
	e := newEnv(t).withPassphrase(t)
	cli := e.operator(t)
	for i := 0; i < 5; i++ {
		p := e.dashboard(t)
		if i%2 == 1 {
			p = cli
		}
		p.Source = "127.0.0.1:" + itoa(int64(40000+i))
		e.clock.Add(time.Duration(1<<i)*time.Second + time.Millisecond)
		_, err := e.s.StepUp(e.ctx, p, "wrong passphrase!!", "")
		if ae := AsError(err); ae == nil || ae.Code != "stepup_failed" || ae.Extra["attempts_left"] != 4-i {
			t.Fatalf("attempt %d: %v %+v", i+1, err, ae)
		}
	}
	fresh := e.dashboard(t)
	fresh.Source = "127.0.0.1:49999"
	if _, err := e.s.StepUp(e.ctx, fresh, testPass, ""); code(err) != "rate_limited" {
		t.Fatalf("a new session reset the limit: %v", err)
	}
	items, _ := e.s.NeedsAttention(e.ctx)
	if len(items) == 0 || items[0].Kind != "stepup_failures" {
		t.Fatalf("no needs-attention item after 5 failures: %+v", items)
	}
}

// Gate-bypass (review H1, M4): parallel guesses are checked one at a time,
// so each one sees the failures before it.
func TestStepUpParallelGuessesAreSerialized(t *testing.T) {
	e := newEnv(t).withPassphrase(t)
	errs := make(chan error, 8)
	ops := make([]Principal, 8)
	for i := range ops {
		ops[i] = e.operator(t)
	}
	for i := 0; i < 8; i++ {
		go func() {
			_, err := e.s.StepUp(e.ctx, ops[i], "wrong passphrase!!", "")
			errs <- err
		}()
	}
	failed := 0
	for i := 0; i < 8; i++ {
		if code(<-errs) == "stepup_failed" {
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("%d guesses were checked without waiting for the delay, want 1", failed)
	}
}

// Gate-bypass (review H2): passphrase rotation is not a guessing oracle.
func TestRotationIsNoPassphraseOracle(t *testing.T) {
	e := newEnv(t).withPassphrase(t)
	p := e.operator(t)
	wrong := e.s.RotatePassphrase(e.ctx, p, "", "wrong passphrase!!", "a new long passphrase")
	right := e.s.RotatePassphrase(e.ctx, p, "", testPass, "a new long passphrase")
	if code(wrong) != "stepup_required" || code(right) != "stepup_required" {
		t.Fatalf("without an elevation: wrong=%v right=%v, want the same stepup_required", wrong, right)
	}
	if e.count(t, `SELECT count(*) FROM stepup_failures`) != 0 {
		t.Fatal("the passphrase was checked before the elevation")
	}
	d := e.dashboard(t)
	el := mustStepUp(t, e, d)
	if err := e.s.RotatePassphrase(e.ctx, d, el, "wrong passphrase!!", "a new long passphrase"); code(err) != "stepup_failed" {
		t.Fatalf("wrong current: %v", err)
	}
	if e.auditCount(t, "stepup.failed") != 1 {
		t.Fatal("wrong current passphrase not audited")
	}
	e.clock.Add(2 * time.Second)
	if err := e.s.RotatePassphrase(e.ctx, d, el, testPass, "a new long passphrase"); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if err := e.s.tx(e.ctx, func(tx *Tx) error { return e.s.RequireElevation(e.ctx, tx, d, el) }); code(err) != "stepup_required" {
		t.Fatalf("elevation survived rotation: %v", err)
	}
	if _, err := e.s.StepUp(e.ctx, d, "a new long passphrase", ""); err != nil {
		t.Fatalf("new passphrase: %v", err)
	}
}

// Review M1: a failed checkpoint is recorded once; after an acknowledgement
// approvals can resume.
func TestFailedCheckpointCanBeAcknowledged(t *testing.T) {
	e := newEnv(t).withPassphrase(t)
	p := e.dashboard(t)
	mustStepUp(t, e, p)
	e.db.W.Exec(`UPDATE audit_checkpoints SET hmac = 'forged'`)
	e.clock.Add(time.Second)
	el := mustStepUp(t, e, p)
	if e.s.AuditStatus(e.ctx).State != "broken" {
		t.Fatal("forged checkpoint not reported")
	}
	if _, err := e.s.AcknowledgeAudit(e.ctx, p, el); err != nil {
		t.Fatal(err)
	}
	e.clock.Add(time.Second)
	mustStepUp(t, e, p)
	if st := e.s.AuditStatus(e.ctx); st.State != "verified" || e.auditCount(t, "audit.chain_broken") != 1 {
		t.Fatalf("after acknowledging: %+v, breaks %d", st, e.auditCount(t, "audit.chain_broken"))
	}
}

// A looping agent cannot grow the audit chain without bound: refusal
// records are capped per minute, and the next record counts what was skipped.
func TestRefusalRecordsAreCapped(t *testing.T) {
	e := newEnv(t)
	op := e.operator(t)
	for range refusalsPerMinute + 25 {
		e.s.AuditStepUpRefused(e.ctx, op, "PATCH /settings", "no elevation")
	}
	if n := e.auditCount(t, "stepup.refused"); n != refusalsPerMinute {
		t.Fatalf("%d records in one minute, want %d", n, refusalsPerMinute)
	}
	e.clock.Add(time.Minute)
	e.s.AuditStepUpRefused(e.ctx, op, "PATCH /settings", "no elevation")
	var d string
	e.db.R.QueryRow(`SELECT detail_json FROM audit WHERE event = 'stepup.refused' ORDER BY seq DESC LIMIT 1`).Scan(&d)
	if !strings.Contains(d, `"refusals_not_recorded":25`) {
		t.Fatalf("the skipped refusals are not counted: %s", d)
	}
}

// Flooding operator-side refusals cannot hide an agent's bypass attempt.
func TestRefusalFloodDoesNotHideBypass(t *testing.T) {
	e := newEnv(t)
	op := e.operator(t)
	for range refusalsPerMinute + 10 {
		e.s.AuditStepUpRefused(e.ctx, op, "PATCH /settings", "no elevation")
	}
	e.s.AuditRejected(e.ctx, Principal{Kind: KindLease, CredID: "lse_x"}, "POST /approvals/{id}/decision")
	if n := e.auditCount(t, "auth.bypass_attempt"); n != 1 {
		t.Fatalf("bypass attempt recorded %d times after a refusal flood, want 1", n)
	}
}
