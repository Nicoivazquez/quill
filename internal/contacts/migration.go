package contacts

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"quill/internal/models"
	"quill/pkg/logger"
	"quill/pkg/slug"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// BackfillContactsFileFirst ensures legacy contacts have vault-scoped file-first metadata.
func BackfillContactsFileFirst(ctx context.Context, db *gorm.DB) error {
	var vaults []models.Vault
	if err := db.WithContext(ctx).Order("id ASC").Find(&vaults).Error; err != nil {
		return err
	}
	if len(vaults) == 0 {
		return nil
	}

	vaultByID := make(map[uint]models.Vault, len(vaults))
	for _, vault := range vaults {
		vaultByID[vault.ID] = vault
	}

	defaultVault := vaults[0]
	for _, vault := range vaults {
		if vault.IsActive {
			defaultVault = vault
			break
		}
	}

	var contacts []models.Contact
	if err := db.WithContext(ctx).Order("id ASC").Find(&contacts).Error; err != nil {
		return err
	}

	for i := range contacts {
		contact := &contacts[i]
		if contact.VaultID == 0 {
			contact.VaultID = defaultVault.ID
		}
		vault, ok := vaultByID[contact.VaultID]
		if !ok {
			contact.VaultID = defaultVault.ID
			vault = defaultVault
		}

		if strings.TrimSpace(contact.ContactUID) == "" {
			contact.ContactUID = uuid.NewString()
		}
		if strings.TrimSpace(contact.Slug) == "" {
			contact.Slug = slug.Sanitize(contact.Name, "contact")
		}
		if strings.TrimSpace(contact.SignatureStatus) == "" {
			contact.SignatureStatus = "none"
		}
		normalizeLegacyAssetPath(vault.Path, &contact.VoiceSnippetPath)
		normalizeLegacyAssetPath(vault.Path, &contact.SignatureEmbeddingPath)

		fileService := NewFileService(vault.Path)
		noteAbs := fileService.ResolveAbsPath(contact.NotePath)
		if strings.TrimSpace(contact.NotePath) == "" || !fileExists(noteAbs) {
			if err := fileService.WriteContact(contact); err != nil {
				logger.Warn("contact backfill: failed to materialize markdown", "contact_id", contact.ID, "error", err)
			}
		} else if info, statErr := os.Stat(noteAbs); statErr == nil && !info.IsDir() {
			contact.FileMtimeNS = info.ModTime().UnixNano()
		}

		if err := db.WithContext(ctx).Save(contact).Error; err != nil {
			return err
		}
	}
	return nil
}

func normalizeLegacyAssetPath(vaultPath string, pathValue **string) {
	if *pathValue == nil {
		return
	}
	trimmed := strings.TrimSpace(**pathValue)
	if trimmed == "" {
		*pathValue = nil
		return
	}
	if !filepath.IsAbs(trimmed) {
		**pathValue = filepath.ToSlash(trimmed)
		return
	}
	rel, err := filepath.Rel(vaultPath, trimmed)
	if err != nil || strings.HasPrefix(rel, "..") {
		return
	}
	**pathValue = filepath.ToSlash(rel)
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
