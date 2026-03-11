package api

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"quill/internal/contacts"
	"quill/internal/database"
	"quill/internal/models"
	"quill/internal/repository"
	"quill/pkg/slug"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func setupContactHandlerHarness(t *testing.T) (*Handler, *gorm.DB, models.Vault, func()) {
	t.Helper()

	gin.SetMode(gin.TestMode)

	root := t.TempDir()
	vaultPath := filepath.Join(root, "vault-active")
	if err := os.MkdirAll(vaultPath, 0o755); err != nil {
		t.Fatalf("create vault path: %v", err)
	}

	dbPath := filepath.Join(root, "contacts-handler-test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Vault{}, &models.Contact{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	activeVault := models.Vault{
		Name:     "Active Vault",
		Path:     vaultPath,
		IsActive: true,
	}
	if err := db.Create(&activeVault).Error; err != nil {
		t.Fatalf("seed active vault: %v", err)
	}

	prevDB := database.DB
	database.DB = db

	h := &Handler{
		contactRepo: repository.NewContactRepository(db),
	}

	cleanup := func() {
		database.DB = prevDB
	}
	return h, db, activeVault, cleanup
}

func seedContact(t *testing.T, h *Handler, vaultID uint, name string) *models.Contact {
	t.Helper()

	contact := &models.Contact{
		VaultID:         vaultID,
		ContactUID:      uuid.NewString(),
		Slug:            slug.Sanitize(name, "contact"),
		Name:            name,
		SignatureStatus: "none",
	}
	if err := h.contactRepo.Create(context.Background(), contact); err != nil {
		t.Fatalf("create contact: %v", err)
	}
	if err := h.persistContactFile(context.Background(), contact); err != nil {
		t.Fatalf("persist contact file: %v", err)
	}
	return contact
}

func makeMultipartRequest(t *testing.T, method string, url string, field string, filename string, payload []byte) *http.Request {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile(field, filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatalf("write multipart payload: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(method, url, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func loadContact(t *testing.T, h *Handler, id uint) *models.Contact {
	t.Helper()

	contact, err := h.contactRepo.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("load contact %d: %v", id, err)
	}
	return contact
}

func TestUploadContactSignature_Success(t *testing.T) {
	h, _, vault, cleanup := setupContactHandlerHarness(t)
	defer cleanup()

	contact := seedContact(t, h, vault.ID, "Manual Signature Contact")

	router := gin.New()
	router.POST("/contacts/:id/signature", h.UploadContactSignature)

	signature := []byte(`{
		"version": 1,
		"model": "titanet_large",
		"dimension": 3,
		"vector": [0.1, -0.2, 0.33],
		"created_at": "2026-03-10T00:00:00Z"
	}`)
	signatureURL := "/contacts/" + strconv.FormatUint(uint64(contact.ID), 10) + "/signature"
	req := makeMultipartRequest(t, http.MethodPost, signatureURL, "signature", "manual.json", signature)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.Code, resp.Body.String())
	}

	updated := loadContact(t, h, contact.ID)
	if updated.SignatureStatus != "ready" {
		t.Fatalf("expected signature_status=ready, got %q", updated.SignatureStatus)
	}
	if updated.SyncError != nil {
		t.Fatalf("expected sync_error=nil, got %q", *updated.SyncError)
	}
	if updated.SignatureEmbeddingPath == nil || strings.TrimSpace(*updated.SignatureEmbeddingPath) == "" {
		t.Fatalf("expected signature embedding path to be set")
	}
	if contacts.SignatureSource(updated.SignatureData) != contacts.SignatureSourceManual {
		t.Fatalf("expected signature source manual, got %q", contacts.SignatureSource(updated.SignatureData))
	}

	var currentVault models.Vault
	if err := database.DB.First(&currentVault, updated.VaultID).Error; err != nil {
		t.Fatalf("load vault: %v", err)
	}
	fileService := contacts.NewFileService(currentVault.Path)
	abs, ok := fileService.ResolveAndValidate(*updated.SignatureEmbeddingPath)
	if !ok {
		t.Fatalf("signature path did not resolve inside vault")
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("expected signature artifact on disk: %v", err)
	}
}

func TestUploadContactSignature_InvalidPayload(t *testing.T) {
	h, _, vault, cleanup := setupContactHandlerHarness(t)
	defer cleanup()

	contact := seedContact(t, h, vault.ID, "Invalid Signature Contact")

	router := gin.New()
	router.POST("/contacts/:id/signature", h.UploadContactSignature)

	invalid := []byte(`{"version":1,"model":"x","dimension":4,"vector":[0.1,0.2]}`)
	invalidURL := "/contacts/" + strconv.FormatUint(uint64(contact.ID), 10) + "/signature"
	req := makeMultipartRequest(t, http.MethodPost, invalidURL, "signature", "invalid.json", invalid)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", resp.Code, resp.Body.String())
	}
}

