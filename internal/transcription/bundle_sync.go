package transcription

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"quill/internal/models"
	"quill/internal/repository"
	"quill/pkg/logger"

	"github.com/google/uuid"
)

// ScannedBundle represents a bundle directory found on disk during scanning.
type ScannedBundle struct {
	Dir     string // absolute path to the bundle directory
	Folder  string // relative folder path (empty = root level)
	MtimeNS int64  // mtime of metadata.json in nanoseconds (0 if absent)
}

// BundleSyncResult summarizes a filesystem synchronization pass.
type BundleSyncResult struct {
	Imported int `json:"imported"`
	Updated  int `json:"updated"`
	Deleted  int `json:"deleted"`
	Skipped  int `json:"skipped"`
}

// ScanBundles walks the transcripts directory and returns all bundle directories found.
// A bundle is any directory containing audio.* or transcript.json.
func ScanBundles(transcriptsDir string) ([]ScannedBundle, error) {
	var bundles []ScannedBundle

	err := filepath.WalkDir(transcriptsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible
		}
		if !d.IsDir() || path == transcriptsDir {
			return nil
		}

		if !isBundleDir(path) {
			return nil
		}

		// Determine folder relative to transcriptsDir
		rel, _ := filepath.Rel(transcriptsDir, path)
		folder := ""
		if parent := filepath.Dir(rel); parent != "." {
			folder = parent
		}

		// Get metadata mtime if exists
		var mtimeNS int64
		if info, statErr := os.Stat(MetadataPath(path)); statErr == nil {
			mtimeNS = info.ModTime().UnixNano()
		}

		bundles = append(bundles, ScannedBundle{
			Dir:     path,
			Folder:  folder,
			MtimeNS: mtimeNS,
		})

		return filepath.SkipDir // don't descend into bundle dirs
	})

	return bundles, err
}

// BundleSyncService reconciles bundle metadata.json files on disk with the DB cache.
type BundleSyncService struct {
	jobRepo            repository.JobRepository
	speakerMappingRepo repository.SpeakerMappingRepository
	summaryRepo        repository.SummaryRepository
	noteRepo           repository.NoteRepository
	transcriptsDir     string
	vaultID            *uint

	mu        sync.Mutex
	selfWrite map[string]int64
}

// NewBundleSyncService creates a new bundle sync service.
func NewBundleSyncService(
	jobRepo repository.JobRepository,
	speakerMappingRepo repository.SpeakerMappingRepository,
	summaryRepo repository.SummaryRepository,
	noteRepo repository.NoteRepository,
	transcriptsDir string,
	vaultID *uint,
) *BundleSyncService {
	return &BundleSyncService{
		jobRepo:            jobRepo,
		speakerMappingRepo: speakerMappingRepo,
		summaryRepo:        summaryRepo,
		noteRepo:           noteRepo,
		transcriptsDir:     transcriptsDir,
		vaultID:            vaultID,
		selfWrite:          make(map[string]int64),
	}
}

