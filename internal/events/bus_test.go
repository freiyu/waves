package events

import (
	"testing"
	"time"
)

func TestReplayAndReset(t *testing.T) {
	b := New()
	defer b.Close()
	for i := 0; i < 3; i++ {
		b.Publish("task.changed", map[string]int{"n": i})
	}
	s, replay, reset := b.Subscribe(1)
	defer b.Unsubscribe(s)
	if reset || len(replay) != 2 || replay[0].ID != 2 {
		t.Fatalf("replay = %v reset = %v, want events 2 and 3", replay, reset)
	}
	if _, _, reset := b.Subscribe(999); !reset {
		t.Fatal("unknown id should reset")
	}
	for i := 0; i < RingSize+5; i++ {
		b.Publish("task.changed", i)
	}
	if _, _, reset := b.Subscribe(1); !reset {
		t.Fatal("id older than the ring should reset")
	}
}

func TestSlowSubscriberIsDropped(t *testing.T) {
	b := New()
	defer b.Close()
	s, _, _ := b.Subscribe(0)
	for i := 0; i < 100; i++ {
		b.Publish("task.changed", i)
	}
	n := 0
	for range s.C {
		n++
	}
	if n != 64 {
		t.Fatalf("got %d events before the drop, want the 64 buffered", n)
	}
}

func TestLogEventsAreCoalesced(t *testing.T) {
	b := New()
	defer b.Close()
	s, _, _ := b.Subscribe(0)
	for i := 1; i <= 20; i++ {
		b.Publish("execution.log", map[string]any{"execution_id": "exe_1", "offset": i})
	}
	var got []string
	deadline := time.After(time.Second)
	for len(got) < 2 {
		select {
		case ev := <-s.C:
			got = append(got, string(ev.Data))
		case <-deadline:
			t.Fatalf("got %v, want 2 events", got)
		}
	}
	if got[0] != `{"execution_id":"exe_1","offset":1}` || got[1] != `{"execution_id":"exe_1","offset":20}` {
		t.Fatalf("coalesced = %v, want first and last offset", got)
	}
}
