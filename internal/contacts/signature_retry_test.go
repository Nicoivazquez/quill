package contacts

import (
	"context"
	"slices"
	"testing"
	"time"

	"quill/internal/models"
	"quill/internal/repository"
)

type recordingEmbeddingRunner struct {
	ids []uint
}

func (r *recordingEmbeddingRunner) Start() {}

func (r *recordingEmbeddingRunner) Stop() {}

func (r *recordingEmbeddingRunner) Enqueue(contactID uint) {
	r.ids = append(r.ids, contactID)
}

func TestRetryState_RespectsStatusAndBackoff(t *testing.T) {
	now := time.Now().UTC()
	snippet := "Contacts/People/jane--123/voice-snippet.wav"
	signature := "Contacts/People/jane--123/voice-signature.embedding.json"

	failedDue := models.Contact{
		SignatureStatus:  "failed",
		VoiceSnippetPath: &snippet,
		UpdatedAt:        now.Add(-2 * time.Hour),
		SignatureData: SerializeSignatureMetadata(SignatureMetadata{
			Source:      SignatureSourceExtracted,
			RetryCount:  2,
			NextRetryAt: now.Add(-time.Minute).Format(time.RFC3339),
		}),
	}
	state, _, due := RetryState(&failedDue, now)
	if state != "failed" || !due {
		t.Fatalf("expected failed contact to be due, state=%q due=%v", state, due)
	}

	processingStale := models.Contact{
		SignatureStatus:  "processing",
		VoiceSnippetPath: &snippet,
		UpdatedAt:        now.Add(-time.Hour),
		SignatureData: SerializeSignatureMetadata(SignatureMetadata{
			Source:        SignatureSourceExtracted,
			LastAttemptAt: now.Add(-11 * time.Minute).Format(time.RFC3339),
		}),
	}
	state, _, due = RetryState(&processingStale, now)
	if state != "processing" || !due {
		t.Fatalf("expected stale processing contact to be due, state=%q due=%v", state, due)
	}

	processingFresh := models.Contact{
		SignatureStatus:  "processing",
		VoiceSnippetPath: &snippet,
		UpdatedAt:        now.Add(-time.Minute),
		SignatureData: SerializeSignatureMetadata(SignatureMetadata{
			Source:        SignatureSourceExtracted,
			LastAttemptAt: now.Add(-2 * time.Minute).Format(time.RFC3339),
		}),
	}
	if _, _, due = RetryState(&processingFresh, now); due {
		t.Fatalf("expected fresh processing contact to stay pending")
	}

	manual := models.Contact{
		SignatureStatus:        "failed",
		VoiceSnippetPath:       &snippet,
		SignatureEmbeddingPath: &signature,
		UpdatedAt:              now.Add(-24 * time.Hour),
		SignatureData: SerializeSignatureMetadata(SignatureMetadata{
			Source: SignatureSourceManual,
		}),
	}
	if _, _, due = RetryState(&manual, now); due {
		t.Fatalf("expected manual signature contact to be skipped")
	}
}

func TestManager_RetryDueEmbeddings_QueuesFailedAndStaleProcessing(t *testing.T) {
	ctx := context.Background()
	db, root := setupContactsDB(t)
	vault := createVault(t, db, root+"/vault", true)
	repo := repository.NewContactRepository(db)
	now := time.Now().UTC()

	snippet := "Contacts/People/test--123/voice-snippet.wav"
	signature := "Contacts/People/test--123/voice-signature.embedding.json"

	createContact := func(name string, status string, metadata SignatureMetadata, withManualSignature bool, updatedAt time.Time) models.Contact {
		contact := models.Contact{
			VaultID:          vault.ID,
			ContactUID:       name + "-uid",
			Slug:             name,
			Name:             name,
			NotePath:         "Contacts/People/" + name + "--uid/contact.md",
			VoiceSnippetPath: &snippet,
			SignatureStatus:  status,
			SignatureData:    SerializeSignatureMetadata(metadata),
			UpdatedAt:        updatedAt,
		}
		if withManualSignature {
			contact.SignatureEmbeddingPath = &signature
		}
		if err := repo.Create(ctx, &contact); err != nil {
			t.Fatalf("create contact %s: %v", name, err)
		}
		return contact
	}

	failedDue := createContact("failed-due", "failed", SignatureMetadata{
		Source:      SignatureSourceExtracted,
		RetryCount:  1,
		NextRetryAt: now.Add(-time.Minute).Format(time.RFC3339),
	}, false, now.Add(-time.Hour))
	createContact("failed-future", "failed", SignatureMetadata{
		Source:      SignatureSourceExtracted,
		RetryCount:  1,
		NextRetryAt: now.Add(10 * time.Minute).Format(time.RFC3339),
	}, false, now.Add(-time.Hour))
	processingStale := createContact("processing-stale", "processing", SignatureMetadata{
		Source:        SignatureSourceExtracted,
		LastAttemptAt: now.Add(-11 * time.Minute).Format(time.RFC3339),
	}, false, now.Add(-time.Hour))
	createContact("processing-fresh", "processing", SignatureMetadata{
		Source:        SignatureSourceExtracted,
		LastAttemptAt: now.Add(-2 * time.Minute).Format(time.RFC3339),
	}, false, now.Add(-time.Hour))
	createContact("manual-failed", "failed", SignatureMetadata{
		Source: SignatureSourceManual,
	}, true, now.Add(-24*time.Hour))

	runner := &recordingEmbeddingRunner{}
	manager := &Manager{
		db:              db,
		repo:            repo,
		embeddingWorker: runner,
		workerStarted:   true,
		activeVaultID:   vault.ID,
		activePath:      vault.Path,
	}

	result, err := manager.RetryDueEmbeddings(ctx)
	if err != nil {
		t.Fatalf("retry due embeddings: %v", err)
	}
	if result.Queued != 2 {
		t.Fatalf("expected 2 queued contacts, got %+v", result)
	}
	if result.FailedDue != 1 {
		t.Fatalf("expected 1 failed retry, got %+v", result)
	}
	if result.StaleProcessing != 1 {
		t.Fatalf("expected 1 stale processing retry, got %+v", result)
	}

	if !slices.Contains(runner.ids, failedDue.ID) {
		t.Fatalf("expected failed due contact %d to be requeued, got %v", failedDue.ID, runner.ids)
	}
	if !slices.Contains(runner.ids, processingStale.ID) {
		t.Fatalf("expected stale processing contact %d to be requeued, got %v", processingStale.ID, runner.ids)
	}
}
