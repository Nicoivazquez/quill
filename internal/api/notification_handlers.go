package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// GetSystemNotifications returns all active (non-expired) notifications.
// This polling endpoint is intended for API clients (CLI, OpenClaw, AI agents)
// that don't maintain an SSE connection.
func (h *Handler) GetSystemNotifications(c *gin.Context) {
	if h.notifier == nil {
		c.JSON(http.StatusOK, gin.H{"notifications": []any{}})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"notifications": h.notifier.ActiveNotifications(),
	})
}
