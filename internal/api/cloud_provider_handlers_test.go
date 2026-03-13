package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"quill/internal/models"
	"quill/internal/repository"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupCloudProviderHarness creates a Handler wired with real in-memory DB repos.
func setupCloudProviderHarness(t *testing.T) (*Handler, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.CloudProviderConfig{}, &models.LLMConfig{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	h := &Handler{
		cloudProviderRepo: repository.NewCloudProviderConfigRepository(db),
		llmConfigRepo:     repository.NewLLMConfigRepository(db),
	}

	return h, func() {}
}

func buildCloudProviderRouter(h *Handler) *gin.Engine {
	r := gin.New()
	r.GET("/api/v1/cloud-providers", h.ListCloudProviders)
	r.PUT("/api/v1/cloud-providers/:provider", h.UpsertCloudProvider)
	r.DELETE("/api/v1/cloud-providers/:provider", h.DeleteCloudProvider)
	return r
}

// TestCloudProviders_ListEmpty returns empty list when no keys configured.
func TestCloudProviders_ListEmpty(t *testing.T) {
	h, cleanup := setupCloudProviderHarness(t)
	defer cleanup()

	r := buildCloudProviderRouter(h)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/cloud-providers", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// All known providers should be present, each with has_key=false.
	if len(resp) == 0 {
		t.Fatal("expected non-empty provider list (known providers scaffold)")
	}
	for _, p := range resp {
		hasKey, _ := p["has_key"].(bool)
		if hasKey {
			t.Errorf("provider %v should have has_key=false when no key stored", p["provider"])
		}
	}
}

// TestCloudProviders_UpsertAssemblyAI creates an AssemblyAI config.
func TestCloudProviders_UpsertAssemblyAI(t *testing.T) {
	h, cleanup := setupCloudProviderHarness(t)
	defer cleanup()

	r := buildCloudProviderRouter(h)

	body, _ := json.Marshal(map[string]any{"api_key": "aai-key-123", "is_active": true})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/cloud-providers/assemblyai", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Verify stored config via repo directly.
	stored, err := h.cloudProviderRepo.GetByProvider(context.Background(), "assemblyai")
	if err != nil {
		t.Fatalf("GetByProvider after upsert: %v", err)
	}
	if stored.APIKey != "aai-key-123" {
		t.Errorf("expected stored api_key=aai-key-123, got %q", stored.APIKey)
	}
	// The response must NOT expose the actual key.
	var resp map[string]any
	// Re-issue GET to inspect response shape.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/cloud-providers", nil)
	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, getReq)
	var list []map[string]any
	json.NewDecoder(getRec.Body).Decode(&list) //nolint:errcheck
	for _, p := range list {
		if p["provider"] == "assemblyai" {
			resp = p
			break
		}
	}
	if resp == nil {
		t.Fatal("assemblyai not found in list response")
	}
	if _, hasRaw := resp["api_key"]; hasRaw {
		t.Error("api_key field must not be present in list response")
	}
	hasKey, _ := resp["has_key"].(bool)
	if !hasKey {
		t.Error("expected has_key=true after upsert")
	}
}

// TestCloudProviders_UpsertOpenAI_SyncsToLLMConfig verifies bidirectional sync.
func TestCloudProviders_UpsertOpenAI_SyncsToLLMConfig(t *testing.T) {
	h, cleanup := setupCloudProviderHarness(t)
	defer cleanup()

	r := buildCloudProviderRouter(h)

	body, _ := json.Marshal(map[string]any{"api_key": "sk-sync-test", "is_active": true})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/cloud-providers/openai", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Cloud provider config must be stored.
	stored, err := h.cloudProviderRepo.GetByProvider(context.Background(), "openai")
	if err != nil {
		t.Fatalf("GetByProvider openai: %v", err)
	}
	if stored.APIKey != "sk-sync-test" {
		t.Errorf("expected cloud provider key sk-sync-test, got %q", stored.APIKey)
	}

	// Active LLM config must be created/updated with the new key.
	llmCfg, err := h.llmConfigRepo.GetActive(context.Background())
	if err != nil {
		t.Fatalf("GetActive LLM config after openai upsert: %v", err)
	}
	if llmCfg.APIKey == nil || *llmCfg.APIKey != "sk-sync-test" {
		got := "<nil>"
		if llmCfg.APIKey != nil {
			got = *llmCfg.APIKey
		}
		t.Errorf("expected LLM config api_key=sk-sync-test, got %q", got)
	}
}

