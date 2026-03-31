package repository

import (
	"context"
	"quill/internal/models"
	"time"

	"gorm.io/gorm"
)

// UserRepository handles user-specific database operations
type UserRepository interface {
	Repository[models.User]
	FindByUsername(ctx context.Context, username string) (*models.User, error)
	Count(ctx context.Context) (int64, error)
	CountWithAutoTranscription(ctx context.Context) (int64, error)
}

type userRepository struct {
	*BaseRepository[models.User]
}

func NewUserRepository(db *gorm.DB) UserRepository {
	return &userRepository{
		BaseRepository: NewBaseRepository[models.User](db),
	}
}

func (r *userRepository) FindByUsername(ctx context.Context, username string) (*models.User, error) {
	var user models.User
	err := r.db.WithContext(ctx).Where("username = ?", username).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *userRepository) Count(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&models.User{}).Count(&count).Error
	return count, err
}

func (r *userRepository) CountWithAutoTranscription(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&models.User{}).Where("auto_transcription_enabled = ?", true).Count(&count).Error
	return count, err
}

// ListParams consolidates all filtering, sorting, and pagination parameters
// for listing transcription jobs.
type ListParams struct {
	Offset       int
	Limit        int
	SortBy       string
	SortOrder    string
	SearchQuery  string
	FTSJobIDs    []string // Pre-resolved FTS matches; when set, replaces LIKE search
	UpdatedAfter *time.Time
	VaultID      *uint
	Folder       *string // nil = all, pointer to "" = root/unfiled, pointer to "X" = specific folder
	Status        string // filter by job status (e.g., "completed", "failed")
	Speaker       string // filter by speaker custom name (subquery on speaker_mappings)
	SpeakerStatus string // filter by speaker identification status ("needs_attention", "identified")
}

// allowedSortColumns defines the columns that can be used for sorting.
// This prevents SQL injection through the sort_by parameter.
var allowedSortColumns = map[string]bool{
	"created_at": true,
	"updated_at": true,
	"title":      true,
	"status":     true,
}

// IsSortColumnAllowed checks if a column name is in the sort allowlist.
func IsSortColumnAllowed(col string) bool {
	return allowedSortColumns[col]
}

// JobRepository handles transcription job operations
type JobRepository interface {
	Repository[models.TranscriptionJob]
	FindWithAssociations(ctx context.Context, id string) (*models.TranscriptionJob, error)
	FindActiveTrackJobs(ctx context.Context, parentJobID string) ([]models.TranscriptionJob, error)
	FindLatestCompletedExecution(ctx context.Context, jobID string) (*models.TranscriptionJobExecution, error)
	ListWithParams(ctx context.Context, params ListParams) ([]models.TranscriptionJob, int64, error)
	ListByUser(ctx context.Context, userID uint, offset, limit int) ([]models.TranscriptionJob, int64, error)
	ListDistinctSpeakers(ctx context.Context, vaultID *uint) ([]string, error)
	UpdateTranscript(ctx context.Context, jobID string, transcript string) error
	CreateExecution(ctx context.Context, execution *models.TranscriptionJobExecution) error
	UpdateExecution(ctx context.Context, execution *models.TranscriptionJobExecution) error
	DeleteExecutionsByJobID(ctx context.Context, jobID string) error
	DeleteMultiTrackFilesByJobID(ctx context.Context, jobID string) error
	UpdateStatus(ctx context.Context, jobID string, status models.JobStatus) error
	UpdateError(ctx context.Context, jobID string, errorMsg string) error
	FindByStatus(ctx context.Context, status models.JobStatus) ([]models.TranscriptionJob, error)
	CountByStatus(ctx context.Context, status models.JobStatus) (int64, error)
	UpdateSummary(ctx context.Context, jobID string, summary string) error
	ListFolders(ctx context.Context, vaultID *uint) ([]string, error)
	UpdateFolder(ctx context.Context, jobID string, folder *string) error
	UpdateBundlePaths(ctx context.Context, jobID string, artifactDir, audioPath, jsonPath, mdPath *string, folder *string) error
	BulkUpdateFolder(ctx context.Context, oldFolder string, newFolder *string, vaultID *uint) (int64, error)
	FindByIDs(ctx context.Context, ids []string) ([]models.TranscriptionJob, error)
}

type jobRepository struct {
	*BaseRepository[models.TranscriptionJob]
}

func NewJobRepository(db *gorm.DB) JobRepository {
	return &jobRepository{
		BaseRepository: NewBaseRepository[models.TranscriptionJob](db),
	}
}

