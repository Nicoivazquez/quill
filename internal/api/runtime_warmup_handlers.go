package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// GetRuntimeWarmupStatus returns desktop runtime warmup status for the current app instance.
func (h *Handler) GetRuntimeWarmupStatus(c *gin.Context) {
	if h.runtimeWarmup == nil {
		c.JSON(http.StatusOK, gin.H{
			"enabled":                false,
			"state":                  "disabled",
			"transcription_ready":    true,
			"voice_signatures_ready": true,
			"steps":                  []any{},
		})
		return
	}

	c.JSON(http.StatusOK, h.runtimeWarmup.Snapshot())
}

// RetryRuntimeWarmup restarts background runtime warmup after a failure or interruption.
func (h *Handler) RetryRuntimeWarmup(c *gin.Context) {
	if h.runtimeWarmup == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "runtime warmup is not configured"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	started := h.runtimeWarmup.Retry(ctx)
	c.JSON(http.StatusOK, gin.H{
		"started": started,
		"status":  h.runtimeWarmup.Snapshot(),
	})
}
