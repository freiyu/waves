// Package core is the Core Service: authentication and step-up, the single
// guarded transition path, approvals, leases, scheduling and recovery. Every
// state change goes through here; the HTTP API, MCP server and CLI only call it.
package core

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"

	"waves/internal/config"
	"waves/internal/events"
	"waves/internal/store"
)

// Argon holds the passphrase hashing cost. Tests may lower it; nothing may skip it.
type Argon struct {
	Time    uint32
	MemKiB  uint32
	Threads uint8
}

// DefaultArgon is the production cost (t=2, m=19 MiB, p=1).
var DefaultArgon = Argon{Time: 2, MemKiB: 19 * 1024, Threads: 1}

// Options configure a Service.
type Options struct {
	DB      *store.DB
	Bus     *events.Bus
	Dirs    config.Dirs
	Version string
	Argon   Argon
	Now     func() time.Time
	Log     *slog.Logger
}

// Service is the Core Service.
type Service struct {
	db      *store.DB
	bus     *events.Bus
	dirs    config.Dirs
	version string
	argon   Argon
	now     func() time.Time
	log     *slog.Logger
	lc      lifecycle
	passMu  sync.Mutex // one passphrase check at a time
	vr      validatorRuns
	// leasePoke wakes the lease expiry loop.
	leasePoke chan struct{}
	// refusals caps operator-side refusal records, bypasses those of agent
	// credentials, so flooding one kind cannot push the other out of the log.
	refusals refusalLimit
	bypasses refusalLimit
}

// New returns a Service. It does not start background work; see Start.
func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Argon.Time == 0 {
		o.Argon = DefaultArgon
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Bus == nil {
		o.Bus = events.New()
	}
	return &Service{db: o.DB, bus: o.Bus, dirs: o.Dirs, version: o.Version, argon: o.Argon, now: o.Now, log: o.Log,
		vr: validatorRuns{pids: map[int]bool{}, jobs: make(chan string, 64)}, leasePoke: make(chan struct{}, 1)}
}

// Version is the server version string.
func (s *Service) Version() string { return s.version }

// Bus is the event bus the API streams from.
func (s *Service) Bus() *events.Bus { return s.bus }

func (s *Service) ts() string                  { return store.FormatTime(s.now()) }
func (s *Service) tsIn(d time.Duration) string { return store.FormatTime(s.now().Add(d)) }

// tx runs fn in one write transaction and publishes the collected events
// only after the commit succeeded.
func (s *Service) tx(ctx context.Context, fn func(*Tx) error) error {
	t := &Tx{s: s}
	err := s.db.Tx(ctx, func(sqlTx *sql.Tx) error {
		t.Tx = sqlTx
		return fn(t)
	})
	if err != nil {
		return err
	}
	for _, f := range t.after {
		f()
	}
	for _, e := range t.events {
		if f, ok := e.payload.(func() any); ok {
			e.payload = f() // computed after the commit, e.g. the attention count
		}
		s.bus.Publish(e.name, e.payload)
	}
	return nil
}

// Tx is a write transaction with an outbox of events.
type Tx struct {
	*sql.Tx
	s      *Service
	events []outEvent
	after  []func()
}

// afterCommit runs f once the transaction committed.
func (t *Tx) afterCommit(f func()) { t.after = append(t.after, f) }

type outEvent struct {
	name    string
	payload any
}

func (t *Tx) publish(name string, payload any) { t.events = append(t.events, outEvent{name, payload}) }

func (t *Tx) audit(ctx context.Context, r store.AuditRecord) error {
	if r.TS == "" {
		r.TS = t.s.ts()
	}
	_, err := store.AppendAudit(ctx, t.Tx, r)
	return err
}

// Audit appends one record in its own transaction.
func (s *Service) Audit(ctx context.Context, r store.AuditRecord) error {
	return s.tx(ctx, func(t *Tx) error { return t.audit(ctx, r) })
}

// AttentionCount is the number of needs-attention items (badge count).
func (s *Service) AttentionCount(ctx context.Context) int {
	items, err := s.NeedsAttention(ctx)
	if err != nil {
		return 0
	}
	return len(items)
}