func (r *jobRepository) FindWithAssociations(ctx context.Context, id string) (*models.TranscriptionJob, error) {
	var job models.TranscriptionJob
	err := r.db.WithContext(ctx).
		Preload("MultiTrackFiles").
		Where("id = ?", id).
		First(&job).Error
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func (r *jobRepository) ListWithParams(ctx context.Context, params ListParams) ([]models.TranscriptionJob, int64, error) {
	var jobs []models.TranscriptionJob
	var count int64

	db := r.db.WithContext(ctx)

	// Handle delta sync: apply Unscoped before Model to include soft-deleted records
	if params.UpdatedAfter != nil {
		db = db.Unscoped()
	}

	db = db.Model(&models.TranscriptionJob{})

	if params.UpdatedAfter != nil {
		db = db.Where("updated_at > ?", *params.UpdatedAfter)
	}

	if params.VaultID != nil {
		db = db.Where("vault_id = ?", *params.VaultID)
	}

	// Apply folder filter
	if params.Folder != nil {
		if *params.Folder != "" {
			db = db.Where("folder = ?", *params.Folder)
		} else {
			// Explicitly filter for root (no folder)
			db = db.Where("folder IS NULL OR folder = ''")
		}
	}

	// Apply status filter
	if params.Status != "" {
		db = db.Where("status = ?", params.Status)
	}

	// Apply speaker filter via subquery to avoid JOINs and duplicate rows
	if params.Speaker != "" {
		db = db.Where("id IN (SELECT transcription_job_id FROM speaker_mappings WHERE custom_name = ?)", params.Speaker)
	}

	// Apply speaker status filter
	switch params.SpeakerStatus {
	case "needs_attention":
		// Jobs that have at least one unidentified speaker (still using default name)
		db = db.Where("id IN (SELECT transcription_job_id FROM speaker_mappings WHERE custom_name = original_speaker)")
	case "identified":
		// Jobs where ALL speaker mappings are linked to contacts (fully identified)
		db = db.Where(`id IN (
			SELECT transcription_job_id FROM speaker_mappings
			GROUP BY transcription_job_id
			HAVING COUNT(*) = SUM(CASE WHEN contact_id IS NOT NULL THEN 1 ELSE 0 END)
		)`)
	}

	// Apply search filter — prefer FTS5 pre-resolved IDs when available
	if len(params.FTSJobIDs) > 0 {
		db = db.Where("id IN ?", params.FTSJobIDs)
	} else if params.SearchQuery != "" {
		search := "%" + params.SearchQuery + "%"
		db = db.Where("title LIKE ? OR audio_path LIKE ? OR transcript LIKE ?", search, search, search)
	}

	// Count total matching records
	if err := db.Count(&count).Error; err != nil {
		return nil, 0, err
	}

	// Apply sorting with allowlist to prevent SQL injection
	sortBy := params.SortBy
	sortOrder := params.SortOrder
	if sortBy != "" && allowedSortColumns[sortBy] {
		if sortOrder != "asc" && sortOrder != "desc" {
			sortOrder = "desc"
		}
		db = db.Order(sortBy + " " + sortOrder)
	} else {
		// Default sort
		db = db.Order("created_at desc")
	}

	// Apply pagination
	err := db.Offset(params.Offset).Limit(params.Limit).Find(&jobs).Error
	if err != nil {
		return nil, 0, err
	}

	return jobs, count, nil
}

func (r *jobRepository) ListDistinctSpeakers(ctx context.Context, vaultID *uint) ([]string, error) {
	var speakers []string
	db := r.db.WithContext(ctx).Model(&models.SpeakerMapping{}).
		Where("custom_name != '' AND contact_id IS NOT NULL")
	if vaultID != nil {
		db = db.Where("transcription_job_id IN (SELECT id FROM transcription_jobs WHERE vault_id = ?)", *vaultID)
	}
	err := db.Distinct("custom_name").Order("custom_name ASC").Pluck("custom_name", &speakers).Error
	return speakers, err
}

func (r *jobRepository) ListByUser(ctx context.Context, userID uint, offset, limit int) ([]models.TranscriptionJob, int64, error) {
	// Note: Currently TranscriptionJob doesn't have a UserID field in the provided model.
	// Assuming we might need to add it or this is a placeholder for future multi-user support.
	// For now, we'll just return all jobs as the current app seems single-user focused or
	// missing the link.
	// TODO: Add UserID to TranscriptionJob model if multi-user isolation is required.
	return r.List(ctx, offset, limit)
}

func (r *jobRepository) UpdateTranscript(ctx context.Context, jobID string, transcript string) error {
	return r.db.WithContext(ctx).Model(&models.TranscriptionJob{}).
		Where("id = ?", jobID).
		Update("transcript", transcript).Error
}

func (r *jobRepository) CreateExecution(ctx context.Context, execution *models.TranscriptionJobExecution) error {
	return r.db.WithContext(ctx).Create(execution).Error
}

func (r *jobRepository) UpdateExecution(ctx context.Context, execution *models.TranscriptionJobExecution) error {
	return r.db.WithContext(ctx).Save(execution).Error
}

func (r *jobRepository) DeleteExecutionsByJobID(ctx context.Context, jobID string) error {
	return r.db.WithContext(ctx).Where("transcription_job_id = ?", jobID).Delete(&models.TranscriptionJobExecution{}).Error
}

func (r *jobRepository) DeleteMultiTrackFilesByJobID(ctx context.Context, jobID string) error {
	return r.db.WithContext(ctx).Where("transcription_job_id = ?", jobID).Delete(&models.MultiTrackFile{}).Error
}

func (r *jobRepository) FindActiveTrackJobs(ctx context.Context, parentJobID string) ([]models.TranscriptionJob, error) {
	var jobs []models.TranscriptionJob
	err := r.db.WithContext(ctx).
		Where("id LIKE ? AND status IN (?)", "track_"+parentJobID+"_%", []string{"processing", "pending"}).
		Find(&jobs).Error
	return jobs, err
}

func (r *jobRepository) FindLatestCompletedExecution(ctx context.Context, jobID string) (*models.TranscriptionJobExecution, error) {
	var execution models.TranscriptionJobExecution
	err := r.db.WithContext(ctx).
		Where("transcription_job_id = ? AND status = ?", jobID, models.StatusCompleted).
		Order("created_at DESC").
		First(&execution).Error
	if err != nil {
		return nil, err
	}
	return &execution, nil
}

func (r *jobRepository) UpdateStatus(ctx context.Context, jobID string, status models.JobStatus) error {
	return r.db.WithContext(ctx).Model(&models.TranscriptionJob{}).Where("id = ?", jobID).Update("status", status).Error
}

func (r *jobRepository) UpdateError(ctx context.Context, jobID string, errorMsg string) error {
	return r.db.WithContext(ctx).Model(&models.TranscriptionJob{}).Where("id = ?", jobID).Update("error_message", errorMsg).Error
}

func (r *jobRepository) FindByStatus(ctx context.Context, status models.JobStatus) ([]models.TranscriptionJob, error) {
	var jobs []models.TranscriptionJob
	err := r.db.WithContext(ctx).Where("status = ?", status).Find(&jobs).Error
	if err != nil {
		return nil, err
	}
	return jobs, nil
}

func (r *jobRepository) CountByStatus(ctx context.Context, status models.JobStatus) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&models.TranscriptionJob{}).Where("status = ?", status).Count(&count).Error
	return count, err
}

