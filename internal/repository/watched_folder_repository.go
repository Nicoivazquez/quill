package repository

import (
	"context"
	"errors"

	"scriberr/internal/models"

	"gorm.io/gorm"
)

// WatchedFolderRepository handles persistence for desktop auto-import folders.
type WatchedFolderRepository interface {
	Repository[models.WatchedFolder]
	FindByUser(ctx context.Context, userID uint) ([]models.WatchedFolder, error)
	FindEnabled(ctx context.Context) ([]models.WatchedFolder, error)
	FindByUserAndPath(ctx context.Context, userID uint, path string) (*models.WatchedFolder, error)
	FindByUserAndID(ctx context.Context, userID uint, id uint) (*models.WatchedFolder, error)
}

type watchedFolderRepository struct {
	*BaseRepository[models.WatchedFolder]
}

func NewWatchedFolderRepository(db *gorm.DB) WatchedFolderRepository {
	return &watchedFolderRepository{
		BaseRepository: NewBaseRepository[models.WatchedFolder](db),
	}
}

func (r *watchedFolderRepository) FindByUser(ctx context.Context, userID uint) ([]models.WatchedFolder, error) {
	var folders []models.WatchedFolder
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("path ASC").
		Find(&folders).Error
	if err != nil {
		return nil, err
	}
	return folders, nil
}

func (r *watchedFolderRepository) FindEnabled(ctx context.Context) ([]models.WatchedFolder, error) {
	var folders []models.WatchedFolder
	err := r.db.WithContext(ctx).
		Where("enabled = ?", true).
		Order("id ASC").
		Find(&folders).Error
	if err != nil {
		return nil, err
	}
	return folders, nil
}

func (r *watchedFolderRepository) FindByUserAndPath(ctx context.Context, userID uint, path string) (*models.WatchedFolder, error) {
	var folder models.WatchedFolder
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND path = ?", userID, path).
		First(&folder).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &folder, nil
}

func (r *watchedFolderRepository) FindByUserAndID(ctx context.Context, userID uint, id uint) (*models.WatchedFolder, error) {
	var folder models.WatchedFolder
	err := r.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", id, userID).
		First(&folder).Error
	if err != nil {
		return nil, err
	}
	return &folder, nil
}
