package contacts

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"quill/internal/models"
	"quill/pkg/slug"

	"gopkg.in/yaml.v3"
)

const (
	contactMarkdownFormat = "contact-note-v1"
	contactsPeopleDir     = "Contacts/People"
	contactNoteFileName   = "contact.md"
)

// ContactFileInfo describes a discoverable contact folder on disk.
type ContactFileInfo struct {
	FolderAbsPath string
	NoteAbsPath   string
	ContactUID    string
	Slug          string
	MtimeNS       int64
}

// FileService reads and writes canonical contact artifacts in a vault.
type FileService struct {
	vaultPath string
}

func NewFileService(vaultPath string) *FileService {
	return &FileService{vaultPath: strings.TrimSpace(vaultPath)}
}

func (s *FileService) ContactsBaseAbsPath() string {
	return filepath.Join(s.vaultPath, filepath.FromSlash(contactsPeopleDir))
}

func (s *FileService) ContactFolderRelPath(contact *models.Contact) string {
	folderName := fmt.Sprintf("%s--%s", slug.Sanitize(contact.Slug, "contact"), strings.TrimSpace(contact.ContactUID))
	return filepath.ToSlash(filepath.Join(filepath.FromSlash(contactsPeopleDir), folderName))
}

func (s *FileService) ContactFolderAbsPath(contact *models.Contact) string {
	return filepath.Join(s.vaultPath, filepath.FromSlash(s.ContactFolderRelPath(contact)))
}

func (s *FileService) ContactNoteAbsPath(contact *models.Contact) string {
	return filepath.Join(s.ContactFolderAbsPath(contact), contactNoteFileName)
}

func (s *FileService) ResolveAbsPath(relOrAbs string) string {
	trimmed := strings.TrimSpace(relOrAbs)
	if trimmed == "" {
		return ""
	}
	if filepath.IsAbs(trimmed) {
		return trimmed
	}
	return filepath.Join(s.vaultPath, filepath.FromSlash(trimmed))
}

// IsInsideVault returns true when absPath is contained within the vault root.
func (s *FileService) IsInsideVault(absPath string) bool {
	cleaned := filepath.Clean(absPath)
	vaultCleaned := filepath.Clean(s.vaultPath)
	if cleaned == vaultCleaned {
		return true
	}
	return strings.HasPrefix(cleaned, vaultCleaned+string(filepath.Separator))
}

// ResolveAndValidate resolves a relative-or-absolute path and verifies it is
// contained within the vault. Returns ("", false) when the path escapes.
func (s *FileService) ResolveAndValidate(relOrAbs string) (string, bool) {
	abs := s.ResolveAbsPath(relOrAbs)
	if abs == "" {
		return "", false
	}
	if !s.IsInsideVault(abs) {
		return "", false
	}
	return abs, true
}