func (r *jobRepository) UpdateSummary(ctx context.Context, jobID string, summary string) error {
	return r.db.WithContext(ctx).Model(&models.TranscriptionJob{}).Where("id = ?", jobID).Update("summary", summary).Error
}

func (r *jobRepository) ListFolders(ctx context.Context, vaultID *uint) ([]string, error) {
	var folders []string
	db := r.db.WithContext(ctx).Model(&models.TranscriptionJob{}).
		Where("folder IS NOT NULL AND folder != ''")
	if vaultID != nil {
		db = db.Where("vault_id = ?", *vaultID)
	}
	err := db.Distinct("folder").Order("folder ASC").Pluck("folder", &folders).Error
	return folders, err
}

func (r *jobRepository) UpdateFolder(ctx context.Context, jobID string, folder *string) error {
	return r.db.WithContext(ctx).Model(&models.TranscriptionJob{}).
		Where("id = ?", jobID).Update("folder", folder).Error
}

func (r *jobRepository) UpdateBundlePaths(ctx context.Context, jobID string, artifactDir, audioPath, jsonPath, mdPath *string, folder *string) error {
	updates := map[string]interface{}{
		"folder": folder,
	}
	if artifactDir != nil {
		updates["artifact_dir"] = *artifactDir
	}
	if audioPath != nil {
		updates["audio_path"] = *audioPath
	}
	if jsonPath != nil {
		updates["transcript_json_path"] = *jsonPath
	}
	if mdPath != nil {
		updates["transcript_markdown_path"] = *mdPath
	}
	return r.db.WithContext(ctx).Model(&models.TranscriptionJob{}).
		Where("id = ?", jobID).Updates(updates).Error
}

