package slug

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSafeFilename(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		fallback string
		want     string
	}{
		// Preserves spaces and casing.
		{"preserves spaces", "Talk with Ian", "untitled", "Talk with Ian"},
		{"preserves casing", "My Meeting Notes", "untitled", "My Meeting Notes"},

		// Strips filesystem-unsafe characters.
		{"strips colon", "Meeting: Monday", "untitled", "Meeting Monday"},
		{"strips slash", "Q1/Q2 Review", "untitled", "Q1Q2 Review"},
		{"strips backslash", `Notes\Draft`, "untitled", "NotesDraft"},
		{"strips null byte", "File\x00Name", "untitled", "FileName"},
		{"strips angle brackets", "Draft <final>", "untitled", "Draft final"},
		{"strips pipe", "Option A | Option B", "untitled", "Option A Option B"},
		{"strips double quote", `She said "hello"`, "untitled", "She said hello"},
		{"strips question mark", "What happened?", "untitled", "What happened"},
		{"strips asterisk", "Important*", "untitled", "Important"},

		// Preserves dots, hyphens, underscores, parens, and other safe punctuation.
		{"preserves dot", "v2.0 release", "untitled", "v2.0 release"},
		{"preserves hyphen", "end-of-day", "untitled", "end-of-day"},
		{"preserves underscore", "my_file", "untitled", "my_file"},
		{"preserves parens", "Draft (final)", "untitled", "Draft (final)"},
		{"preserves brackets", "Item [3]", "untitled", "Item [3]"},
		{"preserves ampersand", "Tom & Jerry", "untitled", "Tom & Jerry"},
		{"preserves plus", "C++ Notes", "untitled", "C++ Notes"},
		{"preserves comma", "Smith, John", "untitled", "Smith, John"},
		{"preserves single quote", "It's done", "untitled", "It's done"},

		// Trims leading/trailing whitespace and dots (macOS/Windows safety).
		{"trims leading spaces", "  hello", "untitled", "hello"},
		{"trims trailing spaces", "hello   ", "untitled", "hello"},
		{"trims leading dots", "..hidden", "untitled", "hidden"},
		{"trims trailing dots", "file..", "untitled", "file"},
		{"trims mixed", " .hello. ", "untitled", "hello"},

		// Collapses multiple consecutive spaces into one.
		{"collapses spaces", "A    B", "untitled", "A B"},
		{"collapses spaces from stripped chars", "A::B", "untitled", "AB"},

		// Fallback for empty/whitespace-only/unsafe-only inputs.
		{"empty string", "", "untitled", "untitled"},
		{"whitespace only", "   ", "untitled", "untitled"},
		{"unsafe only", ":/?*", "untitled", "untitled"},
		{"dots only", "...", "untitled", "untitled"},

		// Unicode support.
		{"unicode preserved", "日本語メモ", "untitled", "日本語メモ"},
		{"accented chars", "café résumé", "untitled", "café résumé"},
		{"emoji preserved", "Notes 📝", "untitled", "Notes 📝"},

		// Length limit (255 bytes for most filesystems).
		{"truncates long name", longString(300, 'A'), "untitled", longString(255, 'A')},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SafeFilename(tt.input, tt.fallback)
			if got != tt.want {
				t.Errorf("SafeFilename(%q, %q) = %q, want %q", tt.input, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestUniqueName(t *testing.T) {
	dir := t.TempDir()

	// First call — no conflict, returns base name.
	got := UniqueName(dir, "Meeting Notes")
	if got != "Meeting Notes" {
		t.Errorf("first call: got %q, want %q", got, "Meeting Notes")
	}

	// Create that directory so the next call must disambiguate.
	if err := os.Mkdir(filepath.Join(dir, "Meeting Notes"), 0o755); err != nil {
		t.Fatal(err)
	}

	got = UniqueName(dir, "Meeting Notes")
	if got != "Meeting Notes 2" {
		t.Errorf("second call: got %q, want %q", got, "Meeting Notes 2")
	}

	// Create "Meeting Notes 2" so the next call bumps to 3.
	if err := os.Mkdir(filepath.Join(dir, "Meeting Notes 2"), 0o755); err != nil {
		t.Fatal(err)
	}

	got = UniqueName(dir, "Meeting Notes")
	if got != "Meeting Notes 3" {
		t.Errorf("third call: got %q, want %q", got, "Meeting Notes 3")
	}
}

func TestUniqueName_CaseInsensitive(t *testing.T) {
	dir := t.TempDir()

	// Create a folder with different casing.
	if err := os.Mkdir(filepath.Join(dir, "meeting notes"), 0o755); err != nil {
		t.Fatal(err)
	}

	// On case-insensitive FS (macOS/Windows), should detect the conflict.
	// On case-sensitive FS (Linux), this test still passes because we do
	// explicit case-folded comparison.
	got := UniqueName(dir, "Meeting Notes")
	if got != "Meeting Notes 2" {
		t.Errorf("case-insensitive conflict: got %q, want %q", got, "Meeting Notes 2")
	}
}

func TestUniqueName_NoConflictDifferentName(t *testing.T) {
	dir := t.TempDir()

	if err := os.Mkdir(filepath.Join(dir, "Other Folder"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := UniqueName(dir, "Meeting Notes")
	if got != "Meeting Notes" {
		t.Errorf("no conflict expected: got %q, want %q", got, "Meeting Notes")
	}
}

// longString creates a string of length n by repeating char c.
func longString(n int, c byte) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return string(b)
}
