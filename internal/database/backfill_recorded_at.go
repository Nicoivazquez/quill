package database

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"quill/internal/models"
	"quill/pkg/binaries"

	"gorm.io/gorm"
)

// backfillRecordedAt populates recorded_at for existing transcription jobs
// where the field is NULL. It extracts the recording date from the audio file
// metadata (ffprobe), filename patterns, or file modification time.
// This is idempotent — already-populated rows are skipped.
//
// Note: this duplicates some logic from transcription.ExtractRecordingDate
// to avoid an import cycle (database -> transcription -> database).
func backfillRecordedAt(db *gorm.DB) {
	var jobs []models.TranscriptionJob
	result := db.Where("recorded_at IS NULL AND audio_path != ''").
		Select("id", "audio_path", "original_filename").
		FindInBatches(&jobs, 50, func(tx *gorm.DB, batch int) error {
			for i := range jobs {
				recorded := extractRecordingDateForBackfill(jobs[i].AudioPath, jobs[i].OriginalFilename)
				if recorded != nil {
					tx.Model(&models.TranscriptionJob{}).
						Where("id = ?", jobs[i].ID).
						Update("recorded_at", recorded)
				}
			}
			return nil
		})

	if result.Error != nil {
		fmt.Printf("Warning: backfill recorded_at encountered error: %v\n", result.Error)
	} else if result.RowsAffected > 0 {
		fmt.Printf("Backfilled recorded_at for %d transcription jobs\n", result.RowsAffected)
	}
}

// extractRecordingDateForBackfill mirrors transcription.ExtractRecordingDate
// to avoid an import cycle.
func extractRecordingDateForBackfill(filePath, originalFilename string) *time.Time {
	// 1. Try ffprobe metadata
	if t := backfillFFprobe(filePath); t != nil {
		return t
	}
	// 2. Try filename patterns
	if t := backfillFilename(originalFilename); t != nil {
		return t
	}
	// 3. Fall back to file modification time
	if info, err := os.Stat(filePath); err == nil {
		modTime := info.ModTime()
		return &modTime
	}
	return nil
}

func backfillFFprobe(filePath string) *time.Time {
	ffprobeBin := binaries.FFprobe()
	if ffprobeBin == "" {
		return nil
	}
	cmd := exec.Command(ffprobeBin, "-v", "quiet", "-print_format", "json", "-show_format", filePath)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var probe struct {
		Format struct {
			Tags map[string]string `json:"tags"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		return nil
	}
	for _, key := range []string{"creation_time", "Creation_Time", "date", "Date"} {
		if val, ok := probe.Format.Tags[key]; ok {
			if t := backfillParseTime(val); t != nil {
				return t
			}
		}
	}
	return nil
}

func backfillParseTime(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05.000000Z",
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}

var backfillFilenamePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(\d{4})[\-_.]?(\d{2})[\-_.]?(\d{2})[\s_\-T](\d{2})[\-_.]?(\d{2})[\-_.]?(\d{2})`),
	regexp.MustCompile(`(\d{4})[\-_.](\d{2})[\-_.](\d{2})`),
	regexp.MustCompile(`(?:^|[^0-9])(\d{4})(0[1-9]|1[0-2])(0[1-9]|[12]\d|3[01])(?:[^0-9]|$)`),
}

func backfillFilename(filename string) *time.Time {
	if filename == "" {
		return nil
	}
	name := filepath.Base(filename)
	name = strings.TrimSuffix(name, filepath.Ext(name))

	if m := backfillFilenamePatterns[0].FindStringSubmatch(name); m != nil {
		if t, err := time.Parse("2006-01-02 15:04:05",
			m[1]+"-"+m[2]+"-"+m[3]+" "+m[4]+":"+m[5]+":"+m[6]); err == nil {
			return &t
		}
	}
	if m := backfillFilenamePatterns[1].FindStringSubmatch(name); m != nil {
		if t, err := time.Parse("2006-01-02", m[1]+"-"+m[2]+"-"+m[3]); err == nil {
			return &t
		}
	}
	if m := backfillFilenamePatterns[2].FindStringSubmatch(name); m != nil {
		if t, err := time.Parse("2006-01-02", m[1]+"-"+m[2]+"-"+m[3]); err == nil {
			return &t
		}
	}
	return nil
}