func (r *jobRepository) BulkUpdateFolder(ctx context.Context, oldFolder string, newFolder *string, vaultID *uint) (int64, error) {
	db := r.db.WithContext(ctx).Model(&models.TranscriptionJob{}).
		Where("folder = ?", oldFolder)
	if vaultID != nil {
		db = db.Where("vault_id = ?", *vaultID)
	}
	result := db.Update("folder", newFolder)
	return result.RowsAffected, result.Error
}

func (r *jobRepository) FindByIDs(ctx context.Context, ids []string) ([]models.TranscriptionJob, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var jobs []models.TranscriptionJob
	err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&jobs).Error
	return jobs, err
}

// APIKeyRepository handles API key operations
type APIKeyRepository interface {
	Repository[models.APIKey]
	FindByKey(ctx context.Context, key string) (*models.APIKey, error)
	ListActive(ctx context.Context) ([]models.APIKey, error)
	Revoke(ctx context.Context, id uint) error
}

type apiKeyRepository struct {
	*BaseRepository[models.APIKey]
}

func NewAPIKeyRepository(db *gorm.DB) APIKeyRepository {
	return &apiKeyRepository{
		BaseRepository: NewBaseRepository[models.APIKey](db),
	}
}

func (r *apiKeyRepository) FindByKey(ctx context.Context, key string) (*models.APIKey, error) {
	var apiKey models.APIKey
	err := r.db.WithContext(ctx).Where("key = ?", key).First(&apiKey).Error
	if err != nil {
		return nil, err
	}
	return &apiKey, nil
}

func (r *apiKeyRepository) ListActive(ctx context.Context) ([]models.APIKey, error) {
	var apiKeys []models.APIKey
	err := r.db.WithContext(ctx).Where("is_active = ?", true).Find(&apiKeys).Error
	if err != nil {
		return nil, err
	}
	return apiKeys, nil
}

func (r *apiKeyRepository) Revoke(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Model(&models.APIKey{}).Where("id = ?", id).Update("is_active", false).Error
}

// ProfileRepository handles transcription profile operations
type ProfileRepository interface {
	Repository[models.TranscriptionProfile]
	FindDefault(ctx context.Context) (*models.TranscriptionProfile, error)
	FindByName(ctx context.Context, name string) (*models.TranscriptionProfile, error)
}

type profileRepository struct {
	*BaseRepository[models.TranscriptionProfile]
}

func NewProfileRepository(db *gorm.DB) ProfileRepository {
	return &profileRepository{
		BaseRepository: NewBaseRepository[models.TranscriptionProfile](db),
	}
}

func (r *profileRepository) FindDefault(ctx context.Context) (*models.TranscriptionProfile, error) {
	var profile models.TranscriptionProfile
	err := r.db.WithContext(ctx).Where("is_default = ?", true).First(&profile).Error
	if err != nil {
		return nil, err
	}
	return &profile, nil
}

func (r *profileRepository) FindByName(ctx context.Context, name string) (*models.TranscriptionProfile, error) {
	var profile models.TranscriptionProfile
	err := r.db.WithContext(ctx).Where("name = ?", name).First(&profile).Error
	if err != nil {
		return nil, err
	}
	return &profile, nil
}

// LLMConfigRepository handles LLM configuration operations
type LLMConfigRepository interface {
	Repository[models.LLMConfig]
	GetActive(ctx context.Context) (*models.LLMConfig, error)
}

type llmConfigRepository struct {
	*BaseRepository[models.LLMConfig]
}

func NewLLMConfigRepository(db *gorm.DB) LLMConfigRepository {
	return &llmConfigRepository{
		BaseRepository: NewBaseRepository[models.LLMConfig](db),
	}
}

func (r *llmConfigRepository) GetActive(ctx context.Context) (*models.LLMConfig, error) {
	var config models.LLMConfig
	err := r.db.WithContext(ctx).Where("is_active = ?", true).First(&config).Error
	if err != nil {
		return nil, err
	}
	return &config, nil
}

// SummaryRepository handles summary templates and settings
type SummaryRepository interface {
	Repository[models.SummaryTemplate]
	GetSettings(ctx context.Context) (*models.SummarySetting, error)
	SaveSettings(ctx context.Context, settings *models.SummarySetting) error
	SaveSummary(ctx context.Context, summary *models.Summary) error
	GetLatestSummary(ctx context.Context, transcriptionID string) (*models.Summary, error)
	ListByTranscriptionID(ctx context.Context, transcriptionID string) ([]models.Summary, error)
	DeleteByTranscriptionID(ctx context.Context, transcriptionID string) error
	GetDefaultTemplate(ctx context.Context) (*models.SummaryTemplate, error)
	SetDefaultTemplate(ctx context.Context, id string) error
	DeleteSummary(ctx context.Context, id string) error
}

