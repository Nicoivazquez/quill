package sse

import (
	"sync"
	"time"

	"quill/pkg/logger"
)

// Notification class constants.
const (
	NotifyLLMNotConfigured  = "llm_not_configured"
	NotifyOllamaUnreachable = "ollama_unreachable"
	NotifyNoChatModel       = "no_chat_model"
	NotifyLLMCallFailed     = "llm_call_failed"
)

// NotificationPayload is the SSE payload for user-facing notifications.
type NotificationPayload struct {
	Class   string    `json:"class"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
	Action  string    `json:"action,omitempty"`
	Since   time.Time `json:"since"`
}

// notificationRecord tracks a recently emitted notification for cooldown and
// active-notification queries.
type notificationRecord struct {
	payload  NotificationPayload
	emittedAt time.Time
}

// Notifier emits user-facing notifications via SSE with per-class throttling.
type Notifier struct {
	broadcaster *Broadcaster
	cooldown    time.Duration
	mu          sync.Mutex
	recent      map[string]notificationRecord // class → last emission
}

// NewNotifier creates a Notifier wrapping the given broadcaster.
// The default cooldown is 5 minutes.
func NewNotifier(b *Broadcaster) *Notifier {
	return &Notifier{
		broadcaster: b,
		cooldown:    5 * time.Minute,
		recent:      make(map[string]notificationRecord),
	}
}

// SetCooldown overrides the default throttle window.
func (n *Notifier) SetCooldown(d time.Duration) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cooldown = d
}

// Notify emits a notification to all global SSE subscribers, unless the same
// class was emitted within the cooldown window.
func (n *Notifier) Notify(class, level, message, action string) {
	now := time.Now()

	n.mu.Lock()
	if rec, ok := n.recent[class]; ok && now.Sub(rec.emittedAt) < n.cooldown {
		n.mu.Unlock()
		logger.Debug("notifier: suppressed (cooldown)", "class", class)
		return
	}

	payload := NotificationPayload{
		Class:   class,
		Level:   level,
		Message: message,
		Action:  action,
		Since:   now,
	}
	n.recent[class] = notificationRecord{payload: payload, emittedAt: now}

	// Evict stale entries (older than 2× cooldown).
	cutoff := now.Add(-2 * n.cooldown)
	for k, rec := range n.recent {
		if rec.emittedAt.Before(cutoff) {
			delete(n.recent, k)
		}
	}
	n.mu.Unlock()

	logger.Info("notifier: emitting", "class", class, "severity", level)
	n.broadcaster.BroadcastGlobal("notification", payload)
}

// ActiveNotifications returns all notifications emitted within the cooldown
// window. This is used by the polling endpoint for API clients.
func (n *Notifier) ActiveNotifications() []NotificationPayload {
	now := time.Now()
	n.mu.Lock()
	defer n.mu.Unlock()

	active := make([]NotificationPayload, 0, len(n.recent))
	for _, rec := range n.recent {
		if now.Sub(rec.emittedAt) < n.cooldown {
			active = append(active, rec.payload)
		}
	}
	return active
}
