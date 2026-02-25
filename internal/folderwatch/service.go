package folderwatch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"scriberr/internal/config"
	"scriberr/internal/models"
	"scriberr/internal/repository"

	"github.com/fsnotify/fsnotify"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	fileDebounceDelay = 2 * time.Second
	stabilityInterval = 1 * time.Second
	stabilityChecks   = 3
	importTimeout     = 5 * time.Minute
	minimumAudioBytes = 1
)

var (
	// ErrFolderNotFound means the watched folder does not exist for the user.
	ErrFolderNotFound = errors.New("watched folder not found")
	// ErrFolderAlreadyExists means a user already watches this path.
	ErrFolderAlreadyExists = errors.New("folder is already being watched")
	// ErrInvalidFolderPath means the selected path is invalid or inaccessible.
	ErrInvalidFolderPath = errors.New("invalid folder path")
)

// TaskQueue is the subset of the queue interface required by this service.
type TaskQueue interface {
	EnqueueJob(jobID string) error
}

// FolderView combines persisted folder config with live runtime status.
type FolderView struct {
	Folder           models.WatchedFolder
	Active           bool
	LastRuntimeError string
	LastImportedAt   *time.Time
	LastImportedFile string
}

type runtimeStatus struct {
	Active           bool
	LastRuntimeError string
	LastImportedAt   *time.Time
	LastImportedFile string
}

// Service manages per-user filesystem watchers for desktop auto import.
type Service struct {
	config      *config.Config
	folderRepo  repository.WatchedFolderRepository
	jobRepo     repository.JobRepository
	userRepo    repository.UserRepository
	profileRepo repository.ProfileRepository
	taskQueue   TaskQueue

	mu       sync.RWMutex
	runners  map[uint]*folderRunner
	statuses map[uint]runtimeStatus
}

type fileSignature struct {
	Size    int64
	ModUnix int64
}

type folderRunner struct {
	service *Service
	folder  models.WatchedFolder
	watcher *fsnotify.Watcher

	stopCh chan struct{}
	doneCh chan struct{}

	mu       sync.Mutex
	timers   map[string]*time.Timer
	imported map[string]fileSignature
}

// NewService creates a folder watcher service.
func NewService(
	cfg *config.Config,
	folderRepo repository.WatchedFolderRepository,
	jobRepo repository.JobRepository,
	userRepo repository.UserRepository,
	profileRepo repository.ProfileRepository,
	taskQueue TaskQueue,
) *Service {
	return &Service{
		config:      cfg,
		folderRepo:  folderRepo,
		jobRepo:     jobRepo,
		userRepo:    userRepo,
		profileRepo: profileRepo,
		taskQueue:   taskQueue,
		runners:     make(map[uint]*folderRunner),
		statuses:    make(map[uint]runtimeStatus),
	}
}

// Start restores all enabled folder watchers.
func (s *Service) Start(ctx context.Context) error {
	folders, err := s.folderRepo.FindEnabled(ctx)
	if err != nil {
		return err
	}

	var failures []string
	for _, folder := range folders {
		if err := s.startRunner(folder); err != nil {
			failures = append(failures, fmt.Sprintf("id=%d path=%q: %v", folder.ID, folder.Path, err))
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("failed to restore %d watch folder(s): %s", len(failures), strings.Join(failures, "; "))
	}
	return nil
}

// Stop gracefully shuts down all watchers.
func (s *Service) Stop() {
	s.mu.Lock()
	runners := make([]*folderRunner, 0, len(s.runners))
	for _, runner := range s.runners {
		runners = append(runners, runner)
	}
	s.runners = make(map[uint]*folderRunner)

	for folderID, status := range s.statuses {
		status.Active = false
		s.statuses[folderID] = status
	}
	s.mu.Unlock()

	for _, runner := range runners {
		runner.stop()
	}
}

// ListUserFolders returns all watched folders for a user with runtime state.
func (s *Service) ListUserFolders(ctx context.Context, userID uint) ([]FolderView, error) {
	folders, err := s.folderRepo.FindByUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	views := make([]FolderView, 0, len(folders))
	for _, folder := range folders {
		status := s.getStatus(folder.ID)
		if !folder.Enabled {
			status.Active = false
		}
		views = append(views, FolderView{
			Folder:           folder,
			Active:           status.Active,
			LastRuntimeError: status.LastRuntimeError,
			LastImportedAt:   status.LastImportedAt,
			LastImportedFile: status.LastImportedFile,
		})
	}
	return views, nil
}

// CreateUserFolder creates a new watched folder for a user and starts it when enabled.
func (s *Service) CreateUserFolder(
	ctx context.Context,
	userID uint,
	path string,
	recursive bool,
	enabled bool,
) (*FolderView, error) {
	normalizedPath, err := normalizeFolderPath(path)
	if err != nil {
		return nil, err
	}

	existing, err := s.folderRepo.FindByUserAndPath(ctx, userID, normalizedPath)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, ErrFolderAlreadyExists
	}

	folder := models.WatchedFolder{
		UserID:    userID,
		Path:      normalizedPath,
		Recursive: recursive,
		Enabled:   enabled,
	}
	if err := s.folderRepo.Create(ctx, &folder); err != nil {
		return nil, err
	}

	if folder.Enabled {
		if err := s.startRunner(folder); err != nil {
			_ = s.folderRepo.Delete(ctx, folder.ID)
			s.clearStatus(folder.ID)
			return nil, err
		}
	}

	return s.getFolderView(ctx, userID, folder.ID)
}

