package core

import (
	"strings"
	"testing"
	"time"

	"waves/internal/store"
)

// An offline reset needs no old passphrase, so it must never read as a human decision,
// must name what it replaced, and must stay on the attention list until a human acknowledges it.
func TestPassphraseResetIsNotAHumanDecisionAndStaysUntilAcknowledged(t *testing.T) {
	e := newEnv(t).withPassphrase(t)
	p := e.dashboard(t)
	e.clock.Add(time.Second)
	mustStepUp(t, e, p) // writes a checkpoint under the old passphrase
	var head int64
	var headHash string
	if err := e.db.R.QueryRow(`SELECT seq, hash FROM audit ORDER BY seq DESC LIMIT 1`).Scan(&head, &headHash); err != nil {
		t.Fatal(err)
	}
	checkpoints := e.count(t, `SELECT count(*) FROM audit_checkpoints`)
	if checkpoints == 0 {
		t.Fatal("no checkpoints to replace; the test setup is wrong")
	}
	const next = "a different passphrase 2" // guard:allow fake test fixture
	if err := e.s.ResetPassphrase(e.ctx, next); err != nil {
		t.Fatal(err)
	}

	var scope, detail string
	if err := e.db.R.QueryRow(`SELECT actor_scope, detail_json FROM audit WHERE event = 'passphrase.reset'`).Scan(&scope, &detail); err != nil {
		t.Fatal(err)
	}
	if scope != "local_offline" {
		t.Errorf("reset recorded with scope %q, want local_offline", scope)
	}
	for _, want := range []string{`"previous_head_seq":` + itoa(head), headHash, `"checkpoints_replaced":` + itoa(int64(checkpoints))} {
		if !strings.Contains(detail, want) {
			t.Errorf("reset record lacks %s: %s", want, detail)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM audit WHERE event = 'passphrase.reset' AND actor_scope = 'human'`); n != 0 {
		t.Errorf("%d reset records claim the human scope", n)
	}
	if st := e.s.AuditStatus(e.ctx); st.State != "verified" {
		t.Errorf("the chain must still verify after a reset: %+v", st)
	}

	listed := func() bool {
		items, err := e.s.NeedsAttention(e.ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, i := range items {
			if i.Kind == "passphrase_reset" {
				return true
			}
		}
		return false
	}
	if !listed() {
		t.Fatal("a passphrase reset is not on the attention list")
	}
	e.clock.Add(time.Minute)
	if _, err := e.s.StepUp(e.ctx, p, testPass, ""); code(err) != "stepup_failed" {
		t.Fatalf("the old passphrase still works after a reset: %v", err)
	}
	e.clock.Add(10 * time.Minute)
	r, err := e.s.StepUp(e.ctx, p, next, "")
	if err != nil {
		t.Fatal(err)
	}
	if !listed() {
		t.Fatal("stepping up must not clear the alert")
	}
	if _, err := e.s.AcknowledgeAudit(e.ctx, p, "wrong"); err == nil {
		t.Fatal("acknowledging without a valid elevation succeeded")
	}
	if !listed() {
		t.Fatal("a refused acknowledgement cleared the alert")
	}
	if _, err := e.s.AcknowledgeAudit(e.ctx, p, r.Secret); err != nil {
		t.Fatal(err)
	}
	if listed() {
		t.Error("the alert stays after a human acknowledged it")
	}
	if n := e.count(t, `SELECT count(*) FROM audit WHERE event = 'passphrase.reset'`); n != 1 {
		t.Errorf("the reset record must stay in the audit log, found %d", n)
	}
}

// A restore brings the backup's passphrase, approvals and audit log, so its record (written by store.Restore) is an offline action
// that stays on the attention list until a human acknowledges it.
func TestRestoreIsRecordedAsLocalOfflineAndStaysUntilAcknowledged(t *testing.T) {
	e := newEnv(t).withPassphrase(t)
	p := e.dashboard(t)
	// What store.Restore writes into the restored file; the service only has to treat it as an alert.
	sha := strings.Repeat("ab", 32)
	if err := e.s.Audit(e.ctx, store.AuditRecord{Event: "db.restored", ActorScope: "local_offline", Detail: map[string]any{"method": "tty", "source_sha256": sha}}); err != nil {
		t.Fatal(err)
	}
	var scope, detail string
	if err := e.db.R.QueryRow(`SELECT actor_scope, detail_json FROM audit WHERE event = 'db.restored'`).Scan(&scope, &detail); err != nil {
		t.Fatal(err)
	}
	if scope != "local_offline" || !strings.Contains(detail, sha) {
		t.Errorf("restore record: scope %q detail %s", scope, detail)
	}
	listed := func() bool {
		items, err := e.s.NeedsAttention(e.ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, i := range items {
			if i.Kind == "database_restored" {
				return true
			}
		}
		return false
	}
	if !listed() {
		t.Fatal("a restore is not on the attention list")
	}
	e.clock.Add(time.Second)
	if _, err := e.s.AcknowledgeAudit(e.ctx, p, mustStepUp(t, e, p)); err != nil {
		t.Fatal(err)
	}
	if listed() {
		t.Error("the alert stays after a human acknowledged it")
	}
}