// WriteContact persists the canonical contact markdown and updates file tracking fields.
func (s *FileService) WriteContact(contact *models.Contact) error {
	if strings.TrimSpace(contact.ContactUID) == "" {
		return fmt.Errorf("contact_uid is required")
	}
	if strings.TrimSpace(contact.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if strings.TrimSpace(contact.Slug) == "" {
		contact.Slug = slug.Sanitize(contact.Name, "contact")
	}

	desiredFolderRel := s.ContactFolderRelPath(contact)
	desiredFolderAbs := filepath.Join(s.vaultPath, filepath.FromSlash(desiredFolderRel))

	currentFolderRel := folderRelFromNotePath(contact.NotePath)
	if currentFolderRel != "" && currentFolderRel != desiredFolderRel {
		currentFolderAbs := filepath.Join(s.vaultPath, filepath.FromSlash(currentFolderRel))
		if _, err := os.Stat(currentFolderAbs); err == nil {
			if err := os.MkdirAll(filepath.Dir(desiredFolderAbs), 0o755); err != nil {
				return err
			}
			if err := os.Rename(currentFolderAbs, desiredFolderAbs); err != nil {
				return err
			}
			updateAssetPrefix(contact, currentFolderRel, desiredFolderRel)
		}
	}

	if err := os.MkdirAll(desiredFolderAbs, 0o755); err != nil {
		return err
	}

	payload, err := renderContactMarkdown(contact)
	if err != nil {
		return err
	}

	noteAbs := filepath.Join(desiredFolderAbs, contactNoteFileName)
	tmpPath := noteAbs + ".tmp"
	if err := os.WriteFile(tmpPath, payload, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, noteAbs); err != nil {
		return err
	}

	stat, err := os.Stat(noteAbs)
	if err != nil {
		return err
	}

	contact.NotePath = filepath.ToSlash(filepath.Join(desiredFolderRel, contactNoteFileName))
	contact.FileMtimeNS = stat.ModTime().UnixNano()

	normalizeAssetPaths(contact, desiredFolderRel)
	return nil
}

func (s *FileService) DeleteContactFolder(contact *models.Contact) error {
	folderRel := folderRelFromNotePath(contact.NotePath)
	if folderRel == "" {
		if strings.TrimSpace(contact.ContactUID) == "" {
			return nil
		}
		if strings.TrimSpace(contact.Slug) == "" {
			contact.Slug = slug.Sanitize(contact.Name, "contact")
		}
		folderRel = s.ContactFolderRelPath(contact)
	}
	folderAbs := filepath.Join(s.vaultPath, filepath.FromSlash(folderRel))
	if _, err := os.Stat(folderAbs); os.IsNotExist(err) {
		return nil
	}
	return os.RemoveAll(folderAbs)
}

func (s *FileService) ScanAllContactFolders() ([]ContactFileInfo, error) {
	base := s.ContactsBaseAbsPath()
	if _, err := os.Stat(base); os.IsNotExist(err) {
		return []ContactFileInfo{}, nil
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil, err
	}

	infos := make([]ContactFileInfo, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		slug, uid := parseContactFolderName(entry.Name())
		if uid == "" {
			continue
		}
		folderAbs := filepath.Join(base, entry.Name())
		noteAbs := filepath.Join(folderAbs, contactNoteFileName)
		stat, statErr := os.Stat(noteAbs)
		if statErr != nil || stat.IsDir() {
			continue
		}
		infos = append(infos, ContactFileInfo{
			FolderAbsPath: folderAbs,
			NoteAbsPath:   noteAbs,
			ContactUID:    uid,
			Slug:          slug,
			MtimeNS:       stat.ModTime().UnixNano(),
		})
	}

	sort.Slice(infos, func(i, j int) bool {
		return infos[i].NoteAbsPath < infos[j].NoteAbsPath
	})
	return infos, nil
}

// ReadContactFromNotePath reads contact metadata from a canonical contact note.
func (s *FileService) ReadContactFromNotePath(noteAbsPath string) (*models.Contact, error) {
	raw, err := os.ReadFile(noteAbsPath)
	if err != nil {
		return nil, err
	}
	fm, body, err := parseFrontmatter(raw)
	if err != nil {
		return nil, err
	}
	if fm.Format != contactMarkdownFormat {
		return nil, fmt.Errorf("unsupported contact note format %q", fm.Format)
	}
	if strings.TrimSpace(fm.ContactUID) == "" {
		return nil, fmt.Errorf("contact_uid is required")
	}
	if strings.TrimSpace(fm.Name) == "" {
		return nil, fmt.Errorf("name is required")
	}

	folderAbs := filepath.Dir(noteAbsPath)
	folderName := filepath.Base(folderAbs)
	slugFromFolder, uidFromFolder := parseContactFolderName(folderName)
	if uidFromFolder != "" && uidFromFolder != strings.TrimSpace(fm.ContactUID) {
		return nil, fmt.Errorf("contact_uid mismatch between frontmatter and folder")
	}

	noteRel, relErr := filepath.Rel(s.vaultPath, noteAbsPath)
	if relErr != nil {
		return nil, relErr
	}
	stat, statErr := os.Stat(noteAbsPath)
	if statErr != nil {
		return nil, statErr
	}

	contact := &models.Contact{
		ContactUID:  strings.TrimSpace(fm.ContactUID),
		Slug:        slug.Sanitize(defaultString(strings.TrimSpace(fm.Slug), slugFromFolder), "contact"),
		Name:        strings.TrimSpace(fm.Name),
		NotePath:    filepath.ToSlash(noteRel),
		FileMtimeNS: stat.ModTime().UnixNano(),
	}
	if contact.Slug == "" {
		contact.Slug = slug.Sanitize(contact.Name, "contact")
	}
	if trimmed := strings.TrimSpace(fm.Phone); trimmed != "" {
		contact.Phone = stringPtr(trimmed)
	}
	if trimmed := strings.TrimSpace(fm.Email); trimmed != "" {
		contact.Email = stringPtr(trimmed)
	}
	if trimmed := strings.TrimSpace(fm.SignatureStatus); trimmed != "" {
		contact.SignatureStatus = trimmed
	} else {
		contact.SignatureStatus = "none"
	}

	if trimmed := strings.TrimSpace(body); trimmed != "" {
		contact.Notes = stringPtr(strings.TrimSpace(body))
	}

	folderRel := filepath.ToSlash(filepath.Dir(noteRel))
	if trimmed := strings.TrimSpace(fm.VoiceSnippet); trimmed != "" {
		contact.VoiceSnippetPath = stringPtr(filepath.ToSlash(filepath.Join(folderRel, trimmed)))
	}
	if trimmed := strings.TrimSpace(fm.VoiceSignature); trimmed != "" {
		contact.SignatureEmbeddingPath = stringPtr(filepath.ToSlash(filepath.Join(folderRel, trimmed)))
	}

	return contact, nil
}

type contactFrontmatter struct {
	Format          string `yaml:"format"`
	ContactUID      string `yaml:"contact_uid"`
	Slug            string `yaml:"slug,omitempty"`
	Name            string `yaml:"name"`
	Phone           string `yaml:"phone,omitempty"`
	Email           string `yaml:"email,omitempty"`
	SignatureStatus string `yaml:"signature_status"`
	VoiceSnippet    string `yaml:"voice_snippet,omitempty"`
	VoiceSignature  string `yaml:"voice_signature,omitempty"`
	CreatedAt       string `yaml:"created_at,omitempty"`
	UpdatedAt       string `yaml:"updated_at,omitempty"`
}

func renderContactMarkdown(contact *models.Contact) ([]byte, error) {
	fm := contactFrontmatter{
		Format:          contactMarkdownFormat,
		ContactUID:      strings.TrimSpace(contact.ContactUID),
		Slug:            slug.Sanitize(contact.Slug, "contact"),
		Name:            strings.TrimSpace(contact.Name),
		SignatureStatus: defaultString(strings.TrimSpace(contact.SignatureStatus), "none"),
	}
	if contact.Phone != nil && strings.TrimSpace(*contact.Phone) != "" {
		fm.Phone = strings.TrimSpace(*contact.Phone)
	}
	if contact.Email != nil && strings.TrimSpace(*contact.Email) != "" {
		fm.Email = strings.TrimSpace(*contact.Email)
	}
	if contact.VoiceSnippetPath != nil && strings.TrimSpace(*contact.VoiceSnippetPath) != "" {
		fm.VoiceSnippet = filepath.Base(strings.TrimSpace(*contact.VoiceSnippetPath))
	}
	if contact.SignatureEmbeddingPath != nil && strings.TrimSpace(*contact.SignatureEmbeddingPath) != "" {
		fm.VoiceSignature = filepath.Base(strings.TrimSpace(*contact.SignatureEmbeddingPath))
	}
	if !contact.CreatedAt.IsZero() {
		fm.CreatedAt = contact.CreatedAt.UTC().Format(time.RFC3339)
	}
	if !contact.UpdatedAt.IsZero() {
		fm.UpdatedAt = contact.UpdatedAt.UTC().Format(time.RFC3339)
	}

	var buf bytes.Buffer
	buf.WriteString("---\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&fm); err != nil {
		return nil, err
	}
	_ = enc.Close()
	buf.WriteString("---\n\n")

	body := ""
	if contact.Notes != nil {
		body = strings.TrimSpace(*contact.Notes)
	}
	if body != "" {
		buf.WriteString(body)
		buf.WriteString("\n")
	}
	return buf.Bytes(), nil
}

func parseFrontmatter(content []byte) (contactFrontmatter, string, error) {
	var fm contactFrontmatter
	raw := string(content)
	lines := strings.Split(raw, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return fm, "", fmt.Errorf("frontmatter start delimiter is missing")
	}
	endIdx := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			endIdx = i
			break
		}
	}
	if endIdx == -1 {
		return fm, "", fmt.Errorf("frontmatter end delimiter is missing")
	}

	yamlBlock := strings.Join(lines[1:endIdx], "\n")
	if err := yaml.Unmarshal([]byte(yamlBlock), &fm); err != nil {
		return fm, "", err
	}
	body := strings.Join(lines[endIdx+1:], "\n")
	return fm, strings.TrimSpace(body), nil
}

