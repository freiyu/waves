package core

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"waves/internal/config"
	"waves/internal/events"
	"waves/internal/store"
)

// testPass is a fake passphrase for tests only.
const testPass = "correct horse battery" // guard:allow fake test fixture

// fakeClock is a settable clock.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) Add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

type env struct {
	s     *Service
	db    *store.DB
	clock *fakeClock
	dir   string
	ctx   context.Context
}

// newEnv opens a service on a temp database. The passphrase hash cost is
// lowered through configuration; step-up still runs in full.
func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	dirs := config.Dirs{Config: filepath.Join(dir, "cfg"), Data: filepath.Join(dir, "data"), State: filepath.Join(dir, "state")}
	if err := dirs.Ensure(); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), dirs.DB())
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{t: time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)}
	bus := events.New()
	s := New(Options{DB: db, Bus: bus, Dirs: dirs, Version: "test", Argon: Argon{Time: 1, MemKiB: 64, Threads: 1}, Now: clock.Now})
	t.Cleanup(func() { bus.Close(); db.Close() })
	return &env{s: s, db: db, clock: clock, dir: dir, ctx: context.Background()}
}

func (e *env) withPassphrase(t *testing.T) *env {
	t.Helper()
	if err := e.s.SetInitialPassphrase(e.ctx, testPass); err != nil {
		t.Fatal(err)
	}
	return e
}

// dashboard signs in through a login link like the browser does.
func (e *env) dashboard(t *testing.T) Principal {
	t.Helper()
	op := e.operator(t)
	code, _, err := e.s.CreateLoginLink(e.ctx, op)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := e.s.ConsumeLink(e.ctx, code, "127.0.0.1:5000")
	if err != nil {
		t.Fatal(err)
	}
	p, _, ok := e.s.Session(e.ctx, secret, "127.0.0.1:5000")
	if !ok {
		t.Fatal("session did not resolve")
	}
	return p
}

func (e *env) operator(t *testing.T) Principal {
	t.Helper()
	tok, err := e.s.CreateCredential(e.ctx, "operator")
	if err != nil {
		t.Fatal(err)
	}
	p, ok := e.s.Bearer(e.ctx, tok, "127.0.0.1:5001")
	if !ok || p.Kind != KindOperator {
		t.Fatalf("operator token resolved to %+v", p)
	}
	return p
}

func (e *env) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := e.db.R.QueryRowContext(e.ctx, q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func (e *env) auditCount(t *testing.T, event string) int {
	return e.count(t, `SELECT count(*) FROM audit WHERE event = ?`, event)
}

func code(err error) string {
	if e := AsError(err); e != nil {
		return e.Code
	}
	if err != nil {
		return "error:" + err.Error()
	}
	return ""
}

type sqlTx = sql.Tx
