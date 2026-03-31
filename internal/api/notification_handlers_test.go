package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"quill/internal/sse"
	"quill/pkg/logger"

	"github.com/gin-gonic/gin"
)

func init() {
	logger.Init("")
}

func TestGetSystemNotifications_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)

	b := sse.NewBroadcaster()
	defer b.Shutdown()
	n := sse.NewNotifier(b)

	handler := &Handler{notifier: n}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/system/notifications", nil)

	handler.GetSystemNotifications(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}

	var resp struct {
		Notifications []sse.NotificationPayload `json:"notifications"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Notifications) != 0 {
		t.Errorf("expected 0 notifications, got %d", len(resp.Notifications))
	}
}

func TestGetSystemNotifications_ReturnsActive(t *testing.T) {
	gin.SetMode(gin.TestMode)

	b := sse.NewBroadcaster()
	defer b.Shutdown()
	n := sse.NewNotifier(b)
	n.SetCooldown(time.Minute)

	// Subscribe so BroadcastGlobal doesn't block.
	ch := make(chan sse.Event, 10)
	b.SubscribeGlobal(ch)
	defer b.UnsubscribeGlobal(ch)

	n.Notify(sse.NotifyOllamaUnreachable, "warning", "Ollama is not running", "open_settings_llm")
	n.Notify(sse.NotifyNoChatModel, "warning", "No chat model available", "")

	handler := &Handler{notifier: n}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/system/notifications", nil)

	handler.GetSystemNotifications(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}

	var resp struct {
		Notifications []sse.NotificationPayload `json:"notifications"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Notifications) != 2 {
		t.Fatalf("expected 2 notifications, got %d", len(resp.Notifications))
	}

	classes := map[string]bool{}
	for _, n := range resp.Notifications {
		classes[n.Class] = true
	}
	if !classes[sse.NotifyOllamaUnreachable] {
		t.Error("missing ollama_unreachable")
	}
	if !classes[sse.NotifyNoChatModel] {
		t.Error("missing no_chat_model")
	}
}

func TestGetSystemNotifications_NilNotifier(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := &Handler{notifier: nil}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/system/notifications", nil)

	handler.GetSystemNotifications(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}

	var resp struct {
		Notifications []any `json:"notifications"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Notifications) != 0 {
		t.Errorf("expected 0 notifications, got %d", len(resp.Notifications))
	}
}
