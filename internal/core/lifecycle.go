package core

import (
	"context"
	"sync"

	"waves/internal/store"
)

// lifecycle owns the service's background goroutines.
type lifecycle struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Start verifies the audit chain, runs recovery and starts the background
// loops. Stop ends them.
func (s *Service) Start(ctx context.Context) error {
	st := s.AuditStatus(ctx)
	if st.State == "broken" {
		s.log.Warn("audit chain does not verify; approvals are blocked until a human acknowledges it", "record", st.BrokenAt)
		s.bus.Publish("audit.alert", map[string]string{"kind": "chain_broken"})
	}
	if err := s.Audit(ctx, store.AuditRecord{Event: "service.started", ActorScope: "system", Detail: map[string]any{"version": s.version, "audit": st.State}}); err != nil {
		return err
	}
	if err := s.recover(ctx); err != nil {
		return err
	}
	s.lc.mu.Lock()
	defer s.lc.mu.Unlock()
	bg, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.lc.cancel = cancel
	for _, loop := range s.loops() {
		s.lc.wg.Add(1)
		go func() {
			defer s.lc.wg.Done()
			loop(bg)
		}()
	}
	return nil
}

// Stop cancels the background loops and waits for them.
func (s *Service) Stop() {
	s.lc.mu.Lock()
	cancel := s.lc.cancel
	s.lc.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.lc.wg.Wait()
}