// SetUserFolderEnabled toggles an existing watched folder.
func (s *Service) SetUserFolderEnabled(ctx context.Context, userID uint, folderID uint, enabled bool) (*FolderView, error) {
	folder, err := s.folderRepo.FindByUserAndID(ctx, userID, folderID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrFolderNotFound
	}
	if err != nil {
		return nil, err
	}

	if folder.Enabled == enabled {
		return s.getFolderView(ctx, userID, folderID)
	}

	folder.Enabled = enabled
	if err := s.folderRepo.Update(ctx, folder); err != nil {
		return nil, err
	}

	if enabled {
		if err := s.startRunner(*folder); err != nil {
			folder.Enabled = false
			_ = s.folderRepo.Update(ctx, folder)
			return nil, err
		}
	} else {
		s.stopRunner(folderID)
	}

	return s.getFolderView(ctx, userID, folderID)
}

// DeleteUserFolder removes a watched folder for a user.
func (s *Service) DeleteUserFolder(ctx context.Context, userID uint, folderID uint) error {
	_, err := s.folderRepo.FindByUserAndID(ctx, userID, folderID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrFolderNotFound
	}
	if err != nil {
		return err
	}

	s.stopRunner(folderID)
	s.clearStatus(folderID)
	return s.folderRepo.Delete(ctx, folderID)
}

func (s *Service) getFolderView(ctx context.Context, userID uint, folderID uint) (*FolderView, error) {
	folder, err := s.folderRepo.FindByUserAndID(ctx, userID, folderID)
	if err != nil {
		return nil, err
	}

	status := s.getStatus(folderID)
	if !folder.Enabled {
		status.Active = false
	}

	return &FolderView{
		Folder:           *folder,
		Active:           status.Active,
		LastRuntimeError: status.LastRuntimeError,
		LastImportedAt:   status.LastImportedAt,
		LastImportedFile: status.LastImportedFile,
	}, nil
}

func (s *Service) startRunner(folder models.WatchedFolder) error {
	s.mu.Lock()
	if _, exists := s.runners[folder.ID]; exists {
		status := s.statuses[folder.ID]
		status.Active = true
		s.statuses[folder.ID] = status
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	runner := &folderRunner{
		service:  s,
		folder:   folder,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
		timers:   make(map[string]*time.Timer),
		imported: make(map[string]fileSignature),
	}

	if err := runner.start(); err != nil {
		s.setStatus(folder.ID, func(status *runtimeStatus) {
			status.Active = false
			status.LastRuntimeError = err.Error()
		})
		return err
	}

	s.mu.Lock()
	if _, exists := s.runners[folder.ID]; exists {
		s.mu.Unlock()
		runner.stop()
		return nil
	}
	s.runners[folder.ID] = runner
	status := s.statuses[folder.ID]
	status.Active = true
	status.LastRuntimeError = ""
	s.statuses[folder.ID] = status
	s.mu.Unlock()

	return nil
}

func (s *Service) stopRunner(folderID uint) {
	s.mu.Lock()
	runner := s.runners[folderID]
	delete(s.runners, folderID)
	status := s.statuses[folderID]
	status.Active = false
	s.statuses[folderID] = status
	s.mu.Unlock()

	if runner != nil {
		runner.stop()
	}
}

func (s *Service) markRuntimeError(folderID uint, err error) {
	if err == nil {
		return
	}
	s.setStatus(folderID, func(status *runtimeStatus) {
		status.LastRuntimeError = err.Error()
	})
}

func (s *Service) markImported(folderID uint, sourcePath string) {
	now := time.Now()
	s.setStatus(folderID, func(status *runtimeStatus) {
		status.LastRuntimeError = ""
		status.LastImportedAt = &now
		status.LastImportedFile = sourcePath
	})
}

func (s *Service) getStatus(folderID uint) runtimeStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.statuses[folderID]
}

