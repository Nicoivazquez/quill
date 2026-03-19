package search

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// In-memory SQLite gives each connection its own database.
	// Force single connection so transactions see the same schema.
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	return db
}

func TestEnsureTable_CreatesVirtualTable(t *testing.T) {
	db := setupTestDB(t)
	mgr := NewFTSManager(db)

	if err := mgr.EnsureTable(); err != nil {
		t.Fatalf("EnsureTable: %v", err)
	}

	// Verify the virtual table exists by inserting and querying
	err := db.Exec("INSERT INTO transcription_fts(job_id, title, content, summary) VALUES (?, ?, ?, ?)",
		"test-1", "Hello World", "some content", "a summary").Error
	if err != nil {
		t.Fatalf("insert into fts table failed: %v", err)
	}

	var count int64
	db.Raw("SELECT COUNT(*) FROM transcription_fts").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}
}

func TestEnsureTable_Idempotent(t *testing.T) {
	db := setupTestDB(t)
	mgr := NewFTSManager(db)

	if err := mgr.EnsureTable(); err != nil {
		t.Fatalf("first EnsureTable: %v", err)
	}
	if err := mgr.EnsureTable(); err != nil {
		t.Fatalf("second EnsureTable should be idempotent: %v", err)
	}
}

func TestUpsert_InsertAndUpdate(t *testing.T) {
	db := setupTestDB(t)
	mgr := NewFTSManager(db)
	mgr.EnsureTable()

	// Insert
	if err := mgr.Upsert("job-1", "Meeting Notes", "discussed quarterly goals", "summary of Q4 goals"); err != nil {
		t.Fatalf("Upsert insert: %v", err)
	}

	results, err := mgr.Search("quarterly", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].JobID != "job-1" {
		t.Errorf("expected job-1, got %s", results[0].JobID)
	}

	// Update same job_id with different content
	if err := mgr.Upsert("job-1", "Updated Notes", "discussed annual review", "summary of annual review"); err != nil {
		t.Fatalf("Upsert update: %v", err)
	}

	// Old content should not match
	results, err = mgr.Search("quarterly", 10)
	if err != nil {
		t.Fatalf("Search after update: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for old content, got %d", len(results))
	}

	// New content should match
	results, err = mgr.Search("annual", 10)
	if err != nil {
		t.Fatalf("Search new content: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestSearch_BM25Ranking(t *testing.T) {
	db := setupTestDB(t)
	mgr := NewFTSManager(db)
	mgr.EnsureTable()

	// Insert multiple documents — one with "budget" appearing many times, one with once
	mgr.Upsert("job-sparse", "Project Update", "we briefly mentioned the budget", "")
	mgr.Upsert("job-dense", "Budget Review", "budget analysis budget forecast budget planning budget allocation", "budget summary")

	results, err := mgr.Search("budget", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}
	// The denser document should rank first
	if results[0].JobID != "job-dense" {
		t.Errorf("expected job-dense to rank first, got %s", results[0].JobID)
	}
}

func TestSearch_MatchesTitle(t *testing.T) {
	db := setupTestDB(t)
	mgr := NewFTSManager(db)
	mgr.EnsureTable()

	mgr.Upsert("job-t", "Kubernetes Deployment Guide", "some unrelated content", "")

	results, err := mgr.Search("kubernetes", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].JobID != "job-t" {
		t.Errorf("expected match on title, got %v", results)
	}
}

func TestSearch_MatchesSummary(t *testing.T) {
	db := setupTestDB(t)
	mgr := NewFTSManager(db)
	mgr.EnsureTable()

	mgr.Upsert("job-s", "Generic Title", "generic content", "discussion about microservices architecture")

	results, err := mgr.Search("microservices", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].JobID != "job-s" {
		t.Errorf("expected match on summary, got %v", results)
	}
}

func TestSearch_Snippets(t *testing.T) {
	db := setupTestDB(t)
	mgr := NewFTSManager(db)
	mgr.EnsureTable()

	mgr.Upsert("job-snip", "Test", "The quick brown fox jumps over the lazy dog and the fox ran away", "")

	results, err := mgr.Search("fox", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Snippet == "" {
		t.Error("expected non-empty snippet")
	}
}

func TestSearch_EmptyQuery(t *testing.T) {
	db := setupTestDB(t)
	mgr := NewFTSManager(db)
	mgr.EnsureTable()

	mgr.Upsert("job-1", "Title", "content", "summary")

	results, err := mgr.Search("", 10)
	if err != nil {
		t.Fatalf("Search empty: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty query, got %d", len(results))
	}
}

func TestSearch_LimitRespected(t *testing.T) {
	db := setupTestDB(t)
	mgr := NewFTSManager(db)
	mgr.EnsureTable()

	for i := 0; i < 20; i++ {
		mgr.Upsert(
			fmt.Sprintf("job-%d", i),
			"Common Topic Discussion",
			"the common keyword appears here",
			"",
		)
	}

	results, err := mgr.Search("common", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 5 {
		t.Errorf("expected 5 results (limit), got %d", len(results))
	}
}

func TestDelete_RemovesFromIndex(t *testing.T) {
	db := setupTestDB(t)
	mgr := NewFTSManager(db)
	mgr.EnsureTable()

	mgr.Upsert("job-del", "Deletable Document", "this should be removed", "")

	results, _ := mgr.Search("deletable", 10)
	if len(results) != 1 {
		t.Fatal("precondition: expected 1 result before delete")
	}

	if err := mgr.Delete("job-del"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	results, _ = mgr.Search("deletable", 10)
	if len(results) != 0 {
		t.Errorf("expected 0 results after delete, got %d", len(results))
	}
}

func TestMatchingJobIDs(t *testing.T) {
	db := setupTestDB(t)
	mgr := NewFTSManager(db)
	mgr.EnsureTable()

	mgr.Upsert("id-1", "Alpha Test", "alpha content", "")
	mgr.Upsert("id-2", "Beta Test", "beta content", "")
	mgr.Upsert("id-3", "Alpha Beta", "alpha beta content", "")

	ids, err := mgr.MatchingJobIDs("alpha")
	if err != nil {
		t.Fatalf("MatchingJobIDs: %v", err)
	}

	idSet := make(map[string]bool)
	for _, id := range ids {
		idSet[id] = true
	}

	if !idSet["id-1"] || !idSet["id-3"] {
		t.Errorf("expected id-1 and id-3, got %v", ids)
	}
	if idSet["id-2"] {
		t.Errorf("id-2 should not match 'alpha'")
	}
}

func TestSearch_SanitizesSpecialCharacters(t *testing.T) {
	db := setupTestDB(t)
	mgr := NewFTSManager(db)
	mgr.EnsureTable()

	mgr.Upsert("job-1", "Normal Title", "normal content", "")

	// These should not cause FTS5 syntax errors
	specialQueries := []string{
		`hello "world`,     // unbalanced quote
		"test AND OR NOT",  // FTS operators
		"col:value",        // column filter syntax
		"hello*",           // prefix that could fail
		"(unmatched paren", // unbalanced parenthesis
		`"`,                // single quote
		"NEAR/3 test",      // NEAR operator
	}

	for _, q := range specialQueries {
		_, err := mgr.Search(q, 10)
		if err != nil {
			t.Errorf("Search(%q) should not error, got: %v", q, err)
		}
	}
}

func TestExtractPlainText_JSON(t *testing.T) {
	// Transcript JSON with segments containing text
	transcript := `{"segments":[{"text":"Hello world.","start":0,"end":1.5},{"text":"How are you?","start":1.5,"end":3.0}]}`

	text := extractPlainText(transcript)

	if text == "" {
		t.Fatal("expected non-empty text")
	}
	if !contains(text, "Hello world") {
		t.Errorf("expected 'Hello world' in extracted text, got: %s", text)
	}
	if !contains(text, "How are you") {
		t.Errorf("expected 'How are you' in extracted text, got: %s", text)
	}
}

func TestExtractPlainText_PlainString(t *testing.T) {
	// If transcript is already plain text (not JSON), return as-is
	text := extractPlainText("This is just plain text transcript content")

	if text != "This is just plain text transcript content" {
		t.Errorf("expected plain text passthrough, got: %s", text)
	}
}

func TestExtractPlainText_Empty(t *testing.T) {
	text := extractPlainText("")
	if text != "" {
		t.Errorf("expected empty string, got: %s", text)
	}
}

func TestRebuild(t *testing.T) {
	db := setupTestDB(t)

	// Create a minimal transcription_jobs table to simulate the real table
	db.Exec(`CREATE TABLE IF NOT EXISTS transcription_jobs (
		id TEXT PRIMARY KEY,
		title TEXT,
		transcript TEXT,
		summary TEXT,
		deleted_at DATETIME
	)`)
	db.Exec(`INSERT INTO transcription_jobs (id, title, transcript, summary) VALUES (?, ?, ?, ?)`,
		"rebuild-1", "Rebuild Test", "some transcript content for rebuild", "rebuild summary")
	db.Exec(`INSERT INTO transcription_jobs (id, title, transcript, summary) VALUES (?, ?, ?, ?)`,
		"rebuild-2", "Another Test", "more transcript content", "another summary")
	// Soft-deleted row should be skipped
	db.Exec(`INSERT INTO transcription_jobs (id, title, transcript, summary, deleted_at) VALUES (?, ?, ?, ?, datetime('now'))`,
		"rebuild-3", "Deleted Test", "deleted content", "deleted summary")

	mgr := NewFTSManager(db)
	mgr.EnsureTable()

	if err := mgr.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	results, err := mgr.Search("rebuild", 10)
	if err != nil {
		t.Fatalf("Search after rebuild: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results (non-deleted), got %d", len(results))
	}
}

// helper
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
