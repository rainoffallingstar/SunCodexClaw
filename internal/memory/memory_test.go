package memory

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStoreAddListShowDelete(t *testing.T) {
	store := NewStore(t.TempDir())

	entry, err := store.AddWithOptions("Remember the default language is Chinese.", AddOptions{
		Source:   "feishu/assistant/oc_demo",
		Tags:     []string{"lang", "default"},
		Kind:     "preference",
		Priority: 80,
		Pinned:   true,
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if entry.ID == "" {
		t.Fatalf("Add() returned empty id")
	}

	got, err := store.ReadEntry(entry.ID)
	if err != nil {
		t.Fatalf("ReadEntry() error = %v", err)
	}
	if got.Text != entry.Text {
		t.Fatalf("ReadEntry().Text = %q, want %q", got.Text, entry.Text)
	}
	if !got.Pinned || got.Priority != 80 || got.Kind != "preference" {
		t.Fatalf("ReadEntry() metadata = %#v", got)
	}

	entries, err := store.ListEntries()
	if err != nil {
		t.Fatalf("ListEntries() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("ListEntries() len = %d, want 1", len(entries))
	}

	if err := store.DeleteEntry(entry.ID); err != nil {
		t.Fatalf("DeleteEntry() error = %v", err)
	}
	entries, err = store.ListEntries()
	if err != nil {
		t.Fatalf("ListEntries() after delete error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("ListEntries() after delete len = %d, want 0", len(entries))
	}
}

func TestRememberWithOptionsReinforcesHighConfidenceDuplicate(t *testing.T) {
	store := NewStore(t.TempDir())
	existing := Entry{
		ID:        "mem-existing",
		Text:      "以后默认用简体中文回复",
		Kind:      "note",
		Priority:  20,
		CreatedAt: "2026-03-20T00:00:00Z",
		UpdatedAt: "2026-03-20T00:00:00Z",
	}
	if err := store.WriteEntry(existing); err != nil {
		t.Fatalf("WriteEntry(existing) error = %v", err)
	}

	result, err := store.RememberWithOptions("以后默认用简体中文回复", AddOptions{
		Source:   "feishu/assistant/oc_demo",
		Tags:     []string{"lang"},
		Kind:     "preference",
		Priority: 80,
		Pinned:   true,
	})
	if err != nil {
		t.Fatalf("RememberWithOptions() error = %v", err)
	}
	if result.Action != "reinforced" {
		t.Fatalf("result.Action = %q, want reinforced", result.Action)
	}
	if result.Entry.ID != existing.ID {
		t.Fatalf("result.Entry.ID = %q, want %q", result.Entry.ID, existing.ID)
	}
	if result.MatchScore < DefaultRememberDuplicateMinScore {
		t.Fatalf("result.MatchScore = %d, want >= %d", result.MatchScore, DefaultRememberDuplicateMinScore)
	}
	if result.Entry.Kind != "preference" || result.Entry.Priority != 80 || !result.Entry.Pinned {
		t.Fatalf("result.Entry metadata = %#v", result.Entry)
	}
	if result.Entry.ReinforceCount != 1 || result.Entry.LastReinforcedAt == "" {
		t.Fatalf("result.Entry reinforce tracking = %#v", result.Entry)
	}
	if !strings.Contains(strings.Join(result.Entry.Tags, ","), "lang") {
		t.Fatalf("result.Entry.Tags = %v, want lang", result.Entry.Tags)
	}

	entries, err := store.ListEntriesAll()
	if err != nil {
		t.Fatalf("ListEntriesAll() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("ListEntriesAll() len = %d, want 1", len(entries))
	}
}

func TestRememberWithOptionsGentlyBoostsPriorityWhenReinforcingWithoutExplicitPriority(t *testing.T) {
	store := NewStore(t.TempDir())
	existing := Entry{
		ID:        "mem-existing",
		Text:      "以后默认用简体中文回复",
		Priority:  20,
		CreatedAt: "2026-03-20T00:00:00Z",
		UpdatedAt: "2026-03-20T00:00:00Z",
	}
	if err := store.WriteEntry(existing); err != nil {
		t.Fatalf("WriteEntry(existing) error = %v", err)
	}

	result, err := store.RememberWithOptions("以后默认用简体中文回复", AddOptions{})
	if err != nil {
		t.Fatalf("RememberWithOptions() error = %v", err)
	}
	if result.Action != "reinforced" {
		t.Fatalf("result.Action = %q, want reinforced", result.Action)
	}
	if result.Entry.Priority != 25 {
		t.Fatalf("result.Entry.Priority = %d, want 25", result.Entry.Priority)
	}
}

func TestRememberWithOptionsKeepsExplicitPriorityWithoutExtraBoost(t *testing.T) {
	store := NewStore(t.TempDir())
	existing := Entry{
		ID:        "mem-existing",
		Text:      "以后默认用简体中文回复",
		Priority:  20,
		CreatedAt: "2026-03-20T00:00:00Z",
		UpdatedAt: "2026-03-20T00:00:00Z",
	}
	if err := store.WriteEntry(existing); err != nil {
		t.Fatalf("WriteEntry(existing) error = %v", err)
	}

	result, err := store.RememberWithOptions("以后默认用简体中文回复", AddOptions{Priority: 80})
	if err != nil {
		t.Fatalf("RememberWithOptions() error = %v", err)
	}
	if result.Action != "reinforced" {
		t.Fatalf("result.Action = %q, want reinforced", result.Action)
	}
	if result.Entry.Priority != 80 {
		t.Fatalf("result.Entry.Priority = %d, want 80", result.Entry.Priority)
	}
}

func TestRememberWithOptionsRevivesArchivedDuplicate(t *testing.T) {
	store := NewStore(t.TempDir())
	archived := Entry{
		ID:         "mem-archived",
		Text:       "以后默认用简体中文回复",
		Archived:   true,
		ArchivedAt: "2026-03-01T00:00:00Z",
		CreatedAt:  "2026-03-01T00:00:00Z",
		UpdatedAt:  "2026-03-01T00:00:00Z",
	}
	if err := store.WriteEntry(archived); err != nil {
		t.Fatalf("WriteEntry(archived) error = %v", err)
	}

	result, err := store.RememberWithOptions("以后默认用简体中文回复", AddOptions{})
	if err != nil {
		t.Fatalf("RememberWithOptions() error = %v", err)
	}
	if result.Action != "reinforced" {
		t.Fatalf("result.Action = %q, want reinforced", result.Action)
	}
	if result.Entry.Archived || result.Entry.ArchivedAt != "" {
		t.Fatalf("result.Entry archive fields = archived:%t archived_at:%q", result.Entry.Archived, result.Entry.ArchivedAt)
	}
}

func TestRememberHelperUsesConservativeDeduplication(t *testing.T) {
	store := NewStore(t.TempDir())
	first, err := store.Remember("以后默认用简体中文回复", "feishu/assistant/oc_1", []string{"language"})
	if err != nil {
		t.Fatalf("Remember(first) error = %v", err)
	}
	if first.Action != "added" {
		t.Fatalf("first.Action = %q, want added", first.Action)
	}

	second, err := store.Remember("以后默认用简体中文回复", "feishu/assistant/oc_1", []string{"language"})
	if err != nil {
		t.Fatalf("Remember(second) error = %v", err)
	}
	if second.Action != "reinforced" {
		t.Fatalf("second.Action = %q, want reinforced", second.Action)
	}
	if second.Entry.ID != first.Entry.ID {
		t.Fatalf("second.Entry.ID = %q, want %q", second.Entry.ID, first.Entry.ID)
	}
}

func TestFindBestDuplicateMatchFindsHighestScoringEntry(t *testing.T) {
	store := NewStore(t.TempDir())
	for _, entry := range []Entry{
		{
			ID:        "mem-best",
			Text:      "以后默认用简体中文回复",
			Kind:      "preference",
			CreatedAt: "2026-03-20T00:00:00Z",
			UpdatedAt: "2026-03-20T00:00:00Z",
		},
		{
			ID:        "mem-weaker",
			Text:      "以后默认中文回复",
			Kind:      "note",
			CreatedAt: "2026-03-19T00:00:00Z",
			UpdatedAt: "2026-03-19T00:00:00Z",
		},
	} {
		if err := store.WriteEntry(entry); err != nil {
			t.Fatalf("WriteEntry(%s) error = %v", entry.ID, err)
		}
	}

	match, ok, err := store.FindBestDuplicateMatch(Entry{
		Text: "以后默认用简体中文回复",
		Kind: "preference",
	}, QueryOptions{IncludeArchived: true}, DefaultRememberDuplicateMinScore)
	if err != nil {
		t.Fatalf("FindBestDuplicateMatch() error = %v", err)
	}
	if !ok {
		t.Fatalf("FindBestDuplicateMatch() ok = false, want true")
	}
	if match.Entry.ID != "mem-best" {
		t.Fatalf("match.Entry.ID = %q, want mem-best", match.Entry.ID)
	}
	if match.Score < DefaultRememberDuplicateMinScore {
		t.Fatalf("match.Score = %d, want >= %d", match.Score, DefaultRememberDuplicateMinScore)
	}
	if match.Reason == "" {
		t.Fatalf("match.Reason is empty")
	}
}

func TestSearchMatchesAllTermsAndOrdersByScoreThenTime(t *testing.T) {
	store := NewStore(t.TempDir())
	older := Entry{
		ID:        "mem-older",
		Text:      "The bot should remember using Chinese in team chats.",
		Source:    "feishu/assistant/oc_old",
		Tags:      []string{"lang", "team"},
		CreatedAt: "2026-03-19T00:00:00Z",
		UpdatedAt: "2026-03-19T00:00:00Z",
	}
	newer := Entry{
		ID:        "mem-newer",
		Text:      "Remember: default reply language is Chinese and keep answers concise.",
		Source:    "feishu/assistant/oc_new",
		Tags:      []string{"lang", "reply"},
		CreatedAt: "2026-03-20T00:00:00Z",
		UpdatedAt: "2026-03-20T00:00:00Z",
	}
	if err := store.WriteEntry(older); err != nil {
		t.Fatalf("WriteEntry(older) error = %v", err)
	}
	if err := store.WriteEntry(newer); err != nil {
		t.Fatalf("WriteEntry(newer) error = %v", err)
	}

	results, err := store.Search("reply chinese", 10)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Search() len = %d, want 1", len(results))
	}
	if results[0].ID != newer.ID {
		t.Fatalf("Search()[0].ID = %q, want %q", results[0].ID, newer.ID)
	}
}

func TestSearchBlankQueryReturnsNewestFirst(t *testing.T) {
	store := NewStore(t.TempDir())
	first := Entry{
		ID:        "mem-first",
		Text:      "first",
		CreatedAt: "2026-03-19T00:00:00Z",
		UpdatedAt: "2026-03-19T00:00:00Z",
	}
	second := Entry{
		ID:        "mem-second",
		Text:      "second",
		CreatedAt: time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
		UpdatedAt: time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
	}
	if err := store.WriteEntry(first); err != nil {
		t.Fatalf("WriteEntry(first) error = %v", err)
	}
	if err := store.WriteEntry(second); err != nil {
		t.Fatalf("WriteEntry(second) error = %v", err)
	}

	results, err := store.Search("", 10)
	if err != nil {
		t.Fatalf("Search(blank) error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("Search(blank) len = %d, want 2", len(results))
	}
	if results[0].ID != second.ID {
		t.Fatalf("Search(blank)[0].ID = %q, want %q", results[0].ID, second.ID)
	}
}

func TestSearchWithOptionsCanIncludeArchived(t *testing.T) {
	store := NewStore(t.TempDir())
	active := Entry{
		ID:        "mem-active",
		Text:      "中文回复",
		CreatedAt: "2026-03-20T00:00:00Z",
		UpdatedAt: "2026-03-20T00:00:00Z",
	}
	archived := Entry{
		ID:         "mem-archived",
		Text:       "中文回复",
		Archived:   true,
		ArchivedAt: "2026-03-21T00:00:00Z",
		CreatedAt:  "2026-03-19T00:00:00Z",
		UpdatedAt:  "2026-03-21T00:00:00Z",
	}
	for _, entry := range []Entry{active, archived} {
		if err := store.WriteEntry(entry); err != nil {
			t.Fatalf("WriteEntry(%s) error = %v", entry.ID, err)
		}
	}

	results, err := store.Search("中文", 10)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 1 || results[0].ID != active.ID {
		t.Fatalf("Search() = %#v, want only %s", results, active.ID)
	}

	archivedResults, err := store.SearchWithOptions("中文", 10, QueryOptions{ArchivedOnly: true})
	if err != nil {
		t.Fatalf("SearchWithOptions(ArchivedOnly) error = %v", err)
	}
	if len(archivedResults) != 1 || archivedResults[0].ID != archived.ID {
		t.Fatalf("archived search = %#v, want only %s", archivedResults, archived.ID)
	}
}

func TestFindRecallMatchesUsesRecallRankingAndSkipsArchivedByDefault(t *testing.T) {
	store := NewStore(t.TempDir())
	for _, entry := range []Entry{
		{
			ID:             "mem-reinforced",
			Text:           "中文回复",
			Priority:       20,
			ReinforceCount: 4,
			CreatedAt:      "2026-03-18T00:00:00Z",
			UpdatedAt:      "2026-03-18T00:00:00Z",
		},
		{
			ID:        "mem-used",
			Text:      "中文回复",
			Priority:  20,
			UseCount:  2,
			CreatedAt: "2026-03-19T00:00:00Z",
			UpdatedAt: "2026-03-19T00:00:00Z",
		},
		{
			ID:         "mem-archived",
			Text:       "中文回复",
			Priority:   100,
			Archived:   true,
			ArchivedAt: "2026-03-20T00:00:00Z",
			CreatedAt:  "2026-03-20T00:00:00Z",
			UpdatedAt:  "2026-03-20T00:00:00Z",
		},
	} {
		if err := store.WriteEntry(entry); err != nil {
			t.Fatalf("WriteEntry(%s) error = %v", entry.ID, err)
		}
	}
	matches, err := store.FindRecallMatches("请用中文回复", 10)
	if err != nil {
		t.Fatalf("FindRecallMatches() error = %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("FindRecallMatches() len = %d, want 1", len(matches))
	}
	if matches[0].Entry.ID != "mem-reinforced" {
		t.Fatalf("FindRecallMatches()[0] = %s, want mem-reinforced", matches[0].Entry.ID)
	}
	if got := strings.Join(matches[0].Reasons, ","); !strings.Contains(got, "reinforce:4") || !strings.Contains(got, "term:中文") || !strings.Contains(got, "collapsed_similar:1") {
		t.Fatalf("FindRecallMatches()[0].Reasons = %q", got)
	}
	archivedMatches, err := store.FindRecallMatchesWithOptions("请用中文回复", 10, QueryOptions{ArchivedOnly: true})
	if err != nil {
		t.Fatalf("FindRecallMatchesWithOptions() error = %v", err)
	}
	if len(archivedMatches) != 1 || archivedMatches[0].Entry.ID != "mem-archived" {
		t.Fatalf("archived matches = %#v, want mem-archived", archivedMatches)
	}
}

func TestFindRecallMatchesCollapsesHighlySimilarMatches(t *testing.T) {
	store := NewStore(t.TempDir())
	for _, entry := range []Entry{
		{
			ID:        "mem-primary",
			Text:      "以后默认用简体中文回复",
			Priority:  80,
			Pinned:    true,
			CreatedAt: "2026-03-21T00:00:00Z",
			UpdatedAt: "2026-03-21T00:00:00Z",
		},
		{
			ID:        "mem-duplicate",
			Text:      "以后默认用简体中文回复",
			Priority:  20,
			CreatedAt: "2026-03-20T00:00:00Z",
			UpdatedAt: "2026-03-20T00:00:00Z",
		},
		{
			ID:        "mem-other",
			Text:      "回答前先给结论",
			Priority:  30,
			CreatedAt: "2026-03-19T00:00:00Z",
			UpdatedAt: "2026-03-19T00:00:00Z",
		},
	} {
		if err := store.WriteEntry(entry); err != nil {
			t.Fatalf("WriteEntry(%s) error = %v", entry.ID, err)
		}
	}

	matches, err := store.FindRecallMatches("请以后默认用简体中文回复，并回答前先给结论", 10)
	if err != nil {
		t.Fatalf("FindRecallMatches() error = %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("FindRecallMatches() len = %d, want 2", len(matches))
	}
	if matches[0].Entry.ID != "mem-primary" {
		t.Fatalf("FindRecallMatches()[0] = %s, want mem-primary", matches[0].Entry.ID)
	}
	if got := strings.Join(matches[0].Reasons, ","); !strings.Contains(got, "collapsed_similar:1") {
		t.Fatalf("FindRecallMatches()[0].Reasons = %q, want collapsed_similar:1", got)
	}
	if matches[1].Entry.ID != "mem-other" {
		t.Fatalf("FindRecallMatches()[1] = %s, want mem-other", matches[1].Entry.ID)
	}
}

func TestListEntriesPrefersPinnedThenPriority(t *testing.T) {
	store := NewStore(t.TempDir())
	low := Entry{
		ID:        "mem-low",
		Text:      "low",
		Priority:  10,
		CreatedAt: "2026-03-19T00:00:00Z",
		UpdatedAt: "2026-03-19T00:00:00Z",
	}
	high := Entry{
		ID:        "mem-high",
		Text:      "high",
		Priority:  90,
		CreatedAt: "2026-03-18T00:00:00Z",
		UpdatedAt: "2026-03-18T00:00:00Z",
	}
	pinned := Entry{
		ID:        "mem-pinned",
		Text:      "pinned",
		Priority:  20,
		Pinned:    true,
		CreatedAt: "2026-03-17T00:00:00Z",
		UpdatedAt: "2026-03-17T00:00:00Z",
	}
	for _, entry := range []Entry{low, high, pinned} {
		if err := store.WriteEntry(entry); err != nil {
			t.Fatalf("WriteEntry(%s) error = %v", entry.ID, err)
		}
	}
	entries, err := store.ListEntries()
	if err != nil {
		t.Fatalf("ListEntries() error = %v", err)
	}
	if got := []string{entries[0].ID, entries[1].ID, entries[2].ID}; got[0] != "mem-pinned" || got[1] != "mem-high" || got[2] != "mem-low" {
		t.Fatalf("ListEntries() order = %v", got)
	}
}

func TestUpdateEntryUpdatesMetadataAndTouchTime(t *testing.T) {
	store := NewStore(t.TempDir())
	entry := Entry{
		ID:        "mem-update",
		Text:      "remember concise replies",
		Source:    "feishu/assistant/oc_demo",
		Tags:      []string{"reply"},
		CreatedAt: "2026-03-20T00:00:00Z",
		UpdatedAt: "2026-03-20T00:00:00Z",
	}
	if err := store.WriteEntry(entry); err != nil {
		t.Fatalf("WriteEntry() error = %v", err)
	}

	text := "remember concise Chinese replies"
	source := "manual"
	tags := []string{"reply", "language"}
	kind := "rule"
	priority := 75
	pinned := true

	updated, err := store.UpdateEntry(entry.ID, UpdateOptions{
		Text:     &text,
		Source:   &source,
		Tags:     &tags,
		Kind:     &kind,
		Priority: &priority,
		Pinned:   &pinned,
	})
	if err != nil {
		t.Fatalf("UpdateEntry() error = %v", err)
	}
	if updated.Text != text || updated.Source != source || updated.Kind != "rule" || updated.Priority != 75 || !updated.Pinned {
		t.Fatalf("UpdateEntry() = %#v", updated)
	}
	if updated.UpdatedAt == entry.UpdatedAt {
		t.Fatalf("UpdatedAt was not refreshed: before=%q after=%q", entry.UpdatedAt, updated.UpdatedAt)
	}
	if got := strings.Join(updated.Tags, ","); got != "reply,language" {
		t.Fatalf("Tags = %q, want reply,language", got)
	}
}

func TestUpdateEntryAllowsClearingOptionalMetadata(t *testing.T) {
	store := NewStore(t.TempDir())
	entry, err := store.AddWithOptions("remember this", AddOptions{
		Source:   "manual",
		Tags:     []string{"a", "b"},
		Kind:     "preference",
		Priority: 80,
		Pinned:   true,
	})
	if err != nil {
		t.Fatalf("AddWithOptions() error = %v", err)
	}

	source := ""
	tags := []string{}
	kind := ""
	priority := 0
	pinned := false
	updated, err := store.UpdateEntry(entry.ID, UpdateOptions{
		Source:   &source,
		Tags:     &tags,
		Kind:     &kind,
		Priority: &priority,
		Pinned:   &pinned,
	})
	if err != nil {
		t.Fatalf("UpdateEntry() error = %v", err)
	}
	if updated.Source != "" || len(updated.Tags) != 0 || updated.Kind != "" || updated.Priority != 0 || updated.Pinned {
		t.Fatalf("UpdateEntry() cleared metadata = %#v", updated)
	}
}

func TestListEntriesExcludesArchivedByDefault(t *testing.T) {
	store := NewStore(t.TempDir())
	active := Entry{
		ID:        "mem-active",
		Text:      "active",
		CreatedAt: "2026-03-20T00:00:00Z",
		UpdatedAt: "2026-03-20T00:00:00Z",
	}
	archived := Entry{
		ID:         "mem-archived",
		Text:       "archived",
		Archived:   true,
		ArchivedAt: "2026-03-21T00:00:00Z",
		CreatedAt:  "2026-03-19T00:00:00Z",
		UpdatedAt:  "2026-03-21T00:00:00Z",
	}
	for _, entry := range []Entry{active, archived} {
		if err := store.WriteEntry(entry); err != nil {
			t.Fatalf("WriteEntry(%s) error = %v", entry.ID, err)
		}
	}

	entries, err := store.ListEntries()
	if err != nil {
		t.Fatalf("ListEntries() error = %v", err)
	}
	if len(entries) != 1 || entries[0].ID != active.ID {
		t.Fatalf("ListEntries() = %#v, want only %s", entries, active.ID)
	}

	archivedEntries, err := store.ListEntriesWithOptions(QueryOptions{ArchivedOnly: true})
	if err != nil {
		t.Fatalf("ListEntriesWithOptions(ArchivedOnly) error = %v", err)
	}
	if len(archivedEntries) != 1 || archivedEntries[0].ID != archived.ID {
		t.Fatalf("archived entries = %#v, want only %s", archivedEntries, archived.ID)
	}

	allEntries, err := store.ListEntriesAll()
	if err != nil {
		t.Fatalf("ListEntriesAll() error = %v", err)
	}
	if len(allEntries) != 2 {
		t.Fatalf("ListEntriesAll() len = %d, want 2", len(allEntries))
	}
}

func TestMergeEntriesCombinesMetadataAndDeletesDrops(t *testing.T) {
	store := NewStore(t.TempDir())
	keep := Entry{
		ID:               "mem-keep",
		Text:             "以后默认用中文回复",
		Source:           "manual",
		Tags:             []string{"language"},
		Kind:             "rule",
		Priority:         70,
		UseCount:         2,
		LastUsedAt:       "2026-03-20T00:00:00Z",
		ReinforceCount:   1,
		LastReinforcedAt: "2026-03-20T01:00:00Z",
		CreatedAt:        "2026-03-20T00:00:00Z",
		UpdatedAt:        "2026-03-20T02:00:00Z",
	}
	drop := Entry{
		ID:               "mem-drop",
		Text:             "默认请用中文回复",
		Source:           "feishu/assistant/oc_demo",
		Tags:             []string{"preference", "reply"},
		Kind:             "preference",
		Priority:         85,
		Pinned:           true,
		UseCount:         3,
		LastUsedAt:       "2026-03-21T00:00:00Z",
		ReinforceCount:   2,
		LastReinforcedAt: "2026-03-21T01:00:00Z",
		CreatedAt:        "2026-03-19T00:00:00Z",
		UpdatedAt:        "2026-03-21T02:00:00Z",
	}
	for _, entry := range []Entry{keep, drop} {
		if err := store.WriteEntry(entry); err != nil {
			t.Fatalf("WriteEntry(%s) error = %v", entry.ID, err)
		}
	}

	merged, deletedIDs, err := store.MergeEntries("mem-keep", []string{"mem-drop"})
	if err != nil {
		t.Fatalf("MergeEntries() error = %v", err)
	}
	if got := strings.Join(deletedIDs, ","); got != "mem-drop" {
		t.Fatalf("deletedIDs = %q, want mem-drop", got)
	}
	if merged.ID != "mem-keep" || merged.Kind != "preference" || !merged.Pinned || merged.Priority != 85 {
		t.Fatalf("merged identity/priority = %#v", merged)
	}
	if merged.UseCount != 5 || merged.ReinforceCount != 3 {
		t.Fatalf("merged counters = %#v", merged)
	}
	if got := strings.Join(merged.Tags, ","); got != "language,preference,reply" {
		t.Fatalf("merged tags = %q", got)
	}
	if merged.LastUsedAt != "2026-03-21T00:00:00Z" || merged.LastReinforcedAt != "2026-03-21T01:00:00Z" {
		t.Fatalf("merged timestamps = %#v", merged)
	}
	if merged.CreatedAt != "2026-03-19T00:00:00Z" {
		t.Fatalf("merged CreatedAt = %q, want earliest", merged.CreatedAt)
	}
	if _, err := store.ReadEntry("mem-drop"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("drop entry still exists, err = %v", err)
	}
}

func TestMergeEntriesIgnoresSelfBlankAndDuplicateDropIDs(t *testing.T) {
	store := NewStore(t.TempDir())
	for _, entry := range []Entry{
		{
			ID:        "mem-keep",
			Text:      "以后默认用中文回复",
			CreatedAt: "2026-03-20T00:00:00Z",
			UpdatedAt: "2026-03-20T00:00:00Z",
		},
		{
			ID:        "mem-drop",
			Text:      "默认请用中文回复",
			CreatedAt: "2026-03-19T00:00:00Z",
			UpdatedAt: "2026-03-19T00:00:00Z",
		},
	} {
		if err := store.WriteEntry(entry); err != nil {
			t.Fatalf("WriteEntry(%s) error = %v", entry.ID, err)
		}
	}

	merged, deletedIDs, err := store.MergeEntries("mem-keep", []string{"", " mem-drop ", "mem-keep", "mem-drop"})
	if err != nil {
		t.Fatalf("MergeEntries() error = %v", err)
	}
	if got := strings.Join(deletedIDs, ","); got != "mem-drop" {
		t.Fatalf("deletedIDs = %q, want mem-drop", got)
	}
	if merged.ID != "mem-keep" {
		t.Fatalf("merged.ID = %q, want mem-keep", merged.ID)
	}
	if _, err := store.ReadEntry("mem-drop"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("drop entry still exists, err = %v", err)
	}
}

func TestMergeEntriesRejectsOnlySelfOrBlankDrops(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.WriteEntry(Entry{
		ID:        "mem-keep",
		Text:      "以后默认用中文回复",
		CreatedAt: "2026-03-20T00:00:00Z",
		UpdatedAt: "2026-03-20T00:00:00Z",
	}); err != nil {
		t.Fatalf("WriteEntry(mem-keep) error = %v", err)
	}

	_, _, err := store.MergeEntries("mem-keep", []string{"", " mem-keep ", "mem-keep"})
	if err == nil {
		t.Fatalf("MergeEntries() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "at least one memory id to merge is required") {
		t.Fatalf("MergeEntries() error = %v, want missing drop id error", err)
	}

	if _, err := store.ReadEntry("mem-keep"); err != nil {
		t.Fatalf("keep entry missing after rejected merge, err = %v", err)
	}
}

func TestFindDuplicateGroupsFindsLikelyDuplicates(t *testing.T) {
	store := NewStore(t.TempDir())
	for _, entry := range []Entry{
		{
			ID:        "mem-keep",
			Text:      "以后默认用中文回复",
			Priority:  80,
			Pinned:    true,
			CreatedAt: "2026-03-20T00:00:00Z",
			UpdatedAt: "2026-03-20T00:00:00Z",
		},
		{
			ID:        "mem-drop",
			Text:      "默认请用中文回复",
			Priority:  70,
			CreatedAt: "2026-03-19T00:00:00Z",
			UpdatedAt: "2026-03-19T00:00:00Z",
		},
		{
			ID:        "mem-other",
			Text:      "请始终顺手跑测试",
			Priority:  70,
			CreatedAt: "2026-03-18T00:00:00Z",
			UpdatedAt: "2026-03-18T00:00:00Z",
		},
	} {
		if err := store.WriteEntry(entry); err != nil {
			t.Fatalf("WriteEntry(%s) error = %v", entry.ID, err)
		}
	}
	groups, err := store.FindDuplicateGroups(10)
	if err != nil {
		t.Fatalf("FindDuplicateGroups() error = %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("FindDuplicateGroups() len = %d, want 1", len(groups))
	}
	if groups[0].Keep.ID != "mem-keep" || len(groups[0].Drops) != 1 || groups[0].Drops[0].Entry.ID != "mem-drop" {
		t.Fatalf("FindDuplicateGroups() = %#v", groups)
	}
	if groups[0].Score < 100 || groups[0].Drops[0].Score < 100 {
		t.Fatalf("duplicate scores = %#v", groups)
	}
	if groups[0].Drops[0].Reason == "" {
		t.Fatalf("duplicate reason is empty: %#v", groups)
	}
}

func TestFindDuplicateGroupsWithMinScoreFiltersLowerConfidenceMatches(t *testing.T) {
	store := NewStore(t.TempDir())
	for _, entry := range []Entry{
		{
			ID:        "mem-keep",
			Text:      "以后默认用中文回复",
			Priority:  80,
			Pinned:    true,
			CreatedAt: "2026-03-20T00:00:00Z",
			UpdatedAt: "2026-03-20T00:00:00Z",
		},
		{
			ID:        "mem-drop",
			Text:      "默认请用中文回复",
			Priority:  70,
			CreatedAt: "2026-03-19T00:00:00Z",
			UpdatedAt: "2026-03-19T00:00:00Z",
		},
	} {
		if err := store.WriteEntry(entry); err != nil {
			t.Fatalf("WriteEntry(%s) error = %v", entry.ID, err)
		}
	}
	normalGroups, err := store.FindDuplicateGroupsWithMinScore(10, DefaultDuplicateMinScore)
	if err != nil {
		t.Fatalf("FindDuplicateGroupsWithMinScore(default) error = %v", err)
	}
	if len(normalGroups) != 1 {
		t.Fatalf("default groups len = %d, want 1", len(normalGroups))
	}
	strictGroups, err := store.FindDuplicateGroupsWithMinScore(10, normalGroups[0].Drops[0].Score+1)
	if err != nil {
		t.Fatalf("FindDuplicateGroupsWithMinScore(strict) error = %v", err)
	}
	if len(strictGroups) != 0 {
		t.Fatalf("strict groups len = %d, want 0", len(strictGroups))
	}
}

func TestFindRelatedEntriesReturnsSortedMatches(t *testing.T) {
	store := NewStore(t.TempDir())
	target := Entry{
		ID:        "mem-target",
		Text:      "remember default reply in chinese",
		CreatedAt: "2026-03-20T00:00:00Z",
		UpdatedAt: "2026-03-20T00:00:00Z",
	}
	exact := Entry{
		ID:        "mem-exact",
		Text:      "remember default reply in chinese",
		CreatedAt: "2026-03-19T00:00:00Z",
		UpdatedAt: "2026-03-19T00:00:00Z",
	}
	near := Entry{
		ID:        "mem-near",
		Text:      "default reply in chinese",
		CreatedAt: "2026-03-21T00:00:00Z",
		UpdatedAt: "2026-03-21T00:00:00Z",
	}
	other := Entry{
		ID:        "mem-other",
		Text:      "always run tests after code changes",
		CreatedAt: "2026-03-18T00:00:00Z",
		UpdatedAt: "2026-03-18T00:00:00Z",
	}
	for _, entry := range []Entry{target, exact, near, other} {
		if err := store.WriteEntry(entry); err != nil {
			t.Fatalf("WriteEntry(%s) error = %v", entry.ID, err)
		}
	}

	scoreExact := duplicateScore(target, exact)
	scoreNear := duplicateScore(target, near)
	scoreOther := duplicateScore(target, other)
	if scoreExact <= scoreNear || scoreNear <= 0 || scoreOther >= scoreNear {
		t.Fatalf("unexpected related scores: exact=%d near=%d other=%d", scoreExact, scoreNear, scoreOther)
	}

	gotTarget, matches, err := store.FindRelatedEntriesWithMinScore(target.ID, 10, scoreNear)
	if err != nil {
		t.Fatalf("FindRelatedEntriesWithMinScore() error = %v", err)
	}
	if gotTarget.ID != target.ID {
		t.Fatalf("target.ID = %q, want %q", gotTarget.ID, target.ID)
	}
	if len(matches) != 2 {
		t.Fatalf("matches len = %d, want 2", len(matches))
	}
	if matches[0].Entry.ID != exact.ID || matches[1].Entry.ID != near.ID {
		t.Fatalf("match order = %s,%s want %s,%s", matches[0].Entry.ID, matches[1].Entry.ID, exact.ID, near.ID)
	}
	if matches[0].Reason == "" || matches[1].Reason == "" {
		t.Fatalf("match reason should not be empty: %#v", matches)
	}
}

func TestFindRelatedEntriesWithMinScoreFiltersLowerConfidenceMatches(t *testing.T) {
	store := NewStore(t.TempDir())
	target := Entry{
		ID:        "mem-target",
		Text:      "remember default reply in chinese",
		CreatedAt: "2026-03-20T00:00:00Z",
		UpdatedAt: "2026-03-20T00:00:00Z",
	}
	exact := Entry{
		ID:        "mem-exact",
		Text:      "remember default reply in chinese",
		CreatedAt: "2026-03-19T00:00:00Z",
		UpdatedAt: "2026-03-19T00:00:00Z",
	}
	near := Entry{
		ID:        "mem-near",
		Text:      "default reply in chinese",
		CreatedAt: "2026-03-21T00:00:00Z",
		UpdatedAt: "2026-03-21T00:00:00Z",
	}
	for _, entry := range []Entry{target, exact, near} {
		if err := store.WriteEntry(entry); err != nil {
			t.Fatalf("WriteEntry(%s) error = %v", entry.ID, err)
		}
	}

	strictScore := duplicateScore(target, near) + 1
	_, matches, err := store.FindRelatedEntriesWithMinScore(target.ID, 10, strictScore)
	if err != nil {
		t.Fatalf("FindRelatedEntriesWithMinScore() error = %v", err)
	}
	if len(matches) != 1 || matches[0].Entry.ID != exact.ID {
		t.Fatalf("strict matches = %#v, want only %s", matches, exact.ID)
	}
}

func TestReviewReturnsGovernanceSuggestions(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Now().UTC()
	entries := []Entry{
		{
			ID:        "mem-keep",
			Text:      "以后默认用中文回复",
			Kind:      "preference",
			Priority:  80,
			Pinned:    true,
			CreatedAt: now.AddDate(0, 0, -10).Format(time.RFC3339),
			UpdatedAt: now.AddDate(0, 0, -10).Format(time.RFC3339),
		},
		{
			ID:        "mem-drop",
			Text:      "默认请用中文回复",
			Kind:      "preference",
			Priority:  70,
			CreatedAt: now.AddDate(0, 0, -12).Format(time.RFC3339),
			UpdatedAt: now.AddDate(0, 0, -12).Format(time.RFC3339),
		},
		{
			ID:               "mem-promote",
			Text:             "以后默认先给结论再解释",
			Kind:             "preference",
			Priority:         60,
			ReinforceCount:   2,
			LastReinforcedAt: now.AddDate(0, 0, -1).Format(time.RFC3339),
			CreatedAt:        now.AddDate(0, 0, -20).Format(time.RFC3339),
			UpdatedAt:        now.AddDate(0, 0, -1).Format(time.RFC3339),
		},
		{
			ID:        "mem-stale",
			Text:      "临时记录：查过一次某个截图样式",
			Kind:      "note",
			Priority:  10,
			CreatedAt: now.AddDate(0, 0, -90).Format(time.RFC3339),
			UpdatedAt: now.AddDate(0, 0, -90).Format(time.RFC3339),
		},
	}
	for _, entry := range entries {
		if err := store.WriteEntry(entry); err != nil {
			t.Fatalf("WriteEntry(%s) error = %v", entry.ID, err)
		}
	}

	report, err := store.Review(ReviewOptions{
		Limit:             10,
		DuplicateMinScore: DefaultDuplicateMinScore,
		StaleDays:         30,
	})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if report.TotalEntries != 4 {
		t.Fatalf("TotalEntries = %d, want 4", report.TotalEntries)
	}
	if len(report.DuplicateGroups) != 1 {
		t.Fatalf("DuplicateGroups len = %d, want 1", len(report.DuplicateGroups))
	}
	if len(report.PromoteSuggestions) == 0 || report.PromoteSuggestions[0].Entry.ID != "mem-promote" {
		t.Fatalf("PromoteSuggestions = %#v", report.PromoteSuggestions)
	}
	if report.PromoteSuggestions[0].Reason == "" || !report.PromoteSuggestions[0].TargetPin {
		t.Fatalf("PromoteSuggestions[0] = %#v", report.PromoteSuggestions[0])
	}
	if len(report.StaleSuggestions) == 0 || report.StaleSuggestions[0].Entry.ID != "mem-stale" {
		t.Fatalf("StaleSuggestions = %#v", report.StaleSuggestions)
	}
	if report.StaleSuggestions[0].AgeDays < 30 {
		t.Fatalf("StaleSuggestions[0].AgeDays = %d, want >= 30", report.StaleSuggestions[0].AgeDays)
	}
}

func TestApplyReviewAppliesPromoteAndArchive(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Now().UTC()
	for _, entry := range []Entry{
		{
			ID:               "mem-promote",
			Text:             "以后默认先给结论再解释",
			Kind:             "preference",
			Priority:         60,
			ReinforceCount:   2,
			LastReinforcedAt: now.AddDate(0, 0, -1).Format(time.RFC3339),
			CreatedAt:        now.AddDate(0, 0, -20).Format(time.RFC3339),
			UpdatedAt:        now.AddDate(0, 0, -1).Format(time.RFC3339),
		},
		{
			ID:        "mem-stale",
			Text:      "临时记录：查过一次某个截图样式",
			Kind:      "note",
			Priority:  10,
			CreatedAt: now.AddDate(0, 0, -90).Format(time.RFC3339),
			UpdatedAt: now.AddDate(0, 0, -90).Format(time.RFC3339),
		},
	} {
		if err := store.WriteEntry(entry); err != nil {
			t.Fatalf("WriteEntry(%s) error = %v", entry.ID, err)
		}
	}

	report, err := store.Review(ReviewOptions{
		Limit:             10,
		DuplicateMinScore: DefaultDuplicateMinScore,
		StaleDays:         30,
	})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	result, err := store.ApplyReview(report, ReviewApplyOptions{Promote: true, ArchiveStale: true})
	if err != nil {
		t.Fatalf("ApplyReview() error = %v", err)
	}
	if len(result.Promoted) != 1 || result.Promoted[0].ID != "mem-promote" {
		t.Fatalf("Promoted = %#v", result.Promoted)
	}
	if !result.Promoted[0].Pinned || result.Promoted[0].Priority < 80 {
		t.Fatalf("Promoted[0] = %#v", result.Promoted[0])
	}
	if len(result.Archived) != 1 || result.Archived[0].ID != "mem-stale" {
		t.Fatalf("Archived = %#v", result.Archived)
	}
	if !result.Archived[0].Archived || result.Archived[0].ArchivedAt == "" {
		t.Fatalf("Archived[0] = %#v", result.Archived[0])
	}
}

func TestFindArchivedPurgeCandidatesAndPurge(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Now().UTC()
	for _, entry := range []Entry{
		{
			ID:         "mem-old-archived",
			Text:       "old archived",
			Archived:   true,
			ArchivedAt: now.AddDate(0, 0, -45).Format(time.RFC3339),
			CreatedAt:  now.AddDate(0, 0, -90).Format(time.RFC3339),
			UpdatedAt:  now.AddDate(0, 0, -45).Format(time.RFC3339),
		},
		{
			ID:         "mem-new-archived",
			Text:       "new archived",
			Archived:   true,
			ArchivedAt: now.AddDate(0, 0, -10).Format(time.RFC3339),
			CreatedAt:  now.AddDate(0, 0, -30).Format(time.RFC3339),
			UpdatedAt:  now.AddDate(0, 0, -10).Format(time.RFC3339),
		},
		{
			ID:        "mem-active",
			Text:      "active",
			CreatedAt: now.AddDate(0, 0, -20).Format(time.RFC3339),
			UpdatedAt: now.AddDate(0, 0, -20).Format(time.RFC3339),
		},
	} {
		if err := store.WriteEntry(entry); err != nil {
			t.Fatalf("WriteEntry(%s) error = %v", entry.ID, err)
		}
	}

	candidates, err := store.FindArchivedPurgeCandidates(10, 30)
	if err != nil {
		t.Fatalf("FindArchivedPurgeCandidates() error = %v", err)
	}
	if len(candidates) != 1 || candidates[0].Entry.ID != "mem-old-archived" {
		t.Fatalf("candidates = %#v", candidates)
	}
	deleted, err := store.PurgeArchivedCandidates(candidates)
	if err != nil {
		t.Fatalf("PurgeArchivedCandidates() error = %v", err)
	}
	if got := strings.Join(deleted, ","); got != "mem-old-archived" {
		t.Fatalf("deleted = %q, want mem-old-archived", got)
	}
	if _, err := store.ReadEntry("mem-old-archived"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("purged entry still exists, err = %v", err)
	}
}

func TestStatsSummarizesMemoryLibrary(t *testing.T) {
	store := NewStore(t.TempDir())
	for _, entry := range []Entry{
		{
			ID:             "mem-pinned",
			Text:           "以后默认用中文回复",
			Kind:           "preference",
			Priority:       80,
			Pinned:         true,
			UseCount:       3,
			ReinforceCount: 2,
			CreatedAt:      "2026-03-18T00:00:00Z",
			UpdatedAt:      "2026-03-18T00:00:00Z",
		},
		{
			ID:             "mem-rule",
			Text:           "请始终先给结论",
			Kind:           "rule",
			Priority:       60,
			UseCount:       1,
			ReinforceCount: 4,
			CreatedAt:      "2026-03-19T00:00:00Z",
			UpdatedAt:      "2026-03-19T00:00:00Z",
		},
		{
			ID:         "mem-archived",
			Text:       "旧笔记",
			Kind:       "note",
			Priority:   10,
			Archived:   true,
			ArchivedAt: "2026-03-20T00:00:00Z",
			CreatedAt:  "2026-03-20T00:00:00Z",
			UpdatedAt:  "2026-03-20T00:00:00Z",
		},
	} {
		if err := store.WriteEntry(entry); err != nil {
			t.Fatalf("WriteEntry(%s) error = %v", entry.ID, err)
		}
	}

	stats, err := store.Stats(5)
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}
	if stats.TotalEntries != 3 || stats.ActiveEntries != 2 || stats.ArchivedEntries != 1 {
		t.Fatalf("Stats() counts = %#v", stats)
	}
	if stats.PinnedEntries != 1 {
		t.Fatalf("PinnedEntries = %d, want 1", stats.PinnedEntries)
	}
	if stats.KindCounts["preference"] != 1 || stats.KindCounts["rule"] != 1 || stats.KindCounts["note"] != 1 {
		t.Fatalf("KindCounts = %#v", stats.KindCounts)
	}
	if len(stats.TopUsed) == 0 || stats.TopUsed[0].ID != "mem-pinned" {
		t.Fatalf("TopUsed = %#v", stats.TopUsed)
	}
	if len(stats.TopReinforced) == 0 || stats.TopReinforced[0].ID != "mem-rule" {
		t.Fatalf("TopReinforced = %#v", stats.TopReinforced)
	}
	if len(stats.TopPriority) == 0 || stats.TopPriority[0].ID != "mem-pinned" {
		t.Fatalf("TopPriority = %#v", stats.TopPriority)
	}
}

func TestMarkUsedIncrementsUseCountAndTimestamp(t *testing.T) {
	store := NewStore(t.TempDir())
	entry := Entry{
		ID:        "mem-used",
		Text:      "remember this",
		CreatedAt: "2026-03-20T00:00:00Z",
		UpdatedAt: "2026-03-20T00:00:00Z",
	}
	if err := store.WriteEntry(entry); err != nil {
		t.Fatalf("WriteEntry() error = %v", err)
	}
	if err := store.MarkUsed([]string{entry.ID, entry.ID}); err != nil {
		t.Fatalf("MarkUsed() error = %v", err)
	}
	got, err := store.ReadEntry(entry.ID)
	if err != nil {
		t.Fatalf("ReadEntry() error = %v", err)
	}
	if got.UseCount != 1 {
		t.Fatalf("UseCount = %d, want 1", got.UseCount)
	}
	if got.LastUsedAt == "" {
		t.Fatalf("LastUsedAt is empty")
	}
	if got.UpdatedAt == entry.UpdatedAt {
		t.Fatalf("UpdatedAt was not refreshed: before=%q after=%q", entry.UpdatedAt, got.UpdatedAt)
	}
}

func TestListEntriesUsesUseCountAsSecondarySignal(t *testing.T) {
	store := NewStore(t.TempDir())
	lessUsed := Entry{
		ID:         "mem-less-used",
		Text:       "less used",
		Priority:   50,
		UseCount:   1,
		LastUsedAt: "2026-03-19T00:00:00Z",
		CreatedAt:  "2026-03-19T00:00:00Z",
		UpdatedAt:  "2026-03-19T00:00:00Z",
	}
	moreUsed := Entry{
		ID:         "mem-more-used",
		Text:       "more used",
		Priority:   50,
		UseCount:   4,
		LastUsedAt: "2026-03-18T00:00:00Z",
		CreatedAt:  "2026-03-18T00:00:00Z",
		UpdatedAt:  "2026-03-18T00:00:00Z",
	}
	for _, entry := range []Entry{lessUsed, moreUsed} {
		if err := store.WriteEntry(entry); err != nil {
			t.Fatalf("WriteEntry(%s) error = %v", entry.ID, err)
		}
	}
	entries, err := store.ListEntries()
	if err != nil {
		t.Fatalf("ListEntries() error = %v", err)
	}
	if len(entries) != 2 || entries[0].ID != "mem-more-used" {
		t.Fatalf("ListEntries() = %#v", entries)
	}
}

func TestListEntriesUsesReinforceCountBeforeUseCount(t *testing.T) {
	store := NewStore(t.TempDir())
	moreReinforced := Entry{
		ID:             "mem-more-reinforced",
		Text:           "more reinforced",
		Priority:       50,
		UseCount:       1,
		ReinforceCount: 4,
		CreatedAt:      "2026-03-18T00:00:00Z",
		UpdatedAt:      "2026-03-18T00:00:00Z",
	}
	moreUsed := Entry{
		ID:             "mem-more-used",
		Text:           "more used",
		Priority:       50,
		UseCount:       3,
		ReinforceCount: 1,
		CreatedAt:      "2026-03-19T00:00:00Z",
		UpdatedAt:      "2026-03-19T00:00:00Z",
	}
	for _, entry := range []Entry{moreUsed, moreReinforced} {
		if err := store.WriteEntry(entry); err != nil {
			t.Fatalf("WriteEntry(%s) error = %v", entry.ID, err)
		}
	}
	entries, err := store.ListEntries()
	if err != nil {
		t.Fatalf("ListEntries() error = %v", err)
	}
	if len(entries) != 2 || entries[0].ID != "mem-more-reinforced" {
		t.Fatalf("ListEntries() = %#v", entries)
	}
}

func TestLibraryStoreSeparatesRobotMemories(t *testing.T) {
	repo := t.TempDir()
	assistant := NewLibraryStore(repo, "assistant")
	helper := NewLibraryStore(repo, "helper")

	if _, err := assistant.Add("assistant memory", "feishu/assistant/oc_1", nil); err != nil {
		t.Fatalf("assistant.Add() error = %v", err)
	}
	if _, err := helper.Add("helper memory", "feishu/helper/oc_2", nil); err != nil {
		t.Fatalf("helper.Add() error = %v", err)
	}

	assistantEntries, err := assistant.ListEntries()
	if err != nil {
		t.Fatalf("assistant.ListEntries() error = %v", err)
	}
	helperEntries, err := helper.ListEntries()
	if err != nil {
		t.Fatalf("helper.ListEntries() error = %v", err)
	}
	if len(assistantEntries) != 1 || assistantEntries[0].Text != "assistant memory" {
		t.Fatalf("assistant entries = %#v", assistantEntries)
	}
	if len(helperEntries) != 1 || helperEntries[0].Text != "helper memory" {
		t.Fatalf("helper entries = %#v", helperEntries)
	}
}
