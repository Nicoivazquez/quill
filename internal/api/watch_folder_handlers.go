package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"scriberr/internal/folderwatch"

	"github.com/gin-gonic/gin"
)

// WatchFolderResponse represents a watched folder entry and its runtime status.
type WatchFolderResponse struct {
	ID               uint       `json:"id"`
	Path             string     `json:"path"`
	Recursive        bool       `json:"recursive"`
	Enabled          bool       `json:"enabled"`
	Active           bool       `json:"active"`
	LastRuntimeError string     `json:"last_runtime_error,omitempty"`
	LastImportedAt   *time.Time `json:"last_imported_at,omitempty"`
	LastImportedFile string     `json:"last_imported_file,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// CreateWatchFolderRequest represents folder creation payload.
type CreateWatchFolderRequest struct {
	Path      string `json:"path" binding:"required"`
	Recursive *bool  `json:"recursive,omitempty"`
	Enabled   *bool  `json:"enabled,omitempty"`
}

// UpdateWatchFolderRequest represents folder update payload.
type UpdateWatchFolderRequest struct {
	Enabled *bool `json:"enabled"`
}

func toWatchFolderResponse(view folderwatch.FolderView) WatchFolderResponse {
	return WatchFolderResponse{
		ID:               view.Folder.ID,
		Path:             view.Folder.Path,
		Recursive:        view.Folder.Recursive,
		Enabled:          view.Folder.Enabled,
		Active:           view.Active,
		LastRuntimeError: view.LastRuntimeError,
		LastImportedAt:   view.LastImportedAt,
		LastImportedFile: view.LastImportedFile,
		CreatedAt:        view.Folder.CreatedAt,
		UpdatedAt:        view.Folder.UpdatedAt,
	}
}

func currentUserID(c *gin.Context) (uint, bool) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return 0, false
	}
	return userID.(uint), true
}

func parseWatchFolderID(c *gin.Context) (uint, bool) {
	idValue := c.Param("id")
	parsed, err := strconv.ParseUint(idValue, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid folder id"})
		return 0, false
	}
	return uint(parsed), true
}

func (h *Handler) folderWatcherReady(c *gin.Context) bool {
	if h.folderWatchService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Folder watch service is not available"})
		return false
	}
	return true
}

// ListWatchFolders lists all watched folders for the authenticated user.
func (h *Handler) ListWatchFolders(c *gin.Context) {
	if !h.folderWatcherReady(c) {
		return
	}

	userID, ok := currentUserID(c)
	if !ok {
		return
	}

	views, err := h.folderWatchService.ListUserFolders(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list watched folders"})
		return
	}

	response := make([]WatchFolderResponse, 0, len(views))
	for _, view := range views {
		response = append(response, toWatchFolderResponse(view))
	}

	c.JSON(http.StatusOK, gin.H{"folders": response})
}

// CreateWatchFolder creates a new watched folder and starts watching when enabled.
func (h *Handler) CreateWatchFolder(c *gin.Context) {
	if !h.folderWatcherReady(c) {
		return
	}

	userID, ok := currentUserID(c)
	if !ok {
		return
	}

	var req CreateWatchFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	recursive := true
	if req.Recursive != nil {
		recursive = *req.Recursive
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	view, err := h.folderWatchService.CreateUserFolder(c.Request.Context(), userID, req.Path, recursive, enabled)
	if err != nil {
		switch {
		case errors.Is(err, folderwatch.ErrFolderAlreadyExists):
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		case errors.Is(err, folderwatch.ErrInvalidFolderPath):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create watched folder"})
		}
		return
	}

	c.JSON(http.StatusCreated, toWatchFolderResponse(*view))
}

// UpdateWatchFolder updates mutable fields on a watched folder.
func (h *Handler) UpdateWatchFolder(c *gin.Context) {
	if !h.folderWatcherReady(c) {
		return
	}

	userID, ok := currentUserID(c)
	if !ok {
		return
	}

	folderID, ok := parseWatchFolderID(c)
	if !ok {
		return
	}

	var req UpdateWatchFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}
	if req.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "At least one field must be updated"})
		return
	}

	view, err := h.folderWatchService.SetUserFolderEnabled(c.Request.Context(), userID, folderID, *req.Enabled)
	if err != nil {
		switch {
		case errors.Is(err, folderwatch.ErrFolderNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "Watched folder not found"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update watched folder"})
		}
		return
	}

	c.JSON(http.StatusOK, toWatchFolderResponse(*view))
}

// DeleteWatchFolder removes a watched folder for the authenticated user.
func (h *Handler) DeleteWatchFolder(c *gin.Context) {
	if !h.folderWatcherReady(c) {
		return
	}

	userID, ok := currentUserID(c)
	if !ok {
		return
	}

	folderID, ok := parseWatchFolderID(c)
	if !ok {
		return
	}

	if err := h.folderWatchService.DeleteUserFolder(c.Request.Context(), userID, folderID); err != nil {
		if errors.Is(err, folderwatch.ErrFolderNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Watched folder not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete watched folder"})
		return
	}

	c.Status(http.StatusNoContent)
}
