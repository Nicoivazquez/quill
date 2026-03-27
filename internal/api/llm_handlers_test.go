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

// setupLLMHarness creates a Handler wired with real in-memory DB repos for LLM tests.
func setupLLMHarness(t *testing.T) (*Handler, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.LLMConfig{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	h := &Handler{
		llmConfigRepo: repository.NewLLMConfigRepository(db),
	}

	return h, func() {}
}

func buildLLMRouter(h *Handler) *gin.Engine {
	r := gin.New()
	r.GET("/api/v1/llm/config", h.GetLLMConfig)
	r.POST("/api/v1/llm/config", h.SaveLLMConfig)
	r.GET("/api/v1/llm/ollama/models", h.ListOllamaModels)
	return r
}

// TestLLM_SaveConfigWithModel verifies that the model field is persisted.
func TestLLM_SaveConfigWithModel(t *testing.T) {
	h, cleanup := setupLLMHarness(t)
	defer cleanup()

	r := buildLLMRouter(h)

	model := "llama3.2:3b"
	baseURL := "http://localhost:11434"
	body, _ := json.Marshal(map[string]any{
		"provider":  "ollama",
		"base_url":  baseURL,
		"is_active": true,
		"model":     model,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/llm/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp LLMConfigResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Model == nil || *resp.Model != model {
		t.Errorf("expected model=%q, got %v", model, resp.Model)
	}
	if resp.Provider != "ollama" {
		t.Errorf("expected provider=ollama, got %s", resp.Provider)
	}
}

// TestLLM_SaveConfigWithoutModel verifies model field is optional.
func TestLLM_SaveConfigWithoutModel(t *testing.T) {
	h, cleanup := setupLLMHarness(t)
	defer cleanup()

	r := buildLLMRouter(h)

	baseURL := "http://localhost:11434"
	body, _ := json.Marshal(map[string]any{
		"provider":  "ollama",
		"base_url":  baseURL,
		"is_active": true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/llm/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp LLMConfigResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Model != nil {
		t.Errorf("expected model=nil, got %v", *resp.Model)
	}
}

// TestLLM_GetConfigReturnsModel verifies model is returned on GET.
func TestLLM_GetConfigReturnsModel(t *testing.T) {
	h, cleanup := setupLLMHarness(t)
	defer cleanup()

	r := buildLLMRouter(h)

	// Create config with model
	model := "qwen3:8b"
	baseURL := "http://localhost:11434"
	cfg := &models.LLMConfig{
		Provider: "ollama",
		BaseURL:  &baseURL,
		IsActive: true,
		Model:    &model,
	}
	if err := h.llmConfigRepo.Create(context.Background(), cfg); err != nil {
		t.Fatalf("create config: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/llm/config", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp LLMConfigResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Model == nil || *resp.Model != model {
		t.Errorf("expected model=%q, got %v", model, resp.Model)
	}
}

// TestLLM_UpdateConfigModel verifies model can be updated on existing config.
func TestLLM_UpdateConfigModel(t *testing.T) {
	h, cleanup := setupLLMHarness(t)
	defer cleanup()

	r := buildLLMRouter(h)

	baseURL := "http://localhost:11434"

	// Create initial config
	body, _ := json.Marshal(map[string]any{
		"provider":  "ollama",
		"base_url":  baseURL,
		"is_active": true,
		"model":     "llama3.2:3b",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/llm/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("initial save: expected 200, got %d", rec.Code)
	}

	// Update to different model
	body2, _ := json.Marshal(map[string]any{
		"provider":  "ollama",
		"base_url":  baseURL,
		"is_active": true,
		"model":     "qwen3:8b",
	})
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/llm/config", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("update: expected 200, got %d body=%s", rec2.Code, rec2.Body.String())
	}

	var resp LLMConfigResponse
	if err := json.NewDecoder(rec2.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Model == nil || *resp.Model != "qwen3:8b" {
		t.Errorf("expected updated model=qwen3:8b, got %v", resp.Model)
	}
}

// TestLLM_OllamaModels_MissingBaseURL verifies 400 when base_url is missing.
func TestLLM_OllamaModels_MissingBaseURL(t *testing.T) {
	h, cleanup := setupLLMHarness(t)
	defer cleanup()

	r := buildLLMRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/llm/ollama/models", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestLLM_OllamaModels_InvalidBaseURL verifies 400 for invalid URL.
func TestLLM_OllamaModels_InvalidBaseURL(t *testing.T) {
	h, cleanup := setupLLMHarness(t)
	defer cleanup()

	r := buildLLMRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/llm/ollama/models?base_url=ftp://invalid", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestLLM_OllamaModels_MockServer tests the endpoint against a mock Ollama server.
func TestLLM_OllamaModels_MockServer(t *testing.T) {
	// Create a mock Ollama server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]any{
					{"name": "llama3.2:3b"},
					{"name": "qwen3:8b"},
					{"name": "nomic-embed-text:latest"},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer mockServer.Close()

	h, cleanup := setupLLMHarness(t)
	defer cleanup()

	r := buildLLMRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/llm/ollama/models?base_url="+mockServer.URL, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string][]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	models := resp["models"]
	if len(models) != 3 {
		t.Errorf("expected 3 models (filtering is frontend-side), got %d: %v", len(models), models)
	}

	// Verify all models are returned (embedding filter is client-side)
	found := map[string]bool{}
	for _, m := range models {
		found[m] = true
	}
	for _, expected := range []string{"llama3.2:3b", "qwen3:8b", "nomic-embed-text:latest"} {
		if !found[expected] {
			t.Errorf("expected model %q in response", expected)
		}
	}
}
