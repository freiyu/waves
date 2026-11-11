// Package events is the in-process event bus behind GET /events. Events are
// small notifications; clients refetch what they show. A bounded ring lets a
// reconnecting client replay from Last-Event-ID.
package events

import (
	"encoding/json"
	"sync"
	"time"
)

// RingSize is how many events a reconnecting client can replay.
const RingSize = 500

// Event is one published notification.
type Event struct {
	ID   uint64
	Name string
	Data []byte
}

// Sub is one subscriber. C is closed when the bus drops the subscriber
// (it fell behind) or the bus closes.
type Sub struct {
	C    <-chan Event
	c    chan Event
	gone bool
}

// Bus fans events out to subscribers. The zero value is not usable; use New.
type Bus struct {
	mu      sync.Mutex
	ring    []Event
	next    uint64
	subs    map[*Sub]struct{}
	pending map[string]*pendingLog
	closed  bool
	window  time.Duration
}

type pendingLog struct {
	data  []byte
	timer *time.Timer
	last  time.Time
}

// New returns an empty bus.
func New() *Bus {
	return &Bus{subs: map[*Sub]struct{}{}, pending: map[string]*pendingLog{}, next: 1, window: 250 * time.Millisecond}
}

// Publish sends name with payload (JSON-encoded) to all subscribers.
// execution.log events are coalesced to at most one per execution per 250 ms;
// the last offset wins.
func (b *Bus) Publish(name string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return // payloads are plain structs; this cannot fail in practice
	}
	if name == "execution.log" {
		var k struct {
			ExecutionID string `json:"execution_id"`
		}
		_ = json.Unmarshal(data, &k)
		b.coalesce(k.ExecutionID, data)
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.emitLocked(name, data)
}

func (b *Bus) coalesce(key string, data []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	p := b.pending[key]
	if p == nil {
		p = &pendingLog{}
		b.pending[key] = p
	}
	p.data = data
	if p.timer != nil {
		return // a send is already scheduled; it will carry the newest offset
	}
	wait := b.window - time.Since(p.last)
	if wait <= 0 {
		p.last = time.Now()
		b.emitLocked("execution.log", data)
		return
	}
	p.timer = time.AfterFunc(wait, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		p.timer = nil
		p.last = time.Now()
		if !b.closed {
			b.emitLocked("execution.log", p.data)
		}
	})
}

func (b *Bus) emitLocked(name string, data []byte) {
	if b.closed {
		return
	}
	ev := Event{ID: b.next, Name: name, Data: data}
	b.next++
	if len(b.ring) == RingSize {
		copy(b.ring, b.ring[1:])
		b.ring = b.ring[:RingSize-1]
	}
	b.ring = append(b.ring, ev)
	for s := range b.subs {
		select {
		case s.c <- ev:
		default:
			// The client fell behind its buffer: drop it; it reconnects and replays or resets.
			b.dropLocked(s)
		}
	}
}

func (b *Bus) dropLocked(s *Sub) {
	if s.gone {
		return
	}
	s.gone = true
	delete(b.subs, s)
	close(s.c)
}

// Subscribe registers a subscriber. With lastID > 0 it returns the events
// after lastID from the ring; reset is true when they are no longer all there.
func (b *Bus) Subscribe(lastID uint64) (s *Sub, replay []Event, reset bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := make(chan Event, 64)
	s = &Sub{C: c, c: c}
	if b.closed {
		s.gone = true
		close(c)
		return s, nil, false
	}
	b.subs[s] = struct{}{}
	if lastID > 0 {
		if lastID >= b.next {
			return s, nil, true // the id is from another server run
		}
		if len(b.ring) == 0 || b.ring[0].ID > lastID+1 {
			return s, nil, true
		}
		for _, ev := range b.ring {
			if ev.ID > lastID {
				replay = append(replay, ev)
			}
		}
	}
	return s, replay, false
}

// Unsubscribe removes s.
func (b *Bus) Unsubscribe(s *Sub) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.dropLocked(s)
}

// Close stops pending timers and closes every subscriber.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	for _, p := range b.pending {
		if p.timer != nil {
			p.timer.Stop()
		}
	}
	for s := range b.subs {
		b.dropLocked(s)
	}
}