// SyncFromFilesystem scans the transcripts directory for bundles, reads their
// metadata.json, and creates/updates/deletes DB records to match.
func (s *BundleSyncService) SyncFromFilesystem(ctx context.Context) (BundleSyncResult, error) {
	result := BundleSyncResult{}

	bundles, err := ScanBundles(s.transcriptsDir)
	if err != nil {
		return result, fmt.Errorf("scanning bundles: %w", err)
	}

	seenIDs := make(map[string]bool, len(bundles))

	for _, bundle := range bundles {
		meta, readErr := ReadOrCreateMetadata(bundle.Dir)
		if readErr != nil {
			logger.Warn("bundle sync: failed to read metadata", "dir", bundle.Dir, "error", readErr)
			continue
		}

		if meta.ID == "" {
			meta.ID = uuid.New().String()
		}
		seenIDs[meta.ID] = true

		// Skip self-written metadata
		if bundle.MtimeNS > 0 && s.isSelfWrite(MetadataPath(bundle.Dir), bundle.MtimeNS) {
			result.Skipped++
			continue
		}

		existing, findErr := s.jobRepo.FindByID(ctx, meta.ID)
		if findErr != nil {
			// Job doesn't exist — import
			if importErr := s.importBundle(ctx, meta, bundle); importErr != nil {
				logger.Warn("bundle sync: import failed", "id", meta.ID, "dir", bundle.Dir, "error", importErr)
				continue
			}
			result.Imported++
			continue
		}

		// Force reconciliation if the bundle moved on disk but DB paths are stale.
		// This repairs data corrupted by the pre-fix folder move bug where disk
		// rename succeeded but DB paths were not updated correctly.
		pathMismatch := existing.ArtifactDir != nil &&
			*existing.ArtifactDir != "" &&
			filepath.Clean(*existing.ArtifactDir) != filepath.Clean(bundle.Dir)

		if !pathMismatch {
			// Job exists — skip if DB is at least as new as metadata on disk.
			// Compare at second precision because SQLite truncates timestamp nanoseconds.
			if bundle.MtimeNS > 0 {
				metaSec := time.Unix(0, bundle.MtimeNS).Unix()
				if existing.UpdatedAt.Unix() >= metaSec {
					result.Skipped++
					continue
				}
			} else {
				// No metadata.json on disk — nothing to update from
				result.Skipped++
				continue
			}
		}

		if pathMismatch {
			logger.Info("bundle sync: detected path mismatch, forcing reconciliation",
				"id", meta.ID, "db_dir", *existing.ArtifactDir, "disk_dir", bundle.Dir)
		}

		if updateErr := s.updateFromMetadata(ctx, existing, meta, bundle); updateErr != nil {
			logger.Warn("bundle sync: update failed", "id", meta.ID, "error", updateErr)
			continue
		}
		result.Updated++
	}

	// Delete orphaned DB records (bundle dir no longer on disk)
	if s.vaultID != nil {
		jobs, _, listErr := s.jobRepo.ListWithParams(ctx, repository.ListParams{
			Offset:  0,
			Limit:   10000,
			VaultID: s.vaultID,
		})
		if listErr == nil {
			for _, job := range jobs {
				if seenIDs[job.ID] {
					continue
				}
				if job.ArtifactDir == nil || !strings.HasPrefix(*job.ArtifactDir, s.transcriptsDir) {
					continue
				}
				if _, statErr := os.Stat(*job.ArtifactDir); statErr == nil {
					continue // dir still exists
				}
				if delErr := s.deleteJobAndRelated(ctx, job.ID); delErr != nil {
					logger.Warn("bundle sync: failed to delete orphan", "id", job.ID, "error", delErr)
					continue
				}
				result.Deleted++
			}
		}
	}

	return result, nil
}

