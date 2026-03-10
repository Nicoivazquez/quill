package contacts

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"quill/internal/models"
	"quill/internal/repository"
	"quill/pkg/logger"

	"gorm.io/gorm"
)

// SyncResult summarizes a filesystem synchronization pass.
type SyncResult struct {
	Imported int `json:"imported"`
	Updated  int `json:"updated"`
	Deleted  int `json:"deleted"`
	Skipped  int `json:"skipped"`
}

// SyncService reconciles contact markdown files with the DB cache.
type SyncService struct {
	fileService *FileService
	repo        repository.ContactRepository
	vaultID     uint

	mu        sync.Mutex
	selfWrite map[string]int64
}

func NewSyncService(fileService *FileService, repo repository.ContactRepository, vaultID uint) *SyncService {
	return &SyncService{
		fileService: fileService,
		repo:        repo,
		vaultID:     vaultID,
		selfWrite:   make(map[string]int64),
	}
}

func (s *SyncService) VaultID() uint {
	return s.vaultID
}

func (s *SyncService) SyncFromFilesystem(ctx context.Context) (SyncResult, error) {
	result := SyncResult{}
	folders, err := s.fileService.ScanAllContactFolders()
	if err != nil {
		return result, err
	}

	seenUIDs := make(map[string]bool, len(folders))

	for _, folder := range folders {
		seenUIDs[folder.ContactUID] = true

		if s.isSelfWrite(folder.NoteAbsPath, folder.MtimeNS) {
			result.Skipped++
			continue
		}

		existing, findErr := s.repo.GetByUID(ctx, s.vaultID, folder.ContactUID)
		if findErr != nil && !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return result, findErr
		}

		if existing != nil && folder.MtimeNS <= existing.FileMtimeNS {
			result.Skipped++
			continue
		}

		parsed, parseErr := s.fileService.ReadContactFromNotePath(folder.NoteAbsPath)
		if parseErr != nil {
			logger.Warn("failed to parse contact markdown", "vault_id", s.vaultID, "note", folder.NoteAbsPath, "error", parseErr)
			if existing != nil {
				msg := parseErr.Error()
				existing.SyncError = &msg
				_ = s.repo.Update(ctx, existing)
			}
			continue
		}

		parsed.VaultID = s.vaultID
		parsed.SyncError = nil

		if existing == nil {
			if createErr := s.repo.Create(ctx, parsed); createErr != nil {
				return result, createErr
			}
			result.Imported++
			continue
		}

		parsed.ID = existing.ID
		parsed.CreatedAt = existing.CreatedAt
		parsed.UpdatedAt = time.Now().UTC()
		if updateErr := s.repo.Update(ctx, parsed); updateErr != nil {
			return result, updateErr
		}
		result.Updated++
	}

	dbContacts, err := s.repo.ListByVault(ctx, s.vaultID)
	if err != nil {
		return result, err
	}
	for _, contact := range dbContacts {
		uid := strings.TrimSpace(contact.ContactUID)
		if uid == "" {
			continue
		}
		if seenUIDs[uid] {
			continue
		}
		if deleteErr := s.repo.Delete(ctx, contact.ID); deleteErr != nil {
			return result, deleteErr
		}
		result.Deleted++
	}

	return result, nil
}

func (s *SyncService) WriteContactToFile(ctx context.Context, contact *models.Contact) error {
	contact.VaultID = s.vaultID
	if err := s.fileService.WriteContact(contact); err != nil {
		return err
	}
	noteAbs := s.fileService.ResolveAbsPath(contact.NotePath)
	s.markSelfWrite(noteAbs, contact.FileMtimeNS)
	return s.repo.Update(ctx, contact)
}

func (s *SyncService) markSelfWrite(noteAbsPath string, mtimeNS int64) {
	normalized := normalizePath(noteAbsPath)
	s.mu.Lock()
	s.selfWrite[normalized] = mtimeNS
	s.mu.Unlock()
}

func (s *SyncService) isSelfWrite(noteAbsPath string, mtimeNS int64) bool {
	normalized := normalizePath(noteAbsPath)
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, exists := s.selfWrite[normalized]
	if !exists {
		return false
	}
	if stored == mtimeNS {
		delete(s.selfWrite, normalized)
		return true
	}
	if mtimeNS > stored+int64(time.Minute) {
		delete(s.selfWrite, normalized)
	}
	return false
}

func normalizePath(value string) string {
	return filepath.Clean(strings.TrimSpace(value))
}
