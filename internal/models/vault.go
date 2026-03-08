package models

import "time"

// Vault represents a local-first workspace root.
type Vault struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	Name      string    `json:"name" gorm:"type:varchar(255);not null"`
	Path      string    `json:"path" gorm:"type:text;not null;uniqueIndex"`
	IsActive  bool      `json:"is_active" gorm:"type:boolean;not null;default:false"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

// AppSetup stores one-time local app setup and integration paths.
type AppSetup struct {
	ID               uint      `json:"id" gorm:"primaryKey"`
	Completed        bool      `json:"completed" gorm:"type:boolean;not null;default:false"`
	AuthMode         string    `json:"auth_mode" gorm:"type:varchar(20);not null;default:'local'"`
	ActiveVaultID    *uint     `json:"active_vault_id,omitempty"`
	ObsidianVaultDir *string   `json:"obsidian_vault_dir,omitempty" gorm:"type:text"`
	OpenClawDropDir  *string   `json:"openclaw_drop_dir,omitempty" gorm:"type:text"`
	CreatedAt        time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt        time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

// Contact stores contact and voice snippet/signature scaffold data.
type Contact struct {
	ID               uint      `json:"id" gorm:"primaryKey"`
	Name             string    `json:"name" gorm:"type:varchar(255);not null"`
	Phone            *string   `json:"phone,omitempty" gorm:"type:varchar(64)"`
	Email            *string   `json:"email,omitempty" gorm:"type:varchar(255)"`
	Notes            *string   `json:"notes,omitempty" gorm:"type:text"`
	VoiceSnippetPath *string   `json:"voice_snippet_path,omitempty" gorm:"type:text"`
	SignatureStatus  string    `json:"signature_status" gorm:"type:varchar(32);not null;default:'none'"`
	SignatureData    *string   `json:"signature_data,omitempty" gorm:"type:text"`
	CreatedAt        time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt        time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}