func (s *BundleSyncService) importBundle(ctx context.Context, meta *BundleMetadata, bundle ScannedBundle) error {
	audioPath := findAudioFile(bundle.Dir)
	if audioPath == "" {
		// Audio-less bundles are allowed (audio may have been lost or never moved).
		// Import with empty audio path so the transcript/notes/summaries are accessible.
		logger.Warn("bundle sync: no audio file in bundle, importing anyway", "dir", bundle.Dir)
	}

	jsonPath := filepath.Join(bundle.Dir, "transcript.json")
	mdPath := filepath.Join(bundle.Dir, "transcript.md")

	var jsonPtr, mdPtr *string
	if _, err := os.Stat(jsonPath); err == nil {
		jsonPtr = &jsonPath
	}
	if _, err := os.Stat(mdPath); err == nil {
		mdPtr = &mdPath
	}

	dir := bundle.Dir
	job := &models.TranscriptionJob{
		ID:                     meta.ID,
		Status:                 models.JobStatus(meta.Status),
		AudioPath:              audioPath,
		VaultID:                s.vaultID,
		ArtifactDir:            &dir,
		TranscriptJSONPath:     jsonPtr,
		TranscriptMarkdownPath: mdPtr,
		Diarization:            meta.Diarization,
		IsMultiTrack:           meta.IsMultiTrack,
		CreatedAt:              meta.CreatedAt,
		UpdatedAt:              meta.UpdatedAt,
	}

	if meta.Title != "" {
		job.Title = &meta.Title
	}
	if meta.Folder != "" {
		job.Folder = &meta.Folder
	} else if bundle.Folder != "" {
		job.Folder = &bundle.Folder
	}

	// Read transcript content if available
	if jsonPtr != nil {
		if data, readErr := os.ReadFile(*jsonPtr); readErr == nil {
			content := string(data)
			job.Transcript = &content
		}
	}

	if err := s.jobRepo.Create(ctx, job); err != nil {
		return fmt.Errorf("creating job: %w", err)
	}

	// Import speaker mappings
	if len(meta.SpeakerMappings) > 0 {
		var mappings []models.SpeakerMapping
		for _, m := range meta.SpeakerMappings {
			mappings = append(mappings, models.SpeakerMapping{
				TranscriptionJobID: meta.ID,
				OriginalSpeaker:    m.OriginalSpeaker,
				CustomName:         m.CustomName,
			})
		}
		if err := s.speakerMappingRepo.UpdateMappings(ctx, meta.ID, mappings); err != nil {
			logger.Warn("bundle sync: speaker mapping import failed", "id", meta.ID, "error", err)
		}
	}

	// Import summaries
	for _, sum := range meta.Summaries {
		var templateID *string
		if sum.TemplateID != "" {
			templateID = &sum.TemplateID
		}
		summary := &models.Summary{
			TranscriptionID: meta.ID,
			TemplateID:      templateID,
			Model:           sum.Model,
			Content:         sum.Content,
			CreatedAt:       sum.CreatedAt,
		}
		if err := s.summaryRepo.SaveSummary(ctx, summary); err != nil {
			logger.Warn("bundle sync: summary import failed", "id", meta.ID, "error", err)
		}
	}

	// Import notes
	for _, n := range meta.Notes {
		noteID := n.ID
		if noteID == "" {
			noteID = uuid.New().String()
		}
		note := &models.Note{
			ID:              noteID,
			TranscriptionID: meta.ID,
			StartWordIndex:  n.StartWordIndex,
			EndWordIndex:    n.EndWordIndex,
			StartTime:       n.StartTime,
			EndTime:         n.EndTime,
			Quote:           n.Quote,
			Content:         n.Content,
			CreatedAt:       n.CreatedAt,
		}
		if err := s.noteRepo.Create(ctx, note); err != nil {
			logger.Warn("bundle sync: note import failed", "id", meta.ID, "note_id", noteID, "error", err)
		}
	}

	return nil
}

