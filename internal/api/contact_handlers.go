package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"quill/internal/contacts"
	"quill/internal/database"
	"quill/internal/models"
	"quill/pkg/logger"
	"quill/pkg/slug"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ContactRequest is the JSON body for creating/updating a contact.
type ContactRequest struct {
	Name  string  `json:"name" binding:"required"`
	Phone *string `json:"phone,omitempty"`
	Email *string `json:"email,omitempty"`
	Notes *string `json:"notes,omitempty"`
}

const (
	maxSignatureUploadBytes   int64 = 10 << 20
	signatureArtifactFileName       = "voice-signature.embedding.json"
)

type voiceSignaturePayload struct {
	Version   int       `json:"version"`
	Model     string    `json:"model"`
	Dimension int       `json:"dimension"`
	Vector    []float64 `json:"vector"`
}

// getActiveVault returns the currently active vault from the database.
// Shared across multiple handler files.
func getActiveVault() (*models.Vault, error) {
	var vault models.Vault
	if err := database.DB.Where("is_active = ?", true).First(&vault).Error; err != nil {
		return nil, err
	}
	return &vault, nil
}

func setSignatureMetadata(contact *models.Contact, source string, model string) {
	existing := contacts.ParseSignatureMetadata(contact.SignatureData)
	existing.Source = strings.TrimSpace(source)
	if existing.Source == "" {
		contact.SignatureData = nil
		return
	}
	existing.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if strings.TrimSpace(model) != "" {
		existing.Model = strings.TrimSpace(model)
	}
	contact.SignatureData = contacts.SerializeSignatureMetadata(existing)
}

func hasManualSignature(contact *models.Contact) bool {
	return contacts.HasManualSignature(contact)
}

func validateSignaturePayload(raw []byte) (*voiceSignaturePayload, []byte, error) {
	var payload voiceSignaturePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, nil, fmt.Errorf("signature must be valid JSON")
	}

	if payload.Version <= 0 {
		return nil, nil, fmt.Errorf("signature.version is required and must be > 0")
	}
	if strings.TrimSpace(payload.Model) == "" {
		return nil, nil, fmt.Errorf("signature.model is required")
	}
	if payload.Dimension <= 0 {
		return nil, nil, fmt.Errorf("signature.dimension is required and must be > 0")
	}
	if len(payload.Vector) == 0 {
		return nil, nil, fmt.Errorf("signature.vector is required and must be a non-empty array")
	}
	if payload.Dimension != len(payload.Vector) {
		return nil, nil, fmt.Errorf("signature.dimension must match len(signature.vector)")
	}
	for _, value := range payload.Vector {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, nil, fmt.Errorf("signature.vector must contain only finite numbers")
		}
	}

	var payloadAny map[string]any
	if err := json.Unmarshal(raw, &payloadAny); err == nil {
		normalized, marshalErr := json.MarshalIndent(payloadAny, "", "  ")
		if marshalErr == nil {
			return &payload, normalized, nil
		}
	}

	normalized, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to normalize signature payload")
	}
	return &payload, normalized, nil
}

func (h *Handler) ListContacts(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}

	query := strings.TrimSpace(c.Query("q"))
	contacts, err := h.contactRepo.Search(c.Request.Context(), vault.ID, query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list contacts"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"contacts": contacts, "vault_id": vault.ID})
}

func (h *Handler) CreateContact(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}

	var req ContactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	contact := models.Contact{
		VaultID:         vault.ID,
		ContactUID:      uuid.NewString(),
		Slug:            slug.Sanitize(strings.TrimSpace(req.Name), "contact"),
		Name:            strings.TrimSpace(req.Name),
		Phone:           req.Phone,
		Email:           req.Email,
		Notes:           req.Notes,
		SignatureStatus: "none",
	}
	if contact.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Name is required"})
		return
	}

	if err := h.contactRepo.Create(c.Request.Context(), &contact); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create contact"})
		return
	}

	if err := h.persistContactFile(c.Request.Context(), &contact); err != nil {
		_ = h.contactRepo.Delete(c.Request.Context(), contact.ID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create contact file artifacts"})
		return
	}

	c.JSON(http.StatusCreated, contact)
}

func (h *Handler) GetContact(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}

	contact, err := h.contactRepo.GetByIDInVault(c.Request.Context(), vault.ID, uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contact not found"})
		return
	}
	c.JSON(http.StatusOK, contact)
}

