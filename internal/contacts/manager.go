package contacts

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"quill/internal/models"
	"quill/internal/repository"
	"quill/pkg/logger"

	"gorm.io/gorm"
)

const signatureRetryScanInterval = 2 * time.Minute

type embeddingRunner interface {
	Start()
	Stop()
	Enqueue(contactID uint)
}

type RetryScanResult struct {
	Queued          int `json:"queued"`
	FailedDue       int `json:"failed_due"`
	StaleProcessing int `json:"stale_processing"`
	Skipped         int `json:"skipped"`
}

// Manager coordinates contact sync, watcher lifecycle, and embedding jobs.
type Manager struct {
	db          *gorm.DB
	repo        repository.ContactRepository
	whisperXEnv string

	mu            sync.RWMutex
	activeVaultID uint
	activePath    string
	fileService   *FileService
	syncService   *SyncService
	watcher       *Watcher

	embeddingWorker  embeddingRunner
	workerStarted    bool
	retryStop        chan struct{}
	retryWG          sync.WaitGroup
	retryStarted     bool
	retroactiveScan  *RetroactiveScanService
}

func NewManager(db *gorm.DB, repo repository.ContactRepository, whisperXEnv string) *Manager {
	worker := NewEmbeddingWorker(db, repo, whisperXEnv)

	jobRepo := repository.NewJobRepository(db)
	speakerMapRepo := repository.NewSpeakerMappingRepository(db)
	retroScan := NewRetroactiveScanService(jobRepo, repo, speakerMapRepo, db)

	m := &Manager{
		db:              db,
		repo:            repo,
		whisperXEnv:     strings.TrimSpace(whisperXEnv),
		embeddingWorker: worker,
		retroactiveScan: retroScan,
	}

	worker.SetOnContactReady(func(contactID uint) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			result, err := m.retroactiveScan.ScanForContact(ctx, contactID)
			if err != nil {
				logger.Warn("retroactive scan failed", "contact_id", contactID, "error", err)
				return
			}
			logger.Info("retroactive scan completed",
				"contact_id", contactID,
				"jobs_scanned", result.JobsScanned,
				"auto_assigned", result.AutoAssigned,
				"suggestions", result.Suggestions,
			)
		}()
	})

	return m
}

func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if !m.workerStarted {
		m.embeddingWorker.Start()
		m.workerStarted = true
	}
	if !m.retryStarted {
		m.retryStop = make(chan struct{})
		m.retryWG.Add(1)
		go m.retryLoop(m.retryStop)
		m.retryStarted = true
	}
	m.mu.Unlock()

	return m.HandleActiveVaultChange(ctx)
}

func (m *Manager) Stop() {
	m.mu.Lock()
	watcher := m.watcher
	m.watcher = nil
	m.syncService = nil
	m.fileService = nil
	m.activeVaultID = 0
	m.activePath = ""
	worker := m.embeddingWorker
	started := m.workerStarted
	m.workerStarted = false
	retryStop := m.retryStop
	retryStarted := m.retryStarted
	m.retryStop = nil
	m.retryStarted = false
	m.mu.Unlock()

	if watcher != nil {
		_ = watcher.Stop()
	}
	if retryStarted && retryStop != nil {
		close(retryStop)
		m.retryWG.Wait()
	}
	if started && worker != nil {
		worker.Stop()
	}
}

func (m *Manager) HandleActiveVaultChange(ctx context.Context) error {
	var vault models.Vault
	err := m.db.WithContext(ctx).Where("is_active = ?", true).First(&vault).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		m.clearWatcher()
		return nil
	}
	if err != nil {
		return err
	}
	return m.SwitchVault(ctx, vault.ID, vault.Path)
}

func (m *Manager) SwitchVault(ctx context.Context, vaultID uint, vaultPath string) error {
	newFileService := NewFileService(vaultPath)
	newSyncService := NewSyncService(newFileService, m.repo, vaultID)
	if _, err := newSyncService.SyncFromFilesystem(ctx); err != nil {
		return err
	}

	newWatcher := NewWatcher(newSyncService, newFileService)
	if err := newWatcher.Start(); err != nil {
		return err
	}

	m.mu.Lock()
	oldWatcher := m.watcher
	m.watcher = newWatcher
	m.fileService = newFileService
	m.syncService = newSyncService
	m.activeVaultID = vaultID
	m.activePath = strings.TrimSpace(vaultPath)
	m.mu.Unlock()

	if oldWatcher != nil {
		_ = oldWatcher.Stop()
	}
	if _, err := m.RetryDueEmbeddings(ctx); err != nil {
		logger.Warn("contact retry scan after vault switch failed", "vault_id", vaultID, "error", err)
	}
	return nil
}

func (m *Manager) ReindexActiveVault(ctx context.Context) (SyncResult, error) {
	m.mu.RLock()
	syncService := m.syncService
	m.mu.RUnlock()
	if syncService == nil {
		return SyncResult{}, nil
	}
	return syncService.SyncFromFilesystem(ctx)
}

