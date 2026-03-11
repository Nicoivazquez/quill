package api

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"quill/internal/contacts"
	"quill/internal/models"
)

// ---- isWithinBoundary -------------------------------------------------------

func TestIsWithinBoundary_InsideRoot(t *testing.T) {
	root := t.TempDir()
	allowed := []string{root}

	cases := []struct {
		name string
		path string
		want bool
	}{
		{
			name: "direct child file",
			path: filepath.Join(root, "file.wav"),
			want: true,
		},
		{
			name: "deeply nested child",
			path: filepath.Join(root, "sub", "dir", "file.wav"),
			want: true,
		},
		{
			name: "root itself is within boundary",
			path: root,
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isWithinBoundary(tc.path, allowed); got != tc.want {
				t.Fatalf("isWithinBoundary(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestIsWithinBoundary_OutsideRoot_Rejected(t *testing.T) {
	root := t.TempDir()
	sibling := t.TempDir()
	allowed := []string{root}

	cases := []struct {
		name string
		path string
	}{
		{
			name: "parent directory",
			path: filepath.Dir(root),
		},
		{
			name: "sibling directory",
			path: sibling,
		},
		{
			name: "file inside sibling",
			path: filepath.Join(sibling, "secret.wav"),
		},
		{
			name: "dotdot escape one level",
			// filepath.Clean of root/../escape == parent dir / escape
			path: filepath.Clean(filepath.Join(root, "..", "escape")),
		},
		{
			name: "dotdot deep traversal",
			path: filepath.Clean(filepath.Join(root, "..", "..", "..", "etc", "passwd")),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if isWithinBoundary(tc.path, allowed) {
				t.Fatalf("isWithinBoundary(%q) should be false (path traversal rejected), got true", tc.path)
			}
		})
	}
}

func TestIsWithinBoundary_MultipleRoots_MatchesAny(t *testing.T) {
	root1 := t.TempDir()
	root2 := t.TempDir()
	outsider := t.TempDir()
	allowed := []string{root1, root2}

	if !isWithinBoundary(filepath.Join(root1, "a.wav"), allowed) {
		t.Error("file in root1 should be within boundary")
	}
	if !isWithinBoundary(filepath.Join(root2, "b.wav"), allowed) {
		t.Error("file in root2 should be within boundary")
	}
	if isWithinBoundary(filepath.Join(outsider, "c.wav"), allowed) {
		t.Error("file outside all roots must NOT be within boundary")
	}
}

func TestIsWithinBoundary_EmptyRoots_AlwaysFalse(t *testing.T) {
	path := "/some/absolute/file.wav"
	if isWithinBoundary(path, nil) {
		t.Error("nil roots: expected false")
	}
	if isWithinBoundary(path, []string{}) {
		t.Error("empty roots slice: expected false")
	}
}

func TestIsWithinBoundary_SymlinkEscapeDoesNotAffectStringCheck(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test skipped on Windows")
	}

	vault := t.TempDir()
	outside := t.TempDir()
	allowed := []string{vault}

	// Create symlink inside vault pointing outside.
	linkDir := filepath.Join(vault, "link")
	if err := os.Symlink(outside, linkDir); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	// isWithinBoundary is a string prefix check; the real escaped path is
	// outside the vault. Confirm the resolved outside path is rejected.
	escapedPath := filepath.Join(outside, "secret.wav")
	if isWithinBoundary(escapedPath, allowed) {
		t.Fatalf("resolved symlink escape path %q must NOT pass boundary check for vault %q", escapedPath, vault)
	}
}

// ---- resolveJobAudioPath ----------------------------------------------------

func TestResolveJobAudioPath_AbsolutePathInsideVault(t *testing.T) {
	root := t.TempDir()
	audioFile := filepath.Join(root, "audio", "recording.wav")
	mustMkdirAll(t, filepath.Dir(audioFile))
	mustWriteFile(t, audioFile, []byte("RIFF"))

	job := &models.TranscriptionJob{AudioPath: audioFile}

	got, err := resolveJobAudioPath(job, root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != audioFile {
		t.Fatalf("expected %q, got %q", audioFile, got)
	}
}

func TestResolveJobAudioPath_AbsolutePathOutsideVault_Rejected(t *testing.T) {
	vault := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.wav")
	mustWriteFile(t, outsideFile, []byte("RIFF"))

	job := &models.TranscriptionJob{AudioPath: outsideFile}

	_, err := resolveJobAudioPath(job, vault)
	if err == nil {
		t.Fatal("expected error for absolute path outside vault and upload dirs, got nil")
	}
}

func TestResolveJobAudioPath_DotDotTraversal_Rejected(t *testing.T) {
	vault := t.TempDir()
	// e.g. /tmp/vault/../../etc/passwd
	traversal := filepath.Clean(filepath.Join(vault, "..", "..", "etc", "passwd"))

	job := &models.TranscriptionJob{AudioPath: traversal}

	_, err := resolveJobAudioPath(job, vault)
	if err == nil {
		t.Fatalf("dotdot traversal path %q must be rejected, got nil error", traversal)
	}
}

func TestResolveJobAudioPath_MergedAudioTakesPrecedenceOverAudioPath(t *testing.T) {
	root := t.TempDir()
	mergedFile := filepath.Join(root, "merged.wav")
	plainFile := filepath.Join(root, "plain.wav")
	mustWriteFile(t, mergedFile, []byte("RIFF"))
	mustWriteFile(t, plainFile, []byte("RIFF"))

	job := &models.TranscriptionJob{
		AudioPath:       plainFile,
		MergedAudioPath: &mergedFile,
	}

	got, err := resolveJobAudioPath(job, root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != mergedFile {
		t.Fatalf("merged audio should take precedence; expected %q got %q", mergedFile, got)
	}
}

func TestResolveJobAudioPath_EmptyMergedPathFallsBackToAudioPath(t *testing.T) {
	root := t.TempDir()
	audioFile := filepath.Join(root, "audio.wav")
	mustWriteFile(t, audioFile, []byte("RIFF"))

	empty := ""
	job := &models.TranscriptionJob{
		AudioPath:       audioFile,
		MergedAudioPath: &empty,
	}

	got, err := resolveJobAudioPath(job, root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != audioFile {
		t.Fatalf("expected fallback to audio path %q, got %q", audioFile, got)
	}
}

func TestResolveJobAudioPath_MergedPathOutsideVaultFallsBackToAudioPath(t *testing.T) {
	vault := t.TempDir()
	outside := t.TempDir()

	// mergedPath is outside vault; audioPath is inside.
	mergedFile := filepath.Join(outside, "merged.wav")
	audioFile := filepath.Join(vault, "audio.wav")
	mustWriteFile(t, mergedFile, []byte("RIFF"))
	mustWriteFile(t, audioFile, []byte("RIFF"))

	job := &models.TranscriptionJob{
		AudioPath:       audioFile,
		MergedAudioPath: &mergedFile,
	}

	got, err := resolveJobAudioPath(job, vault)
	if err != nil {
		t.Fatalf("expected fallback to audio path; got error: %v", err)
	}
	if got != audioFile {
		t.Fatalf("expected fallback to safe audio path %q, got %q", audioFile, got)
	}
}

func TestResolveJobAudioPath_NonexistentFileReturnsError(t *testing.T) {
	root := t.TempDir()
	job := &models.TranscriptionJob{
		AudioPath: filepath.Join(root, "nonexistent.wav"),
	}

	_, err := resolveJobAudioPath(job, root)
	if err == nil {
		t.Fatal("expected error for nonexistent audio file, got nil")
	}
}

func TestResolveJobAudioPath_DirectoryRejected(t *testing.T) {
	root := t.TempDir()
	subDir := filepath.Join(root, "subdir")
	mustMkdirAll(t, subDir)

	job := &models.TranscriptionJob{AudioPath: subDir}

	_, err := resolveJobAudioPath(job, root)
	if err == nil {
		t.Fatal("expected error when audio path points to a directory, got nil")
	}
}

func TestResolveJobAudioPath_EmptyAudioPath_ReturnsError(t *testing.T) {
	root := t.TempDir()
	job := &models.TranscriptionJob{AudioPath: ""}

	_, err := resolveJobAudioPath(job, root)
	if err == nil {
		t.Fatal("expected error for empty audio path, got nil")
	}
}

func TestResolveJobAudioPath_NilJobReturnsError(t *testing.T) {
	// resolveJobAudioPath dereferences job fields; nil job should not panic
	// but may panic by design. We test that non-nil empty job returns error.
	root := t.TempDir()
	job := &models.TranscriptionJob{}
	_, err := resolveJobAudioPath(job, root)
	if err == nil {
		t.Fatal("expected error for job with no audio path, got nil")
	}
}

// ---- contactAlreadyBootstrapped / existingVaultFile -------------------------

func TestContactAlreadyBootstrapped_NilContact_ReturnsTrue(t *testing.T) {
	root := t.TempDir()
	fs := contacts.NewFileService(root)
	if !contactAlreadyBootstrapped(nil, fs) {
		t.Fatal("nil contact must be treated as already bootstrapped (safety default)")
	}
}

func TestContactAlreadyBootstrapped_ManualSignatureWithFile_ReturnsTrue(t *testing.T) {
	root := t.TempDir()
	fs := contacts.NewFileService(root)

	embPath := "Contacts/People/alice--uid/voice-signature.embedding.json"
	mustMakeVaultFile(t, root, embPath)

	contact := &models.Contact{
		SignatureEmbeddingPath: strPtr(embPath),
		SignatureData:          strPtr(`{"source":"manual"}`),
	}

	if !contactAlreadyBootstrapped(contact, fs) {
		t.Fatal("contact with manual signature + existing embedding should be bootstrapped")
	}
}

func TestContactAlreadyBootstrapped_ExistingEmbeddingFile_ReturnsTrue(t *testing.T) {
	root := t.TempDir()
	fs := contacts.NewFileService(root)

	embPath := "Contacts/People/bob--uid/voice-signature.embedding.json"
	mustMakeVaultFile(t, root, embPath)

	contact := &models.Contact{
		SignatureEmbeddingPath: strPtr(embPath),
		// No manual source — extracted embedding on disk is enough.
	}

	if !contactAlreadyBootstrapped(contact, fs) {
		t.Fatal("contact with existing embedding file should be already bootstrapped")
	}
}

func TestContactAlreadyBootstrapped_ExistingSnippetFile_ReturnsTrue(t *testing.T) {
	root := t.TempDir()
	fs := contacts.NewFileService(root)

	snippetPath := "Contacts/People/carol--uid/voice-snippet.wav"
	mustMakeVaultFile(t, root, snippetPath)

	contact := &models.Contact{
		VoiceSnippetPath: strPtr(snippetPath),
	}

	if !contactAlreadyBootstrapped(contact, fs) {
		t.Fatal("contact with existing snippet file should be already bootstrapped")
	}
}

func TestContactAlreadyBootstrapped_NoFilesOnDisk_ReturnsFalse(t *testing.T) {
	root := t.TempDir()
	fs := contacts.NewFileService(root)

	contact := &models.Contact{
		VoiceSnippetPath:       strPtr("Contacts/People/dave--uid/voice-snippet.wav"),
		SignatureEmbeddingPath: strPtr("Contacts/People/dave--uid/voice-signature.embedding.json"),
	}

	if contactAlreadyBootstrapped(contact, fs) {
		t.Fatal("contact whose files do not exist on disk should NOT be already bootstrapped")
	}
}

func TestContactAlreadyBootstrapped_NilPathsNoFiles_ReturnsFalse(t *testing.T) {
	root := t.TempDir()
	fs := contacts.NewFileService(root)

	contact := &models.Contact{}

	if contactAlreadyBootstrapped(contact, fs) {
		t.Fatal("contact with no paths set should NOT be already bootstrapped")
	}
}

func TestExistingVaultFile_NilPointer_ReturnsFalse(t *testing.T) {
	root := t.TempDir()
	fs := contacts.NewFileService(root)

	if existingVaultFile(fs, nil) {
		t.Fatal("nil pointer should return false")
	}
}

func TestExistingVaultFile_EmptyString_ReturnsFalse(t *testing.T) {
	root := t.TempDir()
	fs := contacts.NewFileService(root)
	empty := ""

	if existingVaultFile(fs, &empty) {
		t.Fatal("empty string should return false")
	}
}

func TestExistingVaultFile_WhitespaceOnly_ReturnsFalse(t *testing.T) {
	root := t.TempDir()
	fs := contacts.NewFileService(root)
	ws := "   "

	if existingVaultFile(fs, &ws) {
		t.Fatal("whitespace-only path should return false")
	}
}

func TestExistingVaultFile_Directory_ReturnsFalse(t *testing.T) {
	root := t.TempDir()
	fs := contacts.NewFileService(root)

	dirRel := "Contacts/People/eve--uid"
	mustMkdirAll(t, filepath.Join(root, filepath.FromSlash(dirRel)))

	if existingVaultFile(fs, strPtr(dirRel)) {
		t.Fatal("directory must not be treated as a regular vault file")
	}
}

func TestExistingVaultFile_RealFile_ReturnsTrue(t *testing.T) {
	root := t.TempDir()
	fs := contacts.NewFileService(root)

	rel := "Contacts/People/frank--uid/voice-snippet.wav"
	mustMakeVaultFile(t, root, rel)

	if !existingVaultFile(fs, strPtr(rel)) {
		t.Fatal("real file on disk should return true")
	}
}

func TestExistingVaultFile_PathOutsideVault_ReturnsFalse(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	fs := contacts.NewFileService(root)

	// Absolute path outside vault — ResolveAndValidate should reject it.
	outsideFile := filepath.Join(outside, "secret.wav")
	mustWriteFile(t, outsideFile, []byte("data"))

	if existingVaultFile(fs, strPtr(outsideFile)) {
		t.Fatal("path outside vault must return false (boundary enforcement)")
	}
}

// ---- normalizeNameKey -------------------------------------------------------

func TestNormalizeNameKey_BasicCases(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"Alice", "alice"},
		{"  Bob  ", "bob"},
		{"JOHN DOE", "john doe"},
		{"", ""},
		{"   ", ""},
	}
	for _, tc := range cases {
		got := normalizeNameKey(tc.input)
		if got != tc.want {
			t.Errorf("normalizeNameKey(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestNormalizeNameKey_InvalidUTF8_Replaced(t *testing.T) {
	// Simulate non-UTF8 bytes; strings.ToValidUTF8 should replace them.
	invalid := "name\x80value"
	got := normalizeNameKey(invalid)
	if strings.ContainsRune(got, 0x80) {
		t.Fatalf("normalizeNameKey should replace invalid UTF-8 bytes, got %q", got)
	}
	// Result should be lowercase.
	if got != strings.ToLower(got) {
		t.Fatalf("normalizeNameKey result should be lowercase, got %q", got)
	}
}

func TestNormalizeNameKey_Unicode(t *testing.T) {
	got := normalizeNameKey("Ångström")
	if got != "ångström" {
		t.Fatalf("expected unicode lowercased, got %q", got)
	}
}

// ---- shared test helpers ----------------------------------------------------

func strPtr(s string) *string { return &s }

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", dir, err)
	}
}

func mustWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll for %q: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func mustMakeVaultFile(t *testing.T, root, rel string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	mustWriteFile(t, abs, []byte("test-payload"))
}