type summaryRepository struct {
	*BaseRepository[models.SummaryTemplate]
}

func NewSummaryRepository(db *gorm.DB) SummaryRepository {
	return &summaryRepository{
		BaseRepository: NewBaseRepository[models.SummaryTemplate](db),
	}
}

func (r *summaryRepository) GetSettings(ctx context.Context) (*models.SummarySetting, error) {
	var settings models.SummarySetting
	// Assuming singleton settings or per-user (but currently model might not have user_id)
	// If it's a singleton table:
	err := r.db.WithContext(ctx).First(&settings).Error
	if err != nil {
		return nil, err
	}
	return &settings, nil
}

func (r *summaryRepository) SaveSettings(ctx context.Context, settings *models.SummarySetting) error {
	return r.db.WithContext(ctx).Save(settings).Error
}

func (r *summaryRepository) SaveSummary(ctx context.Context, summary *models.Summary) error {
	return r.db.WithContext(ctx).Create(summary).Error
}

func (r *summaryRepository) GetLatestSummary(ctx context.Context, transcriptionID string) (*models.Summary, error) {
	var summary models.Summary
	err := r.db.WithContext(ctx).Where("transcription_id = ?", transcriptionID).Order("created_at DESC").First(&summary).Error
	if err != nil {
		return nil, err
	}
	return &summary, nil
}

func (r *summaryRepository) ListByTranscriptionID(ctx context.Context, transcriptionID string) ([]models.Summary, error) {
	var summaries []models.Summary
	err := r.db.WithContext(ctx).Where("transcription_id = ?", transcriptionID).Order("created_at ASC").Find(&summaries).Error
	return summaries, err
}

func (r *summaryRepository) DeleteByTranscriptionID(ctx context.Context, transcriptionID string) error {
	return r.db.WithContext(ctx).Where("transcription_id = ?", transcriptionID).Delete(&models.Summary{}).Error
}

func (r *summaryRepository) GetDefaultTemplate(ctx context.Context) (*models.SummaryTemplate, error) {
	var template models.SummaryTemplate
	err := r.db.WithContext(ctx).Where("is_default = ?", true).First(&template).Error
	if err != nil {
		return nil, err
	}
	return &template, nil
}

func (r *summaryRepository) SetDefaultTemplate(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.SummaryTemplate{}).Where("is_default = ?", true).Update("is_default", false).Error; err != nil {
			return err
		}
		return tx.Model(&models.SummaryTemplate{}).Where("id = ?", id).Update("is_default", true).Error
	})
}

func (r *summaryRepository) DeleteSummary(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&models.Summary{}).Error
}

// ChatRepository handles chat sessions and messages
type ChatRepository interface {
	Repository[models.ChatSession]
	GetSessionWithMessages(ctx context.Context, id string) (*models.ChatSession, error)
	GetSessionWithTranscription(ctx context.Context, id string) (*models.ChatSession, error)
	AddMessage(ctx context.Context, message *models.ChatMessage) error
	ListByJob(ctx context.Context, jobID string) ([]models.ChatSession, error)
	DeleteSession(ctx context.Context, id string) error
	GetMessages(ctx context.Context, sessionID string, limit int) ([]models.ChatMessage, error)
	DeleteByJobID(ctx context.Context, jobID string) error
	GetMessageCountsBySessionIDs(ctx context.Context, sessionIDs []string) (map[string]int64, error)
	GetLastMessagesBySessionIDs(ctx context.Context, sessionIDs []string) (map[string]*models.ChatMessage, error)
}

type chatRepository struct {
	*BaseRepository[models.ChatSession]
}

func NewChatRepository(db *gorm.DB) ChatRepository {
	return &chatRepository{
		BaseRepository: NewBaseRepository[models.ChatSession](db),
	}
}

func (r *chatRepository) GetSessionWithMessages(ctx context.Context, id string) (*models.ChatSession, error) {
	var session models.ChatSession
	err := r.db.WithContext(ctx).Preload("Messages").Where("id = ?", id).First(&session).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *chatRepository) GetSessionWithTranscription(ctx context.Context, id string) (*models.ChatSession, error) {
	var session models.ChatSession
	err := r.db.WithContext(ctx).Preload("Transcription").Where("id = ?", id).First(&session).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *chatRepository) AddMessage(ctx context.Context, message *models.ChatMessage) error {
	return r.db.WithContext(ctx).Create(message).Error
}