func (s *BundleSyncService) updateFromMetadata(ctx context.Context, job *models.TranscriptionJob, meta *BundleMetadata, bundle ScannedBundle) error {
	changed := false

	// Path reconciliation: if the bundle moved on disk (e.g. manual rename/move),
	// recompute all paths to match the actual directory.
	if job.ArtifactDir != nil && *job.ArtifactDir != "" && filepath.Clean(*job.ArtifactDir) != filepath.Clean(bundle.Dir) {
		oldDir := *job.ArtifactDir
		newDir := bundle.Dir
		job.ArtifactDir = &newDir
		job.AudioPath = rebasePath(job.AudioPath, oldDir, newDir)
		if job.TranscriptJSONPath != nil {
			p := rebasePath(*job.TranscriptJSONPath, oldDir, newDir)
			job.TranscriptJSONPath = &p
		}
		if job.TranscriptMarkdownPath != nil {
			p := rebasePath(*job.TranscriptMarkdownPath, oldDir, newDir)
			job.TranscriptMarkdownPath = &p
		}
		// Also discover audio if path doesn't resolve
		if _, err := os.Stat(job.AudioPath); err != nil {
			if found := findAudioFile(newDir); found != "" {
				job.AudioPath = found
			}
		}
		changed = true
		logger.Info("bundle sync: reconciled paths", "id", meta.ID, "old_dir", oldDir, "new_dir", newDir)
	}

	if meta.Title != "" && (job.Title == nil || *job.Title != meta.Title) {
		job.Title = &meta.Title
		changed = true
	}
	if meta.Folder != "" {
		if job.Folder == nil || *job.Folder != meta.Folder {
			job.Folder = &meta.Folder
			changed = true
		}
	} else if bundle.Folder != "" {
		if job.Folder == nil || *job.Folder != bundle.Folder {
			job.Folder = &bundle.Folder
			changed = true
		}
	}
	if job.Diarization != meta.Diarization {
		job.Diarization = meta.Diarization
		changed = true
	}
	if job.IsMultiTrack != meta.IsMultiTrack {
		job.IsMultiTrack = meta.IsMultiTrack
		changed = true
	}

	// Only update job record if fields actually changed, to avoid bumping
	// updated_at via GORM autoUpdateTime (which would make Obsidian sync
	// status appear stale on every restart).
	if changed {
		if err := s.jobRepo.Update(ctx, job); err != nil {
			return fmt.Errorf("updating job: %w", err)
		}
	}

	// Replace speaker mappings
	if len(meta.SpeakerMappings) > 0 {
		var mappings []models.SpeakerMapping
		for _, m := range meta.SpeakerMappings {
			mappings = append(mappings, models.SpeakerMapping{
				TranscriptionJobID: meta.ID,
				OriginalSpeaker:    m.OriginalSpeaker,
				CustomName:         m.CustomName,
			})
		}
		_ = s.speakerMappingRepo.UpdateMappings(ctx, meta.ID, mappings)
	}

	// Replace summaries
	_ = s.summaryRepo.DeleteByTranscriptionID(ctx, meta.ID)
	for _, sum := range meta.Summaries {
		var templateID *string
		if sum.TemplateID != "" {
			templateID = &sum.TemplateID
		}
		_ = s.summaryRepo.SaveSummary(ctx, &models.Summary{
			TranscriptionID: meta.ID,
			TemplateID:      templateID,
			Model:           sum.Model,
			Content:         sum.Content,
			CreatedAt:       sum.CreatedAt,
		})
	}

	// Replace notes
	_ = s.noteRepo.DeleteByTranscriptionID(ctx, meta.ID)
	for _, n := range meta.Notes {
		noteID := n.ID
		if noteID == "" {
			noteID = uuid.New().String()
		}
		_ = s.noteRepo.Create(ctx, &models.Note{
			ID:              noteID,
			TranscriptionID: meta.ID,
			StartWordIndex:  n.StartWordIndex,
			EndWordIndex:    n.EndWordIndex,
			StartTime:       n.StartTime,
			EndTime:         n.EndTime,
			Quote:           n.Quote,
			Content:         n.Content,
			CreatedAt:       n.CreatedAt,
		})
	}

	return nil
}

func (s *BundleSyncService) deleteJobAndRelated(ctx context.Context, jobID string) error {
	_ = s.speakerMappingRepo.DeleteByJobID(ctx, jobID)
	_ = s.summaryRepo.DeleteByTranscriptionID(ctx, jobID)
	_ = s.noteRepo.DeleteByTranscriptionID(ctx, jobID)
	return s.jobRepo.Delete(ctx, jobID)
}

// findAudioFile looks for an audio.* file in a bundle directory.
func findAudioFile(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "audio.") {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

// MarkSelfWrite records a metadata.json write so the next sync skips it.
func (s *BundleSyncService) MarkSelfWrite(metadataPath string, mtimeNS int64) {
	normalized := filepath.Clean(strings.TrimSpace(metadataPath))
	s.mu.Lock()
	s.selfWrite[normalized] = mtimeNS
	s.mu.Unlock()
}

// rebasePath replaces oldDir prefix with newDir in path.
// If path doesn't start with oldDir, returns the basename under newDir.
func rebasePath(path, oldDir, newDir string) string {
	cleanPath := filepath.Clean(path)
	cleanOld := filepath.Clean(oldDir)
	if strings.HasPrefix(cleanPath, cleanOld+string(filepath.Separator)) {
		rel := strings.TrimPrefix(cleanPath, cleanOld+string(filepath.Separator))
		return filepath.Join(newDir, rel)
	}
	if cleanPath == cleanOld {
		return newDir
	}
	// Fallback: use the filename under the new dir
	return filepath.Join(newDir, filepath.Base(path))
}

func (s *BundleSyncService) isSelfWrite(metadataPath string, mtimeNS int64) bool {
	normalized := filepath.Clean(strings.TrimSpace(metadataPath))
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, exists := s.selfWrite[normalized]
	if !exists {
		return false
	}
	if stored == mtimeNS {
		delete(s.selfWrite, normalized)
		return true
	}
	// Expire stale entries after 1 minute
	if mtimeNS > stored+int64(time.Minute) {
		delete(s.selfWrite, normalized)
	}
	return false
}
