package transcription

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mockBundleSyncer records calls to SyncFromFilesystem.
type mockBundleSyncer struct {
	calls   int
	results []BundleSyncResult
	err     error
}

func (m *mockBundleSyncer) SyncFromFilesystem(ctx context.Context) (BundleSyncResult, error) {
	m.calls++
	r := BundleSyncResult{}
	if len(m.results) > 0 {
		r = m.results[0]
		m.results = m.results[1:]
	}
	return r, m.err
}

func TestBundleWatcher_StartStop(t *testing.T) {
	dir := t.TempDir()
	syncer := &mockBundleSyncer{}
	w := NewBundleWatcher(syncer, dir)

	if err := w.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if err := w.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

func TestBundleWatcher_StopWithoutStart(t *testing.T) {
	dir := t.TempDir()
	syncer := &mockBundleSyncer{}
	w := NewBundleWatcher(syncer, dir)

	// Stopping without starting should not panic
	if err := w.Stop(); err != nil {
		t.Fatalf("Stop without start should not error: %v", err)
	}
}

func TestBundleWatcher_TriggersSync(t *testing.T) {
	dir := t.TempDir()
	syncer := &mockBundleSyncer{}
	w := NewBundleWatcher(syncer, dir)

	if err := w.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer w.Stop()

	// Create a bundle directory with metadata.json.
	// Sleep briefly after mkdir so the watcher has time to add the new directory.
	bundleDir := filepath.Join(dir, "test-recording")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(bundleDir, "metadata.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Wait for debounce + sync to fire
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if syncer.calls > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if syncer.calls == 0 {
		t.Error("expected at least 1 sync call after file creation, got 0")
	}
}

func TestBundleWatcher_DebounceCoalesces(t *testing.T) {
	dir := t.TempDir()
	syncer := &mockBundleSyncer{}
	w := NewBundleWatcher(syncer, dir)

	if err := w.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer w.Stop()

	// Rapidly create multiple files — should coalesce into a single sync
	for i := 0; i < 5; i++ {
		path := filepath.Join(dir, "metadata.json")
		_ = os.WriteFile(path, []byte(`{}`), 0o644)
		time.Sleep(10 * time.Millisecond)
	}

	// Wait for debounce window + processing
	time.Sleep(2 * time.Second)

	// Should have coalesced — at most 2 sync calls (not 5)
	if syncer.calls > 2 {
		t.Errorf("expected at most 2 coalesced sync calls, got %d", syncer.calls)
	}
	if syncer.calls == 0 {
		t.Error("expected at least 1 sync call, got 0")
	}
}

func TestBundleWatcher_WatchesSubdirs(t *testing.T) {
	dir := t.TempDir()

	// Create a subfolder before starting the watcher
	subDir := filepath.Join(dir, "Meetings")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	syncer := &mockBundleSyncer{}
	w := NewBundleWatcher(syncer, dir)

	if err := w.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer w.Stop()

	// Write a metadata file inside the subfolder.
	// Sleep briefly after mkdir so the watcher has time to add the new directory.
	bundleDir := filepath.Join(subDir, "sub-recording")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(bundleDir, "metadata.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if syncer.calls > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if syncer.calls == 0 {
		t.Error("expected sync to fire for file in subfolder, got 0 calls")
	}
}

func TestBundleWatcher_IgnoresIrrelevantFiles(t *testing.T) {
	dir := t.TempDir()
	syncer := &mockBundleSyncer{}
	w := NewBundleWatcher(syncer, dir)

	if err := w.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer w.Stop()

	// Write a file that doesn't match bundle patterns
	if err := os.WriteFile(filepath.Join(dir, "random.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Wait longer than the debounce window
	time.Sleep(1500 * time.Millisecond)

	if syncer.calls != 0 {
		t.Errorf("expected 0 sync calls for irrelevant file, got %d", syncer.calls)
	}
}
