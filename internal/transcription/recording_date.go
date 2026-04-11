package transcription

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"quill/pkg/binaries"
)

// ExtractRecordingDate attempts to determine when an audio file was originally
// recorded. It tries three sources in priority order:
//  1. ffprobe metadata (format.tags.creation_time)
//  2. Filename date patterns (Apple Voice Memos, generic timestamps)
//  3. File system modification time
//
// Returns nil only if all sources fail.
func ExtractRecordingDate(filePath string, originalFilename string) *time.Time {
	// 1. Try ffprobe metadata
	if t := extractFromFFprobe(filePath); t != nil {
		return t
	}

	// 2. Try filename patterns
	if t := extractFromFilename(originalFilename); t != nil {
		return t
	}

	// 3. Fall back to file modification time
	if info, err := os.Stat(filePath); err == nil {
		modTime := info.ModTime()
		return &modTime
	}

	return nil
}

// ffprobeFormatOutput mirrors the JSON structure returned by ffprobe -show_format.
type ffprobeFormatOutput struct {
	Format struct {
		Tags map[string]string `json:"tags"`
	} `json:"format"`
}

// extractFromFFprobe runs ffprobe and looks for a creation_time tag.
func extractFromFFprobe(filePath string) *time.Time {
	ffprobeBin := binaries.FFprobe()
	if ffprobeBin == "" {
		return nil
	}

	cmd := exec.Command(ffprobeBin, "-v", "quiet", "-print_format", "json", "-show_format", filePath)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	var probe ffprobeFormatOutput
	if err := json.Unmarshal(out, &probe); err != nil {
		return nil
	}

	// Try common tag names (case-insensitive keys in the map)
	for _, key := range []string{"creation_time", "Creation_Time", "date", "Date"} {
		if val, ok := probe.Format.Tags[key]; ok {
			if t := parseTimeString(val); t != nil {
				return t
			}
		}
	}

	return nil
}

// parseTimeString tries several common timestamp formats.
func parseTimeString(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}

	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05.000000Z",
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}

	for _, layout := range formats {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}

// Filename patterns for date extraction.
var filenamePatterns = []*regexp.Regexp{
	// Apple Voice Memos: "20240115 143022.m4a" or with separators
	regexp.MustCompile(`(\d{4})[\-_.]?(\d{2})[\-_.]?(\d{2})[\s_\-T](\d{2})[\-_.]?(\d{2})[\-_.]?(\d{2})`),
	// Date only: "20240115.m4a"
	regexp.MustCompile(`(\d{4})[\-_.](\d{2})[\-_.](\d{2})`),
	// Compact: "20240115" (only if 8 digits match a valid date)
	regexp.MustCompile(`(?:^|[^0-9])(\d{4})(0[1-9]|1[0-2])(0[1-9]|[12]\d|3[01])(?:[^0-9]|$)`),
}

// extractFromFilename tries to parse a date from the original filename.
func extractFromFilename(filename string) *time.Time {
	if filename == "" {
		return nil
	}

	// Strip extension and path
	name := filepath.Base(filename)
	name = strings.TrimSuffix(name, filepath.Ext(name))

	// Pattern 1: Full datetime (YYYY-MM-DD HH:MM:SS variants)
	if m := filenamePatterns[0].FindStringSubmatch(name); m != nil {
		t, err := time.Parse("2006-01-02 15:04:05",
			m[1]+"-"+m[2]+"-"+m[3]+" "+m[4]+":"+m[5]+":"+m[6])
		if err == nil {
			return &t
		}
	}

	// Pattern 2: Date with separators (YYYY-MM-DD)
	if m := filenamePatterns[1].FindStringSubmatch(name); m != nil {
		t, err := time.Parse("2006-01-02", m[1]+"-"+m[2]+"-"+m[3])
		if err == nil {
			return &t
		}
	}

	// Pattern 3: Compact date (YYYYMMDD)
	if m := filenamePatterns[2].FindStringSubmatch(name); m != nil {
		t, err := time.Parse("2006-01-02", m[1]+"-"+m[2]+"-"+m[3])
		if err == nil {
			return &t
		}
	}

	return nil
}
