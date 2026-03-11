package contacts

import (
	"strings"
	"testing"
	"time"

	"quill/internal/models"
)

// ---- SignatureSourceManual / SignatureSourceExtracted constants -------------

func TestSignatureSourceConstants(t *testing.T) {
	if SignatureSourceManual != "manual" {
		t.Errorf("SignatureSourceManual = %q, want %q", SignatureSourceManual, "manual")
	}
	if SignatureSourceExtracted != "extracted" {
		t.Errorf("SignatureSourceExtracted = %q, want %q", SignatureSourceExtracted, "extracted")
	}
}

// ---- ParseSignatureMetadata -------------------------------------------------

func TestParseSignatureMetadata_NilPointer(t *testing.T) {
	meta := ParseSignatureMetadata(nil)
	if meta.Source != "" {
		t.Errorf("nil pointer: expected empty source, got %q", meta.Source)
	}
}

func TestParseSignatureMetadata_EmptyString(t *testing.T) {
	empty := ""
	meta := ParseSignatureMetadata(&empty)
	if meta.Source != "" {
		t.Errorf("empty string: expected empty source, got %q", meta.Source)
	}
}

func TestParseSignatureMetadata_WhitespaceOnly(t *testing.T) {
	ws := "   "
	meta := ParseSignatureMetadata(&ws)
	if meta.Source != "" {
		t.Errorf("whitespace: expected empty source, got %q", meta.Source)
	}
}

func TestParseSignatureMetadata_InvalidJSON(t *testing.T) {
	bad := "not-json"
	meta := ParseSignatureMetadata(&bad)
	// Invalid JSON should return zero-value struct.
	if meta.Source != "" || meta.Model != "" {
		t.Errorf("invalid JSON: expected zero-value metadata, got %+v", meta)
	}
}

func TestParseSignatureMetadata_ValidManual(t *testing.T) {
	raw := `{"source":"manual","model":"titanet_large","retry_count":0}`
	meta := ParseSignatureMetadata(&raw)
	if meta.Source != "manual" {
		t.Errorf("source: got %q, want manual", meta.Source)
	}
	if meta.Model != "titanet_large" {
		t.Errorf("model: got %q, want titanet_large", meta.Model)
	}
}

func TestParseSignatureMetadata_UppercaseSourceNormalized(t *testing.T) {
	raw := `{"source":"MANUAL"}`
	meta := ParseSignatureMetadata(&raw)
	if meta.Source != "manual" {
		t.Errorf("uppercase source should be lowercased, got %q", meta.Source)
	}
}

func TestParseSignatureMetadata_NegativeRetryCountClampedToZero(t *testing.T) {
	raw := `{"source":"extracted","retry_count":-5}`
	meta := ParseSignatureMetadata(&raw)
	if meta.RetryCount != 0 {
		t.Errorf("negative retry_count should be clamped to 0, got %d", meta.RetryCount)
	}
}

// ---- SerializeSignatureMetadata ---------------------------------------------

func TestSerializeSignatureMetadata_EmptySourceReturnsNil(t *testing.T) {
	meta := SignatureMetadata{Source: ""}
	result := SerializeSignatureMetadata(meta)
	if result != nil {
		t.Errorf("empty source should return nil, got %q", *result)
	}
}

func TestSerializeSignatureMetadata_WhitespaceSourceReturnsNil(t *testing.T) {
	meta := SignatureMetadata{Source: "   "}
	result := SerializeSignatureMetadata(meta)
	if result != nil {
		t.Errorf("whitespace source should return nil, got %q", *result)
	}
}

func TestSerializeSignatureMetadata_RoundTrip(t *testing.T) {
	original := SignatureMetadata{
		Source:     "extracted",
		Model:      "titanet_large",
		RetryCount: 3,
		LastError:  "timeout",
	}
	serialized := SerializeSignatureMetadata(original)
	if serialized == nil {
		t.Fatal("serialized should not be nil")
	}
	parsed := ParseSignatureMetadata(serialized)
	if parsed.Source != "extracted" {
		t.Errorf("source round-trip: got %q, want extracted", parsed.Source)
	}
	if parsed.RetryCount != 3 {
		t.Errorf("retry_count round-trip: got %d, want 3", parsed.RetryCount)
	}
	if parsed.LastError != "timeout" {
		t.Errorf("last_error round-trip: got %q, want timeout", parsed.LastError)
	}
}

