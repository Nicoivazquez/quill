package sse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"quill/pkg/logger"
)

func init() {
	logger.Init("")
}

func TestBroadcaster(t *testing.T) {
	b := NewBroadcaster()
	defer b.Shutdown()

	// Use an httptest.Server for a real TCP connection — avoids the
	// data race that occurs when sharing an httptest.Recorder across
	// goroutines (ServeHTTP writes headers while the test reads them).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.ServeHTTP(w, r)
	}))
	defer srv.Close()

	// Connect as an SSE client with a cancellable context.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL+"/events?job_id=test-job-1", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// 1. Check headers
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Expected Content-Type text/event-stream, got %s", ct)
	}

	// Read the initial connected message.
	buf := make([]byte, 4096)
	n, err := resp.Body.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	initial := string(buf[:n])
	if !strings.Contains(initial, `{"type":"connected", "job_id":"test-job-1"}`) {
		t.Errorf("Expected connected message not found, got: %s", initial)
	}

	// 2. Broadcast a message and verify it arrives.
	testPayload := map[string]string{"status": "completed"}
	b.Broadcast("test-job-1", "status_update", testPayload)

	// Read with a deadline to avoid hanging on failure.
	deadline := time.After(2 * time.Second)
	dataCh := make(chan string, 1)
	go func() {
		n2, err2 := resp.Body.Read(buf)
		if err2 == nil {
			dataCh <- string(buf[:n2])
		} else {
			dataCh <- fmt.Sprintf("read error: %v", err2)
		}
	}()

	var body string
	select {
	case body = <-dataCh:
	case <-deadline:
		t.Fatal("Timed out waiting for broadcast message")
	}

	expectedJSON, _ := json.Marshal(Event{Type: "status_update", Payload: testPayload})
	if !strings.Contains(body, string(expectedJSON)) {
		t.Errorf("Expected message %s not found in body: %s", string(expectedJSON), body)
	}
}
