package clawhub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchBuildsQueryAndParsesJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/search" {
			t.Fatalf("path = %q, want /api/v1/search", r.URL.Path)
		}
		if got := r.URL.Query().Get("q"); got != "timer skill" {
			t.Fatalf("q = %q, want timer skill", got)
		}
		if got := r.URL.Query().Get("limit"); got != "7" {
			t.Fatalf("limit = %q, want 7", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"slug":"openclaw/timer-helper","name":"Timer Helper","description":"Schedules tasks","latestVersion":"1.0.0","score":0.98}]}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	payload, err := client.Search(context.Background(), "timer skill", 7)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	results, ok := payload["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("results = %#v, want one item", payload["results"])
	}
}

func TestListPassesCursorAndSort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/skills" {
			t.Fatalf("path = %q, want /api/v1/skills", r.URL.Path)
		}
		if got := r.URL.Query().Get("sort"); got != "updated" {
			t.Fatalf("sort = %q, want updated", got)
		}
		if got := r.URL.Query().Get("cursor"); got != "cursor-1" {
			t.Fatalf("cursor = %q, want cursor-1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"skills":[{"slug":"openclaw/example"}],"nextCursor":"cursor-2"}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	payload, err := client.List(context.Background(), "updated", 20, "cursor-1")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if got := payload["nextCursor"]; got != "cursor-2" {
		t.Fatalf("nextCursor = %#v, want cursor-2", got)
	}
}

func TestFileFetchesRawBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/skills/openclaw%2Ftimer-helper/file" && r.URL.Path != "/api/v1/skills/openclaw/timer-helper/file" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("path"); got != "SKILL.md" {
			t.Fatalf("path query = %q, want SKILL.md", got)
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("# Timer Helper\n"))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	body, err := client.File(context.Background(), "openclaw/timer-helper", "SKILL.md", "")
	if err != nil {
		t.Fatalf("File() error = %v", err)
	}
	if got := strings.TrimSpace(string(body)); got != "# Timer Helper" {
		t.Fatalf("body = %q, want # Timer Helper", got)
	}
}

func TestShowReturnsHTTPErrorSummary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing skill", http.StatusNotFound)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	_, err = client.Show(context.Background(), "missing/skill")
	if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "missing skill") {
		t.Fatalf("Show() error = %v, want 404 summary", err)
	}
}