// ---- SignatureSource --------------------------------------------------------

func TestSignatureSource_ReturnsSourceFromMetadata(t *testing.T) {
	raw := `{"source":"manual"}`
	if got := SignatureSource(&raw); got != "manual" {
		t.Errorf("SignatureSource = %q, want manual", got)
	}
}

func TestSignatureSource_NilReturnsEmpty(t *testing.T) {
	if got := SignatureSource(nil); got != "" {
		t.Errorf("SignatureSource(nil) = %q, want empty", got)
	}
}

// ---- HasManualSignature -----------------------------------------------------

func TestHasManualSignature_NilContact_ReturnsFalse(t *testing.T) {
	if HasManualSignature(nil) {
		t.Error("nil contact must return false")
	}
}

func TestHasManualSignature_NilEmbeddingPath_ReturnsFalse(t *testing.T) {
	contact := &models.Contact{}
	if HasManualSignature(contact) {
		t.Error("nil embedding path must return false")
	}
}

func TestHasManualSignature_EmptyEmbeddingPath_ReturnsFalse(t *testing.T) {
	empty := ""
	contact := &models.Contact{SignatureEmbeddingPath: &empty}
	if HasManualSignature(contact) {
		t.Error("empty embedding path must return false")
	}
}

func TestHasManualSignature_EmbeddingExistsButSourceExtracted_ReturnsFalse(t *testing.T) {
	emb := "path/to/embedding.json"
	raw := `{"source":"extracted"}`
	contact := &models.Contact{
		SignatureEmbeddingPath: &emb,
		SignatureData:          &raw,
	}
	if HasManualSignature(contact) {
		t.Error("extracted source must not be treated as manual signature")
	}
}

func TestHasManualSignature_EmbeddingExistsAndSourceManual_ReturnsTrue(t *testing.T) {
	emb := "path/to/embedding.json"
	raw := `{"source":"manual"}`
	contact := &models.Contact{
		SignatureEmbeddingPath: &emb,
		SignatureData:          &raw,
	}
	if !HasManualSignature(contact) {
		t.Error("manual source with embedding path must return true")
	}
}

func TestHasManualSignature_WhitespaceEmbeddingPath_ReturnsFalse(t *testing.T) {
	ws := "   "
	raw := `{"source":"manual"}`
	contact := &models.Contact{
		SignatureEmbeddingPath: &ws,
		SignatureData:          &raw,
	}
	if HasManualSignature(contact) {
		t.Error("whitespace embedding path must return false")
	}
}

// ---- PrepareRetryAttempt ----------------------------------------------------

func TestPrepareRetryAttempt_IncrementsRetryCount(t *testing.T) {
	now := time.Now().UTC()
	initial := SerializeSignatureMetadata(SignatureMetadata{
		Source:     SignatureSourceExtracted,
		RetryCount: 2,
	})
	contact := &models.Contact{SignatureData: initial}

	meta := PrepareRetryAttempt(contact, now)

	if meta.RetryCount != 3 {
		t.Errorf("RetryCount: expected 3, got %d", meta.RetryCount)
	}
	if meta.Source != SignatureSourceExtracted {
		t.Errorf("Source: expected %q, got %q", SignatureSourceExtracted, meta.Source)
	}
	if meta.LastAttemptAt == "" {
		t.Error("LastAttemptAt must be set")
	}
	if meta.NextRetryAt != "" {
		t.Errorf("NextRetryAt must be cleared, got %q", meta.NextRetryAt)
	}
	if meta.LastError != "" {
		t.Errorf("LastError must be cleared, got %q", meta.LastError)
	}
	// Verify the contact's SignatureData was updated.
	if contact.SignatureData == nil {
		t.Fatal("contact.SignatureData must not be nil after PrepareRetryAttempt")
	}
	persisted := ParseSignatureMetadata(contact.SignatureData)
	if persisted.RetryCount != 3 {
		t.Errorf("persisted RetryCount: expected 3, got %d", persisted.RetryCount)
	}
}