func (s *Service) setStatus(folderID uint, update func(status *runtimeStatus)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.statuses[folderID]
	update(&status)
	s.statuses[folderID] = status
}

func (s *Service) clearStatus(folderID uint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.statuses, folderID)
}

func (r *folderRunner) start() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create filesystem watcher: %w", err)
	}
	r.watcher = watcher

	if err := r.addWatchedPath(r.folder.Path); err != nil {
		_ = r.watcher.Close()
		return err
	}

	go r.run()
	return nil
}

func (r *folderRunner) stop() {
	select {
	case <-r.stopCh:
		return
	default:
		close(r.stopCh)
	}

	if r.watcher != nil {
		_ = r.watcher.Close()
	}

	r.mu.Lock()
	for _, timer := range r.timers {
		timer.Stop()
	}
	r.timers = make(map[string]*time.Timer)
	r.mu.Unlock()

	select {
	case <-r.doneCh:
	case <-time.After(3 * time.Second):
	}
}

func (r *folderRunner) run() {
	defer close(r.doneCh)

	for {
		select {
		case <-r.stopCh:
			return
		case event, ok := <-r.watcher.Events:
			if !ok {
				return
			}
			r.handleEvent(event)
		case err, ok := <-r.watcher.Errors:
			if !ok {
				return
			}
			r.service.markRuntimeError(r.folder.ID, fmt.Errorf("watcher error: %w", err))
		}
	}
}

func (r *folderRunner) handleEvent(event fsnotify.Event) {
	if event.Op&fsnotify.Create == fsnotify.Create {
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() && r.folder.Recursive {
			if err := r.addWatchedPath(event.Name); err != nil {
				r.service.markRuntimeError(r.folder.ID, err)
			}
			return
		}
	}

	if event.Op&(fsnotify.Create|fsnotify.Write) == 0 {
		return
	}
	if !isWatchableAudioFile(event.Name) {
		return
	}

	r.scheduleImport(event.Name)
}

func (r *folderRunner) scheduleImport(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if timer, exists := r.timers[path]; exists {
		timer.Stop()
	}

	r.timers[path] = time.AfterFunc(fileDebounceDelay, func() {
		r.processCandidate(path)
		r.mu.Lock()
		delete(r.timers, path)
		r.mu.Unlock()
	})
}

func (r *folderRunner) processCandidate(path string) {
	signature, err := waitForStableFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		r.service.markRuntimeError(r.folder.ID, err)
		return
	}

	if r.wasImported(path, signature) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), importTimeout)
	defer cancel()

	if err := r.service.importFile(ctx, r.folder.UserID, path); err != nil {
		r.service.markRuntimeError(r.folder.ID, err)
		return
	}

	r.recordImported(path, signature)
	r.service.markImported(r.folder.ID, path)
}

func (r *folderRunner) addWatchedPath(root string) error {
	if err := r.watcher.Add(root); err != nil {
		return fmt.Errorf("failed to watch %q: %w", root, err)
	}

	if !r.folder.Recursive {
		return nil
	}

	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == root || !d.IsDir() {
			return nil
		}
		if watchErr := r.watcher.Add(path); watchErr != nil {
			r.service.markRuntimeError(r.folder.ID, fmt.Errorf("failed to watch subdirectory %q: %w", path, watchErr))
		}
		return nil
	})

	return nil
}

func (r *folderRunner) wasImported(path string, signature fileSignature) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	prev, exists := r.imported[path]
	return exists && prev == signature
}

func (r *folderRunner) recordImported(path string, signature fileSignature) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.imported[path] = signature
}

