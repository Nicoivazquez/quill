package contacts

import (
	"context"
	"fmt"
	"strings"

	"quill/internal/models"
	"quill/internal/repository"
	"quill/pkg/logger"

	"gorm.io/gorm"
)

// SpeakerEmbeddingExtractor extracts per-speaker voice embeddings from a
// transcription job's audio file.  The function returns a map of speaker
// label → 256-dim TitaNet embedding vector.
//
// In production this runs FFmpeg + TitaNet.  In tests it can be replaced
// with a mock that returns pre-built vectors.
type SpeakerEmbeddingExtractor func(
	ctx context.Context,
	job *models.TranscriptionJob,
	vaultPath string,
) (map[string][]float64, error)

// RetroactiveScanResult summarizes a retroactive speaker identification pass.
type RetroactiveScanResult struct {
	JobsScanned  int `json:"jobs_scanned"`
	JobsMatched  int `json:"jobs_matched"`
	AutoAssigned int `json:"auto_assigned"`
	Suggestions  int `json:"suggestions"`
	Skipped      int `json:"skipped"`
	Errors       int `json:"errors"`
}

// RetroactiveScanService scans past transcriptions when a contact receives a
// new voice signature, matching the contact's embedding against speakers in
// completed diarized jobs.
type RetroactiveScanService struct {
	jobRepo        repository.JobRepository
	contactRepo    repository.ContactRepository
	speakerMapRepo repository.SpeakerMappingRepository
	db             *gorm.DB
	extractFunc    SpeakerEmbeddingExtractor
	llmCaller      LLMCaller
}

// NewRetroactiveScanService creates a new retroactive scan service.
func NewRetroactiveScanService(
	jobRepo repository.JobRepository,
	contactRepo repository.ContactRepository,
	speakerMapRepo repository.SpeakerMappingRepository,
	db *gorm.DB,
) *RetroactiveScanService {
	return &RetroactiveScanService{
		jobRepo:        jobRepo,
		contactRepo:    contactRepo,
		speakerMapRepo: speakerMapRepo,
		db:             db,
	}
}

// SetExtractor sets the speaker embedding extraction function.
// Use this to inject a mock in tests or to swap in a cached extractor.
func (s *RetroactiveScanService) SetExtractor(fn SpeakerEmbeddingExtractor) {
	s.extractFunc = fn
}

// SetLLMCaller injects an LLM caller for voice+LLM fusion scoring during
// retroactive scanning.
func (s *RetroactiveScanService) SetLLMCaller(caller LLMCaller) {
	s.llmCaller = caller
}

