package timer

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNextRunAfterInterval(t *testing.T) {
	task := Task{
		ID:        "hourly",
		Account:   "assistant",
		ChatID:    "oc_x",
		Prompt:    "ping",
		CreatedAt: "2026-03-20T00:00:00Z",
		Schedule: Schedule{
			Kind:  "interval",
			Every: "1h",
		},
	}
	next, err := task.NextRunAfter(time.Date(2026, 3, 20, 1, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("NextRunAfter() error = %v", err)
	}
	want := time.Date(2026, 3, 20, 2, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("NextRunAfter() = %s, want %s", next.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestNextRunAfterDaily(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("LoadLocation() error = %v", err)
	}
	task := Task{
		ID:      "daily",
		Account: "assistant",
		ChatID:  "oc_x",
		Prompt:  "ping",
		Schedule: Schedule{
			Kind:     "daily",
			At:       "09:00",
			Timezone: "Asia/Shanghai",
		},
	}
	next, err := task.NextRunAfter(time.Date(2026, 3, 20, 8, 0, 0, 0, loc))
	if err != nil {
		t.Fatalf("NextRunAfter() error = %v", err)
	}
	want := time.Date(2026, 3, 20, 9, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("NextRunAfter() = %s, want %s", next.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestNextRunAfterWeekly(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("LoadLocation() error = %v", err)
	}
	task := Task{
		ID:      "weekly",
		Account: "assistant",
		ChatID:  "oc_x",
		Prompt:  "ping",
		Schedule: Schedule{
			Kind:     "weekly",
			Weekdays: []string{"mon", "fri"},
			At:       "09:00",
			Timezone: "Asia/Shanghai",
		},
	}
	next, err := task.NextRunAfter(time.Date(2026, 3, 20, 10, 0, 0, 0, loc))
	if err != nil {
		t.Fatalf("NextRunAfter() error = %v", err)
	}
	want := time.Date(2026, 3, 23, 9, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("NextRunAfter() = %s, want %s", next.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestStoreSeparatesTasksByAccountNamespace(t *testing.T) {
	repo := t.TempDir()
	store := NewStore(repo)

	assistantTask := Task{
		ID:        "daily-report",
		Account:   "assistant",
		ChatID:    "oc_assistant",
		Prompt:    "assistant prompt",
		Enabled:   true,
		CreatedAt: "2026-03-20T00:00:00Z",
		UpdatedAt: "2026-03-20T00:00:00Z",
		Schedule:  Schedule{Kind: "interval", Every: "1h"},
	}
	helperTask := Task{
		ID:        "daily-report",
		Account:   "helper",
		ChatID:    "oc_helper",
		Prompt:    "helper prompt",
		Enabled:   true,
		CreatedAt: "2026-03-20T00:00:00Z",
		UpdatedAt: "2026-03-20T00:00:00Z",
		Schedule:  Schedule{Kind: "interval", Every: "2h"},
	}
	if err := store.WriteTask(assistantTask, "assistant"); err != nil {
		t.Fatalf("WriteTask(assistant) error = %v", err)
	}
	if err := store.WriteTask(helperTask, "helper"); err != nil {
		t.Fatalf("WriteTask(helper) error = %v", err)
	}

	assistantTasks, err := store.ListTasks("assistant")
	if err != nil {
		t.Fatalf("ListTasks(assistant) error = %v", err)
	}
	helperTasks, err := store.ListTasks("helper")
	if err != nil {
		t.Fatalf("ListTasks(helper) error = %v", err)
	}
	if len(assistantTasks) != 1 || assistantTasks[0].Prompt != "assistant prompt" {
		t.Fatalf("assistant tasks = %#v", assistantTasks)
	}
	if len(helperTasks) != 1 || helperTasks[0].Prompt != "helper prompt" {
		t.Fatalf("helper tasks = %#v", helperTasks)
	}

	allTasks, err := store.ListAllTasks()
	if err != nil {
		t.Fatalf("ListAllTasks() error = %v", err)
	}
	if len(allTasks) != 2 {
		t.Fatalf("ListAllTasks() len = %d, want 2", len(allTasks))
	}
}

func TestStoreSeparatesStateAndLogsByAccountNamespace(t *testing.T) {
	repo := t.TempDir()
	store := NewStore(repo)

	if err := store.WriteState(State{ID: "daily-report", LastError: "assistant"}, "assistant"); err != nil {
		t.Fatalf("WriteState(assistant) error = %v", err)
	}
	if err := store.WriteState(State{ID: "daily-report", LastError: "helper"}, "helper"); err != nil {
		t.Fatalf("WriteState(helper) error = %v", err)
	}

	assistantState, err := store.ReadState("daily-report", "assistant")
	if err != nil {
		t.Fatalf("ReadState(assistant) error = %v", err)
	}
	helperState, err := store.ReadState("daily-report", "helper")
	if err != nil {
		t.Fatalf("ReadState(helper) error = %v", err)
	}
	if assistantState.LastError != "assistant" {
		t.Fatalf("assistantState.LastError = %q, want assistant", assistantState.LastError)
	}
	if helperState.LastError != "helper" {
		t.Fatalf("helperState.LastError = %q, want helper", helperState.LastError)
	}

	if err := os.MkdirAll(filepath.Dir(store.LogPath("daily-report", "assistant")), 0o755); err != nil {
		t.Fatalf("MkdirAll(assistant log dir) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(store.LogPath("daily-report", "helper")), 0o755); err != nil {
		t.Fatalf("MkdirAll(helper log dir) error = %v", err)
	}
	if err := os.WriteFile(store.LogPath("daily-report", "assistant"), []byte("assistant log\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(assistant log) error = %v", err)
	}
	if err := os.WriteFile(store.LogPath("daily-report", "helper"), []byte("helper log\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(helper log) error = %v", err)
	}

	assistantLog, err := store.ReadLogTail("daily-report", "assistant", 10)
	if err != nil {
		t.Fatalf("ReadLogTail(assistant) error = %v", err)
	}
	helperLog, err := store.ReadLogTail("daily-report", "helper", 10)
	if err != nil {
		t.Fatalf("ReadLogTail(helper) error = %v", err)
	}
	if assistantLog != "assistant log" {
		t.Fatalf("assistant log = %q, want assistant log", assistantLog)
	}
	if helperLog != "helper log" {
		t.Fatalf("helper log = %q, want helper log", helperLog)
	}
}
