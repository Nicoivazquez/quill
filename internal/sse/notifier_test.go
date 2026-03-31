package sse

import (
	"testing"
	"time"
)

func TestNotifier_FirstCallEmits(t *testing.T) {
	b := NewBroadcaster()
	defer b.Shutdown()
	n := NewNotifier(b)

	ch := make(chan Event, 10)
	b.SubscribeGlobal(ch)
	defer b.UnsubscribeGlobal(ch)

	n.Notify(NotifyOllamaUnreachable, "warning", "Ollama is not running", "open_settings_llm")

	select {
	case evt := <-ch:
		if evt.Type != "notification" {
			t.Errorf("event type: got %q, want %q", evt.Type, "notification")
		}
		payload, ok := evt.Payload.(NotificationPayload)
		if !ok {
			t.Fatalf("payload type: got %T, want NotificationPayload", evt.Payload)
		}
		if payload.Class != NotifyOllamaUnreachable {
			t.Errorf("class: got %q, want %q", payload.Class, NotifyOllamaUnreachable)
		}
		if payload.Level != "warning" {
			t.Errorf("level: got %q, want %q", payload.Level, "warning")
		}
		if payload.Message != "Ollama is not running" {
			t.Errorf("message: got %q", payload.Message)
		}
		if payload.Action != "open_settings_llm" {
			t.Errorf("action: got %q, want %q", payload.Action, "open_settings_llm")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for notification event")
	}
}

func TestNotifier_DuplicateSuppressed(t *testing.T) {
	b := NewBroadcaster()
	defer b.Shutdown()
	n := NewNotifier(b)
	n.SetCooldown(time.Minute) // long cooldown

	ch := make(chan Event, 10)
	b.SubscribeGlobal(ch)
	defer b.UnsubscribeGlobal(ch)

	n.Notify(NotifyOllamaUnreachable, "warning", "msg1", "")
	n.Notify(NotifyOllamaUnreachable, "warning", "msg2", "") // should be suppressed

	// Drain first event.
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first event")
	}

	// Second event should NOT arrive.
	select {
	case evt := <-ch:
		t.Errorf("expected suppression, got event: %+v", evt)
	case <-time.After(100 * time.Millisecond):
		// OK — suppressed.
	}
}

func TestNotifier_DifferentClassesIndependent(t *testing.T) {
	b := NewBroadcaster()
	defer b.Shutdown()
	n := NewNotifier(b)
	n.SetCooldown(time.Minute)

	ch := make(chan Event, 10)
	b.SubscribeGlobal(ch)
	defer b.UnsubscribeGlobal(ch)

	n.Notify(NotifyOllamaUnreachable, "warning", "msg1", "")
	n.Notify(NotifyNoChatModel, "warning", "msg2", "")

	received := 0
	for i := 0; i < 2; i++ {
		select {
		case <-ch:
			received++
		case <-time.After(time.Second):
			t.Fatalf("timed out, received %d of 2 events", received)
		}
	}
	if received != 2 {
		t.Errorf("expected 2 events for different classes, got %d", received)
	}
}

func TestNotifier_CooldownExpiry(t *testing.T) {
	b := NewBroadcaster()
	defer b.Shutdown()
	n := NewNotifier(b)
	n.SetCooldown(50 * time.Millisecond)

	ch := make(chan Event, 10)
	b.SubscribeGlobal(ch)
	defer b.UnsubscribeGlobal(ch)

	n.Notify(NotifyNoChatModel, "warning", "first", "")
	<-ch // drain first

	time.Sleep(80 * time.Millisecond) // wait past cooldown

	n.Notify(NotifyNoChatModel, "warning", "second", "")

	select {
	case evt := <-ch:
		payload := evt.Payload.(NotificationPayload)
		if payload.Message != "second" {
			t.Errorf("expected second message, got %q", payload.Message)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for re-emitted event after cooldown")
	}
}

func TestNotifier_ActiveNotifications_Empty(t *testing.T) {
	b := NewBroadcaster()
	defer b.Shutdown()
	n := NewNotifier(b)

	active := n.ActiveNotifications()
	if len(active) != 0 {
		t.Errorf("expected 0 active notifications, got %d", len(active))
	}
}

func TestNotifier_ActiveNotifications_ReturnsRecent(t *testing.T) {
	b := NewBroadcaster()
	defer b.Shutdown()
	n := NewNotifier(b)
	n.SetCooldown(time.Minute) // long cooldown so notifications stay active

	// No subscribers needed — we're testing ActiveNotifications, not SSE delivery.
	// But we need a subscriber so BroadcastGlobal doesn't block.
	ch := make(chan Event, 10)
	b.SubscribeGlobal(ch)
	defer b.UnsubscribeGlobal(ch)

	n.Notify(NotifyOllamaUnreachable, "warning", "msg1", "open_settings_llm")
	n.Notify(NotifyNoChatModel, "warning", "msg2", "")

	active := n.ActiveNotifications()
	if len(active) != 2 {
		t.Fatalf("expected 2 active notifications, got %d", len(active))
	}

	classes := map[string]bool{}
	for _, a := range active {
		classes[a.Class] = true
	}
	if !classes[NotifyOllamaUnreachable] {
		t.Error("missing ollama_unreachable in active notifications")
	}
	if !classes[NotifyNoChatModel] {
		t.Error("missing no_chat_model in active notifications")
	}
}

func TestNotifier_ActiveNotifications_ExpiresAfterCooldown(t *testing.T) {
	b := NewBroadcaster()
	defer b.Shutdown()
	n := NewNotifier(b)
	n.SetCooldown(50 * time.Millisecond)

	ch := make(chan Event, 10)
	b.SubscribeGlobal(ch)
	defer b.UnsubscribeGlobal(ch)

	n.Notify(NotifyLLMCallFailed, "error", "failed", "")

	active := n.ActiveNotifications()
	if len(active) != 1 {
		t.Fatalf("expected 1 active, got %d", len(active))
	}

	time.Sleep(80 * time.Millisecond)

	active = n.ActiveNotifications()
	if len(active) != 0 {
		t.Errorf("expected 0 active after cooldown, got %d", len(active))
	}
}
