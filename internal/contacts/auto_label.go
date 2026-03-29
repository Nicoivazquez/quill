package contacts

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"quill/internal/models"
	"quill/internal/repository"
	"quill/pkg/logger"

	"gorm.io/gorm"
)

// AutoLabelResult holds the categorised outcome of an auto-label run.
type AutoLabelResult struct {
	// AutoAssigned contains speakers that were automatically mapped to a contact
	// (cosine similarity >= 0.80).
	AutoAssigned []SpeakerMatch

	// Suggestions contains speakers for which a likely contact was found but
	// confidence was not high enough to auto-apply (0.60 <= score < 0.80).
	Suggestions []SpeakerMatch

	// Unmatched contains speaker labels for which no contact embedding scored
	// above the minimum threshold (0.60).
	Unmatched []string
}

// LLMCaller is a function that sends a prompt to an LLM and returns the
// response text.  Inject via SetLLMCaller to enable LLM-assisted speaker
// identification (voice + LLM fusion scoring).
type LLMCaller func(ctx context.Context, prompt string) (string, error)

// AutoLabelService orchestrates the speaker auto-identification pipeline after
// a transcription job completes.
//
// Workflow:
//  1. Load all contacts with SignatureStatus == "ready" for the active vault.
//  2. Load each contact's embedding vector from disk.
//  3. Obtain per-speaker embeddings for the completed transcription (see note).
//  4. Run MatchSpeakers to find the best contact per speaker.
//  5. Optionally fuse voice scores with LLM contextual analysis (if LLMCaller
//     is set and transcript text is provided).
//  6. For auto-tier matches, persist SpeakerMapping rows.
//  7. Return the result so the caller can push an SSE notification.
//
// Speaker embedding source (current implementation):
// The diarization pipeline does not yet emit per-speaker embedding files.
// This service therefore accepts a pre-built speakerEmbeddings map from the
// caller (populated by the transcription adapter or the bootstrap flow).
// When the adapter starts producing embeddings natively, this map can be
// populated directly from the transcript artifact without a separate FFmpeg
// clip step.
type AutoLabelService struct {
	contactRepo    repository.ContactRepository
	speakerMapRepo repository.SpeakerMappingRepository
	db             *gorm.DB
	llmCaller      LLMCaller
}

// NewAutoLabelService creates a ready-to-use AutoLabelService.
func NewAutoLabelService(
	contactRepo repository.ContactRepository,
	speakerMapRepo repository.SpeakerMappingRepository,
	db *gorm.DB,
) *AutoLabelService {
	return &AutoLabelService{
		contactRepo:    contactRepo,
		speakerMapRepo: speakerMapRepo,
		db:             db,
	}
}

// SetLLMCaller injects an LLM caller for voice+LLM fusion scoring.
// When set (and transcript text is provided to LabelSpeakers), the service
// will query the LLM for contextual speaker guesses and fuse the scores
// with voice embeddings using FuseScores (60:40 voice:LLM weighting).
func (s *AutoLabelService) SetLLMCaller(caller LLMCaller) {
	s.llmCaller = caller
}