func (h *Handler) UpdateContact(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}

	contact, err := h.contactRepo.GetByIDInVault(c.Request.Context(), vault.ID, uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contact not found"})
		return
	}

	var req ContactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	trimmed := strings.TrimSpace(req.Name)
	if trimmed == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Name is required"})
		return
	}

	contact.Name = trimmed
	contact.Slug = slug.Sanitize(trimmed, "contact")
	contact.Phone = req.Phone
	contact.Email = req.Email
	contact.Notes = req.Notes

	if err := h.persistContactFile(c.Request.Context(), contact); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update contact"})
		return
	}
	c.JSON(http.StatusOK, contact)
}

func (h *Handler) DeleteContact(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}

	contact, err := h.contactRepo.GetByIDInVault(c.Request.Context(), vault.ID, uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contact not found"})
		return
	}

	if err := h.deleteContactFiles(c.Request.Context(), contact); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete contact artifacts"})
		return
	}
	if err := h.contactRepo.Delete(c.Request.Context(), uint(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete contact"})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) UploadContactSnippet(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}

	contact, err := h.contactRepo.GetByIDInVault(c.Request.Context(), vault.ID, uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contact not found"})
		return
	}

	header, err := c.FormFile("snippet")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Snippet file is required (field: snippet)"})
		return
	}

	fileService := contacts.NewFileService(vault.Path)
	if err := h.persistContactFile(c.Request.Context(), contact); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to prepare contact folder"})
		return
	}

	src, err := header.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to open snippet file"})
		return
	}
	defer src.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext == "" {
		ext = ".wav"
	}

	folderRel := filepath.ToSlash(filepath.Dir(contact.NotePath))
	targetRel := filepath.ToSlash(filepath.Join(folderRel, "voice-snippet"+ext))
	targetPath := fileService.ResolveAbsPath(targetRel)
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to prepare contact snippet path"})
		return
	}

	dst, err := os.Create(targetPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to store snippet file"})
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to write snippet file"})
		return
	}

	contact.VoiceSnippetPath = &targetRel
	contact.SyncError = nil

	manualLocked := hasManualSignature(contact)
	if manualLocked {
		contact.SignatureStatus = "ready"
		if err := h.persistContactFile(c.Request.Context(), contact); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update contact"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"contact_id":               contact.ID,
			"snippet_path":             contact.VoiceSnippetPath,
			"signature_status":         contact.SignatureStatus,
			"signature_embedding_path": contact.SignatureEmbeddingPath,
			"manual_signature_locked":  true,
		})
		return
	}

	contact.SignatureStatus = "processing"
	contact.SignatureEmbeddingPath = nil
	setSignatureMetadata(contact, contacts.SignatureSourceExtracted, "")

	if err := h.persistContactFile(c.Request.Context(), contact); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update contact"})
		return
	}

	if h.contactManager != nil {
		h.contactManager.EnqueueEmbedding(contact.ID)
	} else {
		errMsg := "contact embedding worker is unavailable"
		contact.SignatureStatus = "failed"
		contact.SyncError = &errMsg
		_ = h.persistContactFile(c.Request.Context(), contact)
	}

	c.JSON(http.StatusOK, gin.H{
		"contact_id":               contact.ID,
		"snippet_path":             contact.VoiceSnippetPath,
		"signature_status":         contact.SignatureStatus,
		"signature_embedding_path": contact.SignatureEmbeddingPath,
	})
}

func (h *Handler) GetContactSnippet(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}
	contact, err := h.contactRepo.GetByIDInVault(c.Request.Context(), vault.ID, uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contact not found"})
		return
	}
	if contact.VoiceSnippetPath == nil || strings.TrimSpace(*contact.VoiceSnippetPath) == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "Voice snippet not found"})
		return
	}
	fileService := contacts.NewFileService(vault.Path)
	snippetAbs, ok := fileService.ResolveAndValidate(*contact.VoiceSnippetPath)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid snippet path"})
		return
	}
	if _, statErr := os.Stat(snippetAbs); statErr != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Voice snippet not found"})
		return
	}
	c.File(snippetAbs)
}

