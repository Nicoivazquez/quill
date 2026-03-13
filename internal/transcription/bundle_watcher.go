package transcription

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"quill/pkg/logger"

	"github.com/fsnotify/fsnotify"
)

const bundleWatcherDebounceDelay = 500 * time.Millisecond

// bundleRelevantFiles are filenames that indicate bundle changes worth syncing.
var bundleRelevantFiles = map[string]bool{
	"metadata.json":   true,
	"transcript.json": true,
	"transcript.md":   true,
}

// BundleSyncer is the interface for triggering a filesystem sync.
type BundleSyncer interface {
	SyncFromFilesystem(ctx context.Context) (BundleSyncResult, error)
}

// BundleWatcher watches a Transcripts directory for changes and triggers sync.
type BundleWatcher struct {
	syncer         BundleSyncer
	transcriptsDir string

	watcher *fsnotify.Watcher
	stopCh  chan struct{}
	doneCh  chan struct{}

	mu    sync.Mutex
	timer *time.Timer
}

// NewBundleWatcher creates a new bundle watcher.
func NewBundleWatcher(syncer BundleSyncer, transcriptsDir string) *BundleWatcher {
	return &BundleWatcher{
		syncer:         syncer,
		transcriptsDir: transcriptsDir,
	}
}

// Start begins watching the transcripts directory for filesystem changes.
func (w *BundleWatcher) Start() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(w.transcriptsDir, 0o755); err != nil {
		_ = watcher.Close()
		return err
	}
	if err := watcher.Add(w.transcriptsDir); err != nil {
		_ = watcher.Close()
		return err
	}

	// Watch existing subdirectories (folder-level directories)
	w.addSubdirs(watcher, w.transcriptsDir)

	w.stopCh = make(chan struct{})
	w.doneCh = make(chan struct{})
	w.watcher = watcher
	go w.loop()
	return nil
}

// Stop gracefully shuts down the watcher.
func (w *BundleWatcher) Stop() error {
	if w.stopCh == nil {
		return nil
	}
	close(w.stopCh)
	<-w.doneCh

	w.mu.Lock()
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	w.mu.Unlock()

	if w.watcher != nil {
		return w.watcher.Close()
	}
	return nil
}

func (w *BundleWatcher) loop() {
	defer close(w.doneCh)
	for {
		select {
		case <-w.stopCh:
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			w.handleEvent(event)
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			logger.Warn("bundle watcher error", "error", err)
		}
	}
}

func (w *BundleWatcher) handleEvent(event fsnotify.Event) {
	eventPath := filepath.Clean(event.Name)
	base := filepath.Clean(w.transcriptsDir)
	if !isInsideBundlePath(base, eventPath) {
		return
	}

	// Auto-watch new directories
	if event.Op&fsnotify.Create == fsnotify.Create {
		if info, err := os.Stat(eventPath); err == nil && info.IsDir() {
			_ = w.watcher.Add(eventPath)
		}
	}

	// Only trigger sync for bundle-relevant files or audio files
	basename := strings.ToLower(filepath.Base(eventPath))
	if !bundleRelevantFiles[basename] && !strings.HasPrefix(basename, "audio.") {
		return
	}

	w.scheduleSync()
}

func (w *BundleWatcher) scheduleSync() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timer != nil {
		w.timer.Stop()
	}
	w.timer = time.AfterFunc(bundleWatcherDebounceDelay, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if result, err := w.syncer.SyncFromFilesystem(ctx); err != nil {
			logger.Warn("bundle watcher sync failed", "error", err)
		} else if result.Imported+result.Updated+result.Deleted > 0 {
			logger.Info("bundle watcher sync",
				"imported", result.Imported,
				"updated", result.Updated,
				"deleted", result.Deleted,
				"skipped", result.Skipped,
			)
		}
	})
}

func (w *BundleWatcher) addSubdirs(watcher *fsnotify.Watcher, dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		subPath := filepath.Join(dir, entry.Name())
		_ = watcher.Add(subPath)
		// Also watch bundle dirs inside folders
		w.addSubdirs(watcher, subPath)
	}
}

func isInsideBundlePath(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && rel != "")
}
