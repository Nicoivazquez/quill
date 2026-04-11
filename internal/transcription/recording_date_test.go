package transcription

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExtractFromFilename(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		wantNil  bool
		wantDate string // expected date in "2006-01-02" or "2006-01-02 15:04:05"
	}{
		{
			name:     "datetime with underscores",
			filename: "20240115_143022.m4a",
			wantDate: "2024-01-15 14:30:22",
		},
		{
			name:     "datetime with dashes and T separator",
			filename: "2024-01-15T14-30-22.wav",
			wantDate: "2024-01-15 14:30:22",
		},
		{
			name:     "datetime with spaces",
			filename: "20240115 143022.m4a",
			wantDate: "2024-01-15 14:30:22",
		},
		{
			name:     "date with dashes",
			filename: "recording-2024-03-22.m4a",
			wantDate: "2024-03-22",
		},
		{
			name:     "date with dots",
			filename: "voice_2024.03.22.mp3",
			wantDate: "2024-03-22",
		},
		{
			name:     "compact date YYYYMMDD",
			filename: "Recording 20240322.m4a",
			wantDate: "2024-03-22",
		},
		{
			name:     "apple voice memo style with prefix",
			filename: "Voice Memo 20240115_092030.m4a",
			wantDate: "2024-01-15 09:20:30",
		},
		{
			name:    "no date in filename",
			filename: "my-recording.wav",
			wantNil: true,
		},
		{
			name:    "empty filename",
			filename: "",
			wantNil: true,
		},
		{
			name:    "invalid month",
			filename: "20241301.m4a",
			wantNil: true,
		},
		{
			name:    "invalid day",
			filename: "20240132.m4a",
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFromFilename(tt.filename)

			if tt.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
				return
			}

			if got == nil {
				t.Fatalf("expected date %q, got nil", tt.wantDate)
			}

			// Try full datetime first, then date only
			var want time.Time
			var err error
			if len(tt.wantDate) > 10 {
				want, err = time.Parse("2006-01-02 15:04:05", tt.wantDate)
			} else {
				want, err = time.Parse("2006-01-02", tt.wantDate)
			}
			if err != nil {
				t.Fatalf("invalid test wantDate %q: %v", tt.wantDate, err)
			}

			if !got.Equal(want) {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	}
}

func TestExtractRecordingDate_FallsBackToModTime(t *testing.T) {
	// Create a temp file
	dir := t.TempDir()
	fp := filepath.Join(dir, "nodate.wav")
	if err := os.WriteFile(fp, []byte("fake audio"), 0644); err != nil {
		t.Fatal(err)
	}

	// Set a known mod time
	knownTime := time.Date(2023, 6, 15, 10, 30, 0, 0, time.UTC)
	if err := os.Chtimes(fp, knownTime, knownTime); err != nil {
		t.Fatal(err)
	}

	got := ExtractRecordingDate(fp, "nodate.wav")
	if got == nil {
		t.Fatal("expected mod time fallback, got nil")
	}

	if !got.Equal(knownTime) {
		t.Errorf("got %v, want %v", got, knownTime)
	}
}

func TestExtractRecordingDate_FilenameOverridesModTime(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.wav")
	if err := os.WriteFile(fp, []byte("fake audio"), 0644); err != nil {
		t.Fatal(err)
	}

	// Filename has a date, mod time is different
	got := ExtractRecordingDate(fp, "20240315_120000.wav")
	if got == nil {
		t.Fatal("expected filename date, got nil")
	}

	want := time.Date(2024, 3, 15, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTimeString(t *testing.T) {
	tests := []struct {
		input   string
		wantNil bool
	}{
		{"2024-01-15T14:30:22Z", false},
		{"2024-01-15T14:30:22.000000Z", false},
		{"2024-01-15 14:30:22", false},
		{"2024-01-15", false},
		{"", true},
		{"not a date", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseTimeString(tt.input)
			if tt.wantNil && got != nil {
				t.Errorf("expected nil, got %v", got)
			}
			if !tt.wantNil && got == nil {
				t.Errorf("expected non-nil for %q", tt.input)
			}
		})
	}
}