func (r *chatRepository) ListByJob(ctx context.Context, jobID string) ([]models.ChatSession, error) {
	var sessions []models.ChatSession
	err := r.db.WithContext(ctx).Where("transcription_id = ?", jobID).Order("created_at DESC").Find(&sessions).Error
	if err != nil {
		return nil, err
	}
	return sessions, nil
}

func (r *chatRepository) DeleteSession(ctx context.Context, id string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// Delete messages first
		if err := tx.Where("chat_session_id = ?", id).Delete(&models.ChatMessage{}).Error; err != nil {
			return err
		}
		// Delete session
		return tx.Delete(&models.ChatSession{}, "id = ?", id).Error
	})
}

func (r *chatRepository) DeleteByJobID(ctx context.Context, jobID string) error {
	// Find all sessions for this job
	var sessions []models.ChatSession
	if err := r.db.WithContext(ctx).Where("transcription_id = ?", jobID).Find(&sessions).Error; err != nil {
		return err
	}

	// Delete each session (which deletes messages)
	for _, session := range sessions {
		if err := r.DeleteSession(ctx, session.ID); err != nil {
			return err
		}
	}
	return nil
}

func (r *chatRepository) GetMessages(ctx context.Context, sessionID string, limit int) ([]models.ChatMessage, error) {
	var messages []models.ChatMessage
	query := r.db.WithContext(ctx).Where("chat_session_id = ?", sessionID).Order("created_at ASC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	err := query.Find(&messages).Error
	if err != nil {
		return nil, err
	}
	return messages, nil
}

func (r *chatRepository) GetMessageCountsBySessionIDs(ctx context.Context, sessionIDs []string) (map[string]int64, error) {
	if len(sessionIDs) == 0 {
		return make(map[string]int64), nil
	}

	type MessageCount struct {
		SessionID string `gorm:"column:session_id"`
		Count     int64  `gorm:"column:count"`
	}
	var counts []MessageCount

	err := r.db.WithContext(ctx).Model(&models.ChatMessage{}).
		Select("chat_session_id as session_id, COUNT(*) as count").
		Where("chat_session_id IN ?", sessionIDs).
		Group("chat_session_id").
		Scan(&counts).Error
	if err != nil {
		return nil, err
	}

	result := make(map[string]int64)
	for _, c := range counts {
		result[c.SessionID] = c.Count
	}
	return result, nil
}

func (r *chatRepository) GetLastMessagesBySessionIDs(ctx context.Context, sessionIDs []string) (map[string]*models.ChatMessage, error) {
	if len(sessionIDs) == 0 {
		return make(map[string]*models.ChatMessage), nil
	}

	var lastMessages []models.ChatMessage
	err := r.db.WithContext(ctx).Where(`id IN (
		SELECT id FROM chat_messages cm1
		WHERE cm1.chat_session_id IN ? 
		AND cm1.created_at = (
			SELECT MAX(cm2.created_at) 
			FROM chat_messages cm2 
			WHERE cm2.chat_session_id = cm1.chat_session_id
		)
	)`, sessionIDs).Find(&lastMessages).Error
	if err != nil {
		return nil, err
	}

	result := make(map[string]*models.ChatMessage)
	for i := range lastMessages {
		result[lastMessages[i].ChatSessionID] = &lastMessages[i]
	}
	return result, nil
}

// NoteRepository handles notes
type NoteRepository interface {
	Repository[models.Note]
	ListByJob(ctx context.Context, jobID string) ([]models.Note, error)
	DeleteByTranscriptionID(ctx context.Context, transcriptionID string) error
}

type noteRepository struct {
	*BaseRepository[models.Note]
}

func NewNoteRepository(db *gorm.DB) NoteRepository {
	return &noteRepository{
		BaseRepository: NewBaseRepository[models.Note](db),
	}
}

func (r *noteRepository) ListByJob(ctx context.Context, jobID string) ([]models.Note, error) {
	var notes []models.Note
	err := r.db.WithContext(ctx).Where("transcription_id = ?", jobID).Order("created_at DESC").Find(&notes).Error
	if err != nil {
		return nil, err
	}
	return notes, nil
}

func (r *noteRepository) DeleteByTranscriptionID(ctx context.Context, transcriptionID string) error {
	return r.db.WithContext(ctx).Where("transcription_id = ?", transcriptionID).Delete(&models.Note{}).Error
}

// SpeakerAttentionSummary holds per-job speaker identification status counts.
type SpeakerAttentionSummary struct {
	PendingSuggestions int `json:"pending_suggestions"`
	AutoAssigned       int `json:"auto_assigned"`
	TotalMappings      int `json:"total_mappings"`
	Renamed            int `json:"renamed"`
}

// SpeakerMappingRepository handles speaker mappings
type SpeakerMappingRepository interface {
	Repository[models.SpeakerMapping]
	ListByJob(ctx context.Context, jobID string) ([]models.SpeakerMapping, error)
	ListPendingSuggestions(ctx context.Context, jobID string) ([]models.SpeakerMapping, error)
	CountPendingSuggestions(ctx context.Context, jobIDs []string) (map[string]int, error)
	GetSpeakerAttentionSummary(ctx context.Context, jobIDs []string) (map[string]SpeakerAttentionSummary, error)
	UpdateReviewStatus(ctx context.Context, id uint, status string) error
	UpdateMappings(ctx context.Context, jobID string, mappings []models.SpeakerMapping) error
	UpsertMapping(ctx context.Context, jobID string, mapping models.SpeakerMapping) (*models.SpeakerMapping, error)
	DeleteByJobID(ctx context.Context, jobID string) error
	ListJobIDsByContactID(ctx context.Context, contactID uint) ([]string, error)
	ListByContactID(ctx context.Context, contactID uint) ([]models.SpeakerMapping, error)
	SetContactID(ctx context.Context, mappingID uint, contactID *uint) error
}

type speakerMappingRepository struct {
	*BaseRepository[models.SpeakerMapping]
}

func NewSpeakerMappingRepository(db *gorm.DB) SpeakerMappingRepository {
	return &speakerMappingRepository{
		BaseRepository: NewBaseRepository[models.SpeakerMapping](db),
	}
}

func (r *speakerMappingRepository) ListByJob(ctx context.Context, jobID string) ([]models.SpeakerMapping, error) {
	var mappings []models.SpeakerMapping
	err := r.db.WithContext(ctx).Where("transcription_job_id = ?", jobID).Find(&mappings).Error
	if err != nil {
		return nil, err
	}
	return mappings, nil
}

func (r *speakerMappingRepository) DeleteByJobID(ctx context.Context, jobID string) error {
	return r.db.WithContext(ctx).Where("transcription_job_id = ?", jobID).Delete(&models.SpeakerMapping{}).Error
}

func (r *speakerMappingRepository) UpsertMapping(ctx context.Context, jobID string, mapping models.SpeakerMapping) (*models.SpeakerMapping, error) {
	var result models.SpeakerMapping
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing models.SpeakerMapping
		findErr := tx.Where("transcription_job_id = ? AND original_speaker = ?", jobID, mapping.OriginalSpeaker).First(&existing).Error
		if findErr == nil {
			// Update existing row.
			existing.CustomName = mapping.CustomName
			existing.ContactID = mapping.ContactID
			existing.ConfidenceScore = mapping.ConfidenceScore
			existing.MatchSource = mapping.MatchSource
			existing.MatchTier = mapping.MatchTier
			existing.ReviewStatus = mapping.ReviewStatus
			if err := tx.Save(&existing).Error; err != nil {
				return err
			}
			result = existing
			return nil
		}
		if findErr != gorm.ErrRecordNotFound {
			return findErr
		}
		// Create new row.
		mapping.TranscriptionJobID = jobID
		if err := tx.Create(&mapping).Error; err != nil {
			return err
		}
		result = mapping
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (r *speakerMappingRepository) UpdateMappings(ctx context.Context, jobID string, mappings []models.SpeakerMapping) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Delete existing mappings for this job
		if err := tx.Where("transcription_job_id = ?", jobID).Delete(&models.SpeakerMapping{}).Error; err != nil {
			return err
		}

		// Create new mappings
		if len(mappings) > 0 {
			if err := tx.Create(&mappings).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *speakerMappingRepository) ListPendingSuggestions(ctx context.Context, jobID string) ([]models.SpeakerMapping, error) {
	var mappings []models.SpeakerMapping
	err := r.db.WithContext(ctx).
		Where("transcription_job_id = ? AND review_status = ?", jobID, "pending").
		Find(&mappings).Error
	if err != nil {
		return nil, err
	}
	return mappings, nil
}

func (r *speakerMappingRepository) CountPendingSuggestions(ctx context.Context, jobIDs []string) (map[string]int, error) {
	if len(jobIDs) == 0 {
		return map[string]int{}, nil
	}

	type countRow struct {
		TranscriptionJobID string
		Count              int
	}
	var rows []countRow
	err := r.db.WithContext(ctx).
		Model(&models.SpeakerMapping{}).
		Select("transcription_job_id, COUNT(*) as count").
		Where("transcription_job_id IN ? AND review_status = ?", jobIDs, "pending").
		Group("transcription_job_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	result := make(map[string]int, len(rows))
	for _, row := range rows {
		result[row.TranscriptionJobID] = row.Count
	}
	return result, nil
}

func (r *speakerMappingRepository) GetSpeakerAttentionSummary(ctx context.Context, jobIDs []string) (map[string]SpeakerAttentionSummary, error) {
	if len(jobIDs) == 0 {
		return map[string]SpeakerAttentionSummary{}, nil
	}

	type summaryRow struct {
		TranscriptionJobID string
		Total              int
		Pending            int
		Auto               int
		Renamed            int
	}
	var rows []summaryRow
	err := r.db.WithContext(ctx).
		Model(&models.SpeakerMapping{}).
		Select(`transcription_job_id,
			COUNT(*) as total,
			SUM(CASE WHEN review_status = 'pending' THEN 1 ELSE 0 END) as pending,
			SUM(CASE WHEN match_tier = 'auto' THEN 1 ELSE 0 END) as auto,
			SUM(CASE WHEN custom_name != '' AND custom_name != original_speaker AND (review_status IS NULL OR review_status = '' OR review_status != 'pending') THEN 1 ELSE 0 END) as renamed`).
		Where("transcription_job_id IN ?", jobIDs).
		Group("transcription_job_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	result := make(map[string]SpeakerAttentionSummary, len(rows))
	for _, row := range rows {
		result[row.TranscriptionJobID] = SpeakerAttentionSummary{
			PendingSuggestions: row.Pending,
			AutoAssigned:       row.Auto,
			TotalMappings:      row.Total,
			Renamed:            row.Renamed,
		}
	}
	return result, nil
}

func (r *speakerMappingRepository) UpdateReviewStatus(ctx context.Context, id uint, status string) error {
	return r.db.WithContext(ctx).
		Model(&models.SpeakerMapping{}).
		Where("id = ?", id).
		Update("review_status", status).Error
}

func (r *speakerMappingRepository) ListJobIDsByContactID(ctx context.Context, contactID uint) ([]string, error) {
	var jobIDs []string
	err := r.db.WithContext(ctx).
		Model(&models.SpeakerMapping{}).
		Where("contact_id = ?", contactID).
		Distinct("transcription_job_id").
		Pluck("transcription_job_id", &jobIDs).Error
	return jobIDs, err
}

func (r *speakerMappingRepository) ListByContactID(ctx context.Context, contactID uint) ([]models.SpeakerMapping, error) {
	var mappings []models.SpeakerMapping
	err := r.db.WithContext(ctx).Where("contact_id = ?", contactID).Find(&mappings).Error
	if err != nil {
		return nil, err
	}
	return mappings, nil
}

func (r *speakerMappingRepository) SetContactID(ctx context.Context, mappingID uint, contactID *uint) error {
	return r.db.WithContext(ctx).
		Model(&models.SpeakerMapping{}).
		Where("id = ?", mappingID).
		Update("contact_id", contactID).Error
}

// RefreshTokenRepository handles refresh token operations
type RefreshTokenRepository interface {
	Create(ctx context.Context, token *models.RefreshToken) error
	FindByHash(ctx context.Context, hash string) (*models.RefreshToken, error)
	Revoke(ctx context.Context, id uint) error
	RevokeByHash(ctx context.Context, hash string) error
}

type refreshTokenRepository struct {
	db *gorm.DB
}

func NewRefreshTokenRepository(db *gorm.DB) RefreshTokenRepository {
	return &refreshTokenRepository{db: db}
}

func (r *refreshTokenRepository) Create(ctx context.Context, token *models.RefreshToken) error {
	return r.db.WithContext(ctx).Create(token).Error
}

func (r *refreshTokenRepository) FindByHash(ctx context.Context, hash string) (*models.RefreshToken, error) {
	var token models.RefreshToken
	err := r.db.WithContext(ctx).Where("hashed = ?", hash).First(&token).Error
	if err != nil {
		return nil, err
	}
	return &token, nil
}

func (r *refreshTokenRepository) Revoke(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Model(&models.RefreshToken{}).Where("id = ?", id).Update("revoked", true).Error
}

func (r *refreshTokenRepository) RevokeByHash(ctx context.Context, hash string) error {
	return r.db.WithContext(ctx).Model(&models.RefreshToken{}).Where("hashed = ?", hash).Update("revoked", true).Error
}
