package transcription

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"quill/internal/repository"
	"quill/pkg/logger"
)

// BundleIssueType categorizes bundle integrity issues.
type BundleIssueType string

const (
	IssueMissingAudio      BundleIssueType = "missing_audio"
	IssueMissingTranscript BundleIssueType = "missing_transcript"
	IssuePathMismatch      BundleIssueType = "path_mismatch"
	IssueOrphanedDB        BundleIssueType = "orphaned_db"
	IssueOrphanedDisk      BundleIssueType = "orphaned_disk"
)

// BundleIssue represents a single integrity problem found during audit.
type BundleIssue struct {
	JobID       string          `json:"job_id,omitempty"`
	BundleDir   string          `json:"bundle_dir,omitempty"`
	Type        BundleIssueType `json:"type"`
	Description string          `json:"description"`
	Repairable  bool            `json:"repairable"`
}

// AuditResult summarizes a vault integrity audit.
type AuditResult struct {
	TotalJobs  int           `json:"total_jobs"`
	TotalDirs  int           `json:"total_dirs"`
	Issues     []BundleIssue `json:"issues"`
	Healthy    int           `json:"healthy"`
	IssueCount int           `json:"issue_count"`
}

// RepairResult summarizes repairs performed.
type RepairResult struct {
	Attempted int `json:"attempted"`
	Fixed     int `json:"fixed"`
	Failed    int `json:"failed"`
}

// BundleRepairService audits and repairs bundle integrity.
type BundleRepairService struct {
	jobRepo        repository.JobRepository
	transcriptsDir string
	uploadsDir     string
	vaultID        *uint
}

// NewBundleRepairService creates a new repair service.
func NewBundleRepairService(
	jobRepo repository.JobRepository,
	transcriptsDir string,
	uploadsDir string,
	vaultID *uint,
) *BundleRepairService {
	return &BundleRepairService{
		jobRepo:        jobRepo,
		transcriptsDir: transcriptsDir,
		uploadsDir:     uploadsDir,
		vaultID:        vaultID,
	}
}

// AuditVault checks all DB jobs and on-disk bundles for integrity issues.
func (s *BundleRepairService) AuditVault(ctx context.Context) (*AuditResult, error) {
	result := &AuditResult{}

	// Get all jobs from DB
	jobs, total, err := s.jobRepo.ListWithParams(ctx, repository.ListParams{
		Offset:  0,
		Limit:   10000,
		VaultID: s.vaultID,
	})
	if err != nil {
		return nil, fmt.Errorf("listing jobs: %w", err)
	}
	result.TotalJobs = int(total)

	// Scan disk bundles
	var diskBundles []ScannedBundle
	if s.transcriptsDir != "" {
		diskBundles, _ = ScanBundles(s.transcriptsDir)
	}
	result.TotalDirs = len(diskBundles)

	// Index disk bundles by dir
	diskByDir := make(map[string]bool, len(diskBundles))
	for _, b := range diskBundles {
		diskByDir[filepath.Clean(b.Dir)] = true
	}

	// Index DB jobs by artifact dir
	dbByDir := make(map[string]bool)

	for _, job := range jobs {
		hasIssue := false

		// Check audio file
		if job.AudioPath == "" {
			result.Issues = append(result.Issues, BundleIssue{
				JobID:       job.ID,
				Type:        IssueMissingAudio,
				Description: "No audio path recorded",
				Repairable:  false,
			})
			hasIssue = true
		} else if _, statErr := os.Stat(job.AudioPath); statErr != nil {
			repairable := s.canRepairAudio(job.ID, job.ArtifactDir)
			result.Issues = append(result.Issues, BundleIssue{
				JobID:       job.ID,
				Type:        IssueMissingAudio,
				Description: fmt.Sprintf("Audio file not found: %s", job.AudioPath),
				Repairable:  repairable,
			})
			hasIssue = true
		}

		// Check artifact dir exists
		if job.ArtifactDir != nil && *job.ArtifactDir != "" {
			cleanDir := filepath.Clean(*job.ArtifactDir)
			dbByDir[cleanDir] = true

			if _, statErr := os.Stat(*job.ArtifactDir); statErr != nil {
				result.Issues = append(result.Issues, BundleIssue{
					JobID:       job.ID,
					BundleDir:   *job.ArtifactDir,
					Type:        IssueOrphanedDB,
					Description: "Bundle directory missing from disk",
					Repairable:  false,
				})
				hasIssue = true
			} else {
				// Check path mismatch: DB paths point outside bundle dir
				if job.AudioPath != "" && !strings.HasPrefix(filepath.Clean(job.AudioPath), cleanDir) {
					// Audio path is outside the bundle — check if it actually exists in the bundle
					if found := findAudioFile(*job.ArtifactDir); found != "" {
						result.Issues = append(result.Issues, BundleIssue{
							JobID:       job.ID,
							BundleDir:   *job.ArtifactDir,
							Type:        IssuePathMismatch,
							Description: fmt.Sprintf("Audio path %s is outside bundle dir, but audio exists in bundle", job.AudioPath),
							Repairable:  true,
						})
						hasIssue = true
					}
				}

				// Check transcript JSON
				if job.TranscriptJSONPath != nil && *job.TranscriptJSONPath != "" {
					if _, statErr := os.Stat(*job.TranscriptJSONPath); statErr != nil {
						jsonInBundle := filepath.Join(*job.ArtifactDir, "transcript.json")
						repairable := false
						if _, e := os.Stat(jsonInBundle); e == nil {
							repairable = true
						}
						result.Issues = append(result.Issues, BundleIssue{
							JobID:       job.ID,
							Type:        IssueMissingTranscript,
							Description: fmt.Sprintf("Transcript JSON not found: %s", *job.TranscriptJSONPath),
							Repairable:  repairable,
						})
						hasIssue = true
					}
				}
			}
		}

		if !hasIssue {
			result.Healthy++
		}
	}

	// Check for orphaned disk bundles (on disk but not in DB)
	for _, bundle := range diskBundles {
		cleanDir := filepath.Clean(bundle.Dir)
		if !dbByDir[cleanDir] {
			// Read metadata to see if we can identify it
			meta, _ := ReadOrCreateMetadata(bundle.Dir)
			desc := fmt.Sprintf("Bundle on disk not tracked in DB: %s", bundle.Dir)
			if meta != nil && meta.ID != "" {
				// Check if ID exists in DB
				if _, findErr := s.jobRepo.FindByID(ctx, meta.ID); findErr != nil {
					desc = fmt.Sprintf("Bundle %s (id=%s) on disk but not in DB", bundle.Dir, meta.ID)
				} else {
					continue // DB record exists, just dir mismatch (handled above)
				}
			}
			result.Issues = append(result.Issues, BundleIssue{
				BundleDir:   bundle.Dir,
				Type:        IssueOrphanedDisk,
				Description: desc,
				Repairable:  false, // needs manual review or re-import via bundle sync
			})
		}
	}

	result.IssueCount = len(result.Issues)
	return result, nil
}

