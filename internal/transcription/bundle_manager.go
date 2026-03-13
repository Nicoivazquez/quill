package transcription

import (
	"context"
	"errors"
	"path/filepath"
	"sync"

	"quill/internal/models"
	"quill/internal/repository"
	"quill/pkg/logger"

	"gorm.io/gorm"
)

// BundleManager coordinates bundle sync, watcher lifecycle, and vault switching.
type BundleManager struct {
	db *gorm.DB

	mu          sync.RWMutex
	syncService *BundleSyncService
	watcher     *BundleWatcher
	activeVault uint
	activePath  string
}

// NewBundleManager creates a new bundle manager.
func NewBundleManager(db *gorm.DB) *BundleManager {
	return &BundleManager{db: db}
}

// Start initializes the manager, syncs the active vault, and starts watching.
func (m *BundleManager) Start(ctx context.Context) error {
	var vault models.Vault
	err := m.db.WithContext(ctx).Where("is_active = ?", true).First(&vault).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil // no active vault — nothing to do
	}
	if err != nil {
		return err
	}
	return m.SwitchVault(ctx, vault.ID, vault.Path)
}

// Stop shuts down the watcher and clears state.
func (m *BundleManager) Stop() {
	m.mu.Lock()
	watcher := m.watcher
	m.watcher = nil
	m.syncService = nil
	m.activeVault = 0
	m.activePath = ""
	m.mu.Unlock()

	if watcher != nil {
		_ = watcher.Stop()
	}
}

// SwitchVault stops the current watcher, syncs the new vault, and starts a new watcher.
func (m *BundleManager) SwitchVault(ctx context.Context, vaultID uint, vaultPath string) error {
	transcriptsDir := filepath.Join(vaultPath, "Transcripts")

	jobRepo := repository.NewJobRepository(m.db)
	speakerMappingRepo := repository.NewSpeakerMappingRepository(m.db)
	summaryRepo := repository.NewSummaryRepository(m.db)
	noteRepo := repository.NewNoteRepository(m.db)

	syncService := NewBundleSyncService(
		jobRepo, speakerMappingRepo, summaryRepo, noteRepo,
		transcriptsDir, &vaultID,
	)

	// Initial sync
	if _, err := syncService.SyncFromFilesystem(ctx); err != nil {
		logger.Warn("bundle manager: initial sync failed", "vault_id", vaultID, "error", err)
		// Continue anyway — watcher will retry
	}

	// Start watcher
	newWatcher := NewBundleWatcher(syncService, transcriptsDir)
	if err := newWatcher.Start(); err != nil {
		logger.Warn("bundle manager: watcher start failed", "vault_id", vaultID, "error", err)
		// Continue without watcher — sync still works via manual calls
	}

	// Swap state
	m.mu.Lock()
	oldWatcher := m.watcher
	m.watcher = newWatcher
	m.syncService = syncService
	m.activeVault = vaultID
	m.activePath = vaultPath
	m.mu.Unlock()

	if oldWatcher != nil {
		_ = oldWatcher.Stop()
	}

	return nil
}

// HandleActiveVaultChange detects the active vault and switches to it.
func (m *BundleManager) HandleActiveVaultChange(ctx context.Context) error {
	var vault models.Vault
	err := m.db.WithContext(ctx).Where("is_active = ?", true).First(&vault).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		m.Stop()
		return nil
	}
	if err != nil {
		return err
	}
	return m.SwitchVault(ctx, vault.ID, vault.Path)
}

// ReindexActiveVault runs a full sync pass for the active vault.
func (m *BundleManager) ReindexActiveVault(ctx context.Context) (BundleSyncResult, error) {
	m.mu.RLock()
	svc := m.syncService
	m.mu.RUnlock()
	if svc == nil {
		return BundleSyncResult{}, nil
	}
	return svc.SyncFromFilesystem(ctx)
}

// SyncService returns the active sync service (or nil if no vault is active).
func (m *BundleManager) SyncService() *BundleSyncService {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.syncService
}
