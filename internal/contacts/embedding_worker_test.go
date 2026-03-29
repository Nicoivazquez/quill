package contacts

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"quill/internal/models"
	"quill/internal/repository"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// setupEmbeddingWorkerDB creates a file-based SQLite database migrated with all
// tables required by the EmbeddingWorker under test.
//
// A file-based database is used deliberately: the EmbeddingWorker processes
// jobs in a background goroutine, and the glebarez/sqlite `:memory:` driver
// allocates a separate in-memory database per connection, so a worker goroutine
// acquiring a new connection from the pool would see an empty schema. A named
// temp file avoids this issue and mirrors the production setup.
func setupEmbeddingWorkerDB(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	root := t.TempDir()
	dbPath := filepath.Join(root, "embedding-worker-test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&models.Vault{}, &models.Contact{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	return db, root
}

// createVaultAndContact inserts a Vault row with the given path and a Contact
// row linked to it, returning both. The vault directory is created on disk.
func createVaultAndContact(
	t *testing.T,
	db *gorm.DB,
	vaultPath string,
	snippetPath *string,
) (models.Vault, models.Contact) {
	t.Helper()

	if err := os.MkdirAll(vaultPath, 0o755); err != nil {
		t.Fatalf("create vault dir: %v", err)
	}

	vault := models.Vault{
		Name:     "Test Vault",
		Path:     vaultPath,
		IsActive: true,
	}
	if err := db.Create(&vault).Error; err != nil {
		t.Fatalf("create vault: %v", err)
	}

	contact := models.Contact{
		VaultID:          vault.ID,
		ContactUID:       uuid.NewString(),
		Slug:             "test-contact",
		Name:             "Test Contact",
		SignatureStatus:  "none",
		VoiceSnippetPath: snippetPath,
	}
	if err := db.Create(&contact).Error; err != nil {
		t.Fatalf("create contact: %v", err)
	}

	return vault, contact
}

// startWorker starts an EmbeddingWorker and registers a t.Cleanup to Stop it.
func startWorker(t *testing.T, db *gorm.DB, repo repository.ContactRepository, whisperXEnv string) *EmbeddingWorker {
	t.Helper()
	w := NewEmbeddingWorker(db, repo, whisperXEnv)
	w.Start()
	t.Cleanup(func() { w.Stop() })
	return w
}

// waitForContactStatus polls the repository until the contact's SignatureStatus
// matches expected or the deadline elapses. It reports a fatal error on timeout.
func waitForContactStatus(
	t *testing.T,
	repo repository.ContactRepository,
	contactID uint,
	expected string,
	timeout time.Duration,
) *models.Contact {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err := repo.GetByID(ctx, contactID)
		if err == nil && c.SignatureStatus == expected {
			return c
		}
		time.Sleep(20 * time.Millisecond)
	}
	c, err := repo.GetByID(ctx, contactID)
	if err != nil {
		t.Fatalf("timed out waiting for status=%q; GetByID error: %v", expected, err)
	}
	t.Fatalf("timed out waiting for status=%q; current status=%q", expected, c.SignatureStatus)
	return nil
}

// ── Constructor ─────────────────────────────────────────────────────────────

func TestNewEmbeddingWorker_InitializesFields(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	repo := repository.NewContactRepository(db)
	w := NewEmbeddingWorker(db, repo, root+"/whisperx")

	if w.db == nil {
		t.Error("db field must not be nil")
	}
	if w.repo == nil {
		t.Error("repo field must not be nil")
	}
	if w.whisperXEnv == "" {
		t.Error("whisperXEnv must be stored")
	}
	if w.jobs == nil {
		t.Error("jobs channel must be initialized")
	}
	if w.stop == nil {
		t.Error("stop channel must be initialized")
	}
	if cap(w.jobs) != 128 {
		t.Errorf("jobs channel capacity: got %d, want 128", cap(w.jobs))
	}
}

// ── Lifecycle: Start / Stop ──────────────────────────────────────────────────

func TestEmbeddingWorker_StartStop_Idempotent(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	repo := repository.NewContactRepository(db)
	w := NewEmbeddingWorker(db, repo, root+"/whisperx")

	// Start and Stop must complete without deadlock or panic.
	w.Start()
	w.Stop()
}

func TestEmbeddingWorker_StopDrainsGracefully(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	repo := repository.NewContactRepository(db)
	w := NewEmbeddingWorker(db, repo, root+"/whisperx")

	w.Start()

	// Stop must return within a short deadline with nothing in the queue.
	done := make(chan struct{})
	go func() {
		w.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop() did not return within 3 seconds")
	}
}

