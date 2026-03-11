package contacts

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"quill/internal/models"
	"quill/internal/repository"
	"quill/pkg/binaries"
	"quill/pkg/logger"
	"quill/pkg/slug"

	"gorm.io/gorm"
)

//go:embed py/extract_titanet_embedding.py
var titanetEmbeddingScript string

const embeddingFileName = "voice-signature.embedding.json"

// EmbeddingWorker extracts voice embeddings asynchronously.
type EmbeddingWorker struct {
	db          *gorm.DB
	repo        repository.ContactRepository
	whisperXEnv string

	jobs chan uint
	stop chan struct{}
	wg   sync.WaitGroup
}

func NewEmbeddingWorker(db *gorm.DB, repo repository.ContactRepository, whisperXEnv string) *EmbeddingWorker {
	return &EmbeddingWorker{
		db:          db,
		repo:        repo,
		whisperXEnv: whisperXEnv,
		jobs:        make(chan uint, 128),
		stop:        make(chan struct{}),
	}
}

func (w *EmbeddingWorker) Start() {
	w.wg.Add(1)
	go w.loop()
}

func (w *EmbeddingWorker) Stop() {
	close(w.stop)
	w.wg.Wait()
}

func (w *EmbeddingWorker) Enqueue(contactID uint) {
	select {
	case w.jobs <- contactID:
	default:
		logger.Warn("contact embedding queue is full; dropping job", "contact_id", contactID)
	}
}

func (w *EmbeddingWorker) loop() {
	defer w.wg.Done()
	for {
		select {
		case <-w.stop:
			return
		case contactID := <-w.jobs:
			w.process(contactID)
		}
	}
}

func (w *EmbeddingWorker) process(contactID uint) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	attemptedAt := time.Now().UTC()

	contact, err := w.repo.GetByID(ctx, contactID)
	if err != nil {
		logger.Warn("contact embedding worker: contact not found", "contact_id", contactID, "error", err)
		return
	}

	var vault models.Vault
	if err := w.db.WithContext(ctx).First(&vault, contact.VaultID).Error; err != nil {
		msg := "failed to load contact vault"
		w.markFailed(ctx, contact, msg+": "+err.Error())
		return
	}

	PrepareRetryAttempt(contact, attemptedAt)
	contact.SignatureStatus = "processing"
	contact.SyncError = nil
	if err := w.repo.Update(ctx, contact); err != nil {
		logger.Warn("contact embedding worker: failed to persist retry attempt", "contact_id", contact.ID, "error", err)
	}

	if contact.VoiceSnippetPath == nil || strings.TrimSpace(*contact.VoiceSnippetPath) == "" {
		w.markFailed(ctx, contact, "voice snippet is missing")
		return
	}

	fileService := NewFileService(vault.Path)
	if err := fileService.WriteContact(contact); err != nil {
		logger.Warn("contact embedding worker: failed to persist processing state to markdown", "contact_id", contact.ID, "error", err)
	}
	snippetAbs := fileService.ResolveAbsPath(*contact.VoiceSnippetPath)
	if _, err := os.Stat(snippetAbs); err != nil {
		w.markFailed(ctx, contact, "voice snippet file is not accessible")
		return
	}

	folderRel := folderRelFromNotePath(contact.NotePath)
	if folderRel == "" {
		contact.Slug = slug.Sanitize(contact.Name, "contact")
		folderRel = fileService.ContactFolderRelPath(contact)
	}
	embeddingRel := filepath.ToSlash(filepath.Join(folderRel, embeddingFileName))
	embeddingAbs := fileService.ResolveAbsPath(embeddingRel)
	if err := os.MkdirAll(filepath.Dir(embeddingAbs), 0o755); err != nil {
		w.markFailed(ctx, contact, "failed to prepare embedding directory")
		return
	}

	if runErr := w.extractEmbedding(ctx, snippetAbs, embeddingAbs); runErr != nil {
		w.markFailed(ctx, contact, runErr.Error())
		return
	}

	contact.SignatureEmbeddingPath = &embeddingRel
	contact.SignatureStatus = "ready"
	contact.SyncError = nil
	metadata, metadataErr := json.Marshal(MarkRetryReady(contact, "", time.Now().UTC()))
	if metadataErr == nil {
		value := string(metadata)
		contact.SignatureData = &value
	}
	if err := fileService.WriteContact(contact); err != nil {
		msg := "embedding extracted, but failed to update contact markdown"
		w.markFailed(ctx, contact, msg+": "+err.Error())
		return
	}
	if err := w.repo.Update(ctx, contact); err != nil {
		logger.Warn("contact embedding worker: db update failed", "contact_id", contact.ID, "error", err)
	}
}

func (w *EmbeddingWorker) markFailed(ctx context.Context, contact *models.Contact, message string) {
	trimmed := strings.TrimSpace(message)
	contact.SignatureStatus = "failed"
	contact.SyncError = &trimmed
	metadata, metadataErr := json.Marshal(MarkRetryFailure(contact, trimmed, time.Now().UTC()))
	if metadataErr == nil {
		value := string(metadata)
		contact.SignatureData = &value
	}

	var vault models.Vault
	if err := w.db.WithContext(ctx).First(&vault, contact.VaultID).Error; err == nil {
		fileService := NewFileService(vault.Path)
		if writeErr := fileService.WriteContact(contact); writeErr != nil {
			logger.Warn("contact embedding worker: failed to persist failure to markdown", "contact_id", contact.ID, "error", writeErr)
		}
	}

	if err := w.repo.Update(ctx, contact); err != nil {
		logger.Warn("contact embedding worker: failed to persist failure", "contact_id", contact.ID, "error", err)
	}
}

func (w *EmbeddingWorker) extractEmbedding(ctx context.Context, inputPath string, outputPath string) error {
	envPath := filepath.Join(w.whisperXEnv, "parakeet")
	if err := PrepareTitaNetRuntime(ctx, envPath); err != nil {
		return err
	}

	if err := os.MkdirAll(envPath, 0o755); err != nil {
		return fmt.Errorf("failed to prepare NeMo environment path: %w", err)
	}

	scriptPath := filepath.Join(envPath, "extract_titanet_embedding.py")
	if err := os.WriteFile(scriptPath, []byte(titanetEmbeddingScript), 0o755); err != nil {
		return fmt.Errorf("failed to write embedding extraction script: %w", err)
	}

	cmd := exec.CommandContext(ctx,
		binaries.UV(),
		"run", "--native-tls", "--project", envPath,
		"python", scriptPath,
		"--input", inputPath,
		"--output", outputPath,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		trimmed := strings.TrimSpace(string(output))
		if trimmed == "" {
			trimmed = err.Error()
		}
		if strings.Contains(trimmed, "No module named") || strings.Contains(trimmed, "not found") {
			return fmt.Errorf("voice-signature runtime is not ready yet. Quill is still preparing the local NeMo/TitaNet tools. Wait for the runtime status banner to finish, then retry extraction")
		}
		return fmt.Errorf("embedding extraction failed: %s", trimmed)
	}

	if _, err := os.Stat(outputPath); err != nil {
		return fmt.Errorf("embedding extraction finished without output artifact")
	}
	return nil
}
