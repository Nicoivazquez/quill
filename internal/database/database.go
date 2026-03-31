package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"quill/internal/contacts"
	"quill/internal/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DB is the global database instance
var DB *gorm.DB

// Initialize initializes the database connection with optimized settings
func Initialize(dbPath string) error {
	var err error

	// Create database directory if it doesn't exist
	dbDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return fmt.Errorf("failed to create database directory: %v", err)
	}

	// SQLite connection string with performance optimizations
	dsn := fmt.Sprintf("%s?"+
		"_pragma=foreign_keys(1)&"+ // Enable foreign keys
		"_pragma=journal_mode(WAL)&"+ // Use WAL mode for better concurrency
		"_pragma=synchronous(NORMAL)&"+ // Balance between safety and performance
		"_pragma=cache_size(-64000)&"+ // 64MB cache size
		"_pragma=temp_store(MEMORY)&"+ // Store temp tables in memory
		"_pragma=mmap_size(268435456)&"+ // 256MB mmap size
		"_timeout=30000", // 30 second timeout
		dbPath)

	// Open database connection with optimized config
	DB, err = gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger:          logger.Default.LogMode(logger.Warn), // Reduce logging overhead
		CreateBatchSize: 100,                                 // Optimize batch inserts
	})
	if err != nil {
		return fmt.Errorf("failed to connect to database: %v", err)
	}

	// Get underlying sql.DB for connection pool configuration
	sqlDB, err := DB.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying sql.DB: %v", err)
	}

	// Configure connection pool for optimal performance
	sqlDB.SetMaxOpenConns(10)                  // SQLite generally works well with lower connection counts
	sqlDB.SetMaxIdleConns(5)                   // Keep some connections idle
	sqlDB.SetConnMaxLifetime(30 * time.Minute) // Reset connections every 30 minutes
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)  // Close idle connections after 5 minutes

	// Auto migrate the schema
	if err := DB.AutoMigrate(
		&models.TranscriptionJob{},
		&models.TranscriptionJobExecution{},
		&models.SpeakerMapping{},
		&models.MultiTrackFile{},
		&models.User{},
		&models.APIKey{},
		&models.TranscriptionProfile{},
		&models.LLMConfig{},
		&models.ChatSession{},
		&models.ChatMessage{},
		&models.SummaryTemplate{},
		&models.SummarySetting{},
		&models.Summary{},
		&models.Note{},
		&models.RefreshToken{},
		&models.WatchedFolder{},
		&models.Vault{},
		&models.AppSetup{},
		&models.Contact{},
		&models.CloudProviderConfig{},
	); err != nil {
		return fmt.Errorf("failed to auto migrate: %v", err)
	}

	if err := contacts.BackfillContactsFileFirst(context.Background(), DB); err != nil {
		return fmt.Errorf("failed to backfill contacts file-first schema: %v", err)
	}

	// Cleanup duplicate speaker mappings before creating unique index (for backward compatibility)
	// Keep the latest mapping for each (job_id, original_speaker) pair
	cleanupQuery := `
		DELETE FROM speaker_mappings 
		WHERE id NOT IN (
			SELECT MAX(id) 
			FROM speaker_mappings 
			GROUP BY transcription_job_id, original_speaker
		)
	`
	if err := DB.Exec(cleanupQuery).Error; err != nil {
		// Log warning but continue, as table might not exist yet or query might fail for other reasons
		// We don't want to block startup if this fails, but index creation might fail next.
		fmt.Printf("Warning: Failed to cleanup duplicate speaker mappings: %v\n", err)
	}

	// Add unique constraint for speaker mappings (transcription_job_id + original_speaker)
	if err := DB.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_speaker_mappings_unique ON speaker_mappings(transcription_job_id, original_speaker)").Error; err != nil {
		return fmt.Errorf("failed to create unique constraint for speaker mappings: %v", err)
	}

	// Backfill raw speaker mappings for existing completed diarized jobs
	if err := backfillRawSpeakerMappings(DB); err != nil {
		fmt.Printf("Warning: Failed to backfill raw speaker mappings: %v\n", err)
	}

	// Link orphaned speaker_mappings to contacts by name match
	if err := backfillSpeakerMappingContactIDs(DB); err != nil {
		fmt.Printf("Warning: Failed to backfill speaker mapping contact IDs: %v\n", err)
	}

	// Seed default summary template if none exist
	if err := seedDefaultSummaryTemplate(DB); err != nil {
		return fmt.Errorf("failed to seed default summary template: %v", err)
	}

	// Migrate legacy machine-readable folder names to human-readable names.
	if err := MigrateHumanReadableFolderNames(DB); err != nil {
		fmt.Printf("Warning: Failed to migrate folder names: %v\n", err)
	}

	// Backfill empty match_source on speaker mappings so UI labels are consistent.
	if err := backfillSpeakerMappingMatchSource(DB); err != nil {
		fmt.Printf("Warning: Failed to backfill speaker mapping match_source: %v\n", err)
	}

	return nil
}