// ── Enqueue ──────────────────────────────────────────────────────────────────

func TestEmbeddingWorker_Enqueue_SendsToChannel(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	repo := repository.NewContactRepository(db)

	// Do NOT call Start so the loop goroutine doesn't consume items.
	w := NewEmbeddingWorker(db, repo, root+"/whisperx")

	w.Enqueue(42)

	select {
	case id := <-w.jobs:
		if id != 42 {
			t.Errorf("expected contact id=42, got %d", id)
		}
	default:
		t.Fatal("expected job to be present in channel")
	}
}

func TestEmbeddingWorker_Enqueue_DropsSilentlyWhenFull(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	repo := repository.NewContactRepository(db)
	w := NewEmbeddingWorker(db, repo, root+"/whisperx")

	// Fill the queue to capacity.
	for i := 0; i < 128; i++ {
		w.Enqueue(uint(i))
	}

	// One more enqueue must not block or panic; it is silently dropped.
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Enqueue(9999) // must not block
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Enqueue() blocked on a full queue")
	}

	if len(w.jobs) != 128 {
		t.Errorf("expected queue length 128 after overflow, got %d", len(w.jobs))
	}
}

func TestEmbeddingWorker_Enqueue_MultipleIDs_OrderPreserved(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	repo := repository.NewContactRepository(db)
	w := NewEmbeddingWorker(db, repo, root+"/whisperx")

	ids := []uint{10, 20, 30, 40}
	for _, id := range ids {
		w.Enqueue(id)
	}

	for _, expected := range ids {
		select {
		case got := <-w.jobs:
			if got != expected {
				t.Errorf("order mismatch: got %d, want %d", got, expected)
			}
		default:
			t.Fatalf("expected id %d in channel but it was absent", expected)
		}
	}
}

// ── SetOnContactReady callback ────────────────────────────────────────────────

func TestEmbeddingWorker_SetOnContactReady_StoresCallback(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	repo := repository.NewContactRepository(db)
	w := NewEmbeddingWorker(db, repo, root+"/whisperx")

	if w.onContactReady != nil {
		t.Error("onContactReady should be nil before SetOnContactReady")
	}

	called := false
	w.SetOnContactReady(func(_ uint) { called = true })

	if w.onContactReady == nil {
		t.Error("onContactReady should be set after SetOnContactReady")
	}

	// Sanity-check the stored function executes.
	w.onContactReady(1)
	if !called {
		t.Error("stored callback was not invoked")
	}
}

func TestEmbeddingWorker_SetOnContactReady_CanBeOverwritten(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	repo := repository.NewContactRepository(db)
	w := NewEmbeddingWorker(db, repo, root+"/whisperx")

	var seen []uint
	w.SetOnContactReady(func(id uint) { seen = append(seen, id) })
	w.SetOnContactReady(func(id uint) { seen = append(seen, id+100) })

	w.onContactReady(5)

	if len(seen) != 1 || seen[0] != 105 {
		t.Errorf("expected second callback to win, got %v", seen)
	}
}

// ── markFailed state mutations ────────────────────────────────────────────────

func TestMarkFailed_SetsStatusAndError(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	vaultPath := filepath.Join(root, "vault")
	repo := repository.NewContactRepository(db)

	_, contact := createVaultAndContact(t, db, vaultPath, nil)

	w := NewEmbeddingWorker(db, repo, root+"/whisperx")

	ctx := context.Background()
	w.markFailed(ctx, &contact, "  test error message  ")

	if contact.SignatureStatus != "failed" {
		t.Errorf("SignatureStatus: got %q, want %q", contact.SignatureStatus, "failed")
	}
	if contact.SyncError == nil {
		t.Fatal("SyncError must not be nil after markFailed")
	}
	if *contact.SyncError != "test error message" {
		t.Errorf("SyncError: got %q, want %q", *contact.SyncError, "test error message")
	}
}

