package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"quill/internal/models"

	"github.com/gin-gonic/gin"
)

func buildHFTokenRouter(h *Handler) *gin.Engine {
	r := gin.New()
	r.GET("/api/v1/settings/hf-token", h.GetHFTokenStatus)
	r.PUT("/api/v1/settings/hf-token", h.UpsertHFToken)
	r.DELETE("/api/v1/settings/hf-token", h.DeleteHFToken)
	return r
}

func TestHFToken_GetStatus_NoToken(t *testing.T) {
	h, cleanup := setupCloudProviderHarness(t)
	defer cleanup()

	r := buildHFTokenRouter(h)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/hf-token", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp hfTokenStatusResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.HasToken {
		t.Error("expected has_token=false when no token stored")
	}
}

func TestHFToken_UpsertAndGet(t *testing.T) {
	h, cleanup := setupCloudProviderHarness(t)
	defer cleanup()

	r := buildHFTokenRouter(h)

	// Upsert a token
	body, _ := json.Marshal(map[string]string{"token": "hf_test_token_123"})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/hf-token", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var upsertResp hfTokenStatusResponse
	if err := json.NewDecoder(rec.Body).Decode(&upsertResp); err != nil {
		t.Fatalf("decode upsert response: %v", err)
	}
	if !upsertResp.HasToken {
		t.Error("expected has_token=true after upsert")
	}

	// Verify stored in DB under "huggingface" provider key
	stored, err := h.cloudProviderRepo.GetByProvider(context.Background(), "huggingface")
	if err != nil {
		t.Fatalf("GetByProvider huggingface: %v", err)
	}
	if stored.APIKey != "hf_test_token_123" {
		t.Errorf("expected stored token hf_test_token_123, got %q", stored.APIKey)
	}

	// GET should now return has_token=true
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/settings/hf-token", nil)
	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", getRec.Code)
	}

	var getResp hfTokenStatusResponse
	json.NewDecoder(getRec.Body).Decode(&getResp) //nolint:errcheck
	if !getResp.HasToken {
		t.Error("expected has_token=true after upsert")
	}
}

func TestHFToken_UpsertEmptyToken(t *testing.T) {
	h, cleanup := setupCloudProviderHarness(t)
	defer cleanup()

	r := buildHFTokenRouter(h)

	body, _ := json.Marshal(map[string]string{"token": ""})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/hf-token", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty token, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHFToken_Delete(t *testing.T) {
	h, cleanup := setupCloudProviderHarness(t)
	defer cleanup()

	// Seed a token
	if err := h.cloudProviderRepo.Upsert(context.Background(), &models.CloudProviderConfig{
		Provider: "huggingface",
		APIKey:   "hf_to_delete",
		IsActive: true,
	}); err != nil {
		t.Fatalf("seed huggingface: %v", err)
	}

	r := buildHFTokenRouter(h)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/settings/hf-token", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Verify deleted
	_, err := h.cloudProviderRepo.GetByProvider(context.Background(), "huggingface")
	if err == nil {
		t.Fatal("expected error after deletion, got nil")
	}
}

func TestHFToken_DeleteNonexistent(t *testing.T) {
	h, cleanup := setupCloudProviderHarness(t)
	defer cleanup()

	r := buildHFTokenRouter(h)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/settings/hf-token", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for nonexistent token, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestHFToken_NotInCloudProvidersList verifies that after removing "huggingface"
// from knownProviders, it no longer appears in the cloud providers list.
func TestHFToken_NotInCloudProvidersList(t *testing.T) {
	h, cleanup := setupCloudProviderHarness(t)
	defer cleanup()

	// Store HF token via the dedicated endpoint's underlying mechanism
	if err := h.cloudProviderRepo.Upsert(context.Background(), &models.CloudProviderConfig{
		Provider: "huggingface",
		APIKey:   "hf_hidden",
		IsActive: true,
	}); err != nil {
		t.Fatalf("seed huggingface: %v", err)
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

	for _, p := range list {
		if p["provider"] == "huggingface" {
			t.Error("huggingface should not appear in cloud providers list")
		}
	}

	// Should have exactly 3 providers (assemblyai, deepgram, openai)
	if len(list) != 3 {
		t.Errorf("expected 3 providers, got %d", len(list))
	}
}

// TestHFToken_UpsertViaCloudProviderReturns400 verifies the old endpoint rejects HF.
func TestHFToken_UpsertViaCloudProviderReturns400(t *testing.T) {
	h, cleanup := setupCloudProviderHarness(t)
	defer cleanup()

	r := buildCloudProviderRouter(h)

	body, _ := json.Marshal(map[string]any{"api_key": "hf_blocked"})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/cloud-providers/huggingface", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for huggingface via cloud-providers, got %d body=%s", rec.Code, rec.Body.String())
	}
}
