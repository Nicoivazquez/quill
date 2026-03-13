package models

import "time"

// CloudProviderConfig stores API keys for cloud transcription providers.
// The APIKey field is excluded from JSON serialisation to prevent accidental exposure.
type CloudProviderConfig struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	Provider  string    `json:"provider" gorm:"type:varchar(50);not null;uniqueIndex"` // "assemblyai", "deepgram", "openai"
	APIKey    string    `json:"-" gorm:"type:text;not null"`                           // Never exposed in JSON
	IsActive  bool      `json:"is_active" gorm:"type:boolean;default:false"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}