func TestMarkFailed_PopulatesSignatureData(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	vaultPath := filepath.Join(root, "vault")
	repo := repository.NewContactRepository(db)

	_, contact := createVaultAndContact(t, db, vaultPath, nil)

	w := NewEmbeddingWorker(db, repo, root+"/whisperx")
	ctx := context.Background()
	w.markFailed(ctx, &contact, "embedding extraction failed")

	if contact.SignatureData == nil {
		t.Fatal("SignatureData must be set after markFailed")
	}
	metadata := ParseSignatureMetadata(contact.SignatureData)
	if metadata.LastError == "" {
		t.Error("SignatureData.LastError must not be empty")
	}
	if metadata.NextRetryAt == "" {
		t.Error("SignatureData.NextRetryAt must not be empty")
	}
	if metadata.Source != SignatureSourceExtracted {
		t.Errorf("SignatureData.Source: got %q, want %q", metadata.Source, SignatureSourceExtracted)
	}
}

func TestMarkFailed_TrimsWhitespaceFromMessage(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	vaultPath := filepath.Join(root, "vault")
	repo := repository.NewContactRepository(db)

	_, contact := createVaultAndContact(t, db, vaultPath, nil)

	w := NewEmbeddingWorker(db, repo, root+"/whisperx")
	ctx := context.Background()
	w.markFailed(ctx, &contact, "   spaces around   ")

	if contact.SyncError == nil || *contact.SyncError != "spaces around" {
		t.Errorf("expected trimmed error, got %v", contact.SyncError)
	}
}

func TestMarkFailed_PersistsToDatabase(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	vaultPath := filepath.Join(root, "vault")
	repo := repository.NewContactRepository(db)

	_, contact := createVaultAndContact(t, db, vaultPath, nil)

	w := NewEmbeddingWorker(db, repo, root+"/whisperx")
	ctx := context.Background()
	w.markFailed(ctx, &contact, "some failure")

	// Reload from DB and verify.
	reloaded, err := repo.GetByID(ctx, contact.ID)
	if err != nil {
		t.Fatalf("reload contact: %v", err)
	}
	if reloaded.SignatureStatus != "failed" {
		t.Errorf("DB SignatureStatus: got %q, want %q", reloaded.SignatureStatus, "failed")
	}
	if reloaded.SyncError == nil || *reloaded.SyncError != "some failure" {
		t.Errorf("DB SyncError: got %v, want %q", reloaded.SyncError, "some failure")
	}
}

func TestMarkFailed_EmptyMessage(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	vaultPath := filepath.Join(root, "vault")
	repo := repository.NewContactRepository(db)

	_, contact := createVaultAndContact(t, db, vaultPath, nil)

	w := NewEmbeddingWorker(db, repo, root+"/whisperx")
	ctx := context.Background()
	w.markFailed(ctx, &contact, "")

	if contact.SignatureStatus != "failed" {
		t.Errorf("expected status=failed even for empty message, got %q", contact.SignatureStatus)
	}
	if contact.SyncError == nil {
		t.Fatal("SyncError must not be nil")
	}
	if *contact.SyncError != "" {
		t.Errorf("expected empty SyncError string, got %q", *contact.SyncError)
	}
}

// ── process: failure paths without external binaries ─────────────────────────

func TestProcess_ContactNotFound_ReturnsSilently(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	repo := repository.NewContactRepository(db)
	w := NewEmbeddingWorker(db, repo, root+"/whisperx")

	// process must not panic when the contact ID does not exist.
	w.process(99999)
}

func TestProcess_VaultNotFound_MarksContactFailed(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	repo := repository.NewContactRepository(db)
	ctx := context.Background()

	// Create a contact with a vault_id that references a non-existent vault.
	snippetPath := "Contacts/People/test--uid/voice-snippet.wav"
	contact := models.Contact{
		VaultID:          9999, // does not exist in vaults table
		ContactUID:       uuid.NewString(),
		Slug:             "orphan",
		Name:             "Orphan Contact",
		SignatureStatus:  "none",
		VoiceSnippetPath: &snippetPath,
	}
	if err := db.Create(&contact).Error; err != nil {
		t.Fatalf("create contact: %v", err)
	}

	w := NewEmbeddingWorker(db, repo, root+"/whisperx")
	w.process(contact.ID)

	reloaded, err := repo.GetByID(ctx, contact.ID)
	if err != nil {
		t.Fatalf("reload contact: %v", err)
	}
	if reloaded.SignatureStatus != "failed" {
		t.Errorf("expected status=failed, got %q", reloaded.SignatureStatus)
	}
	if reloaded.SyncError == nil || !strings.Contains(*reloaded.SyncError, "vault") {
		t.Errorf("expected error about vault, got %v", reloaded.SyncError)
	}
}

