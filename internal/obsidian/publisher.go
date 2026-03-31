package obsidian

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"unicode"
)

const quillSubdir = "Quill"

// Publisher handles publishing transcript markdown to an Obsidian vault.
type Publisher struct {
	vaultPath string
}

// PublishableJob represents a transcript job to be published to Obsidian.
type PublishableJob struct {
	JobID    string
	Title    string
	Markdown string
}

// PublishResult contains the outcome of publishing a single job.
type PublishResult struct {
	JobID string
	Path  string
	Error error
}

// NewPublisher creates a Publisher targeting the given Obsidian vault path.
func NewPublisher(vaultPath string) *Publisher {
	return &Publisher{vaultPath: vaultPath}
}

// InjectQuillID ensures the markdown frontmatter contains a quill-id field.
// If frontmatter exists, it inserts/updates quill-id right after the opening ---.
// If no frontmatter exists, it prepends one with only quill-id.
func InjectQuillID(markdown, jobID string) string {
	quillLine := "quill-id: " + jobID

	// Check for existing frontmatter
	if strings.HasPrefix(markdown, "---\n") {
		endIdx := strings.Index(markdown[4:], "\n---")
		if endIdx < 0 {
			// Malformed frontmatter — prepend
			return "---\n" + quillLine + "\n---\n\n" + markdown
		}
		endIdx += 4 // adjust for the 4-char offset

		frontmatter := markdown[4:endIdx]
		body := markdown[endIdx+4:] // skip past \n---

		// Check if quill-id already exists in frontmatter
		lines := strings.Split(frontmatter, "\n")
		found := false
		for i, line := range lines {
			if strings.HasPrefix(line, "quill-id:") {
				lines[i] = quillLine
				found = true
				break
			}
		}
		if !found {
			// Insert quill-id as first line of frontmatter
			lines = append([]string{quillLine}, lines...)
		}

		return "---\n" + strings.Join(lines, "\n") + "\n---" + body
	}

	// No frontmatter — prepend minimal frontmatter
	return "---\n" + quillLine + "\n---\n\n" + markdown
}

// FindExistingByQuillID scans the Quill subdirectory for a .md file
// whose frontmatter contains quill-id matching the given jobID.
// Returns the full path if found, empty string if not found.
func (p *Publisher) FindExistingByQuillID(jobID string) (string, error) {
	quillDir := filepath.Join(p.vaultPath, quillSubdir)
	entries, err := os.ReadDir(quillDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading Quill directory: %w", err)
	}

	target := "quill-id: " + jobID
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		path := filepath.Join(quillDir, entry.Name())
		if frontmatterContains(path, target) {
			return path, nil
		}
	}

	return "", nil
}

// frontmatterContains checks if a markdown file's frontmatter contains the target line.
// Only reads the frontmatter section (up to the closing ---) for efficiency.
func frontmatterContains(path, target string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() || scanner.Text() != "---" {
		return false
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "---" {
			return false // End of frontmatter, not found
		}
		if strings.TrimSpace(line) == target {
			return true
		}
	}
	return false
}

// PublishTranscript writes or updates a transcript markdown file in the Obsidian vault.
// It injects quill-id into frontmatter, finds existing files by quill-id for deterministic
// updates, and creates new files when no existing match is found.
func (p *Publisher) PublishTranscript(markdown, jobID, title string) (string, error) {
	if strings.TrimSpace(markdown) == "" {
		return "", fmt.Errorf("empty markdown content")
	}

	// Inject quill-id into frontmatter
	markdown = InjectQuillID(markdown, jobID)

	quillDir := filepath.Join(p.vaultPath, quillSubdir)
	if err := os.MkdirAll(quillDir, 0755); err != nil {
		return "", fmt.Errorf("creating Quill directory: %w", err)
	}

	// Compute the target filename — preserve spaces, strip only filesystem-unsafe chars.
	safeTitle := sanitizeFilename(strings.TrimSpace(title), "transcript")
	filename := fmt.Sprintf("%s.md", safeTitle)
	targetPath := filepath.Join(quillDir, filename)

	// Check if an existing file matches by quill-id
	existingPath, err := p.FindExistingByQuillID(jobID)
	if err != nil {
		return "", fmt.Errorf("finding existing file: %w", err)
	}

	if existingPath != "" && existingPath != targetPath {
		// Title changed — write to new path and remove old file
		if err := os.WriteFile(targetPath, []byte(markdown), 0644); err != nil {
			return "", fmt.Errorf("writing updated file: %w", err)
		}
		_ = os.Remove(existingPath)
		return targetPath, nil
	}

	// Write to target path (new file or same path)
	if err := os.WriteFile(targetPath, []byte(markdown), 0644); err != nil {
		return "", fmt.Errorf("writing file: %w", err)
	}

	return targetPath, nil
}

// BulkPublish publishes multiple transcripts to the Obsidian vault.
// Errors on individual jobs are captured in PublishResult.Error rather than
// failing the entire operation.
func (p *Publisher) BulkPublish(jobs []PublishableJob) ([]PublishResult, error) {
	results := make([]PublishResult, 0, len(jobs))

	for _, job := range jobs {
		path, err := p.PublishTranscript(job.Markdown, job.JobID, job.Title)
		results = append(results, PublishResult{
			JobID: job.JobID,
			Path:  path,
			Error: err,
		})
	}

	return results, nil
}

// sanitizeFilename strips characters that are unsafe for filesystems while
// preserving spaces, capitalisation, and other human-readable characters.
// Falls back to fallback if the result is empty.
func sanitizeFilename(name, fallback string) string {
	var b strings.Builder
	for _, r := range name {
		// Strip control chars and filesystem-unsafe characters: / \ : * ? " < > |
		if unicode.IsControl(r) {
			continue
		}
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			continue
		}
		b.WriteRune(r)
	}
	result := strings.TrimSpace(b.String())
	if result == "" {
		return fallback
	}
	return result
}