// TestCloudProviders_UpsertOpenAI_SyncsToExistingLLMConfig updates existing active config.
func TestCloudProviders_UpsertOpenAI_SyncsToExistingLLMConfig(t *testing.T) {
	h, cleanup := setupCloudProviderHarness(t)
	defer cleanup()

	// Pre-create an active LLM config for openai.
	existing := &models.LLMConfig{
		Provider: "openai",
		APIKey:   strPtr("sk-old-key"),
		IsActive: true,
	}
	if err := h.llmConfigRepo.Create(context.Background(), existing); err != nil {
		t.Fatalf("seed LLM config: %v", err)
	}

	r := buildCloudProviderRouter(h)

	body, _ := json.Marshal(map[string]any{"api_key": "sk-new-key", "is_active": true})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/cloud-providers/openai", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	llmCfg, err := h.llmConfigRepo.GetActive(context.Background())
	if err != nil {
		t.Fatalf("GetActive after sync: %v", err)
	}
	if llmCfg.APIKey == nil || *llmCfg.APIKey != "sk-new-key" {
		got := "<nil>"
		if llmCfg.APIKey != nil {
			got = *llmCfg.APIKey
		}
		t.Errorf("expected updated LLM api_key=sk-new-key, got %q", got)
	}
}

// TestCloudProviders_DeleteAssemblyAI removes a stored key.
func TestCloudProviders_DeleteAssemblyAI(t *testing.T) {
	h, cleanup := setupCloudProviderHarness(t)
	defer cleanup()

	// Seed a key first.
	if err := h.cloudProviderRepo.Upsert(context.Background(), &models.CloudProviderConfig{
		Provider: "assemblyai",
		APIKey:   "aai-to-delete",
		IsActive: true,
	}); err != nil {
		t.Fatalf("seed assemblyai: %v", err)
	}

	r := buildCloudProviderRouter(h)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/cloud-providers/assemblyai", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", rec.Code, rec.Body.String())
	}

	_, err := h.cloudProviderRepo.GetByProvider(context.Background(), "assemblyai")
	if err == nil {
		t.Fatal("expected error after deletion, got nil")
	}
}

// TestCloudProviders_DeleteNonexistent returns 404.
func TestCloudProviders_DeleteNonexistent(t *testing.T) {
	h, cleanup := setupCloudProviderHarness(t)
	defer cleanup()

	r := buildCloudProviderRouter(h)
	// "nonexistent" is not in the provider allowlist, so we get 400.
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/cloud-providers/nonexistent", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown provider, got %d", rec.Code)
	}
}

// TestCloudProviders_UpsertEmptyAPIKey returns 400.
func TestCloudProviders_UpsertEmptyAPIKey(t *testing.T) {
	h, cleanup := setupCloudProviderHarness(t)
	defer cleanup()

	r := buildCloudProviderRouter(h)

	body, _ := json.Marshal(map[string]any{"api_key": "", "is_active": true})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/cloud-providers/assemblyai", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty api_key, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestCloudProviders_UpsertMissingAPIKey returns 400.
func TestCloudProviders_UpsertMissingAPIKey(t *testing.T) {
	h, cleanup := setupCloudProviderHarness(t)
	defer cleanup()

	r := buildCloudProviderRouter(h)

	body, _ := json.Marshal(map[string]any{"is_active": true})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/cloud-providers/deepgram", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing api_key, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestCloudProviders_ListShowsHasKey reflects correct has_key for each provider.
func TestCloudProviders_ListShowsHasKey(t *testing.T) {
	h, cleanup := setupCloudProviderHarness(t)
	defer cleanup()

	// Store one provider key.
	if err := h.cloudProviderRepo.Upsert(context.Background(), &models.CloudProviderConfig{
		Provider: "deepgram",
		APIKey:   "dg-present",
		IsActive: true,
	}); err != nil {
		t.Fatalf("seed deepgram: %v", err)
	}

	r := buildCloudProviderRouter(h)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/cloud-providers", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var list []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}

	byProvider := map[string]map[string]any{}
	for _, p := range list {
		if name, ok := p["provider"].(string); ok {
			byProvider[name] = p
		}
	}

	if dg, ok := byProvider["deepgram"]; !ok {
		t.Error("deepgram not in list")
	} else if hasKey, _ := dg["has_key"].(bool); !hasKey {
		t.Error("deepgram should have has_key=true")
	}

	for _, provider := range []string{"assemblyai", "openai"} {
		if p, ok := byProvider[provider]; ok {
			if hasKey, _ := p["has_key"].(bool); hasKey {
				t.Errorf("%s should have has_key=false, got true", provider)
			}
		}
	}
}