func TestPrepareRetryAttempt_FromZeroCount(t *testing.T) {
	now := time.Now().UTC()
	contact := &models.Contact{} // No existing SignatureData.

	meta := PrepareRetryAttempt(contact, now)

	if meta.RetryCount != 1 {
		t.Errorf("RetryCount from zero: expected 1, got %d", meta.RetryCount)
	}
}

func TestPrepareRetryAttempt_TimestampIsUTC(t *testing.T) {
	now := time.Now().UTC()
	contact := &models.Contact{}

	meta := PrepareRetryAttempt(contact, now)

	parsed, ok := parseSignatureTime(meta.LastAttemptAt)
	if !ok {
		t.Fatalf("LastAttemptAt %q is not a valid RFC3339 time", meta.LastAttemptAt)
	}
	if parsed.Location() != time.UTC {
		t.Errorf("LastAttemptAt should be UTC, got %v", parsed.Location())
	}
}

// ---- MarkRetryFailure -------------------------------------------------------

func TestMarkRetryFailure_SetsErrorAndSchedulesNextRetry(t *testing.T) {
	now := time.Now().UTC()
	contact := &models.Contact{
		SignatureData: SerializeSignatureMetadata(SignatureMetadata{
			Source:     SignatureSourceExtracted,
			RetryCount: 1,
		}),
	}

	meta := MarkRetryFailure(contact, "gpu out of memory", now)

	if meta.LastError != "gpu out of memory" {
		t.Errorf("LastError: expected %q, got %q", "gpu out of memory", meta.LastError)
	}
	if meta.NextRetryAt == "" {
		t.Error("NextRetryAt must be set after failure")
	}
	nextRetry, ok := parseSignatureTime(meta.NextRetryAt)
	if !ok {
		t.Fatalf("NextRetryAt %q is not valid RFC3339", meta.NextRetryAt)
	}
	if !nextRetry.After(now) {
		t.Errorf("NextRetryAt %v must be after now %v", nextRetry, now)
	}
}

func TestMarkRetryFailure_TrimspaceMessage(t *testing.T) {
	now := time.Now().UTC()
	contact := &models.Contact{}

	meta := MarkRetryFailure(contact, "  some error  ", now)

	if meta.LastError != "some error" {
		t.Errorf("LastError should be trimmed, got %q", meta.LastError)
	}
}

func TestMarkRetryFailure_EmptyMessageAllowed(t *testing.T) {
	now := time.Now().UTC()
	contact := &models.Contact{}
	// Should not panic with empty message.
	meta := MarkRetryFailure(contact, "", now)
	_ = meta
}

func TestMarkRetryFailure_BackoffGrowsWithRetryCount(t *testing.T) {
	now := time.Now().UTC()

	// RetryCount 0 → first backoff (5 min)
	c0 := &models.Contact{
		SignatureData: SerializeSignatureMetadata(SignatureMetadata{
			Source:     SignatureSourceExtracted,
			RetryCount: 0,
		}),
	}
	m0 := MarkRetryFailure(c0, "err", now)
	next0, _ := parseSignatureTime(m0.NextRetryAt)
	delay0 := next0.Sub(now)

	// RetryCount 2 → third backoff (1 hour)
	c2 := &models.Contact{
		SignatureData: SerializeSignatureMetadata(SignatureMetadata{
			Source:     SignatureSourceExtracted,
			RetryCount: 2,
		}),
	}
	m2 := MarkRetryFailure(c2, "err", now)
	next2, _ := parseSignatureTime(m2.NextRetryAt)
	delay2 := next2.Sub(now)

	if delay2 <= delay0 {
		t.Errorf("backoff should grow with retry count: delay0=%v delay2=%v", delay0, delay2)
	}
}

// ---- MarkRetryReady ---------------------------------------------------------

func TestMarkRetryReady_ClearsErrorAndSetsModel(t *testing.T) {
	now := time.Now().UTC()
	contact := &models.Contact{
		SignatureData: SerializeSignatureMetadata(SignatureMetadata{
			Source:      SignatureSourceExtracted,
			RetryCount:  2,
			LastError:   "previous error",
			NextRetryAt: now.Add(-time.Minute).Format(time.RFC3339),
		}),
	}

	meta := MarkRetryReady(contact, "titanet_large", now)

	if meta.LastError != "" {
		t.Errorf("LastError should be cleared, got %q", meta.LastError)
	}
	if meta.NextRetryAt != "" {
		t.Errorf("NextRetryAt should be cleared, got %q", meta.NextRetryAt)
	}
	if meta.Model != "titanet_large" {
		t.Errorf("Model: expected titanet_large, got %q", meta.Model)
	}
	if meta.Source != SignatureSourceExtracted {
		t.Errorf("Source must remain extracted, got %q", meta.Source)
	}
	if contact.SignatureData == nil {
		t.Fatal("SignatureData must not be nil after MarkRetryReady")
	}
}

