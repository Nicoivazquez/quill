package api

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"quill/internal/config"
	"quill/internal/contacts"
	"quill/internal/database"
	"quill/internal/models"
	"quill/pkg/binaries"
	"quill/pkg/logger"
	"quill/pkg/slug"

	"github.com/google/uuid"
)

const (
	autoSpeakerSnippetTargetSeconds = 8.0
	autoSpeakerSnippetMinSeconds    = 2.0
	autoSpeakerSnippetMaxSeconds    = 14.0
	autoSpeakerMergeGapSeconds      = 0.6
	autoSpeakerSnippetTimeout       = 45 * time.Second
)

type clipWindow struct {
	Start float64
	End   float64
}

type clipSpan struct {
	Start float64
	End   float64
}

type speakerContactBootstrapSummary struct {
	StartedCount         int `json:"started_count"`
	CreatedCount         int `json:"created_count"`
	SkippedExistingCount int `json:"skipped_existing_count"`
}

func (h *Handler) bootstrapContactsFromSpeakerMappings(ctx context.Context, job *models.TranscriptionJob, mappings []models.SpeakerMapping) (speakerContactBootstrapSummary, error) {
	summary := speakerContactBootstrapSummary{}
	if job == nil || len(mappings) == 0 || h.contactRepo == nil {
		return summary, nil
	}
	if job.Transcript == nil || strings.TrimSpace(*job.Transcript) == "" {
		return summary, nil
	}

	transcript, err := parseTranscriptJSON(*job.Transcript)
	if err != nil {
		return summary, fmt.Errorf("parse transcript: %w", err)
	}
	if len(transcript.Segments) == 0 {
		return summary, nil
	}

	windowsBySpeaker := buildSpeakerClipWindows(transcript.Segments)
	if len(windowsBySpeaker) == 0 {
		return summary, nil
	}

	vault, err := resolveJobVault(ctx, job)
	if err != nil {
		return summary, fmt.Errorf("resolve job vault: %w", err)
	}
	fileService := contacts.NewFileService(vault.Path)

	existingContacts, err := h.contactRepo.ListByVault(ctx, vault.ID)
	if err != nil {
		return summary, fmt.Errorf("list contacts: %w", err)
	}

	contactsByName := make(map[string]models.Contact, len(existingContacts))
	for _, contact := range existingContacts {
		key := normalizeNameKey(contact.Name)
		if key == "" {
			continue
		}
		if _, exists := contactsByName[key]; !exists {
			contactsByName[key] = contact
		}
	}

	processedNames := make(map[string]struct{})

	for _, mapping := range mappings {
		originalSpeaker := strings.TrimSpace(mapping.OriginalSpeaker)
		customName := strings.TrimSpace(mapping.CustomName)
		if originalSpeaker == "" || customName == "" {
			continue
		}
		if strings.EqualFold(originalSpeaker, customName) {
			continue
		}

		speakerKey := normalizeNameKey(originalSpeaker)
		window, ok := windowsBySpeaker[speakerKey]
		if !ok {
			continue
		}

		nameKey := normalizeNameKey(customName)
		if nameKey == "" {
			continue
		}
		if _, seen := processedNames[nameKey]; seen {
			continue
		}

		contact, exists := contactsByName[nameKey]
		if !exists {
			contact = models.Contact{
				VaultID:         vault.ID,
				ContactUID:      uuid.NewString(),
				Slug:            slug.Sanitize(customName, "contact"),
				Name:            customName,
				SignatureStatus: "none",
			}

			if err := h.contactRepo.Create(ctx, &contact); err != nil {
				logger.Warn("speaker bootstrap: failed to create contact", "name", customName, "error", err)
				continue
			}
			summary.CreatedCount++
			if err := h.persistContactFile(ctx, &contact); err != nil {
				_ = h.contactRepo.Delete(ctx, contact.ID)
				summary.CreatedCount--
				logger.Warn("speaker bootstrap: failed to materialize contact file", "name", customName, "error", err)
				continue
			}
			contactsByName[nameKey] = contact
		}

		processedNames[nameKey] = struct{}{}

		if contactAlreadyBootstrapped(&contact, fileService) {
			summary.SkippedExistingCount++
			continue
		}

		if err := h.extractSpeakerSnippetForContact(ctx, job, vault, &contact, window); err != nil {
			logger.Warn("speaker bootstrap: failed to extract speaker snippet", "contact_id", contact.ID, "speaker", originalSpeaker, "error", err)
			continue
		}

		summary.StartedCount++
		contactsByName[nameKey] = contact
	}

	return summary, nil
}