func TestUploadContactSnippet_ManualSignaturePrecedence(t *testing.T) {
	h, _, vault, cleanup := setupContactHandlerHarness(t)
	defer cleanup()

	contact := seedContact(t, h, vault.ID, "Manual Lock Contact")

	router := gin.New()
	router.POST("/contacts/:id/signature", h.UploadContactSignature)
	router.POST("/contacts/:id/snippet", h.UploadContactSnippet)

	signature := []byte(`{"version":1,"model":"manual","dimension":2,"vector":[0.11,0.22]}`)
	signatureURL := "/contacts/" + strconv.FormatUint(uint64(contact.ID), 10) + "/signature"
	signatureReq := makeMultipartRequest(t, http.MethodPost, signatureURL, "signature", "manual.json", signature)
	signatureResp := httptest.NewRecorder()
	router.ServeHTTP(signatureResp, signatureReq)
	if signatureResp.Code != http.StatusOK {
		t.Fatalf("signature upload failed: code=%d body=%s", signatureResp.Code, signatureResp.Body.String())
	}

	snippetURL := "/contacts/" + strconv.FormatUint(uint64(contact.ID), 10) + "/snippet"
	snippetReq := makeMultipartRequest(t, http.MethodPost, snippetURL, "snippet", "voice.wav", []byte("fake-audio-data"))
	snippetResp := httptest.NewRecorder()
	router.ServeHTTP(snippetResp, snippetReq)
	if snippetResp.Code != http.StatusOK {
		t.Fatalf("snippet upload failed: code=%d body=%s", snippetResp.Code, snippetResp.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(snippetResp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode snippet response: %v", err)
	}
	locked, _ := payload["manual_signature_locked"].(bool)
	if !locked {
		t.Fatalf("expected manual_signature_locked=true")
	}

	updated := loadContact(t, h, contact.ID)
	if updated.SignatureStatus != "ready" {
		t.Fatalf("expected signature_status=ready, got %q", updated.SignatureStatus)
	}
	if contacts.SignatureSource(updated.SignatureData) != contacts.SignatureSourceManual {
		t.Fatalf("expected source=manual after snippet upload, got %q", contacts.SignatureSource(updated.SignatureData))
	}
	if updated.SignatureEmbeddingPath == nil || strings.TrimSpace(*updated.SignatureEmbeddingPath) == "" {
		t.Fatalf("expected signature embedding path to remain set")
	}
	if updated.VoiceSnippetPath == nil || strings.TrimSpace(*updated.VoiceSnippetPath) == "" {
		t.Fatalf("expected snippet path to be set")
	}
}

func TestExtractContactSignature_EnqueuesAndSetsProcessing(t *testing.T) {
	h, db, vault, cleanup := setupContactHandlerHarness(t)
	defer cleanup()

	contact := seedContact(t, h, vault.ID, "Extract Contact")

	fileService := contacts.NewFileService(vault.Path)
	snippetRel := filepath.ToSlash(filepath.Join(filepath.ToSlash(filepath.Dir(contact.NotePath)), "voice-snippet.wav"))
	snippetAbs, ok := fileService.ResolveAndValidate(snippetRel)
	if !ok {
		t.Fatalf("snippet path did not resolve inside vault")
	}
	if err := os.WriteFile(snippetAbs, []byte("fake-audio"), 0o644); err != nil {
		t.Fatalf("write snippet file: %v", err)
	}
	contact.VoiceSnippetPath = &snippetRel
	if err := h.persistContactFile(context.Background(), contact); err != nil {
		t.Fatalf("persist contact with snippet: %v", err)
	}

	// Provide a non-started manager so handler returns processing without immediate fallback failure.
	h.contactManager = contacts.NewManager(db, h.contactRepo, "")

	router := gin.New()
	router.POST("/contacts/:id/signature/extract", h.ExtractContactSignature)

	extractURL := "/contacts/" + strconv.FormatUint(uint64(contact.ID), 10) + "/signature/extract"
	req := httptest.NewRequest(http.MethodPost, extractURL, nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.Code, resp.Body.String())
	}

	updated := loadContact(t, h, contact.ID)
	if updated.SignatureStatus != "processing" {
		t.Fatalf("expected processing status, got %q", updated.SignatureStatus)
	}
	if contacts.SignatureSource(updated.SignatureData) != contacts.SignatureSourceExtracted {
		t.Fatalf("expected source=extracted, got %q", contacts.SignatureSource(updated.SignatureData))
	}
}

func TestUploadContactSignature_VaultIsolation(t *testing.T) {
	h, db, _, cleanup := setupContactHandlerHarness(t)
	defer cleanup()

	secondVaultPath := filepath.Join(t.TempDir(), "vault-secondary")
	if err := os.MkdirAll(secondVaultPath, 0o755); err != nil {
		t.Fatalf("create second vault path: %v", err)
	}
	secondVault := models.Vault{
		Name:     "Secondary",
		Path:     secondVaultPath,
		IsActive: false,
	}
	if err := db.Create(&secondVault).Error; err != nil {
		t.Fatalf("create second vault row: %v", err)
	}

	contact := seedContact(t, h, secondVault.ID, "Other Vault Contact")

	router := gin.New()
	router.POST("/contacts/:id/signature", h.UploadContactSignature)

	signatureURL := "/contacts/" + strconv.FormatUint(uint64(contact.ID), 10) + "/signature"
	req := makeMultipartRequest(
		t,
		http.MethodPost,
		signatureURL,
		"signature",
		"manual.json",
		[]byte(`{"version":1,"model":"x","dimension":2,"vector":[0.1,0.2]}`),
	)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for contact in non-active vault, got %d body=%s", resp.Code, resp.Body.String())
	}

	unchanged := loadContact(t, h, contact.ID)
	if unchanged.SignatureEmbeddingPath != nil {
		t.Fatalf("expected no signature path update for isolated vault contact")
	}
}
