package api

import (
	"net/http"
	"path/filepath"

	"quill/internal/transcription"

	"github.com/gin-gonic/gin"
)

// GetVaultIntegrity audits the active vault for bundle integrity issues.
func (h *Handler) GetVaultIntegrity(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault"})
		return
	}

	transcriptsDir := filepath.Join(vault.Path, "Transcripts")
	uploadsDir := h.config.UploadDir

	svc := transcription.NewBundleRepairService(
		h.jobRepo,
		transcriptsDir,
		uploadsDir,
		&vault.ID,
	)

	result, err := svc.AuditVault(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// RepairVaultPaths repairs path mismatches in the active vault.
func (h *Handler) RepairVaultPaths(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault"})
		return
	}

	transcriptsDir := filepath.Join(vault.Path, "Transcripts")
	uploadsDir := h.config.UploadDir

	svc := transcription.NewBundleRepairService(
		h.jobRepo,
		transcriptsDir,
		uploadsDir,
		&vault.ID,
	)

	result, err := svc.RepairPaths(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}