func TestProcess_MissingVoiceSnippet_MarksContactFailed(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	vaultPath := filepath.Join(root, "vault")
	repo := repository.NewContactRepository(db)
	ctx := context.Background()

	// Contact with nil VoiceSnippetPath.
	_, contact := createVaultAndContact(t, db, vaultPath, nil)

	w := NewEmbeddingWorker(db, repo, root+"/whisperx")
	w.process(contact.ID)

	reloaded, err := repo.GetByID(ctx, contact.ID)
	if err != nil {
		t.Fatalf("reload contact: %v", err)
	}
	if reloaded.SignatureStatus != "failed" {
		t.Errorf("expected status=failed, got %q", reloaded.SignatureStatus)
	}
	if reloaded.SyncError == nil || !strings.Contains(*reloaded.SyncError, "snippet") {
		t.Errorf("expected error containing 'snippet', got %v", reloaded.SyncError)
	}
}

func TestProcess_BlankVoiceSnippetPath_MarksContactFailed(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	vaultPath := filepath.Join(root, "vault")
	repo := repository.NewContactRepository(db)
	ctx := context.Background()

	blank := "   "
	_, contact := createVaultAndContact(t, db, vaultPath, &blank)

	w := NewEmbeddingWorker(db, repo, root+"/whisperx")
	w.process(contact.ID)

	reloaded, err := repo.GetByID(ctx, contact.ID)
	if err != nil {
		t.Fatalf("reload contact: %v", err)
	}
	if reloaded.SignatureStatus != "failed" {
		t.Errorf("expected status=failed for blank snippet, got %q", reloaded.SignatureStatus)
	}
}

func TestProcess_InaccessibleSnippetFile_MarksContactFailed(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	vaultPath := filepath.Join(root, "vault")
	repo := repository.NewContactRepository(db)
	ctx := context.Background()

	// Voice snippet path points to a file that does not exist on disk.
	missing := "Contacts/People/test--uid/nonexistent-snippet.wav"
	_, contact := createVaultAndContact(t, db, vaultPath, &missing)

	w := NewEmbeddingWorker(db, repo, root+"/whisperx")
	w.process(contact.ID)

	reloaded, err := repo.GetByID(ctx, contact.ID)
	if err != nil {
		t.Fatalf("reload contact: %v", err)
	}
	if reloaded.SignatureStatus != "failed" {
		t.Errorf("expected status=failed for missing snippet file, got %q", reloaded.SignatureStatus)
	}
	if reloaded.SyncError == nil || !strings.Contains(*reloaded.SyncError, "not accessible") {
		t.Errorf("expected error about accessibility, got %v", reloaded.SyncError)
	}
}