func (h *Handler) DeleteContactSnippet(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}
	contact, err := h.contactRepo.GetByIDInVault(c.Request.Context(), vault.ID, uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contact not found"})
		return
	}

	fileService := contacts.NewFileService(vault.Path)
	if contact.VoiceSnippetPath != nil {
		if abs, ok := fileService.ResolveAndValidate(*contact.VoiceSnippetPath); ok {
			_ = os.Remove(abs)
		}
	}

	contact.VoiceSnippetPath = nil
	contact.SyncError = nil
	if contact.SignatureEmbeddingPath == nil || strings.TrimSpace(*contact.SignatureEmbeddingPath) == "" {
		contact.SignatureStatus = "none"
		setSignatureMetadata(contact, "", "")
	} else if strings.TrimSpace(contact.SignatureStatus) == "" || strings.EqualFold(contact.SignatureStatus, "none") {
		contact.SignatureStatus = "ready"
	}
	if err := h.persistContactFile(c.Request.Context(), contact); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update contact"})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) UploadContactSignature(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}
	contact, err := h.contactRepo.GetByIDInVault(c.Request.Context(), vault.ID, uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contact not found"})
		return
	}

	header, err := c.FormFile("signature")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Signature file is required (field: signature)"})
		return
	}

	if err := h.persistContactFile(c.Request.Context(), contact); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to prepare contact folder"})
		return
	}

	src, err := header.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to open signature file"})
		return
	}
	defer src.Close()

	raw, err := io.ReadAll(io.LimitReader(src, maxSignatureUploadBytes+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read signature file"})
		return
	}
	if int64(len(raw)) > maxSignatureUploadBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Signature file exceeds maximum size"})
		return
	}

	payload, normalized, err := validateSignaturePayload(raw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	fileService := contacts.NewFileService(vault.Path)
	folderRel := filepath.ToSlash(filepath.Dir(contact.NotePath))
	if folderRel == "" || folderRel == "." {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to resolve contact folder"})
		return
	}
	targetRel := filepath.ToSlash(filepath.Join(folderRel, signatureArtifactFileName))
	targetAbs, ok := fileService.ResolveAndValidate(targetRel)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid signature target path"})
		return
	}
	if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to prepare signature path"})
		return
	}
	if err := os.WriteFile(targetAbs, normalized, 0o644); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to store signature file"})
		return
	}

	contact.SignatureEmbeddingPath = &targetRel
	contact.SignatureStatus = "ready"
	contact.SyncError = nil
	setSignatureMetadata(contact, contacts.SignatureSourceManual, payload.Model)
	if err := h.persistContactFile(c.Request.Context(), contact); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update contact"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"contact_id":               contact.ID,
		"signature_status":         contact.SignatureStatus,
		"signature_embedding_path": contact.SignatureEmbeddingPath,
		"signature_source":         contacts.SignatureSourceManual,
	})
}

func (h *Handler) DeleteContactSignature(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}
	contact, err := h.contactRepo.GetByIDInVault(c.Request.Context(), vault.ID, uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contact not found"})
		return
	}

	fileService := contacts.NewFileService(vault.Path)
	if contact.SignatureEmbeddingPath != nil {
		if abs, ok := fileService.ResolveAndValidate(*contact.SignatureEmbeddingPath); ok {
			_ = os.Remove(abs)
		}
	}

	contact.SignatureEmbeddingPath = nil
	contact.SignatureStatus = "none"
	contact.SyncError = nil
	setSignatureMetadata(contact, "", "")

	if err := h.persistContactFile(c.Request.Context(), contact); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update contact"})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) ExtractContactSignature(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}
	contact, err := h.contactRepo.GetByIDInVault(c.Request.Context(), vault.ID, uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contact not found"})
		return
	}
	if contact.VoiceSnippetPath == nil || strings.TrimSpace(*contact.VoiceSnippetPath) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Voice snippet is required before extraction"})
		return
	}

	fileService := contacts.NewFileService(vault.Path)
	snippetAbs, ok := fileService.ResolveAndValidate(*contact.VoiceSnippetPath)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid snippet path"})
		return
	}
	if _, statErr := os.Stat(snippetAbs); statErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Voice snippet is required before extraction"})
		return
	}

	contact.SignatureStatus = "processing"
	contact.SyncError = nil
	setSignatureMetadata(contact, contacts.SignatureSourceExtracted, "")
	if err := h.persistContactFile(c.Request.Context(), contact); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update contact"})
		return
	}

	if h.contactManager != nil {
		h.contactManager.EnqueueEmbedding(contact.ID)
	} else {
		errMsg := "contact embedding worker is unavailable"
		contact.SignatureStatus = "failed"
		contact.SyncError = &errMsg
		_ = h.persistContactFile(c.Request.Context(), contact)
	}

	c.JSON(http.StatusOK, gin.H{
		"contact_id":               contact.ID,
		"signature_status":         contact.SignatureStatus,
		"signature_embedding_path": contact.SignatureEmbeddingPath,
	})
}

// RetroactiveScanForContact triggers a manual retroactive speaker identification
// scan for a contact. It matches the contact's voice signature against speakers
// in all past diarized transcriptions.
func (h *Handler) RetroactiveScanForContact(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}
	contact, err := h.contactRepo.GetByIDInVault(c.Request.Context(), vault.ID, uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contact not found"})
		return
	}
	if contact.SignatureStatus != "ready" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Contact voice signature is not ready"})
		return
	}
	if h.contactManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Contact manager is unavailable"})
		return
	}

	// Inject LLM caller for voice+LLM fusion scoring if available.
	if caller := h.buildSpeakerIDLLMCaller(c.Request.Context()); caller != nil {
		h.contactManager.SetRetroactiveScanLLMCaller(caller)
	}

	result, scanErr := h.contactManager.RetroactiveScanForContact(c.Request.Context(), contact.ID)
	if scanErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": scanErr.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

