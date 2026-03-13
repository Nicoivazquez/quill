package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"quill/internal/models"
	"quill/pkg/logger"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// knownProviders is the canonical list of cloud transcription providers surfaced
// in the list endpoint. New providers should be added here.
var knownProviders = []string{"assemblyai", "deepgram", "openai"}

var knownProviderSet = func() map[string]bool {
	m := make(map[string]bool, len(knownProviders))
	for _, p := range knownProviders {
		m[p] = true
	}
	return m
}()

// cloudProviderResponse is the safe public representation of a provider config.
// The actual API key is never included.
type cloudProviderResponse struct {
	Provider string `json:"provider"`
	HasKey   bool   `json:"has_key"`
	IsActive bool   `json:"is_active"`
}

// upsertCloudProviderRequest is the request body for PUT /cloud-providers/:provider.
type upsertCloudProviderRequest struct {
	APIKey   string `json:"api_key"`
	IsActive *bool  `json:"is_active"`
}

// ListCloudProviders returns all known providers with has_key flags. API keys are
// never included in the response.
//
// @Summary List cloud provider configurations
// @Description Returns all known cloud transcription providers with has_key flags
// @Tags cloud-providers
// @Produce json
// @Success 200 {array} cloudProviderResponse
// @Security BearerAuth
// @Router /api/v1/cloud-providers [get]
func (h *Handler) ListCloudProviders(c *gin.Context) {
	ctx := c.Request.Context()

	allStored, err := h.cloudProviderRepo.ListAll(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list cloud provider configurations"})
		return
	}

	byProvider := make(map[string]models.CloudProviderConfig, len(allStored))
	for _, cfg := range allStored {
		byProvider[cfg.Provider] = cfg
	}

	resp := make([]cloudProviderResponse, 0, len(knownProviders))
	for _, name := range knownProviders {
		entry := cloudProviderResponse{
			Provider: name,
			HasKey:   false,
			IsActive: false,
		}
		if cfg, ok := byProvider[name]; ok {
			entry.HasKey = cfg.APIKey != ""
			entry.IsActive = cfg.IsActive
		}
		resp = append(resp, entry)
	}

	c.JSON(http.StatusOK, resp)
}

// UpsertCloudProvider creates or updates the API key for a cloud provider.
// When provider="openai", the key is also synced to the active LLM configuration.
//
// @Summary Create or update a cloud provider API key
// @Description Stores an API key for the specified cloud transcription provider
// @Tags cloud-providers
// @Accept json
// @Produce json
// @Param provider path string true "Provider name (assemblyai, deepgram, openai)"
// @Param request body upsertCloudProviderRequest true "API key details"
// @Success 200 {object} cloudProviderResponse
// @Failure 400 {object} map[string]string
// @Security BearerAuth
// @Router /api/v1/cloud-providers/{provider} [put]
func (h *Handler) UpsertCloudProvider(c *gin.Context) {
	provider := strings.ToLower(strings.TrimSpace(c.Param("provider")))
	if !knownProviderSet[provider] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown provider: " + provider})
		return
	}
	ctx := c.Request.Context()

	var req upsertCloudProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	if strings.TrimSpace(req.APIKey) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "api_key is required and must not be empty"})
		return
	}

	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	cfg := &models.CloudProviderConfig{
		Provider: provider,
		APIKey:   strings.TrimSpace(req.APIKey),
		IsActive: isActive,
	}

	if err := h.cloudProviderRepo.Upsert(ctx, cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save cloud provider configuration"})
		return
	}

	// Bidirectional sync: when saving an OpenAI key, also update the active LLM config.
	if provider == "openai" {
		if err := h.syncOpenAIKeyToLLMConfig(ctx, cfg.APIKey); err != nil {
			logger.Warn("cloud-providers: failed to sync OpenAI key to LLM config", "error", err)
		}
	}

	c.JSON(http.StatusOK, cloudProviderResponse{
		Provider: cfg.Provider,
		HasKey:   true,
		IsActive: cfg.IsActive,
	})
}

// DeleteCloudProvider removes the API key for a cloud provider.
//
// @Summary Delete a cloud provider API key
// @Description Removes the stored API key for the specified provider
// @Tags cloud-providers
// @Produce json
// @Param provider path string true "Provider name"
// @Success 204
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /api/v1/cloud-providers/{provider} [delete]
func (h *Handler) DeleteCloudProvider(c *gin.Context) {
	provider := strings.ToLower(strings.TrimSpace(c.Param("provider")))
	if !knownProviderSet[provider] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown provider: " + provider})
		return
	}
	ctx := c.Request.Context()

	// Verify the provider exists before deleting.
	if _, err := h.cloudProviderRepo.GetByProvider(ctx, provider); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Provider not found: " + provider})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to look up provider"})
		return
	}

	if err := h.cloudProviderRepo.Delete(ctx, provider); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete cloud provider configuration"})
		return
	}

	c.Status(http.StatusNoContent)
}

// syncOpenAIKeyToLLMConfig updates the active LLM config's API key when a new
// OpenAI key is stored via the cloud providers endpoint. If no active LLM config
// exists yet, a new active OpenAI LLM config is created.
func (h *Handler) syncOpenAIKeyToLLMConfig(ctx context.Context, apiKey string) error {
	existing, err := h.llmConfigRepo.GetActive(ctx)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		// No active config — create a new one for openai.
		newCfg := &models.LLMConfig{
			Provider: "openai",
			APIKey:   &apiKey,
			IsActive: true,
		}
		return h.llmConfigRepo.Create(ctx, newCfg)
	}

	// Update the existing active config's API key.
	existing.APIKey = &apiKey
	return h.llmConfigRepo.Update(ctx, existing)
}