func waitForStableFile(path string) (fileSignature, error) {
	var prevSize int64 = -1
	var prevModUnix int64 = -1
	stableReads := 0

	for i := 0; i < stabilityChecks+2; i++ {
		info, err := os.Stat(path)
		if err != nil {
			return fileSignature{}, err
		}
		if info.IsDir() {
			return fileSignature{}, fmt.Errorf("cannot import directory %q", path)
		}

		size := info.Size()
		modUnix := info.ModTime().UnixNano()

		if size == prevSize && modUnix == prevModUnix {
			stableReads++
		} else {
			stableReads = 0
		}

		prevSize = size
		prevModUnix = modUnix

		if stableReads >= stabilityChecks && size >= minimumAudioBytes {
			return fileSignature{
				Size:    size,
				ModUnix: modUnix,
			}, nil
		}

		time.Sleep(stabilityInterval)
	}

	return fileSignature{}, fmt.Errorf("file %q did not stabilize before timeout", path)
}

func normalizeFolderPath(path string) (string, error) {
	cleaned := strings.TrimSpace(path)
	if cleaned == "" {
		return "", fmt.Errorf("%w: empty path", ErrInvalidFolderPath)
	}

	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidFolderPath, err)
	}
	normalized := filepath.Clean(absPath)

	info, err := os.Stat(normalized)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: path does not exist", ErrInvalidFolderPath)
		}
		return "", fmt.Errorf("%w: %v", ErrInvalidFolderPath, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: path is not a directory", ErrInvalidFolderPath)
	}

	return normalized, nil
}

func isWatchableAudioFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".mp3", ".wav", ".flac", ".m4a", ".aac", ".ogg", ".wma", ".aiff", ".mp4", ".mov", ".mkv", ".webm", ".avi":
		return true
	default:
		return false
	}
}

func (s *Service) importFile(ctx context.Context, userID uint, sourcePath string) error {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to access source file: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("source path %q is a directory", sourcePath)
	}
	if !isWatchableAudioFile(sourcePath) {
		return fmt.Errorf("unsupported file type for %q", sourcePath)
	}

	if err := os.MkdirAll(s.config.UploadDir, 0755); err != nil {
		return fmt.Errorf("failed to create upload directory: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(sourcePath))
	jobID := uuid.New().String()
	destPath := filepath.Join(s.config.UploadDir, jobID+ext)

	if err := copyFile(sourcePath, destPath); err != nil {
		return fmt.Errorf("failed to copy file for import: %w", err)
	}

	title := filepath.Base(sourcePath)
	job := models.TranscriptionJob{
		ID:        jobID,
		AudioPath: destPath,
		Status:    models.StatusUploaded,
		Title:     &title,
	}

	if err := s.jobRepo.Create(ctx, &job); err != nil {
		_ = os.Remove(destPath)
		return fmt.Errorf("failed to create transcription job: %w", err)
	}

	s.maybeQueueAutoTranscription(ctx, userID, &job)
	return nil
}

func (s *Service) maybeQueueAutoTranscription(ctx context.Context, userID uint, job *models.TranscriptionJob) {
	user, err := s.userRepo.FindByID(ctx, userID)
	if err != nil || !user.AutoTranscriptionEnabled {
		return
	}

	var profile *models.TranscriptionProfile
	if user.DefaultProfileID != nil {
		profile, _ = s.profileRepo.FindByID(ctx, *user.DefaultProfileID)
	}
	if profile == nil {
		profile, _ = s.profileRepo.FindDefault(ctx)
	}
	if profile == nil {
		profiles, _, listErr := s.profileRepo.List(ctx, 0, 1)
		if listErr == nil && len(profiles) > 0 {
			profile = &profiles[0]
		}
	}
	if profile == nil {
		return
	}

	job.Parameters = profile.Parameters
	job.Diarization = profile.Parameters.Diarize
	job.Status = models.StatusPending

	if err := s.jobRepo.Update(ctx, job); err != nil {
		job.Status = models.StatusUploaded
		_ = s.jobRepo.Update(ctx, job)
		return
	}

	if err := s.taskQueue.EnqueueJob(job.ID); err != nil {
		job.Status = models.StatusUploaded
		_ = s.jobRepo.Update(ctx, job)
	}
}

func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return err
	}
	return destFile.Sync()
}