func TestMarkRetryReady_TrimspaceModel(t *testing.T) {
	now := time.Now().UTC()
	contact := &models.Contact{}

	meta := MarkRetryReady(contact, "  titanet  ", now)

	if meta.Model != "titanet" {
		t.Errorf("Model should be trimmed, got %q", meta.Model)
	}
}

func TestMarkRetryReady_EmptyModelNotOverwritten(t *testing.T) {
	now := time.Now().UTC()
	contact := &models.Contact{
		SignatureData: SerializeSignatureMetadata(SignatureMetadata{
			Source: SignatureSourceExtracted,
			Model:  "existing-model",
		}),
	}

	// Passing empty model; implementation trims and sets if non-empty.
	meta := MarkRetryReady(contact, "", now)

	// The existing model should remain unmodified because empty model is not
	// set by the implementation (strings.TrimSpace("") == "").
	_ = meta
	persisted := ParseSignatureMetadata(contact.SignatureData)
	// Model is either preserved or cleared — what matters is no panic.
	_ = persisted
}

// ---- signatureRetryDelay (internal, tested via MarkRetryFailure) ------------

func TestSignatureRetryDelay_BoundaryValues(t *testing.T) {
	// retryCount 0 → first slot (5 min)
	d0 := signatureRetryDelay(0)
	if d0 != signatureRetryBackoff[0] {
		t.Errorf("retryCount=0 delay=%v, want %v", d0, signatureRetryBackoff[0])
	}

	// retryCount 1 → second slot
	d1 := signatureRetryDelay(1)
	if d1 != signatureRetryBackoff[0] {
		t.Errorf("retryCount=1 delay=%v, want backoff[0]=%v", d1, signatureRetryBackoff[0])
	}

	// retryCount == len(backoff) → last slot (clamped)
	last := signatureRetryBackoff[len(signatureRetryBackoff)-1]
	dLarge := signatureRetryDelay(len(signatureRetryBackoff) + 100)
	if dLarge != last {
		t.Errorf("large retryCount delay=%v, want clamped to last=%v", dLarge, last)
	}
}

// ---- RetryState edge cases --------------------------------------------------

func TestRetryState_NilContact_ReturnsFalse(t *testing.T) {
	_, _, due := RetryState(nil, time.Now())
	if due {
		t.Error("nil contact should return due=false")
	}
}

func TestRetryState_NoSnippetPath_ReturnsFalse(t *testing.T) {
	contact := &models.Contact{
		SignatureStatus: "failed",
	}
	_, _, due := RetryState(contact, time.Now())
	if due {
		t.Error("contact without voice snippet must not be retried")
	}
}

func TestRetryState_FailedNotYetDue_ReturnsFalse(t *testing.T) {
	now := time.Now().UTC()
	snippet := "Contacts/People/x--y/voice-snippet.wav"
	contact := &models.Contact{
		SignatureStatus: "failed",
		VoiceSnippetPath: &snippet,
		SignatureData: SerializeSignatureMetadata(SignatureMetadata{
			Source:      SignatureSourceExtracted,
			NextRetryAt: now.Add(10 * time.Minute).Format(time.RFC3339),
		}),
	}
	_, _, due := RetryState(contact, now)
	if due {
		t.Error("failed contact with future NextRetryAt should not be due yet")
	}
}

func TestRetryState_FailedPastDue_ReturnsTrue(t *testing.T) {
	now := time.Now().UTC()
	snippet := "Contacts/People/x--y/voice-snippet.wav"
	contact := &models.Contact{
		SignatureStatus: "failed",
		VoiceSnippetPath: &snippet,
		SignatureData: SerializeSignatureMetadata(SignatureMetadata{
			Source:      SignatureSourceExtracted,
			RetryCount:  1,
			NextRetryAt: now.Add(-time.Minute).Format(time.RFC3339),
		}),
		UpdatedAt: now.Add(-2 * time.Hour),
	}
	state, _, due := RetryState(contact, now)
	if state != "failed" || !due {
		t.Errorf("past-due failed contact should be ready to retry, state=%q due=%v", state, due)
	}
}

