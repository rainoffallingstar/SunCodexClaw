package main

import "testing"

func TestNormalizePositionalAccountArgs(t *testing.T) {
	in := []string{"assistant", "--node-bin", "node"}
	out := normalizePositionalAccountArgs(in)
	if len(out) < 2 || out[0] != "--account" || out[1] != "assistant" {
		t.Fatalf("unexpected: %#v", out)
	}
}

func TestNormalizeLogsArgs(t *testing.T) {
	in := []string{"assistant", "-f", "--lines", "10"}
	out := normalizeLogsArgs(in)
	// should become: --account assistant --follow --lines 10
	if len(out) < 4 {
		t.Fatalf("unexpected: %#v", out)
	}
	if out[0] != "--account" || out[1] != "assistant" {
		t.Fatalf("unexpected: %#v", out)
	}
	foundFollow := false
	for _, a := range out {
		if a == "--follow" {
			foundFollow = true
		}
	}
	if !foundFollow {
		t.Fatalf("missing --follow: %#v", out)
	}
}

func TestParseAccountFromErrorLine(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"[error] missing config for assistant: /x", "assistant"},
		{"[error] assistant preflight failed: boom", "assistant"},
		{"[error]   assistant   preflight failed: boom", "assistant"},
	}
	for _, tt := range tests {
		if got := parseAccountFromErrorLine(tt.line); got != tt.want {
			t.Fatalf("line=%q got=%q want=%q", tt.line, got, tt.want)
		}
	}
}
