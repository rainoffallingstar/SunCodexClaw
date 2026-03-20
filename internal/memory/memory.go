package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var entryIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type Entry struct {
	ID        string   `json:"id"`
	Text      string   `json:"text"`
	Source    string   `json:"source,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

type Store struct {
	RepoRoot string
	Library  string
}

func NewStore(repoRoot string) *Store {
	return NewLibraryStore(repoRoot, "default")
}

func NewLibraryStore(repoRoot, library string) *Store {
	name := sanitizeLibrary(library)
	if name == "" {
		name = "default"
	}
	return &Store{RepoRoot: repoRoot, Library: name}
}

func (s *Store) memoryDir() string {
	return filepath.Join(s.RepoRoot, "config", "memory", "libraries", s.Library, "entries")
}

func (s *Store) EntryPath(id string) string {
	return filepath.Join(s.memoryDir(), id+".json")
}

func (s *Store) ListEntries() ([]Entry, error) {
	dir := s.memoryDir()
	ents, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Entry{}, nil
		}
		return nil, err
	}
	out := make([]Entry, 0, len(ents))
	for _, ent := range ents {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		entry, err := s.ReadEntry(strings.TrimSuffix(ent.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	sortEntries(out)
	return out, nil
}

func (s *Store) ReadEntry(id string) (Entry, error) {
	var entry Entry
	body, err := os.ReadFile(s.EntryPath(id))
	if err != nil {
		return entry, err
	}
	if err := json.Unmarshal(body, &entry); err != nil {
		return entry, err
	}
	if entry.ID == "" {
		entry.ID = id
	}
	return entry, nil
}

func (s *Store) WriteEntry(entry Entry) error {
	if err := ValidateEntry(entry); err != nil {
		return err
	}
	if err := os.MkdirAll(s.memoryDir(), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.EntryPath(entry.ID), append(body, '\n'), 0o644)
}

func (s *Store) DeleteEntry(id string) error {
	if err := os.Remove(s.EntryPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Store) Add(text, source string, tags []string) (Entry, error) {
	now := time.Now().UTC()
	entry := Entry{
		ID:        s.nextID(now),
		Text:      strings.TrimSpace(text),
		Source:    strings.TrimSpace(source),
		Tags:      normalizeStringSlice(tags),
		CreatedAt: now.Format(time.RFC3339),
		UpdatedAt: now.Format(time.RFC3339),
	}
	if err := s.WriteEntry(entry); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func (s *Store) Search(query string, limit int) ([]Entry, error) {
	entries, err := s.ListEntries()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	trimmedQuery := strings.TrimSpace(query)
	if trimmedQuery == "" {
		if len(entries) > limit {
			return entries[:limit], nil
		}
		return entries, nil
	}
	terms := strings.Fields(strings.ToLower(trimmedQuery))
	type rankedEntry struct {
		entry Entry
		score int
	}
	ranked := make([]rankedEntry, 0, len(entries))
	for _, entry := range entries {
		score, ok := entryMatchScore(entry, strings.ToLower(trimmedQuery), terms)
		if !ok {
			continue
		}
		ranked = append(ranked, rankedEntry{entry: entry, score: score})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		ti := entrySortTime(ranked[i].entry)
		tj := entrySortTime(ranked[j].entry)
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return ranked[i].entry.ID < ranked[j].entry.ID
	})
	out := make([]Entry, 0, min(limit, len(ranked)))
	for _, item := range ranked {
		if len(out) >= limit {
			break
		}
		out = append(out, item.entry)
	}
	return out, nil
}

func ValidateEntry(entry Entry) error {
	if !entryIDPattern.MatchString(strings.TrimSpace(entry.ID)) {
		return fmt.Errorf("invalid memory id %q", entry.ID)
	}
	if strings.TrimSpace(entry.Text) == "" {
		return fmt.Errorf("memory %s: text is required", entry.ID)
	}
	return nil
}

func (s *Store) nextID(now time.Time) string {
	base := now.UTC().Format("20060102-150405")
	for i := 0; i < 1000; i++ {
		id := fmt.Sprintf("mem-%s-%03d", base, i)
		if _, err := os.Stat(s.EntryPath(id)); errors.Is(err, os.ErrNotExist) {
			return id
		}
	}
	return fmt.Sprintf("mem-%s-%d", base, now.UTC().UnixNano())
}

func entryMatchScore(entry Entry, query string, terms []string) (int, bool) {
	searchable := strings.ToLower(strings.Join([]string{
		entry.ID,
		entry.Text,
		entry.Source,
		strings.Join(entry.Tags, " "),
	}, "\n"))
	score := 0
	if strings.Contains(searchable, query) {
		score += 3
	}
	for _, term := range terms {
		if !strings.Contains(searchable, term) {
			return 0, false
		}
		score++
	}
	if score == 0 {
		return 0, false
	}
	if strings.Contains(strings.ToLower(entry.Text), query) {
		score += 2
	}
	return score, true
}

func entrySortTime(entry Entry) time.Time {
	for _, raw := range []string{entry.UpdatedAt, entry.CreatedAt} {
		if ts, err := time.Parse(time.RFC3339, strings.TrimSpace(raw)); err == nil {
			return ts
		}
	}
	return time.Time{}
}

func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		ti := entrySortTime(entries[i])
		tj := entrySortTime(entries[j])
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return entries[i].ID < entries[j].ID
	})
}

func normalizeStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func sanitizeLibrary(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "\\", "-")
	text = strings.ReplaceAll(text, "/", "-")
	text = strings.ReplaceAll(text, " ", "-")
	text = strings.Trim(text, "-.")
	return text
}
