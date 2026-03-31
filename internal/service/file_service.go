package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// SaveUploadResult contains the file path and content hash from a saved upload.
type SaveUploadResult struct {
	FilePath string
	FileHash string // SHA-256 hex digest
}

// FileService handles file system operations
type FileService interface {
	SaveUpload(file *multipart.FileHeader, destDir string) (string, error)
	SaveUploadWithHash(file *multipart.FileHeader, destDir string) (*SaveUploadResult, error)
	CreateDirectory(path string) error
	RemoveFile(path string) error
	RemoveDirectory(path string) error
	ReadFile(path string) ([]byte, error)
	FileExists(path string) (bool, error)
}

type fileService struct{}

func NewFileService() FileService {
	return &fileService{}
}

func (s *fileService) SaveUpload(fileHeader *multipart.FileHeader, destDir string) (string, error) {
	result, err := s.SaveUploadWithHash(fileHeader, destDir)
	if err != nil {
		return "", err
	}
	return result.FilePath, nil
}

func (s *fileService) SaveUploadWithHash(fileHeader *multipart.FileHeader, destDir string) (*SaveUploadResult, error) {
	// Create directory if it doesn't exist
	if err := s.CreateDirectory(destDir); err != nil {
		return nil, err
	}

	// Generate unique filename
	id := uuid.New().String()
	ext := filepath.Ext(fileHeader.Filename)
	filename := fmt.Sprintf("%s%s", id, ext)
	filePath := filepath.Join(destDir, filename)

	// Open source file
	src, err := fileHeader.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to open source file: %w", err)
	}
	defer src.Close()

	// Create destination file
	dst, err := os.Create(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create destination file: %w", err)
	}
	defer dst.Close()

	// Hash while writing — no extra disk read
	hasher := sha256.New()
	writer := io.MultiWriter(dst, hasher)

	if _, err = io.Copy(writer, src); err != nil {
		os.Remove(filePath) // Clean up on error
		return nil, fmt.Errorf("failed to copy file content: %w", err)
	}

	return &SaveUploadResult{
		FilePath: filePath,
		FileHash: hex.EncodeToString(hasher.Sum(nil)),
	}, nil
}

func (s *fileService) CreateDirectory(path string) error {
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", path, err)
	}
	return nil
}

func (s *fileService) RemoveFile(path string) error {
	return os.Remove(path)
}

func (s *fileService) RemoveDirectory(path string) error {
	return os.RemoveAll(path)
}

func (s *fileService) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (s *fileService) FileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