// RepairPaths fixes path mismatch issues by re-pointing DB paths to actual files in bundle dirs.
func (s *BundleRepairService) RepairPaths(ctx context.Context) (*RepairResult, error) {
	result := &RepairResult{}

	jobs, _, err := s.jobRepo.ListWithParams(ctx, repository.ListParams{
		Offset:  0,
		Limit:   10000,
		VaultID: s.vaultID,
	})
	if err != nil {
		return nil, fmt.Errorf("listing jobs: %w", err)
	}

	for _, job := range jobs {
		if job.ArtifactDir == nil || *job.ArtifactDir == "" {
			continue
		}
		if _, statErr := os.Stat(*job.ArtifactDir); statErr != nil {
			continue // dir doesn't exist, can't repair
		}

		changed := false

		// Fix audio path
		if job.AudioPath != "" {
			if _, statErr := os.Stat(job.AudioPath); statErr != nil {
				if found := findAudioFile(*job.ArtifactDir); found != "" {
					job.AudioPath = found
					changed = true
				}
			}
		} else {
			// No audio path at all — try to discover
			if found := findAudioFile(*job.ArtifactDir); found != "" {
				job.AudioPath = found
				changed = true
			}
		}

		// Fix transcript JSON path
		if job.TranscriptJSONPath != nil && *job.TranscriptJSONPath != "" {
			if _, statErr := os.Stat(*job.TranscriptJSONPath); statErr != nil {
				candidate := filepath.Join(*job.ArtifactDir, "transcript.json")
				if _, e := os.Stat(candidate); e == nil {
					job.TranscriptJSONPath = &candidate
					changed = true
				}
			}
		}

		// Fix transcript markdown path
		if job.TranscriptMarkdownPath != nil && *job.TranscriptMarkdownPath != "" {
			if _, statErr := os.Stat(*job.TranscriptMarkdownPath); statErr != nil {
				candidate := filepath.Join(*job.ArtifactDir, "transcript.md")
				if _, e := os.Stat(candidate); e == nil {
					job.TranscriptMarkdownPath = &candidate
					changed = true
				}
			}
		}

		if changed {
			result.Attempted++
			if updateErr := s.jobRepo.Update(ctx, &job); updateErr != nil {
				logger.Warn("repair: failed to update job", "id", job.ID, "error", updateErr)
				result.Failed++
			} else {
				result.Fixed++
				logger.Info("repair: fixed paths", "id", job.ID)
			}
		}
	}

	return result, nil
}

// canRepairAudio checks if audio can be found in the bundle dir.
func (s *BundleRepairService) canRepairAudio(jobID string, artifactDir *string) bool {
	if artifactDir == nil || *artifactDir == "" {
		return false
	}
	if found := findAudioFile(*artifactDir); found != "" {
		return true
	}
	return false
}
