package contacts

import (
	"context"
	"errors"
	"strings"
	"sync"

	"quill/internal/models"
	"quill/internal/repository"

	"gorm.io/gorm"
)

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

	embeddingWorker *EmbeddingWorker
	workerStarted   bool
}

func NewManager(db *gorm.DB, repo repository.ContactRepository, whisperXEnv string) *Manager {
	return &Manager{
		db:              db,
		repo:            repo,
		whisperXEnv:     strings.TrimSpace(whisperXEnv),
		embeddingWorker: NewEmbeddingWorker(db, repo, whisperXEnv),
	}
}

func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if !m.workerStarted {
		m.embeddingWorker.Start()
		m.workerStarted = true
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
	m.mu.Unlock()

	if watcher != nil {
		_ = watcher.Stop()
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

func (m *Manager) FileService() *FileService {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.fileService
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