func (h *Handler) GetContactFiles(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}
	contact, err := h.contactRepo.GetByIDInVault(c.Request.Context(), vault.ID, uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contact not found"})
		return
	}
	fileService := contacts.NewFileService(vault.Path)
	signatureMetadata := contacts.ParseSignatureMetadata(contact.SignatureData)
	noteAbs := ""
	if abs, ok := fileService.ResolveAndValidate(contact.NotePath); ok {
		noteAbs = abs
	}
	c.JSON(http.StatusOK, gin.H{
		"contact_id":                   contact.ID,
		"vault_id":                     contact.VaultID,
		"note_path":                    contact.NotePath,
		"note_abs_path":                noteAbs,
		"voice_snippet_path":           contact.VoiceSnippetPath,
		"signature_embedding_path":     contact.SignatureEmbeddingPath,
		"signature_status":             contact.SignatureStatus,
		"signature_source":             signatureMetadata.Source,
		"signature_model":              signatureMetadata.Model,
		"signature_retry_count":        signatureMetadata.RetryCount,
		"signature_last_attempt_at":    signatureMetadata.LastAttemptAt,
		"signature_next_retry_at":      signatureMetadata.NextRetryAt,
		"sync_error":                   contact.SyncError,
		"file_mtime_ns":                contact.FileMtimeNS,
		"voice_snippet_abs_path":       absFromOptional(fileService, contact.VoiceSnippetPath),
		"signature_embedding_abs_path": absFromOptional(fileService, contact.SignatureEmbeddingPath),
	})
}

func (h *Handler) ReindexContacts(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}

	if h.contactManager != nil {
		result, reindexErr := h.contactManager.ReindexActiveVault(c.Request.Context())
		if reindexErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reindex contacts"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"vault_id": vault.ID, "result": result})
		return
	}

	fileService := contacts.NewFileService(vault.Path)
	syncService := contacts.NewSyncService(fileService, h.contactRepo, vault.ID)
	result, reindexErr := syncService.SyncFromFilesystem(c.Request.Context())
	if reindexErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reindex contacts"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"vault_id": vault.ID, "result": result})
}

func (h *Handler) persistContactFile(ctx context.Context, contact *models.Contact) error {
	if h.contactManager != nil {
		return h.contactManager.WriteContact(ctx, contact)
	}
	var vault models.Vault
	if err := database.DB.WithContext(ctx).First(&vault, contact.VaultID).Error; err != nil {
		return err
	}
	fileService := contacts.NewFileService(vault.Path)
	if err := fileService.WriteContact(contact); err != nil {
		return err
	}
	return h.contactRepo.Update(ctx, contact)
}

func (h *Handler) deleteContactFiles(ctx context.Context, contact *models.Contact) error {
	if h.contactManager != nil {
		return h.contactManager.DeleteContactFiles(ctx, contact)
	}
	var vault models.Vault
	if err := database.DB.WithContext(ctx).First(&vault, contact.VaultID).Error; err != nil {
		return err
	}
	fileService := contacts.NewFileService(vault.Path)
	return fileService.DeleteContactFolder(contact)
}

func absFromOptional(fileService *contacts.FileService, value *string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return ""
	}
	abs, ok := fileService.ResolveAndValidate(*value)
	if !ok {
		return ""
	}
	return abs
}

func (h *Handler) syncContactManager(ctx context.Context, vaultID uint, vaultPath string) {
	if h.contactManager == nil {
		return
	}
	if activeID, activePath, ok := h.contactManager.ActiveVault(); ok &&
		activeID == vaultID &&
		filepath.Clean(activePath) == filepath.Clean(vaultPath) {
		return
	}
	if err := h.contactManager.SwitchVault(ctx, vaultID, vaultPath); err != nil {
		logger.Warn("failed to switch contact manager vault", "vault_id", vaultID, "error", err)
	}
}

func (h *Handler) reindexContactManager(ctx context.Context, vaultID uint, vaultPath string) {
	if h.contactManager == nil {
		return
	}
	if _, err := h.contactManager.ReindexVault(ctx, vaultID, vaultPath); err != nil {
		logger.Warn("failed to reindex contacts for vault", "vault_id", vaultID, "error", err)
	}
}