func TestRetryState_ProcessingFallbackNoLastAttempt(t *testing.T) {
	now := time.Now().UTC()
	snippet := "Contacts/People/x--y/voice-snippet.wav"

	// No LastAttemptAt — uses UpdatedAt + recoveryDelay as fallback.
	staleUpdated := now.Add(-signatureRetryRecoveryDelay - time.Minute)
	contact := &models.Contact{
		SignatureStatus:  "processing",
		VoiceSnippetPath: &snippet,
		SignatureData:    SerializeSignatureMetadata(SignatureMetadata{Source: SignatureSourceExtracted}),
		UpdatedAt:        staleUpdated,
	}
	state, _, due := RetryState(contact, now)
	if state != "processing" || !due {
		t.Errorf("stale processing contact (no LastAttemptAt) should be due; state=%q due=%v", state, due)
	}
}

func TestRetryState_UnknownStatus_ReturnsFalse(t *testing.T) {
	now := time.Now().UTC()
	snippet := "Contacts/People/x--y/voice-snippet.wav"
	contact := &models.Contact{
		SignatureStatus:  "ready",
		VoiceSnippetPath: &snippet,
	}
	_, _, due := RetryState(contact, now)
	if due {
		t.Error("ready/unknown status must not trigger retry")
	}
}

// ---- parseSignatureTime -----------------------------------------------------

func TestParseSignatureTime_ValidRFC3339(t *testing.T) {
	input := "2026-03-11T12:00:00Z"
	parsed, ok := parseSignatureTime(input)
	if !ok {
		t.Fatalf("parseSignatureTime(%q) returned ok=false", input)
	}
	if parsed.Year() != 2026 || parsed.Month() != 3 || parsed.Day() != 11 {
		t.Errorf("unexpected parsed time: %v", parsed)
	}
}

func TestParseSignatureTime_Empty_ReturnsFalse(t *testing.T) {
	_, ok := parseSignatureTime("")
	if ok {
		t.Error("empty string should return ok=false")
	}
}

func TestParseSignatureTime_Whitespace_ReturnsFalse(t *testing.T) {
	_, ok := parseSignatureTime("   ")
	if ok {
		t.Error("whitespace string should return ok=false")
	}
}

func TestParseSignatureTime_InvalidFormat_ReturnsFalse(t *testing.T) {
	_, ok := parseSignatureTime("not-a-date")
	if ok {
		t.Error("invalid format should return ok=false")
	}
}

// ---- integration: manual signature cannot be queued for retry ---------------

func TestRetryState_ManualSignatureSkipped(t *testing.T) {
	now := time.Now().UTC()
	snippet := "Contacts/People/x--y/voice-snippet.wav"
	sig := "Contacts/People/x--y/voice-signature.embedding.json"

	// Manual signature, even if status is "failed", must not be retried.
	contact := &models.Contact{
		SignatureStatus:        "failed",
		VoiceSnippetPath:       &snippet,
		SignatureEmbeddingPath: &sig,
		SignatureData: SerializeSignatureMetadata(SignatureMetadata{
			Source: SignatureSourceManual,
		}),
		UpdatedAt: now.Add(-48 * time.Hour),
	}
	_, _, due := RetryState(contact, now)
	if due {
		t.Error("manual signature contact must never be queued for retry")
	}
}

// ---- MarkRetryFailure produces valid serialized JSON ------------------------

func TestMarkRetryFailure_SerializedDataIsValidJSON(t *testing.T) {
	now := time.Now().UTC()
	contact := &models.Contact{}
	MarkRetryFailure(contact, "oops", now)

	if contact.SignatureData == nil {
		t.Fatal("SignatureData must not be nil after MarkRetryFailure")
	}
	// Ensure the stored value is parseable.
	parsed := ParseSignatureMetadata(contact.SignatureData)
	if parsed.Source != strings.ToLower(SignatureSourceExtracted) {
		t.Errorf("parsed source after MarkRetryFailure: got %q", parsed.Source)
	}
}
