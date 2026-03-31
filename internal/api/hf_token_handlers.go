package api

// HF token handlers — commented out, pyannote disabled
//
// import (
// 	"errors"
// 	"net/http"
// 	"os"
// 	"strings"
//
// 	"quill/internal/models"
//
// 	"github.com/gin-gonic/gin"
// 	"gorm.io/gorm"
// )
//
// const hfProviderKey = "huggingface"
//
// type hfTokenStatusResponse struct {
// 	HasToken bool `json:"has_token"`
// }
//
// type upsertHFTokenRequest struct {
// 	Token string `json:"token"`
// }
//
// // GetHFTokenStatus returns whether a Hugging Face token is configured.
// //
// // @Summary Check Hugging Face token status
// // @Tags settings
// // @Produce json
// // @Success 200 {object} hfTokenStatusResponse
// // @Security BearerAuth
// // @Router /api/v1/settings/hf-token [get]
// func (h *Handler) GetHFTokenStatus(c *gin.Context) {
// 	ctx := c.Request.Context()
//
// 	cfg, err := h.cloudProviderRepo.GetByProvider(ctx, hfProviderKey)
// 	if err != nil {
// 		if errors.Is(err, gorm.ErrRecordNotFound) {
// 			c.JSON(http.StatusOK, hfTokenStatusResponse{HasToken: false})
// 			return
// 		}
// 		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check token status"})
// 		return
// 	}
//
// 	c.JSON(http.StatusOK, hfTokenStatusResponse{HasToken: cfg.APIKey != ""})
// }
//
// // UpsertHFToken stores or updates the Hugging Face token.
// //
// // @Summary Save Hugging Face token
// // @Tags settings
// // @Accept json
// // @Produce json
// // @Param request body upsertHFTokenRequest true "Token"
// // @Success 200 {object} hfTokenStatusResponse
// // @Failure 400 {object} map[string]string
// // @Security BearerAuth
// // @Router /api/v1/settings/hf-token [put]
// func (h *Handler) UpsertHFToken(c *gin.Context) {
// 	var req upsertHFTokenRequest
// 	if err := c.ShouldBindJSON(&req); err != nil {
// 		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
// 		return
// 	}
//
// 	token := strings.TrimSpace(req.Token)
// 	if token == "" {
// 		c.JSON(http.StatusBadRequest, gin.H{"error": "token is required and must not be empty"})
// 		return
// 	}
//
// 	ctx := c.Request.Context()
// 	cfg := &models.CloudProviderConfig{
// 		Provider: hfProviderKey,
// 		APIKey:   token,
// 		IsActive: true,
// 	}
//
// 	if err := h.cloudProviderRepo.Upsert(ctx, cfg); err != nil {
// 		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save token"})
// 		return
// 	}
//
// 	// Keep env var in sync so adapters and Python subprocesses see the token immediately.
// 	_ = os.Setenv("HF_TOKEN", token)
//
// 	c.JSON(http.StatusOK, hfTokenStatusResponse{HasToken: true})
// }
//
// // DeleteHFToken removes the stored Hugging Face token.
// //
// // @Summary Delete Hugging Face token
// // @Tags settings
// // @Success 204
// // @Failure 404 {object} map[string]string
// // @Security BearerAuth
// // @Router /api/v1/settings/hf-token [delete]
// func (h *Handler) DeleteHFToken(c *gin.Context) {
// 	ctx := c.Request.Context()
//
// 	if _, err := h.cloudProviderRepo.GetByProvider(ctx, hfProviderKey); err != nil {
// 		if errors.Is(err, gorm.ErrRecordNotFound) {
// 			c.JSON(http.StatusNotFound, gin.H{"error": "No Hugging Face token configured"})
// 			return
// 		}
// 		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check token"})
// 		return
// 	}
//
// 	if err := h.cloudProviderRepo.Delete(ctx, hfProviderKey); err != nil {
// 		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete token"})
// 		return
// 	}
//
// 	_ = os.Unsetenv("HF_TOKEN")
//
// 	c.Status(http.StatusNoContent)
// }
