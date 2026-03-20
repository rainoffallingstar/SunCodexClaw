package memory

import (
	"testing"
	"time"
)

func TestStoreAddListShowDelete(t *testing.T) {
	store := NewStore(t.TempDir())

	entry, err := store.Add("Remember the default language is Chinese.", "feishu/assistant/oc_demo", []string{"lang", "default"})
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