// backfillRawSpeakerMappings ensures every speaker label found in completed
// diarized transcripts has a corresponding speaker_mappings row.  Older jobs
// only had mappings for matched/suggested speakers; unmatched speakers were
// invisible to the attention-summary query.  This is idempotent — the unique
// index on (transcription_job_id, original_speaker) prevents duplicates.
func backfillRawSpeakerMappings(db *gorm.DB) error {
	type jobRow struct {
		ID         string
		Transcript *string
	}

	// Find completed diarized jobs that have a transcript.
	var jobs []jobRow
	err := db.Model(&models.TranscriptionJob{}).
		Select("id, transcript").
		Where("status = ? AND diarization = ? AND transcript IS NOT NULL AND transcript != ''", "completed", true).
		Scan(&jobs).Error
	if err != nil {
		return fmt.Errorf("query diarized jobs: %w", err)
	}

	if len(jobs) == 0 {
		return nil
	}

	type segment struct {
		Speaker *string `json:"speaker,omitempty"`
	}
	type payload struct {
		Segments []segment `json:"segments,omitempty"`
	}

	var totalCreated int
	for _, job := range jobs {
		if job.Transcript == nil || strings.TrimSpace(*job.Transcript) == "" {
			continue
		}

		var p payload
		if err := json.Unmarshal([]byte(*job.Transcript), &p); err != nil {
			continue
		}

		// Collect unique speaker labels from transcript.
		speakerSet := make(map[string]struct{})
		for _, seg := range p.Segments {
			if seg.Speaker != nil && strings.TrimSpace(*seg.Speaker) != "" {
				speakerSet[strings.TrimSpace(*seg.Speaker)] = struct{}{}
			}
		}
		if len(speakerSet) == 0 {
			continue
		}

		// Find existing mappings for this job.
		var existing []models.SpeakerMapping
		if err := db.Where("transcription_job_id = ?", job.ID).Find(&existing).Error; err != nil {
			continue
		}
		existingSet := make(map[string]struct{}, len(existing))
		for _, m := range existing {
			existingSet[m.OriginalSpeaker] = struct{}{}
		}

		// Create raw mappings for missing speakers.
		for speaker := range speakerSet {
			if _, exists := existingSet[speaker]; exists {
				continue
			}
			raw := models.SpeakerMapping{
				TranscriptionJobID: job.ID,
				OriginalSpeaker:    speaker,
				CustomName:         speaker,
				MatchSource:        "auto",
				MatchTier:          "none",
			}
			if err := db.Create(&raw).Error; err != nil {
				continue // unique index violation or other — skip
			}
			totalCreated++
		}
	}

	if totalCreated > 0 {
		fmt.Printf("Backfilled %d raw speaker mappings for existing jobs\n", totalCreated)
	}
	return nil
}

