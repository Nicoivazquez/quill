package repository

import (
	"context"
	"quill/internal/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CloudProviderConfigRepository manages API keys for cloud transcription providers.
type CloudProviderConfigRepository interface {
	// Upsert inserts or updates the config for the given provider (keyed by Provider field).
	Upsert(ctx context.Context, config *models.CloudProviderConfig) error
	// GetByProvider returns the config for a specific provider, or an error if not found.
	GetByProvider(ctx context.Context, provider string) (*models.CloudProviderConfig, error)
	// ListAll returns all stored provider configs regardless of is_active status.
	ListAll(ctx context.Context) ([]models.CloudProviderConfig, error)
	// ListActive returns all configs where is_active=true.
	ListActive(ctx context.Context) ([]models.CloudProviderConfig, error)
	// Delete removes the config for the given provider. Deleting a non-existent provider is a no-op.
	Delete(ctx context.Context, provider string) error
}

type cloudProviderConfigRepository struct {
	db *gorm.DB
}

// NewCloudProviderConfigRepository constructs a new CloudProviderConfigRepository.
func NewCloudProviderConfigRepository(db *gorm.DB) CloudProviderConfigRepository {
	return &cloudProviderConfigRepository{db: db}
}

// Upsert inserts or updates the config keyed by the unique provider column.
func (r *cloudProviderConfigRepository) Upsert(ctx context.Context, config *models.CloudProviderConfig) error {
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "provider"}},
			DoUpdates: clause.AssignmentColumns([]string{"api_key", "is_active", "updated_at"}),
		}).
		Create(config).Error
}

// GetByProvider returns the config for a specific provider.
func (r *cloudProviderConfigRepository) GetByProvider(ctx context.Context, provider string) (*models.CloudProviderConfig, error) {
	var cfg models.CloudProviderConfig
	err := r.db.WithContext(ctx).Where("provider = ?", provider).First(&cfg).Error
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

// ListAll returns all stored provider configs regardless of is_active status.
func (r *cloudProviderConfigRepository) ListAll(ctx context.Context) ([]models.CloudProviderConfig, error) {
	var cfgs []models.CloudProviderConfig
	err := r.db.WithContext(ctx).Find(&cfgs).Error
	if err != nil {
		return nil, err
	}
	return cfgs, nil
}

// ListActive returns all cloud provider configs with is_active=true.
func (r *cloudProviderConfigRepository) ListActive(ctx context.Context) ([]models.CloudProviderConfig, error) {
	var cfgs []models.CloudProviderConfig
	err := r.db.WithContext(ctx).Where("is_active = ?", true).Find(&cfgs).Error
	if err != nil {
		return nil, err
	}
	return cfgs, nil
}

// Delete removes the config for a provider. Non-existent providers are silently ignored.
func (r *cloudProviderConfigRepository) Delete(ctx context.Context, provider string) error {
	return r.db.WithContext(ctx).
		Where("provider = ?", provider).
		Delete(&models.CloudProviderConfig{}).Error
}
