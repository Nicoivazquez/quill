package contacts

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

const watcherDebounceDelay = 500 * time.Millisecond

// Watcher watches a vault's contact folders and reindexes on change.
type Watcher struct {
	syncService *SyncService
	fileService *FileService

	watcher *fsnotify.Watcher
	stopCh  chan struct{}
	doneCh  chan struct{}

	mu    sync.Mutex
	timer *time.Timer
}

func NewWatcher(syncService *SyncService, fileService *FileService) *Watcher {
	return &Watcher{
		syncService: syncService,
		fileService: fileService,
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
	}
}

func (w *Watcher) Start() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	base := w.fileService.ContactsBaseAbsPath()
	if err := os.MkdirAll(base, 0o755); err != nil {
		_ = watcher.Close()
		return err
	}
	if err := watcher.Add(base); err != nil {
		_ = watcher.Close()
		return err
	}

	entries, _ := os.ReadDir(base)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		_ = watcher.Add(filepath.Join(base, entry.Name()))
	}

	w.watcher = watcher
	go w.loop()
	return nil
}

func (w *Watcher) Stop() error {
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

func (w *Watcher) loop() {
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
			logger.Warn("contact watcher event error", "error", err)
		}
	}
}

func (w *Watcher) handleEvent(event fsnotify.Event) {
	eventPath := filepath.Clean(event.Name)
	base := filepath.Clean(w.fileService.ContactsBaseAbsPath())
	if !isInsidePath(base, eventPath) {
		return
	}

	if event.Op&fsnotify.Create == fsnotify.Create {
		if info, err := os.Stat(eventPath); err == nil && info.IsDir() {
			_ = w.watcher.Add(eventPath)
		}
	}

	basename := strings.ToLower(filepath.Base(eventPath))
	shouldSync := basename == contactNoteFileName || basename == "people" || strings.Contains(eventPath, string(os.PathSeparator)+"People"+string(os.PathSeparator))
	if !shouldSync {
		return
	}

	w.scheduleSync()
}

func (w *Watcher) scheduleSync() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timer != nil {
		w.timer.Stop()
	}
	w.timer = time.AfterFunc(watcherDebounceDelay, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := w.syncService.SyncFromFilesystem(ctx); err != nil {
			logger.Warn("contact watcher sync failed", "error", err)
		}
	})
}

func isInsidePath(base string, path string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && rel != "")
}
