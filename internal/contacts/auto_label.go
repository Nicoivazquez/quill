package contacts

import (
	"context"
	"fmt"
	"path/filepath"

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

// AutoLabelService orchestrates the speaker auto-identification pipeline after
// a transcription job completes.
//
// Workflow:
//  1. Load all contacts with SignatureStatus == "ready" for the active vault.
//  2. Load each contact's embedding vector from disk.
//  3. Obtain per-speaker embeddings for the completed transcription (see note).
//  4. Run MatchSpeakers to find the best contact per speaker.
//  5. For auto-tier matches, persist SpeakerMapping rows.
//  6. Return the result so the caller can push an SSE notification.
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

	result.Unmatched = matchResult.Unmatched

	// Step 4: split matches by tier; persist auto-tier mappings.
	for _, m := range matchResult.Matches {
		switch m.Tier {
		case TierAutoAssign:
			result.AutoAssigned = append(result.AutoAssigned, m)
			if err := s.persistMapping(ctx, jobID, m); err != nil {
				logger.Warn("auto-label: failed to persist speaker mapping",
					"job_id", jobID, "speaker", m.Speaker, "contact_id", m.ContactID, "error", err)
			}
		case TierSuggest:
			result.Suggestions = append(result.Suggestions, m)
		}
	}

	return result, nil
}

// persistMapping writes a SpeakerMapping row linking the speaker label to the
// matched contact for this job.  It checks for an existing mapping first and
// skips creation if one already exists for this job+speaker combination.
func (s *AutoLabelService) persistMapping(ctx context.Context, jobID string, m SpeakerMatch) error {
	// Check if a mapping already exists for this job+speaker combination.
	existing, err := s.speakerMapRepo.ListByJob(ctx, jobID)
	if err == nil {
		for i := range existing {
			if existing[i].OriginalSpeaker == m.Speaker {
				// Already mapped — leave it alone.
				return nil
			}
		}
	}

	mapping := &models.SpeakerMapping{
		TranscriptionJobID: jobID,
		OriginalSpeaker:    m.Speaker,
		CustomName:         m.ContactName,
	}
	return s.speakerMapRepo.Create(ctx, mapping)
}
