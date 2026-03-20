package timer

import (
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