func (h *Handler) extractSpeakerSnippetForContact(ctx context.Context, job *models.TranscriptionJob, vault *models.Vault, contact *models.Contact, window clipWindow) error {
	audioPath, err := resolveJobAudioPath(job, vault.Path)
	if err != nil {
		return err
	}

	if err := h.persistContactFile(ctx, contact); err != nil {
		return err
	}

	fileService := contacts.NewFileService(vault.Path)
	folderRel := filepath.ToSlash(filepath.Dir(contact.NotePath))
	if folderRel == "" || folderRel == "." {
		return fmt.Errorf("contact note path is not initialized")
	}

	snippetRel := filepath.ToSlash(filepath.Join(folderRel, "voice-snippet.wav"))
	snippetAbs, ok := fileService.ResolveAndValidate(snippetRel)
	if !ok {
		return fmt.Errorf("failed to resolve snippet path")
	}
	if err := os.MkdirAll(filepath.Dir(snippetAbs), 0o755); err != nil {
		return err
	}

	if err := extractSnippetWithFFmpeg(ctx, audioPath, snippetAbs, window); err != nil {
		return err
	}

	contact.VoiceSnippetPath = &snippetRel
	contact.SyncError = nil
	contact.SignatureStatus = "processing"
	contact.SignatureEmbeddingPath = nil
	setSignatureMetadata(contact, contacts.SignatureSourceExtracted, "")

	if err := h.persistContactFile(ctx, contact); err != nil {
		return err
	}

	if h.contactManager == nil {
		return fmt.Errorf("contact embedding worker is unavailable")
	}

	h.contactManager.EnqueueEmbedding(contact.ID)
	return nil
}

func resolveJobVault(ctx context.Context, job *models.TranscriptionJob) (*models.Vault, error) {
	if job != nil && job.VaultID != nil && *job.VaultID != 0 {
		var vault models.Vault
		if err := database.DB.WithContext(ctx).First(&vault, *job.VaultID).Error; err != nil {
			return nil, err
		}
		return &vault, nil
	}
	return getActiveVault()
}

func contactAlreadyBootstrapped(contact *models.Contact, fileService *contacts.FileService) bool {
	if contact == nil {
		return true
	}
	if contacts.HasManualSignature(contact) {
		return true
	}
	if existingVaultFile(fileService, contact.SignatureEmbeddingPath) {
		return true
	}
	return existingVaultFile(fileService, contact.VoiceSnippetPath)
}

func existingVaultFile(fileService *contacts.FileService, relOrAbs *string) bool {
	if relOrAbs == nil || strings.TrimSpace(*relOrAbs) == "" {
		return false
	}
	absPath, ok := fileService.ResolveAndValidate(*relOrAbs)
	if !ok {
		return false
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func resolveJobAudioPath(job *models.TranscriptionJob, vaultPath string) (string, error) {
	candidates := make([]string, 0, 2)
	if job.MergedAudioPath != nil && strings.TrimSpace(*job.MergedAudioPath) != "" {
		candidates = append(candidates, strings.TrimSpace(*job.MergedAudioPath))
	}
	if strings.TrimSpace(job.AudioPath) != "" {
		candidates = append(candidates, strings.TrimSpace(job.AudioPath))
	}

	allowedRoots := []string{vaultPath}
	if uploadDir := strings.TrimSpace(config.Load().UploadDir); uploadDir != "" {
		allowedRoots = append(allowedRoots, uploadDir)
	}

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}

		if filepath.IsAbs(candidate) {
			cleaned := filepath.Clean(candidate)
			if !isWithinBoundary(cleaned, allowedRoots) {
				continue
			}
			if isRegularFile(cleaned) {
				return cleaned, nil
			}
			continue
		}

		// Resolve relative paths against vault first, then validate boundary.
		vaultCandidate := filepath.Clean(filepath.Join(vaultPath, filepath.FromSlash(candidate)))
		if isWithinBoundary(vaultCandidate, allowedRoots) && isRegularFile(vaultCandidate) {
			return vaultCandidate, nil
		}
	}

	return "", fmt.Errorf("audio source file not found for job %s", job.ID)
}

