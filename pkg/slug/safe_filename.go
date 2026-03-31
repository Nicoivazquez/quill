package slug

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// unsafeChars are characters that are invalid in filenames on macOS, Windows,
// or Linux: : / \ NUL < > | " ? *
var unsafeChars = map[rune]bool{
	':': true, '/': true, '\\': true, 0: true,
	'<': true, '>': true, '|': true, '"': true,
	'?': true, '*': true,
}

// maxFilenameBytes is the maximum filename length on most filesystems.
const maxFilenameBytes = 255

// SafeFilename converts a human-readable title into a filesystem-safe name
// that preserves spaces, casing, and most punctuation. Only characters that
// are unsafe on macOS/Windows/Linux are stripped. Leading/trailing whitespace
// and dots are trimmed. Multiple consecutive spaces are collapsed.
//
// If the result is empty after sanitisation, fallback is returned.
func SafeFilename(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}

	var b strings.Builder
	b.Grow(len(trimmed))
	lastSpace := false

	for _, r := range trimmed {
		if unsafeChars[r] {
			continue
		}
		if unicode.IsSpace(r) {
			if !lastSpace {
				b.WriteRune(' ')
				lastSpace = true
			}
			continue
		}
		b.WriteRune(r)
		lastSpace = false
	}

	result := strings.TrimSpace(b.String())
	// Trim leading/trailing dots (hidden files on Unix, problematic on Windows).
	result = strings.Trim(result, ".")
	result = strings.TrimSpace(result)

	if result == "" {
		return fallback
	}

	// Truncate to maxFilenameBytes while respecting UTF-8 boundaries.
	if len(result) > maxFilenameBytes {
		for len(result) > maxFilenameBytes {
			_, size := utf8.DecodeLastRuneInString(result)
			result = result[:len(result)-size]
		}
		result = strings.TrimSpace(result)
		result = strings.TrimRight(result, ".")
	}

	if result == "" {
		return fallback
	}
	return result
}

// UniqueName returns a directory name that does not collide with any existing
// entry in parentDir. If baseName is not taken, it is returned as-is.
// Otherwise, Finder-style suffixes are appended: "Name 2", "Name 3", etc.
// Comparison is case-insensitive to be safe on macOS/Windows volumes.
func UniqueName(parentDir, baseName string) string {
	entries, err := os.ReadDir(parentDir)
	if err != nil {
		// Directory doesn't exist or unreadable — no conflicts possible.
		return baseName
	}

	existing := make(map[string]bool, len(entries))
	for _, e := range entries {
		existing[strings.ToLower(e.Name())] = true
	}

	candidate := baseName
	if !existing[strings.ToLower(candidate)] {
		return candidate
	}

	for i := 2; i <= 9999; i++ {
		candidate = baseName + " " + itoa(i)
		if !existing[strings.ToLower(candidate)] {
			return candidate
		}
	}

	// Extremely unlikely — fall back to base name (will collide, but avoids infinite loop).
	return baseName
}

// resolveAbsPath returns parentDir/name as an absolute path.
// Exported for use from other packages if needed.
func ResolveUniquePath(parentDir, baseName string) string {
	return filepath.Join(parentDir, UniqueName(parentDir, baseName))
}

// itoa converts a small int to string without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