// ReindexVault runs a sync pass for a specific vault. If it is active, reuse the live sync service.
func (m *Manager) ReindexVault(ctx context.Context, vaultID uint, vaultPath string) (SyncResult, error) {
	m.mu.RLock()
	activeSyncService := m.syncService
	activeVaultID := m.activeVaultID
	m.mu.RUnlock()

	if activeSyncService != nil && activeVaultID == vaultID {
		return activeSyncService.SyncFromFilesystem(ctx)
	}

	fileService := NewFileService(vaultPath)
	syncService := NewSyncService(fileService, m.repo, vaultID)
	return syncService.SyncFromFilesystem(ctx)
}

func (m *Manager) ActiveVault() (uint, string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.activeVaultID == 0 || strings.TrimSpace(m.activePath) == "" {
		return 0, "", false
	}
	return m.activeVaultID, m.activePath, true
}

func (m *Manager) EnqueueEmbedding(contactID uint) {
	m.mu.RLock()
	worker := m.embeddingWorker
	started := m.workerStarted
	m.mu.RUnlock()
	if started && worker != nil {
		worker.Enqueue(contactID)
	}
}

func (m *Manager) RetryDueEmbeddings(ctx context.Context) (RetryScanResult, error) {
	result := RetryScanResult{}

	m.mu.RLock()
	vaultID := m.activeVaultID
	m.mu.RUnlock()
	if vaultID == 0 {
		return result, nil
	}

	now := time.Now().UTC()
	statuses := []string{"failed", "processing"}
	seen := make(map[uint]struct{})

	for _, status := range statuses {
		contacts, err := m.repo.ListBySignatureStatus(ctx, vaultID, status)
		if err != nil {
			return result, err
		}
		for i := range contacts {
			contact := contacts[i]
			if _, exists := seen[contact.ID]; exists {
				continue
			}
			seen[contact.ID] = struct{}{}

			state, _, due := RetryState(&contact, now)
			if !due {
				result.Skipped++
				continue
			}

			m.EnqueueEmbedding(contact.ID)
			result.Queued++
			if state == "failed" {
				result.FailedDue++
			}
			if state == "processing" {
				result.StaleProcessing++
			}
		}
	}

	return result, nil
}

// WriteContact persists a contact markdown artifact and updates DB cache fields.
func (m *Manager) WriteContact(ctx context.Context, contact *models.Contact) error {
	m.mu.RLock()
	syncService := m.syncService
	activeVaultID := m.activeVaultID
	m.mu.RUnlock()

	if syncService != nil && activeVaultID == contact.VaultID {
		return syncService.WriteContactToFile(ctx, contact)
	}

	vault, err := m.vaultByID(ctx, contact.VaultID)
	if err != nil {
		return err
	}
	fileService := NewFileService(vault.Path)
	if err := fileService.WriteContact(contact); err != nil {
		return err
	}
	return m.repo.Update(ctx, contact)
}

// DeleteContactFiles removes the folder-per-contact artifacts from disk.
func (m *Manager) DeleteContactFiles(ctx context.Context, contact *models.Contact) error {
	vault, err := m.vaultByID(ctx, contact.VaultID)
	if err != nil {
		return err
	}
	fileService := NewFileService(vault.Path)
	return fileService.DeleteContactFolder(contact)
}

// SetRetroactiveScanExtractor sets the production speaker embedding extraction
// function used by the retroactive scan service. Call this after creating the
// Manager, before starting it.
func (m *Manager) SetRetroactiveScanExtractor(fn SpeakerEmbeddingExtractor) {
	if m.retroactiveScan != nil {
		m.retroactiveScan.SetExtractor(fn)
	}
}

// SetRetroactiveScanLLMCaller injects an LLM caller for voice+LLM fusion
// scoring during retroactive scanning.
func (m *Manager) SetRetroactiveScanLLMCaller(caller LLMCaller) {
	if m.retroactiveScan != nil {
		m.retroactiveScan.SetLLMCaller(caller)
	}
}

// RetroactiveScanForContact exposes the retroactive scan to the API layer
// for manual re-scan triggers.
func (m *Manager) RetroactiveScanForContact(ctx context.Context, contactID uint) (*RetroactiveScanResult, error) {
	if m.retroactiveScan == nil || m.retroactiveScan.extractFunc == nil {
		return &RetroactiveScanResult{}, nil
	}
	return m.retroactiveScan.ScanForContact(ctx, contactID)
}

func (m *Manager) vaultByID(ctx context.Context, vaultID uint) (*models.Vault, error) {
	var vault models.Vault
	if err := m.db.WithContext(ctx).First(&vault, vaultID).Error; err != nil {
		return nil, err
	}
	return &vault, nil
}

func (m *Manager) clearWatcher() {
	m.mu.Lock()
	oldWatcher := m.watcher
	m.watcher = nil
	m.fileService = nil
	m.syncService = nil
	m.activeVaultID = 0
	m.activePath = ""
	m.mu.Unlock()

	if oldWatcher != nil {
		_ = oldWatcher.Stop()
	}
}

func (m *Manager) retryLoop(stop <-chan struct{}) {
	defer m.retryWG.Done()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-stop
		cancel()
	}()

	ticker := time.NewTicker(signatureRetryScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if _, err := m.RetryDueEmbeddings(ctx); err != nil {
				logger.Warn("contact retry scan failed", "error", err)
			}
		}
	}
}
