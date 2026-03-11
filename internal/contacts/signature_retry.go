package contacts

import (
	"encoding/json"
	"strings"
	"time"

	"quill/internal/models"
)

const (
	SignatureSourceManual    = "manual"
	SignatureSourceExtracted = "extracted"

	signatureRetryInitialDelay    = 5 * time.Minute
	signatureRetryRecoveryDelay   = 3 * time.Minute
	signatureProcessingStaleAfter = 10 * time.Minute
)

var signatureRetryBackoff = []time.Duration{
	signatureRetryInitialDelay,
	15 * time.Minute,
	1 * time.Hour,
	6 * time.Hour,
	24 * time.Hour,
}

type SignatureMetadata struct {
	Source        string `json:"source"`
	Model         string `json:"model,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
	RetryCount    int    `json:"retry_count,omitempty"`
	LastAttemptAt string `json:"last_attempt_at,omitempty"`
	NextRetryAt   string `json:"next_retry_at,omitempty"`
	LastError     string `json:"last_error,omitempty"`
}

func ParseSignatureMetadata(raw *string) SignatureMetadata {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return SignatureMetadata{}
	}

	var metadata SignatureMetadata
	if err := json.Unmarshal([]byte(strings.TrimSpace(*raw)), &metadata); err != nil {
		return SignatureMetadata{}
	}

	metadata.Source = strings.ToLower(strings.TrimSpace(metadata.Source))
	metadata.Model = strings.TrimSpace(metadata.Model)
	metadata.UpdatedAt = strings.TrimSpace(metadata.UpdatedAt)
	metadata.LastAttemptAt = strings.TrimSpace(metadata.LastAttemptAt)
	metadata.NextRetryAt = strings.TrimSpace(metadata.NextRetryAt)
	metadata.LastError = strings.TrimSpace(metadata.LastError)
	if metadata.RetryCount < 0 {
		metadata.RetryCount = 0
	}
	return metadata
}

func SerializeSignatureMetadata(metadata SignatureMetadata) *string {
	metadata.Source = strings.ToLower(strings.TrimSpace(metadata.Source))
	metadata.Model = strings.TrimSpace(metadata.Model)
	metadata.UpdatedAt = strings.TrimSpace(metadata.UpdatedAt)
	metadata.LastAttemptAt = strings.TrimSpace(metadata.LastAttemptAt)
	metadata.NextRetryAt = strings.TrimSpace(metadata.NextRetryAt)
	metadata.LastError = strings.TrimSpace(metadata.LastError)
	if metadata.Source == "" {
		return nil
	}

	payload, err := json.Marshal(metadata)
	if err != nil {
		return nil
	}
	value := string(payload)
	return &value
}

func SignatureSource(raw *string) string {
	return ParseSignatureMetadata(raw).Source
}

func HasManualSignature(contact *models.Contact) bool {
	if contact == nil {
		return false
	}
	if contact.SignatureEmbeddingPath == nil || strings.TrimSpace(*contact.SignatureEmbeddingPath) == "" {
		return false
	}
	return SignatureSource(contact.SignatureData) == SignatureSourceManual
}

func PrepareRetryAttempt(contact *models.Contact, now time.Time) SignatureMetadata {
	metadata := ParseSignatureMetadata(contact.SignatureData)
	metadata.Source = SignatureSourceExtracted
	metadata.UpdatedAt = now.UTC().Format(time.RFC3339)
	metadata.LastAttemptAt = now.UTC().Format(time.RFC3339)
	metadata.NextRetryAt = ""
	metadata.LastError = ""
	metadata.RetryCount++
	contact.SignatureData = SerializeSignatureMetadata(metadata)
	return metadata
}

func MarkRetryFailure(contact *models.Contact, message string, now time.Time) SignatureMetadata {
	metadata := ParseSignatureMetadata(contact.SignatureData)
	metadata.Source = SignatureSourceExtracted
	metadata.UpdatedAt = now.UTC().Format(time.RFC3339)
	metadata.LastError = strings.TrimSpace(message)
	metadata.NextRetryAt = now.UTC().Add(signatureRetryDelay(metadata.RetryCount)).Format(time.RFC3339)
	contact.SignatureData = SerializeSignatureMetadata(metadata)
	return metadata
}

func MarkRetryReady(contact *models.Contact, model string, now time.Time) SignatureMetadata {
	metadata := ParseSignatureMetadata(contact.SignatureData)
	metadata.Source = SignatureSourceExtracted
	metadata.Model = strings.TrimSpace(model)
	metadata.UpdatedAt = now.UTC().Format(time.RFC3339)
	metadata.NextRetryAt = ""
	metadata.LastError = ""
	contact.SignatureData = SerializeSignatureMetadata(metadata)
	return metadata
}

func RetryState(contact *models.Contact, now time.Time) (string, time.Time, bool) {
	if contact == nil {
		return "", time.Time{}, false
	}
	if HasManualSignature(contact) {
		return "", time.Time{}, false
	}
	if contact.VoiceSnippetPath == nil || strings.TrimSpace(*contact.VoiceSnippetPath) == "" {
		return "", time.Time{}, false
	}

	status := strings.ToLower(strings.TrimSpace(contact.SignatureStatus))
	metadata := ParseSignatureMetadata(contact.SignatureData)

	switch status {
	case "failed":
		if nextRetry, ok := parseSignatureTime(metadata.NextRetryAt); ok {
			if !now.Before(nextRetry) {
				return "failed", nextRetry, true
			}
			return "failed", nextRetry, false
		}
		fallback := contact.UpdatedAt.UTC().Add(signatureRetryInitialDelay)
		if contact.UpdatedAt.IsZero() || !now.Before(fallback) {
			return "failed", fallback, true
		}
		return "failed", fallback, false
	case "processing":
		if lastAttempt, ok := parseSignatureTime(metadata.LastAttemptAt); ok {
			dueAt := lastAttempt.Add(signatureProcessingStaleAfter)
			if !now.Before(dueAt) {
				return "processing", dueAt, true
			}
			return "processing", dueAt, false
		}
		fallback := contact.UpdatedAt.UTC().Add(signatureRetryRecoveryDelay)
		if contact.UpdatedAt.IsZero() || !now.Before(fallback) {
			return "processing", fallback, true
		}
		return "processing", fallback, false
	default:
		return "", time.Time{}, false
	}
}

func signatureRetryDelay(retryCount int) time.Duration {
	if retryCount <= 0 {
		return signatureRetryBackoff[0]
	}
	index := retryCount - 1
	if index >= len(signatureRetryBackoff) {
		index = len(signatureRetryBackoff) - 1
	}
	return signatureRetryBackoff[index]
}

func parseSignatureTime(value string) (time.Time, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}