func extractSnippetWithFFmpeg(ctx context.Context, audioPath string, outputPath string, window clipWindow) error {
	start := math.Max(0, window.Start)
	end := math.Max(start+0.05, window.End)
	duration := end - start

	cmdCtx, cancel := context.WithTimeout(ctx, autoSpeakerSnippetTimeout)
	defer cancel()

	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-y",
		"-ss", fmt.Sprintf("%.3f", start),
		"-t", fmt.Sprintf("%.3f", duration),
		"-i", audioPath,
		"-ac", "1",
		"-ar", "16000",
		"-vn",
		outputPath,
	}
	cmd := exec.CommandContext(cmdCtx, binaries.FFmpeg(), args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(output))
		if trimmed == "" {
			trimmed = err.Error()
		}
		return fmt.Errorf("ffmpeg snippet extraction failed: %s", trimmed)
	}

	if !isRegularFile(outputPath) {
		return fmt.Errorf("snippet extraction produced no output file")
	}
	return nil
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func buildSpeakerClipWindows(segments []transcriptSegment) map[string]clipWindow {
	spansBySpeaker := map[string][]clipSpan{}
	for _, segment := range segments {
		if segment.Speaker == nil {
			continue
		}
		speakerKey := normalizeNameKey(*segment.Speaker)
		if speakerKey == "" {
			continue
		}

		start := math.Max(0, segment.Start)
		end := math.Max(start, segment.End)
		if end-start < 0.15 {
			continue
		}

		spansBySpeaker[speakerKey] = append(spansBySpeaker[speakerKey], clipSpan{
			Start: start,
			End:   end,
		})
	}

	windows := make(map[string]clipWindow, len(spansBySpeaker))
	for speakerKey, spans := range spansBySpeaker {
		if len(spans) == 0 {
			continue
		}
		sort.Slice(spans, func(i, j int) bool {
			return spans[i].Start < spans[j].Start
		})

		merged := mergeClipSpans(spans, autoSpeakerMergeGapSeconds)
		if len(merged) == 0 {
			continue
		}

		best := merged[0]
		for i := 1; i < len(merged); i++ {
			if merged[i].End-merged[i].Start > best.End-best.Start {
				best = merged[i]
			}
		}

		start := math.Max(0, best.Start-0.2)
		end := best.End
		if end-start > autoSpeakerSnippetMaxSeconds {
			end = start + autoSpeakerSnippetMaxSeconds
		}
		if end-start < autoSpeakerSnippetTargetSeconds {
			end = start + autoSpeakerSnippetTargetSeconds
		}
		if end-start < autoSpeakerSnippetMinSeconds {
			end = start + autoSpeakerSnippetMinSeconds
		}
		windows[speakerKey] = clipWindow{Start: start, End: end}
	}

	return windows
}

func mergeClipSpans(spans []clipSpan, maxGap float64) []clipSpan {
	if len(spans) == 0 {
		return nil
	}

	merged := make([]clipSpan, 0, len(spans))
	current := spans[0]

	for i := 1; i < len(spans); i++ {
		next := spans[i]
		if next.Start-current.End <= maxGap {
			if next.End > current.End {
				current.End = next.End
			}
			continue
		}
		merged = append(merged, current)
		current = next
	}
	merged = append(merged, current)
	return merged
}

func isWithinBoundary(absPath string, allowedRoots []string) bool {
	for _, root := range allowedRoots {
		rel, err := filepath.Rel(root, absPath)
		if err != nil {
			continue
		}
		if !strings.HasPrefix(rel, "..") {
			return true
		}
	}
	return false
}

// normalizeNameKey lowercases and trims. Non-UTF8 bytes are replaced with U+FFFD.
func normalizeNameKey(value string) string {
	return strings.ToLower(strings.TrimSpace(strings.ToValidUTF8(value, "\uFFFD")))
}