// ScanForContact scans all completed diarized jobs in the contact's vault,
// comparing each job's speaker embeddings against the contact's voice signature.
// Matches above the auto-assign threshold (>=0.80) are persisted as speaker
// mappings with MatchSource="retroactive".
func (s *RetroactiveScanService) ScanForContact(ctx context.Context, contactID uint) (*RetroactiveScanResult, error) {
	result := &RetroactiveScanResult{}

	// Load the contact.
	contact, err := s.contactRepo.GetByID(ctx, contactID)
	if err != nil {
		return result, fmt.Errorf("retroactive scan: contact not found: %w", err)
	}

	// Must have a ready voice signature.
	if contact.SignatureStatus != "ready" {
		return result, nil
	}
	if contact.SignatureEmbeddingPath == nil || *contact.SignatureEmbeddingPath == "" {
		return result, nil
	}

	// Load the contact's embedding vector.
	contactVec, err := LoadEmbeddingVector(*contact.SignatureEmbeddingPath)
	if err != nil {
		return result, fmt.Errorf("retroactive scan: load contact embedding: %w", err)
	}

	// Resolve vault.
	var vault models.Vault
	if err := s.db.WithContext(ctx).First(&vault, contact.VaultID).Error; err != nil {
		return result, fmt.Errorf("retroactive scan: resolve vault: %w", err)
	}

	// Find all completed diarized jobs in this vault.
	jobs, err := s.findEligibleJobs(ctx, contact.VaultID)
	if err != nil {
		return result, fmt.Errorf("retroactive scan: find eligible jobs: %w", err)
	}

	contactEmb := ContactEmbedding{
		ContactID:   contact.ID,
		ContactName: contact.Name,
		Vector:      contactVec,
	}

	for i := range jobs {
		job := &jobs[i]

		speakerEmbeddings, extractErr := s.extractFunc(ctx, job, vault.Path)
		if extractErr != nil {
			logger.Warn("retroactive scan: extraction failed",
				"job_id", job.ID, "error", extractErr)
			result.Errors++
			continue
		}
		if len(speakerEmbeddings) == 0 {
			continue
		}

		result.JobsScanned++

		// Filter out speakers that already have a non-raw mapping.
		existingMappings, _ := s.speakerMapRepo.ListByJob(ctx, job.ID)
		unmappedSpeakers := filterUnmappedSpeakers(speakerEmbeddings, existingMappings)

		if len(unmappedSpeakers) == 0 {
			result.Skipped++
			continue
		}

		// Match remaining speakers against the single contact.
		matchResult := MatchSpeakers(unmappedSpeakers, []ContactEmbedding{contactEmb})

		// Optionally fuse with LLM analysis.
		fusedMatches := matchResult.Matches
		if s.llmCaller != nil && job.Transcript != nil && strings.TrimSpace(*job.Transcript) != "" {
			speakerLabels := make([]string, 0, len(unmappedSpeakers))
			for label := range unmappedSpeakers {
				speakerLabels = append(speakerLabels, label)
			}
			prompt := BuildSpeakerIDPrompt(*job.Transcript, speakerLabels, []string{contact.Name})
			llmResp, llmErr := s.llmCaller(ctx, prompt)
			if llmErr != nil {
				logger.Warn("retroactive scan: LLM call failed, using voice-only",
					"job_id", job.ID, "error", llmErr)
			} else {
				guesses := ParseLLMSpeakerGuesses(llmResp, speakerLabels)
				fusedMatches = FuseScores(matchResult.Matches, guesses)
			}
		}

		jobMatched := false
		for _, m := range fusedMatches {
			switch m.Tier {
			case TierAutoAssign:
				if persistErr := s.persistRetroactiveMapping(ctx, job.ID, m); persistErr != nil {
					logger.Warn("retroactive scan: persist failed",
						"job_id", job.ID, "speaker", m.Speaker, "error", persistErr)
					result.Errors++
				} else {
					result.AutoAssigned++
					jobMatched = true
				}
			case TierSuggest:
				result.Suggestions++
				jobMatched = true
			}
		}

		if jobMatched {
			result.JobsMatched++
		}
	}

	return result, nil
}

// findEligibleJobs returns completed diarized jobs for the given vault.
func (s *RetroactiveScanService) findEligibleJobs(ctx context.Context, vaultID uint) ([]models.TranscriptionJob, error) {
	var jobs []models.TranscriptionJob
	err := s.db.WithContext(ctx).
		Where("vault_id = ? AND status = ? AND diarization = ?", vaultID, models.StatusCompleted, true).
		Find(&jobs).Error
	return jobs, err
}

// filterUnmappedSpeakers returns only the speaker embeddings for speakers
// that don't already have a non-raw mapping (i.e., a human-assigned or
// auto-assigned custom name that differs from the original label).
func filterUnmappedSpeakers(
	speakerEmbeddings map[string][]float64,
	existingMappings []models.SpeakerMapping,
) map[string][]float64 {
	mapped := make(map[string]bool)
	for _, m := range existingMappings {
		// A speaker is considered "mapped" if it has a custom name that
		// differs from the raw label (e.g. not "speaker_00" → "speaker_00").
		if m.CustomName != "" && m.CustomName != m.OriginalSpeaker {
			mapped[m.OriginalSpeaker] = true
		}
	}

	result := make(map[string][]float64)
	for speaker, vec := range speakerEmbeddings {
		if !mapped[speaker] {
			result[speaker] = vec
		}
	}
	return result
}

// persistRetroactiveMapping creates a speaker mapping from a retroactive scan
// match, marking it with MatchSource="retroactive".
func (s *RetroactiveScanService) persistRetroactiveMapping(ctx context.Context, jobID string, m SpeakerMatch) error {
	mapping := &models.SpeakerMapping{
		TranscriptionJobID: jobID,
		OriginalSpeaker:    m.Speaker,
		CustomName:         m.ContactName,
		ConfidenceScore:    m.Score,
		MatchSource:        "retroactive",
		MatchTier:          string(m.Tier),
	}
	return s.speakerMapRepo.Create(ctx, mapping)
}
