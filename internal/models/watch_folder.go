package models

import "time"

// WatchedFolder stores per-user desktop auto-import folder configuration.
type WatchedFolder struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	UserID    uint      `json:"user_id" gorm:"not null;index:idx_watched_folders_user_path,unique"`
	Path      string    `json:"path" gorm:"type:text;not null;index:idx_watched_folders_user_path,unique"`
	Recursive bool      `json:"recursive" gorm:"type:boolean;not null;default:true"`
	Enabled   bool      `json:"enabled" gorm:"type:boolean;not null;default:true"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}