func parseContactFolderName(name string) (string, string) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", ""
	}
	idx := strings.LastIndex(trimmed, "--")
	if idx <= 0 || idx+2 >= len(trimmed) {
		return "", ""
	}
	s := strings.TrimSpace(trimmed[:idx])
	uid := strings.TrimSpace(trimmed[idx+2:])
	return slug.Sanitize(s, "contact"), uid
}

func folderRelFromNotePath(notePath string) string {
	clean := filepath.ToSlash(strings.TrimSpace(notePath))
	if clean == "" {
		return ""
	}
	dir := pathDir(clean)
	if dir == "." || dir == "/" {
		return ""
	}
	return strings.TrimPrefix(dir, "./")
}

func pathDir(path string) string {
	idx := strings.LastIndex(path, "/")
	if idx == -1 {
		return "."
	}
	if idx == 0 {
		return "/"
	}
	return path[:idx]
}

func updateAssetPrefix(contact *models.Contact, oldFolderRel string, newFolderRel string) {
	rewrite := func(value *string) {
		if value == nil {
			return
		}
		trimmed := filepath.ToSlash(strings.TrimSpace(*value))
		if trimmed == "" {
			return
		}
		oldPrefix := strings.TrimSuffix(filepath.ToSlash(oldFolderRel), "/") + "/"
		newPrefix := strings.TrimSuffix(filepath.ToSlash(newFolderRel), "/") + "/"
		if strings.HasPrefix(trimmed, oldPrefix) {
			*value = filepath.ToSlash(strings.Replace(trimmed, oldPrefix, newPrefix, 1))
		}
	}
	rewrite(contact.VoiceSnippetPath)
	rewrite(contact.SignatureEmbeddingPath)
}

func normalizeAssetPaths(contact *models.Contact, folderRel string) {
	normalize := func(pathValue **string) {
		if *pathValue == nil {
			return
		}
		trimmed := strings.TrimSpace(**pathValue)
		if trimmed == "" {
			*pathValue = nil
			return
		}
		if filepath.IsAbs(trimmed) {
			return
		}
		if strings.Contains(trimmed, "/") {
			**pathValue = filepath.ToSlash(trimmed)
			return
		}
		**pathValue = filepath.ToSlash(filepath.Join(folderRel, trimmed))
	}
	normalize(&contact.VoiceSnippetPath)
	normalize(&contact.SignatureEmbeddingPath)
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func stringPtr(v string) *string {
	s := v
	return &s
}
