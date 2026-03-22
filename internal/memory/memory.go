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

const DefaultDuplicateMinScore = 100
const DefaultRecallCollapseMinScore = 140
const DefaultRememberDuplicateMinScore = 140

type Entry struct {
	ID               string   `json:"id"`
	Text             string   `json:"text"`
	Source           string   `json:"source,omitempty"`
	Tags             []string `json:"tags,omitempty"`
	Kind             string   `json:"kind,omitempty"`
	Priority         int      `json:"priority,omitempty"`
	Pinned           bool     `json:"pinned,omitempty"`
	Archived         bool     `json:"archived,omitempty"`
	ArchivedAt       string   `json:"archived_at,omitempty"`
	UseCount         int      `json:"use_count,omitempty"`
	LastUsedAt       string   `json:"last_used_at,omitempty"`
	ReinforceCount   int      `json:"reinforce_count,omitempty"`
	LastReinforcedAt string   `json:"last_reinforced_at,omitempty"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
}

type AddOptions struct {
	Source   string
	Tags     []string
	Kind     string
	Priority int
	Pinned   bool
}

type UpdateOptions struct {
	Text             *string
	Source           *string
	Tags             *[]string
	Kind             *string
	Priority         *int
	Pinned           *bool
	Archived         *bool
	ArchivedAt       *string
	ReinforceCount   *int
	LastReinforcedAt *string
}

type Store struct {
	RepoRoot string
	Library  string
}

type DuplicateMatch struct {
	Entry  Entry  `json:"entry"`
	Score  int    `json:"score"`
	Reason string `json:"reason"`
}

type DuplicateGroup struct {
	Keep  Entry             `json:"keep"`
	Score int               `json:"score"`
	Drops []DuplicateMatch  `json:"drops"`
}

type QueryOptions struct {
	IncludeArchived bool
	ArchivedOnly    bool
}

type ReviewOptions struct {
	Limit             int
	DuplicateMinScore int
	StaleDays         int
}

type PromoteSuggestion struct {
	Entry       Entry  `json:"entry"`
	Reason      string `json:"reason"`
	TargetPin   bool   `json:"target_pin"`
	TargetScore int    `json:"target_score"`
}

type StaleSuggestion struct {
	Entry    Entry  `json:"entry"`
	Reason   string `json:"reason"`
	AgeDays  int    `json:"age_days"`
	LastSeen string `json:"last_seen"`
}

type ReviewReport struct {
	TotalEntries       int                 `json:"total_entries"`
	DuplicateGroups    []DuplicateGroup    `json:"duplicate_groups"`
	PromoteSuggestions []PromoteSuggestion `json:"promote_suggestions"`
	StaleSuggestions   []StaleSuggestion   `json:"stale_suggestions"`
}

type PurgeCandidate struct {
	Entry      Entry  `json:"entry"`
	AgeDays    int    `json:"age_days"`
	ArchivedAt string `json:"archived_at"`
}

type RecallMatch struct {
	Entry   Entry     `json:"entry"`
	Score   int       `json:"score"`
	Reasons []string  `json:"reasons"`
}

type StatsReport struct {
	TotalEntries    int            `json:"total_entries"`
	ActiveEntries   int            `json:"active_entries"`
	ArchivedEntries int            `json:"archived_entries"`
	PinnedEntries   int            `json:"pinned_entries"`
	KindCounts      map[string]int `json:"kind_counts"`
	TopUsed         []Entry        `json:"top_used"`
	TopReinforced   []Entry        `json:"top_reinforced"`
	TopPriority     []Entry        `json:"top_priority"`
}

type RememberResult struct {
	Entry       Entry  `json:"entry"`
	Action      string `json:"action"`
	MatchScore  int    `json:"match_score,omitempty"`
	MatchReason string `json:"match_reason,omitempty"`
}

type ReviewApplyOptions struct {
	Promote      bool
	ArchiveStale bool
}

type ReviewApplyResult struct {
	Promoted []Entry `json:"promoted"`
	Archived []Entry `json:"archived"`
}

func NewStore(repoRoot string) *Store {
	return NewLibraryStore(repoRoot, "default")
}

func NewLibraryStore(repoRoot, library string) *Store {
	name := LibraryName(library)
	if name == "" {
		name = "default"
	}
	return &Store{RepoRoot: repoRoot, Library: name}
}

func LibraryName(raw string) string {
	return sanitizeLibrary(raw)
}

func (s *Store) memoryDir() string {
	return filepath.Join(s.RepoRoot, "config", "memory", "libraries", s.Library, "entries")
}

func (s *Store) EntryPath(id string) string {
	return filepath.Join(s.memoryDir(), id+".json")
}

func (s *Store) ListEntries() ([]Entry, error) {
	return s.ListEntriesWithOptions(QueryOptions{})
}

func (s *Store) ListEntriesAll() ([]Entry, error) {
	return s.ListEntriesWithOptions(QueryOptions{IncludeArchived: true})
}

func (s *Store) ListEntriesWithOptions(opts QueryOptions) ([]Entry, error) {
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
		if !includeEntry(entry, opts) {
			continue
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

func (s *Store) MergeEntries(keepID string, dropIDs []string) (Entry, []string, error) {
	keepID = strings.TrimSpace(keepID)
	if keepID == "" {
		return Entry{}, nil, fmt.Errorf("keep memory id is required")
	}
	keep, err := s.ReadEntry(keepID)
	if err != nil {
		return Entry{}, nil, err
	}
	uniqueDropIDs := make([]string, 0, len(dropIDs))
	seen := map[string]bool{keepID: true}
	for _, raw := range dropIDs {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		uniqueDropIDs = append(uniqueDropIDs, id)
	}
	if len(uniqueDropIDs) == 0 {
		return Entry{}, nil, fmt.Errorf("at least one memory id to merge is required")
	}
	drops := make([]Entry, 0, len(uniqueDropIDs))
	for _, id := range uniqueDropIDs {
		entry, err := s.ReadEntry(id)
		if err != nil {
			return Entry{}, nil, err
		}
		drops = append(drops, entry)
	}

	merged := keep
	merged.Tags = append([]string{}, keep.Tags...)
	for _, drop := range drops {
		merged.Tags = normalizeStringSlice(append(merged.Tags, drop.Tags...))
		if strongerKind(drop.Kind, merged.Kind) == normalizeKind(drop.Kind) {
			merged.Kind = normalizeKind(drop.Kind)
		}
		if normalizePriority(drop.Priority) > normalizePriority(merged.Priority) {
			merged.Priority = normalizePriority(drop.Priority)
		}
		merged.Pinned = merged.Pinned || drop.Pinned
		if strings.TrimSpace(merged.Source) == "" {
			merged.Source = strings.TrimSpace(drop.Source)
		}
		merged.UseCount += maxInt(drop.UseCount, 0)
		merged.ReinforceCount += maxInt(drop.ReinforceCount, 0)
		merged.LastUsedAt = latestTimestamp(merged.LastUsedAt, drop.LastUsedAt)
		merged.LastReinforcedAt = latestTimestamp(merged.LastReinforcedAt, drop.LastReinforcedAt)
		merged.UpdatedAt = latestTimestamp(merged.UpdatedAt, drop.UpdatedAt)
		merged.CreatedAt = earliestTimestamp(merged.CreatedAt, drop.CreatedAt)
		merged.Archived = merged.Archived && drop.Archived
		if merged.Archived {
			merged.ArchivedAt = latestTimestamp(merged.ArchivedAt, drop.ArchivedAt)
		} else {
			merged.ArchivedAt = ""
		}
	}
	merged.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.WriteEntry(merged); err != nil {
		return Entry{}, nil, err
	}
	for _, id := range uniqueDropIDs {
		if err := s.DeleteEntry(id); err != nil {
			return Entry{}, nil, err
		}
	}
	return merged, uniqueDropIDs, nil
}

func (s *Store) FindDuplicateGroups(limit int) ([]DuplicateGroup, error) {
	return s.FindDuplicateGroupsWithMinScore(limit, DefaultDuplicateMinScore)
}

func (s *Store) Review(opts ReviewOptions) (ReviewReport, error) {
	entries, err := s.ListEntries()
	if err != nil {
		return ReviewReport{}, err
	}
	if opts.Limit <= 0 {
		opts.Limit = 10
	}
	if opts.DuplicateMinScore <= 0 {
		opts.DuplicateMinScore = DefaultDuplicateMinScore
	}
	if opts.StaleDays <= 0 {
		opts.StaleDays = 30
	}
	duplicateGroups, err := s.FindDuplicateGroupsWithMinScore(opts.Limit, opts.DuplicateMinScore)
	if err != nil {
		return ReviewReport{}, err
	}
	excluded := reviewDuplicateEntrySet(duplicateGroups)
	report := ReviewReport{
		TotalEntries:       len(entries),
		DuplicateGroups:    duplicateGroups,
		PromoteSuggestions: reviewPromoteSuggestions(entries, opts.Limit, excluded),
		StaleSuggestions:   reviewStaleSuggestions(entries, opts.Limit, opts.StaleDays, excluded),
	}
	return report, nil
}

func (s *Store) ApplyReview(report ReviewReport, opts ReviewApplyOptions) (ReviewApplyResult, error) {
	result := ReviewApplyResult{}
	if opts.Promote {
		for _, item := range report.PromoteSuggestions {
			targetPinned := item.TargetPin || item.Entry.Pinned
			updated, err := s.UpdateEntry(item.Entry.ID, UpdateOptions{
				Priority: intPtrIfChanged(item.Entry.Priority, item.TargetScore),
				Pinned:   boolPtrIfChanged(item.Entry.Pinned, targetPinned),
			})
			if err != nil {
				return ReviewApplyResult{}, err
			}
			result.Promoted = append(result.Promoted, updated)
		}
	}
	if opts.ArchiveStale {
		now := time.Now().UTC().Format(time.RFC3339)
		for _, item := range report.StaleSuggestions {
			updated, err := s.UpdateEntry(item.Entry.ID, UpdateOptions{
				Archived:   boolPtrIfChanged(item.Entry.Archived, true),
				ArchivedAt: stringPtrIfChanged(strings.TrimSpace(item.Entry.ArchivedAt), now),
			})
			if err != nil {
				return ReviewApplyResult{}, err
			}
			result.Archived = append(result.Archived, updated)
		}
	}
	return result, nil
}

func (s *Store) FindArchivedPurgeCandidates(limit, olderThanDays int) ([]PurgeCandidate, error) {
	entries, err := s.ListEntriesWithOptions(QueryOptions{ArchivedOnly: true})
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	if olderThanDays <= 0 {
		olderThanDays = 30
	}
	now := time.Now().UTC()
	candidates := make([]PurgeCandidate, 0, min(limit, len(entries)))
	for _, entry := range entries {
		ageDays, archivedAt, ok := purgeCandidateAge(entry, now, olderThanDays)
		if !ok {
			continue
		}
		candidates = append(candidates, PurgeCandidate{
			Entry:      entry,
			AgeDays:    ageDays,
			ArchivedAt: archivedAt,
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].AgeDays != candidates[j].AgeDays {
			return candidates[i].AgeDays > candidates[j].AgeDays
		}
		return candidates[i].Entry.ID < candidates[j].Entry.ID
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates, nil
}

func (s *Store) PurgeArchivedCandidates(candidates []PurgeCandidate) ([]string, error) {
	deleted := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		id := strings.TrimSpace(candidate.Entry.ID)
		if id == "" {
			continue
		}
		if err := s.DeleteEntry(id); err != nil {
			return nil, err
		}
		deleted = append(deleted, id)
	}
	return deleted, nil
}

func (s *Store) FindRelatedEntries(id string, limit int) (Entry, []DuplicateMatch, error) {
	return s.FindRelatedEntriesWithMinScore(id, limit, DefaultDuplicateMinScore)
}

func (s *Store) FindBestDuplicateMatch(candidate Entry, opts QueryOptions, minScore int) (DuplicateMatch, bool, error) {
	entries, err := s.ListEntriesWithOptions(opts)
	if err != nil {
		return DuplicateMatch{}, false, err
	}
	match := findBestDuplicateMatch(entries, candidate, minScore)
	return match, match.Score > 0, nil
}

func (s *Store) FindRelatedEntriesWithMinScore(id string, limit int, minScore int) (Entry, []DuplicateMatch, error) {
	target, err := s.ReadEntry(strings.TrimSpace(id))
	if err != nil {
		return Entry{}, nil, err
	}
	entries, err := s.ListEntries()
	if err != nil {
		return Entry{}, nil, err
	}
	if limit <= 0 {
		limit = 10
	}
	if minScore <= 0 {
		minScore = DefaultDuplicateMinScore
	}
	matches := make([]DuplicateMatch, 0, min(limit, len(entries)))
	for _, entry := range entries {
		if entry.ID == target.ID {
			continue
		}
		score := duplicateScore(target, entry)
		if score < minScore {
			continue
		}
		matches = append(matches, DuplicateMatch{
			Entry:  entry,
			Score:  score,
			Reason: duplicateReason(target, entry),
		})
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		ti := entrySortTime(matches[i].Entry)
		tj := entrySortTime(matches[j].Entry)
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return matches[i].Entry.ID < matches[j].Entry.ID
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return target, matches, nil
}

func (s *Store) FindDuplicateGroupsWithMinScore(limit int, minScore int) ([]DuplicateGroup, error) {
	entries, err := s.ListEntries()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	if minScore <= 0 {
		minScore = DefaultDuplicateMinScore
	}
	used := map[string]bool{}
	groups := make([]DuplicateGroup, 0, min(limit, len(entries)))
	for i, keep := range entries {
		if used[keep.ID] {
			continue
		}
		group := DuplicateGroup{Keep: keep}
		scoreTotal := 0
		for j := i + 1; j < len(entries); j++ {
			drop := entries[j]
			if used[drop.ID] {
				continue
			}
			score := duplicateScore(keep, drop)
			if score < minScore {
				continue
			}
			group.Drops = append(group.Drops, DuplicateMatch{
				Entry:  drop,
				Score:  score,
				Reason: duplicateReason(keep, drop),
			})
			scoreTotal += score
			used[drop.ID] = true
		}
		if len(group.Drops) == 0 {
			continue
		}
		group.Score = scoreTotal / len(group.Drops)
		groups = append(groups, group)
		used[keep.ID] = true
		if len(groups) >= limit {
			break
		}
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Score != groups[j].Score {
			return groups[i].Score > groups[j].Score
		}
		return groups[i].Keep.ID < groups[j].Keep.ID
	})
	return groups, nil
}

func (s *Store) MarkUsed(ids []string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	seen := map[string]bool{}
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		entry, err := s.ReadEntry(id)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		entry.UseCount++
		entry.LastUsedAt = now
		entry.UpdatedAt = now
		if err := s.WriteEntry(entry); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) UpdateEntry(id string, opts UpdateOptions) (Entry, error) {
	entry, err := s.ReadEntry(id)
	if err != nil {
		return Entry{}, err
	}
	if opts.Text != nil {
		entry.Text = strings.TrimSpace(*opts.Text)
	}
	if opts.Source != nil {
		entry.Source = strings.TrimSpace(*opts.Source)
	}
	if opts.Tags != nil {
		entry.Tags = normalizeStringSlice(*opts.Tags)
	}
	if opts.Kind != nil {
		entry.Kind = normalizeKind(*opts.Kind)
	}
	if opts.Priority != nil {
		entry.Priority = normalizePriority(*opts.Priority)
	}
	if opts.Pinned != nil {
		entry.Pinned = *opts.Pinned
	}
	if opts.Archived != nil {
		entry.Archived = *opts.Archived
	}
	if opts.ArchivedAt != nil {
		entry.ArchivedAt = strings.TrimSpace(*opts.ArchivedAt)
	}
	if opts.ReinforceCount != nil {
		entry.ReinforceCount = *opts.ReinforceCount
	}
	if opts.LastReinforcedAt != nil {
		entry.LastReinforcedAt = strings.TrimSpace(*opts.LastReinforcedAt)
	}
	entry.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.WriteEntry(entry); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func (s *Store) Add(text, source string, tags []string) (Entry, error) {
	return s.AddWithOptions(text, AddOptions{
		Source: source,
		Tags:   tags,
	})
}

func (s *Store) Remember(text, source string, tags []string) (RememberResult, error) {
	return s.RememberWithOptions(text, AddOptions{
		Source: source,
		Tags:   tags,
	})
}

func (s *Store) AddWithOptions(text string, opts AddOptions) (Entry, error) {
	now := time.Now().UTC()
	entry := Entry{
		ID:        s.nextID(now),
		Text:      strings.TrimSpace(text),
		Source:    strings.TrimSpace(opts.Source),
		Tags:      normalizeStringSlice(opts.Tags),
		Kind:      normalizeKind(opts.Kind),
		Priority:  normalizePriority(opts.Priority),
		Pinned:    opts.Pinned,
		CreatedAt: now.Format(time.RFC3339),
		UpdatedAt: now.Format(time.RFC3339),
	}
	if err := s.WriteEntry(entry); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func (s *Store) RememberWithOptions(text string, opts AddOptions) (RememberResult, error) {
	candidate := Entry{
		Text:     strings.TrimSpace(text),
		Source:   strings.TrimSpace(opts.Source),
		Tags:     normalizeStringSlice(opts.Tags),
		Kind:     normalizeKind(opts.Kind),
		Priority: normalizePriority(opts.Priority),
		Pinned:   opts.Pinned,
	}
	match, ok, err := s.FindBestDuplicateMatch(candidate, QueryOptions{IncludeArchived: true}, DefaultRememberDuplicateMinScore)
	if err != nil {
		return RememberResult{}, err
	}
	if ok {
		updated, err := s.reinforceRememberedEntry(match.Entry, candidate)
		if err != nil {
			return RememberResult{}, err
		}
		return RememberResult{
			Entry:       updated,
			Action:      "reinforced",
			MatchScore:  match.Score,
			MatchReason: match.Reason,
		}, nil
	}
	entry, err := s.AddWithOptions(candidate.Text, AddOptions{
		Source:   candidate.Source,
		Tags:     candidate.Tags,
		Kind:     candidate.Kind,
		Priority: candidate.Priority,
		Pinned:   candidate.Pinned,
	})
	if err != nil {
		return RememberResult{}, err
	}
	return RememberResult{Entry: entry, Action: "added"}, nil
}

func (s *Store) Search(query string, limit int) ([]Entry, error) {
	return s.SearchWithOptions(query, limit, QueryOptions{})
}

func (s *Store) FindRecallMatches(query string, limit int) ([]RecallMatch, error) {
	return s.FindRecallMatchesWithOptions(query, limit, QueryOptions{})
}

func (s *Store) Stats(limit int) (StatsReport, error) {
	entries, err := s.ListEntriesAll()
	if err != nil {
		return StatsReport{}, err
	}
	if limit <= 0 {
		limit = 5
	}
	report := StatsReport{
		TotalEntries: len(entries),
		KindCounts:   map[string]int{},
	}
	for _, entry := range entries {
		if entry.Archived {
			report.ArchivedEntries++
		} else {
			report.ActiveEntries++
		}
		if entry.Pinned {
			report.PinnedEntries++
		}
		kind := normalizeKind(entry.Kind)
		if kind == "" {
			kind = "unknown"
		}
		report.KindCounts[kind]++
	}
	report.TopUsed = topEntriesBy(entries, limit, func(entry Entry) int {
		return entry.UseCount
	})
	report.TopReinforced = topEntriesBy(entries, limit, func(entry Entry) int {
		return entry.ReinforceCount
	})
	report.TopPriority = topEntriesBy(entries, limit, func(entry Entry) int {
		score := entry.Priority
		if entry.Pinned {
			score += 1000
		}
		return score
	})
	return report, nil
}

func (s *Store) FindRecallMatchesWithOptions(query string, limit int, opts QueryOptions) ([]RecallMatch, error) {
	entries, err := s.ListEntriesWithOptions(opts)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 4
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, nil
	}
	terms := recallTerms(query)
	scored := make([]RecallMatch, 0, len(entries))
	for _, entry := range entries {
		score, reasons := recallScore(entry, query, terms)
		if score <= 0 {
			continue
		}
		scored = append(scored, RecallMatch{Entry: entry, Score: score, Reasons: reasons})
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		if scored[i].Entry.Pinned != scored[j].Entry.Pinned {
			return scored[i].Entry.Pinned
		}
		if scored[i].Entry.Priority != scored[j].Entry.Priority {
			return scored[i].Entry.Priority > scored[j].Entry.Priority
		}
		ti := entrySortTime(scored[i].Entry)
		tj := entrySortTime(scored[j].Entry)
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return scored[i].Entry.ID < scored[j].Entry.ID
	})
	scored = collapseRecallMatches(scored, DefaultRecallCollapseMinScore)
	if len(scored) > limit {
		scored = scored[:limit]
	}
	return scored, nil
}

func (s *Store) SearchWithOptions(query string, limit int, opts QueryOptions) ([]Entry, error) {
	entries, err := s.ListEntries()
	if err != nil {
		return nil, err
	}
	if opts.IncludeArchived || opts.ArchivedOnly {
		entries, err = s.ListEntriesWithOptions(opts)
		if err != nil {
			return nil, err
		}
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
	if entry.Kind != "" {
		switch normalizeKind(entry.Kind) {
		case "preference", "rule", "note":
		default:
			return fmt.Errorf("memory %s: invalid kind %q", entry.ID, entry.Kind)
		}
	}
	if entry.Priority < 0 || entry.Priority > 100 {
		return fmt.Errorf("memory %s: priority must be between 0 and 100", entry.ID)
	}
	if entry.UseCount < 0 {
		return fmt.Errorf("memory %s: use_count must be non-negative", entry.ID)
	}
	if entry.ReinforceCount < 0 {
		return fmt.Errorf("memory %s: reinforce_count must be non-negative", entry.ID)
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
		entry.Kind,
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
	score += normalizePriority(entry.Priority)
	score += reinforcementScore(entry.ReinforceCount)
	score += usageScore(entry.UseCount)
	if entry.Pinned {
		score += 5
	}
	return score, true
}

func entrySortTime(entry Entry) time.Time {
	for _, raw := range []string{entry.LastUsedAt, entry.LastReinforcedAt, entry.UpdatedAt, entry.CreatedAt} {
		if ts, err := time.Parse(time.RFC3339, strings.TrimSpace(raw)); err == nil {
			return ts
		}
	}
	return time.Time{}
}

func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Archived != entries[j].Archived {
			return !entries[i].Archived
		}
		if entries[i].Pinned != entries[j].Pinned {
			return entries[i].Pinned
		}
		if normalizePriority(entries[i].Priority) != normalizePriority(entries[j].Priority) {
			return normalizePriority(entries[i].Priority) > normalizePriority(entries[j].Priority)
		}
		if entries[i].ReinforceCount != entries[j].ReinforceCount {
			return entries[i].ReinforceCount > entries[j].ReinforceCount
		}
		if entries[i].UseCount != entries[j].UseCount {
			return entries[i].UseCount > entries[j].UseCount
		}
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

func normalizeKind(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return ""
	case "preference":
		return "preference"
	case "rule":
		return "rule"
	case "note":
		return "note"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

func strongerKind(left, right string) string {
	score := func(value string) int {
		switch normalizeKind(value) {
		case "preference":
			return 3
		case "rule":
			return 2
		case "note":
			return 1
		default:
			return 0
		}
	}
	if score(left) >= score(right) {
		return normalizeKind(left)
	}
	return normalizeKind(right)
}

func normalizePriority(value int) int {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func usageScore(value int) int {
	if value <= 0 {
		return 0
	}
	if value > 10 {
		return 10
	}
	return value
}

func reinforcementScore(value int) int {
	if value <= 0 {
		return 0
	}
	if value > 10 {
		return 10
	}
	return value
}

func reviewPromoteSuggestions(entries []Entry, limit int, excluded map[string]bool) []PromoteSuggestion {
	suggestions := make([]PromoteSuggestion, 0, min(limit, len(entries)))
	for _, entry := range entries {
		if excluded[strings.TrimSpace(entry.ID)] {
			continue
		}
		reason, targetPin, targetScore, ok := reviewPromoteReason(entry)
		if !ok {
			continue
		}
		suggestions = append(suggestions, PromoteSuggestion{
			Entry:       entry,
			Reason:      reason,
			TargetPin:   targetPin,
			TargetScore: targetScore,
		})
	}
	sort.Slice(suggestions, func(i, j int) bool {
		if suggestions[i].TargetScore != suggestions[j].TargetScore {
			return suggestions[i].TargetScore > suggestions[j].TargetScore
		}
		if suggestions[i].Entry.Priority != suggestions[j].Entry.Priority {
			return suggestions[i].Entry.Priority > suggestions[j].Entry.Priority
		}
		return suggestions[i].Entry.ID < suggestions[j].Entry.ID
	})
	if len(suggestions) > limit {
		suggestions = suggestions[:limit]
	}
	return suggestions
}

func reviewPromoteReason(entry Entry) (reason string, targetPin bool, targetScore int, ok bool) {
	kind := normalizeKind(entry.Kind)
	switch {
	case kind == "preference" && !entry.Pinned:
		return "durable_preference", true, maxInt(normalizePriority(entry.Priority), 80), true
	case entry.ReinforceCount >= 2 && (!entry.Pinned || normalizePriority(entry.Priority) < 80):
		return "repeatedly_reinforced", true, maxInt(normalizePriority(entry.Priority), 80), true
	case entry.UseCount >= 3 && (!entry.Pinned || normalizePriority(entry.Priority) < 70):
		return "frequently_recalled", kind == "preference", maxInt(normalizePriority(entry.Priority), 70), true
	case kind == "rule" && normalizePriority(entry.Priority) >= 70 && !entry.Pinned:
		return "durable_rule", true, maxInt(normalizePriority(entry.Priority), 75), true
	default:
		return "", false, 0, false
	}
}

func reviewStaleSuggestions(entries []Entry, limit, staleDays int, excluded map[string]bool) []StaleSuggestion {
	now := time.Now().UTC()
	suggestions := make([]StaleSuggestion, 0, min(limit, len(entries)))
	for _, entry := range entries {
		if excluded[strings.TrimSpace(entry.ID)] {
			continue
		}
		ageDays, lastSeen, ok := reviewStaleReason(entry, now, staleDays)
		if !ok {
			continue
		}
		suggestions = append(suggestions, StaleSuggestion{
			Entry:    entry,
			Reason:   "stale_unused_note",
			AgeDays:  ageDays,
			LastSeen: lastSeen,
		})
	}
	sort.Slice(suggestions, func(i, j int) bool {
		if suggestions[i].AgeDays != suggestions[j].AgeDays {
			return suggestions[i].AgeDays > suggestions[j].AgeDays
		}
		if suggestions[i].Entry.Priority != suggestions[j].Entry.Priority {
			return suggestions[i].Entry.Priority < suggestions[j].Entry.Priority
		}
		return suggestions[i].Entry.ID < suggestions[j].Entry.ID
	})
	if len(suggestions) > limit {
		suggestions = suggestions[:limit]
	}
	return suggestions
}

func reviewStaleReason(entry Entry, now time.Time, staleDays int) (ageDays int, lastSeen string, ok bool) {
	if entry.Archived || entry.Pinned || entry.UseCount > 0 || entry.ReinforceCount > 0 || normalizePriority(entry.Priority) > 30 {
		return 0, "", false
	}
	kind := normalizeKind(entry.Kind)
	if kind != "" && kind != "note" {
		return 0, "", false
	}
	seenAt := entrySortTime(entry)
	if seenAt.IsZero() {
		return 0, "", false
	}
	ageDays = int(now.Sub(seenAt).Hours() / 24)
	if ageDays < staleDays {
		return 0, "", false
	}
	return ageDays, seenAt.Format(time.RFC3339), true
}

func reviewDuplicateEntrySet(groups []DuplicateGroup) map[string]bool {
	seen := make(map[string]bool, len(groups)*2)
	for _, group := range groups {
		seen[strings.TrimSpace(group.Keep.ID)] = true
		for _, drop := range group.Drops {
			seen[strings.TrimSpace(drop.Entry.ID)] = true
		}
	}
	return seen
}

func purgeCandidateAge(entry Entry, now time.Time, olderThanDays int) (ageDays int, archivedAt string, ok bool) {
	if !entry.Archived {
		return 0, "", false
	}
	at := strings.TrimSpace(entry.ArchivedAt)
	if at == "" {
		at = strings.TrimSpace(entry.UpdatedAt)
	}
	ts, valid := parseTimestamp(at)
	if !valid {
		return 0, "", false
	}
	ageDays = int(now.Sub(ts).Hours() / 24)
	if ageDays < olderThanDays {
		return 0, "", false
	}
	return ageDays, ts.Format(time.RFC3339), true
}

func topEntriesBy(entries []Entry, limit int, scoreFn func(Entry) int) []Entry {
	scored := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if scoreFn(entry) <= 0 {
			continue
		}
		scored = append(scored, entry)
	}
	sort.Slice(scored, func(i, j int) bool {
		si := scoreFn(scored[i])
		sj := scoreFn(scored[j])
		if si != sj {
			return si > sj
		}
		if !entrySortTime(scored[i]).Equal(entrySortTime(scored[j])) {
			return entrySortTime(scored[i]).After(entrySortTime(scored[j]))
		}
		return scored[i].ID < scored[j].ID
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	return scored
}

func includeEntry(entry Entry, opts QueryOptions) bool {
	if opts.ArchivedOnly {
		return entry.Archived
	}
	if opts.IncludeArchived {
		return true
	}
	return !entry.Archived
}

func intPtrIfChanged(current, next int) *int {
	if current == next {
		return nil
	}
	return &next
}

func boolPtrIfChanged(current, next bool) *bool {
	if current == next {
		return nil
	}
	return &next
}

func stringPtrIfChanged(current, next string) *string {
	if strings.TrimSpace(current) == strings.TrimSpace(next) {
		return nil
	}
	value := strings.TrimSpace(next)
	return &value
}

func stringSlicePtrIfChanged(current, next []string) *[]string {
	currentNormalized := normalizeStringSlice(current)
	nextNormalized := normalizeStringSlice(next)
	if len(currentNormalized) == len(nextNormalized) {
		same := true
		for i := range currentNormalized {
			if currentNormalized[i] != nextNormalized[i] {
				same = false
				break
			}
		}
		if same {
			return nil
		}
	}
	return &nextNormalized
}

func recallScore(entry Entry, query string, terms []string) (int, []string) {
	searchable := strings.ToLower(strings.Join([]string{
		entry.ID,
		entry.Text,
		entry.Source,
		entry.Kind,
		strings.Join(entry.Tags, " "),
	}, "\n"))
	score := 0
	reasons := make([]string, 0, 8)
	if strings.Contains(searchable, query) {
		score += 12
		reasons = append(reasons, "query_substring")
	}
	for _, term := range terms {
		if strings.Contains(searchable, term) {
			score += 2
			reasons = append(reasons, "term:"+term)
			if strings.Contains(strings.ToLower(entry.Text), term) {
				score += 1
				reasons = append(reasons, "text_term:"+term)
			}
		}
	}
	if entry.Priority > 0 {
		score += entry.Priority
		reasons = append(reasons, fmt.Sprintf("priority:%d", entry.Priority))
	}
	if entry.ReinforceCount > 0 {
		boost := min(entry.ReinforceCount, 10)
		score += boost
		reasons = append(reasons, fmt.Sprintf("reinforce:%d", boost))
	}
	if entry.UseCount > 0 {
		boost := min(entry.UseCount, 10)
		score += boost
		reasons = append(reasons, fmt.Sprintf("use:%d", boost))
	}
	if entry.Pinned {
		score += 15
		reasons = append(reasons, "pinned")
	}
	return score, normalizeStringSlice(reasons)
}

func recallTerms(query string) []string {
	seen := map[string]bool{}
	terms := []string{}
	add := func(term string) {
		term = strings.TrimSpace(term)
		if len([]rune(term)) < 2 || seen[term] {
			return
		}
		seen[term] = true
		terms = append(terms, term)
	}
	for _, field := range strings.Fields(query) {
		add(field)
	}
	runes := []rune(query)
	for i := 0; i+1 < len(runes); i++ {
		pair := strings.TrimSpace(string(runes[i : i+2]))
		if strings.Contains(pair, "<") || strings.Contains(pair, ">") {
			continue
		}
		add(pair)
	}
	return terms
}

func collapseRecallMatches(matches []RecallMatch, minScore int) []RecallMatch {
	if len(matches) <= 1 {
		return matches
	}
	if minScore <= 0 {
		minScore = DefaultRecallCollapseMinScore
	}
	kept := make([]RecallMatch, 0, len(matches))
	suppressedCounts := make([]int, 0, len(matches))
	for _, match := range matches {
		collapsed := false
		for i := range kept {
			if duplicateScore(kept[i].Entry, match.Entry) < minScore {
				continue
			}
			suppressedCounts[i]++
			collapsed = true
			break
		}
		if collapsed {
			continue
		}
		kept = append(kept, match)
		suppressedCounts = append(suppressedCounts, 0)
	}
	for i := range kept {
		if suppressedCounts[i] <= 0 {
			continue
		}
		kept[i].Reasons = append(kept[i].Reasons, fmt.Sprintf("collapsed_similar:%d", suppressedCounts[i]))
		kept[i].Reasons = normalizeStringSlice(kept[i].Reasons)
	}
	return kept
}

func findBestDuplicateMatch(entries []Entry, candidate Entry, minScore int) DuplicateMatch {
	if minScore <= 0 {
		minScore = DefaultRememberDuplicateMinScore
	}
	bestScore := 0
	bestIndex := -1
	bestReason := ""
	for i, entry := range entries {
		if strings.TrimSpace(entry.ID) != "" && strings.TrimSpace(entry.ID) == strings.TrimSpace(candidate.ID) {
			continue
		}
		score := duplicateScore(entry, candidate)
		if score < minScore || score < bestScore {
			continue
		}
		reason := duplicateReason(entry, candidate)
		if score == bestScore && bestIndex >= 0 {
			continue
		}
		bestScore = score
		bestIndex = i
		bestReason = reason
	}
	if bestIndex < 0 {
		return DuplicateMatch{}
	}
	return DuplicateMatch{
		Entry:  entries[bestIndex],
		Score:  bestScore,
		Reason: bestReason,
	}
}

func (s *Store) reinforceRememberedEntry(entry Entry, candidate Entry) (Entry, error) {
	nextTags := normalizeStringSlice(append(append([]string{}, entry.Tags...), candidate.Tags...))
	nextKind := strongerKind(candidate.Kind, entry.Kind)
	nextPriority := maxInt(normalizePriority(entry.Priority), normalizePriority(candidate.Priority))
	if normalizePriority(candidate.Priority) <= normalizePriority(entry.Priority) && nextPriority < 100 {
		nextPriority += 5
		if nextPriority > 100 {
			nextPriority = 100
		}
	}
	nextPinned := entry.Pinned || candidate.Pinned
	nextArchived := false
	nextArchivedAt := ""
	nextSource := strings.TrimSpace(entry.Source)
	if nextSource == "" {
		nextSource = strings.TrimSpace(candidate.Source)
	}
	nextReinforceCount := entry.ReinforceCount + 1
	nextLastReinforcedAt := time.Now().UTC().Format(time.RFC3339)
	updated, err := s.UpdateEntry(entry.ID, UpdateOptions{
		Source:           stringPtrIfChanged(strings.TrimSpace(entry.Source), nextSource),
		Tags:             stringSlicePtrIfChanged(entry.Tags, nextTags),
		Kind:             stringPtrIfChanged(strings.TrimSpace(entry.Kind), nextKind),
		Priority:         intPtrIfChanged(entry.Priority, nextPriority),
		Pinned:           boolPtrIfChanged(entry.Pinned, nextPinned),
		Archived:         boolPtrIfChanged(entry.Archived, nextArchived),
		ArchivedAt:       stringPtrIfChanged(strings.TrimSpace(entry.ArchivedAt), nextArchivedAt),
		ReinforceCount:   intPtrIfChanged(entry.ReinforceCount, nextReinforceCount),
		LastReinforcedAt: stringPtrIfChanged(strings.TrimSpace(entry.LastReinforcedAt), nextLastReinforcedAt),
	})
	if err != nil {
		return Entry{}, err
	}
	return updated, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func latestTimestamp(left, right string) string {
	lt, lok := parseTimestamp(left)
	rt, rok := parseTimestamp(right)
	switch {
	case lok && rok:
		if rt.After(lt) {
			return rt.Format(time.RFC3339)
		}
		return lt.Format(time.RFC3339)
	case lok:
		return lt.Format(time.RFC3339)
	case rok:
		return rt.Format(time.RFC3339)
	default:
		if strings.TrimSpace(left) != "" {
			return strings.TrimSpace(left)
		}
		return strings.TrimSpace(right)
	}
}

func duplicateScore(left, right Entry) int {
	leftText := normalizeDuplicateComparable(left.Text)
	rightText := normalizeDuplicateComparable(right.Text)
	if leftText == "" || rightText == "" {
		return 0
	}
	score := 0
	if leftText == rightText {
		score += 120
	}
	leftCore := duplicateCoreComparable(left.Text)
	rightCore := duplicateCoreComparable(right.Text)
	if leftCore != "" && rightCore != "" {
		if leftCore == rightCore {
			score += 100
		}
		if strings.Contains(leftCore, rightCore) || strings.Contains(rightCore, leftCore) {
			shorter := []rune(leftCore)
			if len([]rune(rightCore)) < len(shorter) {
				shorter = []rune(rightCore)
			}
			if len(shorter) >= 4 {
				score += 80
			}
		}
	}
	shared, total := sharedDuplicateTerms(leftText, rightText)
	if total > 0 {
		score += (shared * 50) / total
	}
	if strongerKind(left.Kind, right.Kind) == normalizeKind(left.Kind) &&
		normalizeKind(left.Kind) == normalizeKind(right.Kind) &&
		normalizeKind(left.Kind) != "" {
		score += 10
	}
	return score
}

func duplicateReason(left, right Entry) string {
	leftText := normalizeDuplicateComparable(left.Text)
	rightText := normalizeDuplicateComparable(right.Text)
	if leftText != "" && leftText == rightText {
		return "exact_text"
	}
	leftCore := duplicateCoreComparable(left.Text)
	rightCore := duplicateCoreComparable(right.Text)
	if leftCore != "" && rightCore != "" {
		if leftCore == rightCore {
			return "same_core"
		}
		if strings.Contains(leftCore, rightCore) || strings.Contains(rightCore, leftCore) {
			return "core_contains"
		}
	}
	return "term_overlap"
}

func earliestTimestamp(left, right string) string {
	lt, lok := parseTimestamp(left)
	rt, rok := parseTimestamp(right)
	switch {
	case lok && rok:
		if rt.Before(lt) {
			return rt.Format(time.RFC3339)
		}
		return lt.Format(time.RFC3339)
	case lok:
		return lt.Format(time.RFC3339)
	case rok:
		return rt.Format(time.RFC3339)
	default:
		if strings.TrimSpace(left) != "" {
			return strings.TrimSpace(left)
		}
		return strings.TrimSpace(right)
	}
}

func parseTimestamp(raw string) (time.Time, bool) {
	ts, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
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

func normalizeDuplicateComparable(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(text)), " "))
}

func duplicateCoreComparable(text string) string {
	normalized := strings.ToLower(strings.TrimSpace(text))
	replacer := strings.NewReplacer(
		"，", " ", "。", " ", "！", " ", "？", " ", "：", " ", "；", " ",
		",", " ", ".", " ", "!", " ", "?", " ", ":", " ", ";", " ",
		"（", " ", "）", " ", "(", " ", ")", " ", "\"", " ", "'", " ",
	)
	normalized = replacer.Replace(normalized)
	for _, token := range []string{
		"请记住", "记住", "记一下", "你要记住",
		"以后", "今后", "默认", "请用", "请按", "请始终", "统一用", "优先用", "用",
		"回复", "回答", "一下",
		"remember", "default", "please", "reply", "respond", "always",
	} {
		normalized = strings.ReplaceAll(normalized, token, " ")
	}
	return strings.Join(strings.Fields(normalized), "")
}

func sharedDuplicateTerms(left, right string) (shared int, total int) {
	leftTerms := duplicateTerms(left)
	rightTerms := duplicateTerms(right)
	if len(leftTerms) == 0 || len(rightTerms) == 0 {
		return 0, 0
	}
	leftSet := map[string]bool{}
	rightSet := map[string]bool{}
	for _, term := range leftTerms {
		leftSet[term] = true
	}
	for _, term := range rightTerms {
		rightSet[term] = true
	}
	union := map[string]bool{}
	for term := range leftSet {
		union[term] = true
		if rightSet[term] {
			shared++
		}
	}
	for term := range rightSet {
		union[term] = true
	}
	return shared, len(union)
}

func duplicateTerms(text string) []string {
	seen := map[string]bool{}
	terms := []string{}
	add := func(term string) {
		term = strings.TrimSpace(term)
		if len([]rune(term)) < 2 || seen[term] {
			return
		}
		seen[term] = true
		terms = append(terms, term)
	}
	for _, field := range strings.Fields(text) {
		add(field)
	}
	runes := []rune(strings.ReplaceAll(text, " ", ""))
	for i := 0; i+1 < len(runes); i++ {
		add(string(runes[i : i+2]))
	}
	return terms
}
