package search

import (
	"encoding/json"
	"strings"

	"gorm.io/gorm"
)

// SearchResult represents a single FTS5 search hit.
type SearchResult struct {
	JobID   string
	Rank    float64
	Snippet string
}

// FTSManager manages the FTS5 virtual table for full-text search across
// transcription jobs (title, content, summary).
type FTSManager struct {
	db *gorm.DB
}

// NewFTSManager creates a new FTS manager backed by the given database.
func NewFTSManager(db *gorm.DB) *FTSManager {
	return &FTSManager{db: db}
}

// EnsureTable creates the FTS5 virtual table if it does not already exist.
func (m *FTSManager) EnsureTable() error {
	return m.db.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS transcription_fts
		USING fts5(job_id, title, content, summary, tokenize='unicode61')
	`).Error
}

// Upsert inserts or replaces the FTS5 entry for a given job.
// This performs a DELETE+INSERT since FTS5 doesn't support UPDATE well.
func (m *FTSManager) Upsert(jobID, title, content, summary string) error {
	return m.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("DELETE FROM transcription_fts WHERE job_id = ?", jobID).Error; err != nil {
			return err
		}
		return tx.Exec(
			"INSERT INTO transcription_fts(job_id, title, content, summary) VALUES (?, ?, ?, ?)",
			jobID, title, content, summary,
		).Error
	})
}

// Delete removes the FTS5 entry for a given job.
func (m *FTSManager) Delete(jobID string) error {
	return m.db.Exec("DELETE FROM transcription_fts WHERE job_id = ?", jobID).Error
}

// Search performs a full-text search and returns results ranked by BM25
// with snippet highlighting. Returns up to `limit` results.
func (m *FTSManager) Search(query string, limit int) ([]SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}

	sanitized := sanitizeQuery(query)
	if sanitized == "" {
		return nil, nil
	}

	var results []SearchResult
	rows, err := m.db.Raw(`
		SELECT job_id, rank, snippet(transcription_fts, 2, '<b>', '</b>', '...', 32) as snippet
		FROM transcription_fts
		WHERE transcription_fts MATCH ?
		ORDER BY rank
		LIMIT ?
	`, sanitized, limit).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.JobID, &r.Rank, &r.Snippet); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// MatchingJobIDs returns job IDs that match the query, for integration with
// ListWithParams. No limit — the caller filters further.
func (m *FTSManager) MatchingJobIDs(query string) ([]string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}

	sanitized := sanitizeQuery(query)
	if sanitized == "" {
		return nil, nil
	}

	var ids []string
	err := m.db.Raw(
		"SELECT job_id FROM transcription_fts WHERE transcription_fts MATCH ? ORDER BY rank",
		sanitized,
	).Scan(&ids).Error
	return ids, err
}

// Rebuild drops all FTS5 data and re-indexes from the transcription_jobs table.
func (m *FTSManager) Rebuild() error {
	// Clear existing FTS data
	if err := m.db.Exec("DELETE FROM transcription_fts").Error; err != nil {
		return err
	}

	// Collect all rows into a slice first so the rows iterator (and its
	// underlying connection) is released before we call Upsert, which
	// opens a transaction requiring its own connection.
	type jobRow struct{ id, title, transcript, summary string }
	var jobs []jobRow

	rows, err := m.db.Raw(`
		SELECT id, COALESCE(title, ''), COALESCE(transcript, ''), COALESCE(summary, '')
		FROM transcription_jobs
		WHERE deleted_at IS NULL
	`).Rows()
	if err != nil {
		return err
	}
	for rows.Next() {
		var r jobRow
		if err := rows.Scan(&r.id, &r.title, &r.transcript, &r.summary); err != nil {
			rows.Close()
			return err
		}
		jobs = append(jobs, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, r := range jobs {
		content := extractPlainText(r.transcript)
		if err := m.Upsert(r.id, r.title, content, r.summary); err != nil {
			return err
		}
	}
	return nil
}

// sanitizeQuery strips FTS5 special syntax to prevent query errors.
// Converts user input into a safe implicit AND query.
func sanitizeQuery(q string) string {
	// Remove FTS5 operators and special characters
	replacer := strings.NewReplacer(
		"AND", "",
		"OR", "",
		"NOT", "",
		"NEAR", "",
		"(", "",
		")", "",
		":", " ",
		"\"", "",
		"*", "",
		"/", " ",
	)
	cleaned := replacer.Replace(q)

	// Split into words, filter empties
	words := strings.Fields(cleaned)
	if len(words) == 0 {
		return ""
	}

	// Quote each word and join with space (implicit AND in FTS5)
	var quoted []string
	for _, w := range words {
		// Skip very short tokens that are noise
		if len(w) < 1 {
			continue
		}
		quoted = append(quoted, `"`+w+`"`)
	}
	if len(quoted) == 0 {
		return ""
	}
	return strings.Join(quoted, " ")
}

// transcriptSegments is used to parse JSON transcript data.
type transcriptSegments struct {
	Segments []struct {
		Text string `json:"text"`
	} `json:"segments"`
}

// extractPlainText extracts searchable plain text from a transcript field.
// If the transcript is JSON with segments, it concatenates segment text.
// If it's already plain text, it returns it as-is.
func extractPlainText(transcript string) string {
	if transcript == "" {
		return ""
	}

	// Try to parse as JSON with segments
	var data transcriptSegments
	if err := json.Unmarshal([]byte(transcript), &data); err == nil && len(data.Segments) > 0 {
		var texts []string
		for _, seg := range data.Segments {
			t := strings.TrimSpace(seg.Text)
			if t != "" {
				texts = append(texts, t)
			}
		}
		return strings.Join(texts, " ")
	}

	// Not JSON — return raw text
	return transcript
}