func TestProcess_SetsStatusAwayFromNone(t *testing.T) {
	// Verify that process() always moves the status away from "none" regardless of
	// which failure path is taken. This uses a contact with a missing vault so
	// the method short-circuits quickly and never calls an external binary.
	db, root := setupEmbeddingWorkerDB(t)
	repo := repository.NewContactRepository(db)
	ctx := context.Background()

	snippetPath := "Contacts/People/test--uid/voice-snippet.wav"
	contact := models.Contact{
		VaultID:          9998, // non-existent vault → fast failure path
		ContactUID:       uuid.NewString(),
		Slug:             "status-probe",
		Name:             "Status Probe",
		NotePath:         "Contacts/People/test--uid/contact.md",
		SignatureStatus:  "none",
		VoiceSnippetPath: &snippetPath,
	}
	if err := db.Create(&contact).Error; err != nil {
		t.Fatalf("create contact: %v", err)
	}

	w := NewEmbeddingWorker(db, repo, root+"/whisperx")
	w.process(contact.ID)

	reloaded, err := repo.GetByID(ctx, contact.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.SignatureStatus == "none" {
		t.Errorf("status must not remain 'none' after process(); got %q", reloaded.SignatureStatus)
	}
}

func TestProcess_SignatureDataRecordsRetryMetadata(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	vaultPath := filepath.Join(root, "vault")
	repo := repository.NewContactRepository(db)
	ctx := context.Background()

	// A contact without a voice snippet reaches markFailed quickly.
	_, contact := createVaultAndContact(t, db, vaultPath, nil)

	w := NewEmbeddingWorker(db, repo, root+"/whisperx")
	w.process(contact.ID)

	reloaded, err := repo.GetByID(ctx, contact.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.SignatureData == nil {
		t.Fatal("SignatureData must be populated after process()")
	}
	metadata := ParseSignatureMetadata(reloaded.SignatureData)
	if metadata.RetryCount < 1 {
		t.Errorf("expected RetryCount >= 1 after first process attempt, got %d", metadata.RetryCount)
	}
}

// ── End-to-end queue processing via Start/Stop ────────────────────────────────

func TestEmbeddingWorker_Loop_ProcessesContactFromQueue(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	vaultPath := filepath.Join(root, "vault")
	repo := repository.NewContactRepository(db)

	// Contact with nil snippet triggers markFailed through the full worker loop.
	_, contact := createVaultAndContact(t, db, vaultPath, nil)

	w := startWorker(t, db, repo, root+"/whisperx")
	w.Enqueue(contact.ID)

	// The loop should process the job and set the status to "failed" (no snippet).
	waitForContactStatus(t, repo, contact.ID, "failed", 5*time.Second)
}

func TestEmbeddingWorker_Loop_ProcessesMultipleJobsSequentially(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	vaultPath := filepath.Join(root, "vault")
	repo := repository.NewContactRepository(db)

	// Create three contacts each without snippets; each will fail quickly.
	contacts := make([]models.Contact, 3)
	for i := range contacts {
		_, contacts[i] = createVaultAndContact(t, db, filepath.Join(vaultPath, "v"+string(rune('a'+i))), nil)
	}

	w := startWorker(t, db, repo, root+"/whisperx")
	for _, c := range contacts {
		w.Enqueue(c.ID)
	}

	// All three must end up in "failed" state within the timeout.
	for _, c := range contacts {
		waitForContactStatus(t, repo, c.ID, "failed", 10*time.Second)
	}
}

func TestEmbeddingWorker_Loop_NonExistentContactDoesNotCrash(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	repo := repository.NewContactRepository(db)

	w := startWorker(t, db, repo, root+"/whisperx")
	// Enqueue a contact ID that does not exist; the worker must not crash.
	w.Enqueue(999999)

	// Give the loop time to consume the job. Absence of a crash is the assertion.
	time.Sleep(200 * time.Millisecond)
}

// ── onContactReady callback via queue ────────────────────────────────────────

func TestEmbeddingWorker_OnContactReady_NotCalledOnFailure(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	vaultPath := filepath.Join(root, "vault")
	repo := repository.NewContactRepository(db)

	_, contact := createVaultAndContact(t, db, vaultPath, nil) // no snippet → fails

	var callbackFired atomic.Bool
	w := NewEmbeddingWorker(db, repo, root+"/whisperx")
	w.SetOnContactReady(func(_ uint) { callbackFired.Store(true) })
	w.Start()
	t.Cleanup(func() { w.Stop() })

	w.Enqueue(contact.ID)
	waitForContactStatus(t, repo, contact.ID, "failed", 5*time.Second)

	// Allow a brief settle window for any spurious goroutine.
	time.Sleep(50 * time.Millisecond)

	if callbackFired.Load() {
		t.Error("onContactReady must not be called when processing fails")
	}
}

// ── Concurrent safety ────────────────────────────────────────────────────────

func TestEmbeddingWorker_ConcurrentEnqueue_NoPanic(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	repo := repository.NewContactRepository(db)
	w := NewEmbeddingWorker(db, repo, root+"/whisperx")
	w.Start()
	t.Cleanup(func() { w.Stop() })

	var wg sync.WaitGroup
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				w.Enqueue(uint(base*10 + i))
			}
		}(g)
	}
	wg.Wait()
	// Success is no panic or race detected.
}

func TestEmbeddingWorker_ConcurrentStartStop_NoPanic(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	repo := repository.NewContactRepository(db)

	// Launching Start then Stop from multiple goroutines is not a typical usage,
	// but calling them once each from different goroutines exercises the channel.
	for range 5 {
		w := NewEmbeddingWorker(db, repo, root+"/whisperx")
		done := make(chan struct{})
		go func() {
			defer close(done)
			w.Start()
			w.Stop()
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("Start/Stop pair did not complete within 3 seconds")
		}
	}
}

// ── Queue capacity boundary ───────────────────────────────────────────────────

func TestEmbeddingWorker_QueueCapacity_ExactlyFull(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	repo := repository.NewContactRepository(db)
	w := NewEmbeddingWorker(db, repo, root+"/whisperx")

	// Fill exactly to capacity — none should be dropped.
	for i := uint(0); i < 128; i++ {
		w.Enqueue(i)
	}

	if len(w.jobs) != 128 {
		t.Errorf("expected all 128 jobs enqueued, got %d", len(w.jobs))
	}
}