// LabelSpeakers runs the full auto-identification pipeline for a single vault.
//
// Parameters:
//   - ctx: request/background context.
//   - vaultID: ID of the vault that owns the transcription.
//   - vaultPath: absolute filesystem path to the vault root (used to resolve
//     relative embedding file paths stored in the DB).
//   - jobID: transcription job ID (used when persisting SpeakerMapping rows).
//   - speakerEmbeddings: map of speaker label → voice embedding vector.  The
//     caller is responsible for extracting these (e.g. via the bootstrap flow
//     in internal/api/speaker_mapping_contact_bootstrap.go).
//
// Returns AutoLabelResult and any fatal error.  Non-fatal per-speaker errors
// are logged and the affected speakers are placed in Unmatched.
func (s *AutoLabelService) LabelSpeakers(
	ctx context.Context,
	vaultID uint,
	vaultPath string,
	jobID string,
	speakerEmbeddings map[string][]float64,
	transcriptText string,
) (*AutoLabelResult, error) {
	result := &AutoLabelResult{
		AutoAssigned: []SpeakerMatch{},
		Suggestions:  []SpeakerMatch{},
		Unmatched:    []string{},
	}

	if len(speakerEmbeddings) == 0 {
		return result, nil
	}

	// Step 1: load contacts with a ready voice signature.
	readyContacts, err := s.contactRepo.ListBySignatureStatus(ctx, vaultID, "ready")
	if err != nil {
		return nil, fmt.Errorf("auto-label: list ready contacts: %w", err)
	}
	if len(readyContacts) == 0 {
		for speaker := range speakerEmbeddings {
			result.Unmatched = append(result.Unmatched, speaker)
		}
		return result, nil
	}

	// Step 2: load embedding vectors from disk.
	fileService := NewFileService(vaultPath)
	contactEmbeddings := make([]ContactEmbedding, 0, len(readyContacts))

	for i := range readyContacts {
		c := &readyContacts[i]
		if c.SignatureEmbeddingPath == nil || *c.SignatureEmbeddingPath == "" {
			logger.Warn("auto-label: contact has ready status but no embedding path",
				"contact_id", c.ID, "contact_name", c.Name)
			continue
		}

		absPath := fileService.ResolveAbsPath(*c.SignatureEmbeddingPath)
		if absPath == "" {
			absPath = filepath.Clean(*c.SignatureEmbeddingPath)
		}

		vec, loadErr := LoadEmbeddingVector(absPath)
		if loadErr != nil {
			logger.Warn("auto-label: failed to load contact embedding",
				"contact_id", c.ID, "path", absPath, "error", loadErr)
			continue
		}

		contactEmbeddings = append(contactEmbeddings, ContactEmbedding{
			ContactID:   c.ID,
			ContactName: c.Name,
			Vector:      vec,
		})
	}

	if len(contactEmbeddings) == 0 {
		for speaker := range speakerEmbeddings {
			result.Unmatched = append(result.Unmatched, speaker)
		}
		return result, nil
	}

	// Step 3: match speakers against contacts.
	matchResult := MatchSpeakers(speakerEmbeddings, contactEmbeddings)

	// Step 3b: optionally fuse voice scores with LLM contextual analysis.
	fusedMatches := matchResult.Matches
	fusedUnmatched := matchResult.Unmatched

	if s.llmCaller != nil && strings.TrimSpace(transcriptText) != "" {
		speakerLabels := make([]string, 0, len(speakerEmbeddings))
		for label := range speakerEmbeddings {
			speakerLabels = append(speakerLabels, label)
		}
		contactNames := make([]string, 0, len(contactEmbeddings))
		for _, ce := range contactEmbeddings {
			contactNames = append(contactNames, ce.ContactName)
		}

		prompt := BuildSpeakerIDPrompt(transcriptText, speakerLabels, contactNames)
		llmResponse, llmErr := s.llmCaller(ctx, prompt)
		if llmErr != nil {
			logger.Warn("auto-label: LLM call failed, falling back to voice-only",
				"job_id", jobID, "error", llmErr)
		} else {
			guesses := ParseLLMSpeakerGuesses(llmResponse, speakerLabels)
			fusedMatches = FuseScores(matchResult.Matches, guesses)

			// Rebuild unmatched list: speakers not present in fused matches
			// or with scores below the minimum threshold.
			matched := make(map[string]struct{}, len(fusedMatches))
			for _, fm := range fusedMatches {
				if fm.Tier != TierUnknown {
					matched[fm.Speaker] = struct{}{}
				}
			}
			fusedUnmatched = []string{}
			for label := range speakerEmbeddings {
				if _, ok := matched[label]; !ok {
					fusedUnmatched = append(fusedUnmatched, label)
				}
			}
		}
	}

	result.Unmatched = fusedUnmatched

	// Step 4: split matches by tier; persist both auto and suggest-tier mappings.
	for _, m := range fusedMatches {
		switch m.Tier {
		case TierAutoAssign:
			result.AutoAssigned = append(result.AutoAssigned, m)
			if err := s.persistMapping(ctx, jobID, m, ""); err != nil {
				logger.Warn("auto-label: failed to persist speaker mapping",
					"job_id", jobID, "speaker", m.Speaker, "contact_id", m.ContactID, "error", err)
			}
		case TierSuggest:
			result.Suggestions = append(result.Suggestions, m)
			if err := s.persistMapping(ctx, jobID, m, "pending"); err != nil {
				logger.Warn("auto-label: failed to persist suggestion mapping",
					"job_id", jobID, "speaker", m.Speaker, "contact_id", m.ContactID, "error", err)
			}
		}
	}

	return result, nil
}

// persistMapping writes a SpeakerMapping row linking the speaker label to the
// matched contact for this job.  It checks for an existing mapping first and
// skips creation if one already exists for this job+speaker combination.
// reviewStatus should be "" for auto-tier (immediately accepted) and "pending"
// for suggest-tier (awaiting user review).
func (s *AutoLabelService) persistMapping(ctx context.Context, jobID string, m SpeakerMatch, reviewStatus string) error {
	// Check if a mapping already exists for this job+speaker combination.
	existing, err := s.speakerMapRepo.ListByJob(ctx, jobID)
	if err != nil {
		return fmt.Errorf("persistMapping: list existing: %w", err)
	}
	for i := range existing {
		if existing[i].OriginalSpeaker == m.Speaker {
			// Already mapped — leave it alone.
			return nil
		}
	}

	contactID := m.ContactID
	mapping := &models.SpeakerMapping{
		TranscriptionJobID: jobID,
		OriginalSpeaker:    m.Speaker,
		CustomName:         m.ContactName,
		ContactID:          &contactID,
		ConfidenceScore:    m.Score,
		MatchSource:        "auto",
		MatchTier:          string(m.Tier),
		ReviewStatus:       reviewStatus,
	}
	return s.speakerMapRepo.Create(ctx, mapping)
}
