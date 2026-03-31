package sse

import (
	"testing"
	"time"
)

// TestBroadcastGlobal_ReachesGlobalSubscribers verifies that BroadcastGlobal
// sends events to all global subscribers regardless of job ID.
func TestBroadcastGlobal_ReachesGlobalSubscribers(t *testing.T) {
	b := NewBroadcaster()
	defer b.Shutdown()

	// Subscribe globally (no job ID filter)
	ch := make(chan Event, 10)
	b.SubscribeGlobal(ch)
	defer b.UnsubscribeGlobal(ch)

	// Broadcast a global event
	b.BroadcastGlobal("speaker_attention_updated", map[string]string{
		"job_id": "job-123",
	})

	select {
	case evt := <-ch:
		if evt.Type != "speaker_attention_updated" {
			t.Errorf("expected event type speaker_attention_updated, got %q", evt.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for global broadcast event")
	}
}

// TestBroadcastGlobal_DoesNotAffectJobSubscribers verifies that global
// broadcasts are independent from job-scoped broadcasts.
func TestBroadcastGlobal_DoesNotAffectJobSubscribers(t *testing.T) {
	b := NewBroadcaster()
	defer b.Shutdown()

	// Subscribe to a specific job
	jobCh := make(chan Event, 10)
	b.register <- Subscription{JobID: "job-456", Channel: jobCh}

	// Subscribe globally
	globalCh := make(chan Event, 10)
	b.SubscribeGlobal(globalCh)
	defer b.UnsubscribeGlobal(globalCh)

	// Give channels time to register
	time.Sleep(50 * time.Millisecond)

	// Broadcast job-specific event — should reach job subscriber, not global
	b.Broadcast("job-456", "progress", map[string]int{"percent": 50})

	select {
	case <-jobCh:
		// expected
	case <-time.After(2 * time.Second):
		t.Fatal("job subscriber did not receive job-scoped event")
	}

	// Global subscriber should NOT receive job-scoped events
	select {
	case evt := <-globalCh:
		t.Errorf("global subscriber should not receive job-scoped events, got %v", evt)
	case <-time.After(200 * time.Millisecond):
		// expected — no event on global channel
	}
}

// TestBroadcastGlobal_MultipleSubscribers verifies that multiple global
// subscribers all receive the same event.
func TestBroadcastGlobal_MultipleSubscribers(t *testing.T) {
	b := NewBroadcaster()
	defer b.Shutdown()

	ch1 := make(chan Event, 10)
	ch2 := make(chan Event, 10)
	b.SubscribeGlobal(ch1)
	b.SubscribeGlobal(ch2)
	defer b.UnsubscribeGlobal(ch1)
	defer b.UnsubscribeGlobal(ch2)

	b.BroadcastGlobal("test_event", "hello")

	for i, ch := range []chan Event{ch1, ch2} {
		select {
		case evt := <-ch:
			if evt.Type != "test_event" {
				t.Errorf("subscriber %d: expected test_event, got %q", i, evt.Type)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("subscriber %d: timed out waiting for global event", i)
		}
	}
}