func TestEmbeddingWorker_QueueCapacity_OverflowDropsExtras(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	repo := repository.NewContactRepository(db)
	w := NewEmbeddingWorker(db, repo, root+"/whisperx")

	// First 128 fill the buffer; the next 10 should be silently dropped.
	for i := uint(0); i < 138; i++ {
		w.Enqueue(i)
	}

	if len(w.jobs) != 128 {
		t.Errorf("expected queue to remain at 128 after overflow, got %d", len(w.jobs))
	}
}

// ── Stop waits for in-flight job ──────────────────────────────────────────────

func TestEmbeddingWorker_StopWaitsForCurrentJob(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	vaultPath := filepath.Join(root, "vault")
	repo := repository.NewContactRepository(db)

	_, contact := createVaultAndContact(t, db, vaultPath, nil) // fails quickly

	w := NewEmbeddingWorker(db, repo, root+"/whisperx")
	w.Start()

	w.Enqueue(contact.ID)

	// Stop before the job might be consumed; it should still drain cleanly.
	stopDone := make(chan struct{})
	go func() {
		w.Stop()
		close(stopDone)
	}()

	select {
	case <-stopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() blocked waiting for in-flight job beyond 5 seconds")
	}
}

// ── whisperXEnv stored correctly ──────────────────────────────────────────────

func TestNewEmbeddingWorker_WhisperXEnvStored(t *testing.T) {
	db, _ := setupEmbeddingWorkerDB(t)
	repo := repository.NewContactRepository(db)
	env := "/custom/path/to/whisperx"
	w := NewEmbeddingWorker(db, repo, env)
	if w.whisperXEnv != env {
		t.Errorf("whisperXEnv: got %q, want %q", w.whisperXEnv, env)
	}
}

// ── markFailed: successful vault file write path ──────────────────────────────

func TestMarkFailed_WithValidVault_WritesMarkdownAndPersists(t *testing.T) {
	// This test exercises the vault-found branch inside markFailed so that the
	// file write path is reached. The contact must have a valid NotePath so that
	// WriteContact can render the markdown.
	db, root := setupEmbeddingWorkerDB(t)
	vaultPath := filepath.Join(root, "vault")
	repo := repository.NewContactRepository(db)

	vault, contact := createVaultAndContact(t, db, vaultPath, nil)

	// Give the contact a proper NotePath so WriteContact succeeds.
	uid := uuid.NewString()
	slug := "mark-failed-valid"
	contact.ContactUID = uid
	contact.Slug = slug
	contact.NotePath = "Contacts/People/" + slug + "--" + uid + "/contact.md"
	if err := repo.Update(context.Background(), &contact); err != nil {
		t.Fatalf("update contact: %v", err)
	}

	w := NewEmbeddingWorker(db, repo, root+"/whisperx")
	ctx := context.Background()
	w.markFailed(ctx, &contact, "deliberate test failure")

	// Verify the markdown was written on disk.
	noteAbs := filepath.Join(vault.Path, "Contacts", "People", slug+"--"+uid, "contact.md")
	if _, err := os.Stat(noteAbs); err != nil {
		t.Errorf("expected contact markdown at %s: %v", noteAbs, err)
	}

	reloaded, err := repo.GetByID(ctx, contact.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.SignatureStatus != "failed" {
		t.Errorf("expected status=failed, got %q", reloaded.SignatureStatus)
	}
}

// ── markFailed: vault lookup failure is non-fatal to DB update ───────────────

func TestMarkFailed_MissingVault_StillPersistsToDatabase(t *testing.T) {
	db, root := setupEmbeddingWorkerDB(t)
	repo := repository.NewContactRepository(db)
	ctx := context.Background()

	// Contact linked to non-existent vault — file write will fail, but DB must
	// still be updated.
	snippetPath := "Contacts/People/x--uid/voice-snippet.wav"
	contact := models.Contact{
		VaultID:          9999,
		ContactUID:       uuid.NewString(),
		Slug:             "no-vault",
		Name:             "No Vault",
		SignatureStatus:  "none",
		VoiceSnippetPath: &snippetPath,
	}
	if err := db.Create(&contact).Error; err != nil {
		t.Fatalf("create contact: %v", err)
	}

	w := NewEmbeddingWorker(db, repo, root+"/whisperx")
	w.markFailed(ctx, &contact, "deliberate test failure")

	reloaded, err := repo.GetByID(ctx, contact.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.SignatureStatus != "failed" {
		t.Errorf("expected status=failed in DB, got %q", reloaded.SignatureStatus)
	}
}
