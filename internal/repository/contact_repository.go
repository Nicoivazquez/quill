package repository

import (
	"context"
	"strings"

	"quill/internal/models"

	"gorm.io/gorm"
)

// ContactRepository handles vault-scoped contact operations.
type ContactRepository interface {
	Create(ctx context.Context, contact *models.Contact) error
	Update(ctx context.Context, contact *models.Contact) error
	Delete(ctx context.Context, id uint) error
	GetByID(ctx context.Context, id uint) (*models.Contact, error)
	GetByIDInVault(ctx context.Context, vaultID uint, id uint) (*models.Contact, error)
	GetByUID(ctx context.Context, vaultID uint, uid string) (*models.Contact, error)
	GetByVaultAndSlug(ctx context.Context, vaultID uint, slug string) (*models.Contact, error)
	ListByVault(ctx context.Context, vaultID uint) ([]models.Contact, error)
	Search(ctx context.Context, vaultID uint, query string) ([]models.Contact, error)
	ListBySignatureStatus(ctx context.Context, vaultID uint, status string) ([]models.Contact, error)
}

type contactRepository struct {
	db *gorm.DB
}

func NewContactRepository(db *gorm.DB) ContactRepository {
	return &contactRepository{db: db}
}

func (r *contactRepository) Create(ctx context.Context, contact *models.Contact) error {
	return r.db.WithContext(ctx).Create(contact).Error
}

func (r *contactRepository) Update(ctx context.Context, contact *models.Contact) error {
	return r.db.WithContext(ctx).Save(contact).Error
}

func (r *contactRepository) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&models.Contact{}, id).Error
}

func (r *contactRepository) GetByID(ctx context.Context, id uint) (*models.Contact, error) {
	var contact models.Contact
	if err := r.db.WithContext(ctx).First(&contact, id).Error; err != nil {
		return nil, err
	}
	return &contact, nil
}

func (r *contactRepository) GetByIDInVault(ctx context.Context, vaultID uint, id uint) (*models.Contact, error) {
	var contact models.Contact
	if err := r.db.WithContext(ctx).Where("vault_id = ? AND id = ?", vaultID, id).First(&contact).Error; err != nil {
		return nil, err
	}
	return &contact, nil
}

func (r *contactRepository) GetByUID(ctx context.Context, vaultID uint, uid string) (*models.Contact, error) {
	var contact models.Contact
	if err := r.db.WithContext(ctx).Where("vault_id = ? AND contact_uid = ?", vaultID, strings.TrimSpace(uid)).First(&contact).Error; err != nil {
		return nil, err
	}
	return &contact, nil
}

func (r *contactRepository) GetByVaultAndSlug(ctx context.Context, vaultID uint, slug string) (*models.Contact, error) {
	var contact models.Contact
	if err := r.db.WithContext(ctx).Where("vault_id = ? AND slug = ?", vaultID, strings.TrimSpace(slug)).First(&contact).Error; err != nil {
		return nil, err
	}
	return &contact, nil
}

func (r *contactRepository) ListByVault(ctx context.Context, vaultID uint) ([]models.Contact, error) {
	var contacts []models.Contact
	err := r.db.WithContext(ctx).
		Where("vault_id = ?", vaultID).
		Order("name ASC").
		Find(&contacts).Error
	if err != nil {
		return nil, err
	}
	return contacts, nil
}

func (r *contactRepository) Search(ctx context.Context, vaultID uint, query string) ([]models.Contact, error) {
	var contacts []models.Contact
	trimmed := strings.TrimSpace(query)
	db := r.db.WithContext(ctx).Where("vault_id = ?", vaultID)
	if trimmed != "" {
		like := "%" + trimmed + "%"
		db = db.Where("name LIKE ? OR email LIKE ? OR phone LIKE ? OR notes LIKE ?", like, like, like, like)
	}
	if err := db.Order("name ASC").Find(&contacts).Error; err != nil {
		return nil, err
	}
	return contacts, nil
}

func (r *contactRepository) ListBySignatureStatus(ctx context.Context, vaultID uint, status string) ([]models.Contact, error) {
	var contacts []models.Contact
	if err := r.db.WithContext(ctx).
		Where("vault_id = ? AND signature_status = ?", vaultID, strings.TrimSpace(status)).
		Order("updated_at DESC").
		Find(&contacts).Error; err != nil {
		return nil, err
	}
	return contacts, nil
}