// backfillSpeakerMappingContactIDs links orphaned speaker_mappings (custom_name
// set but contact_id NULL) to existing contacts by case-insensitive name match
// within the same vault.  This is idempotent — already-linked rows are skipped.
func backfillSpeakerMappingContactIDs(db *gorm.DB) error {
	result := db.Exec(`
		UPDATE speaker_mappings
		SET contact_id = (
			SELECT c.id FROM contacts c
			JOIN transcription_jobs j ON j.vault_id = c.vault_id
			WHERE j.id = speaker_mappings.transcription_job_id
			AND LOWER(c.name) = LOWER(speaker_mappings.custom_name)
			LIMIT 1
		)
		WHERE contact_id IS NULL
		AND custom_name != original_speaker
		AND custom_name != ''
		AND EXISTS (
			SELECT 1 FROM contacts c
			JOIN transcription_jobs j ON j.vault_id = c.vault_id
			WHERE j.id = speaker_mappings.transcription_job_id
			AND LOWER(c.name) = LOWER(speaker_mappings.custom_name)
		)
	`)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		fmt.Printf("Backfilled contact_id on %d speaker mappings\n", result.RowsAffected)
	}
	return nil
}

// backfillSpeakerMappingMatchSource sets match_source = "manual" on legacy rows
// where the field is empty but the speaker was clearly renamed by the user.
func backfillSpeakerMappingMatchSource(db *gorm.DB) error {
	result := db.Exec(`
		UPDATE speaker_mappings
		SET match_source = 'manual'
		WHERE (match_source IS NULL OR match_source = '')
		AND custom_name != original_speaker
		AND custom_name != ''
	`)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		fmt.Printf("Backfilled match_source on %d speaker mappings\n", result.RowsAffected)
	}
	return nil
}

// seedDefaultSummaryTemplate creates a "Meeting Notes" template if no templates exist yet.
func seedDefaultSummaryTemplate(db *gorm.DB) error {
	var count int64
	if err := db.Model(&models.SummaryTemplate{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	desc := "Structured meeting notes with summary, decisions, action items, and next steps."
	template := models.SummaryTemplate{
		Name:               "Meeting Notes",
		Description:        &desc,
		Prompt:             defaultMeetingNotesPrompt,
		IncludeSpeakerInfo: true,
		IsDefault:          true,
	}
	return db.Create(&template).Error
}

const defaultMeetingNotesPrompt = `You are a skilled meeting analyst. Analyze the following transcript and produce structured meeting notes in Markdown format.

## Summary
Write a 2-3 sentence executive overview. Lead with the most important outcome or decision, then provide essential context.

## Key Topics Discussed
Group the discussion into thematic topics (not chronological order). For each topic, summarize the main points in 1-3 sentences. Use sub-bullets for supporting details.

## Decisions Made
List each decision as a bullet point. Include who made or endorsed the decision if identifiable, and note the rationale when it was discussed. Omit this section if no decisions were made.

## Action Items
List each action item using this format:
- **[Owner]**: Task description *(Deadline, if mentioned)*
Use **[Unassigned]** when no owner is clear. Omit this section if no action items were discussed.

## Open Questions & Next Steps
List unresolved questions, deferred topics, and planned follow-ups. Include deadlines or dates if stated. Omit this section if none apply.

Guidelines:
- Be concise and factual. Keep the output readable in under 2 minutes.
- Use speakers' names when available for attribution.
- Do not invent or infer information not present in the transcript.
- Omit any section that has no relevant content rather than writing "None" or "N/A".`

// Close closes the database connection gracefully
func Close() error {
	if DB == nil {
		return nil
	}
	sqlDB, err := DB.DB()
	if err != nil {
		return err
	}
	err = sqlDB.Close()
	DB = nil // Set to nil after closing
	return err
}

// HealthCheck performs a health check on the database connection
func HealthCheck() error {
	if DB == nil {
		return fmt.Errorf("database connection is nil")
	}

	sqlDB, err := DB.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying sql.DB: %v", err)
	}

	// Test the connection with a ping
	if err := sqlDB.Ping(); err != nil {
		return fmt.Errorf("database ping failed: %v", err)
	}

	return nil
}

// GetConnectionStats returns database connection pool statistics
func GetConnectionStats() sql.DBStats {
	if DB == nil {
		return sql.DBStats{}
	}

	sqlDB, err := DB.DB()
	if err != nil {
		return sql.DBStats{}
	}

	return sqlDB.Stats()
}
