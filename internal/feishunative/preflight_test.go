package feishunative

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestCodexResponsesWebsocketURL(t *testing.T) {
	got, skipped, err := codexResponsesWebsocketURL("http://example.com:8317/v1")
	if err != nil {
		t.Fatalf("codexResponsesWebsocketURL() error = %v", err)
	}
	if skipped {
		t.Fatalf("codexResponsesWebsocketURL() skipped = true, want false")
	}
	if got != "ws://example.com:8317/v1/responses" {
		t.Fatalf("codexResponsesWebsocketURL() = %q", got)
	}
}

func TestCodexResponsesWebsocketURLSkipsOfficialOpenAI(t *testing.T) {
	got, skipped, err := codexResponsesWebsocketURL("https://api.openai.com/v1")
	if err != nil {
		t.Fatalf("codexResponsesWebsocketURL() error = %v", err)
	}
	if !skipped {
		t.Fatalf("codexResponsesWebsocketURL() skipped = false, want true")
	}
	if got != "" {
		t.Fatalf("codexResponsesWebsocketURL() = %q, want empty", got)
	}
}

func TestProbeCodexBaseURLOK(t *testing.T) {
	previous := dialCodexWebsocket
	t.Cleanup(func() {
		dialCodexWebsocket = previous
	})
	dialCodexWebsocket = func(ctx context.Context, wsURL string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		if wsURL != "ws://example.com/v1/responses" {
			t.Fatalf("wsURL = %q", wsURL)
		}
		return nil, nil, nil
	}

	result, err := ProbeCodexBaseURL(context.Background(), Config{
		Codex: CodexConfig{BaseURL: "http://example.com/v1"},
	})
	if err != nil {
		t.Fatalf("ProbeCodexBaseURL() error = %v", err)
	}
	if !result.Enabled || result.Skipped {
		t.Fatalf("ProbeCodexBaseURL() = %+v", result)
	}
	if result.Message != "ok" {
		t.Fatalf("ProbeCodexBaseURL() message = %q, want ok", result.Message)
	}
}

func TestProbeCodexBaseURLAcceptsAuthFailure(t *testing.T) {
	previous := dialCodexWebsocket
	t.Cleanup(func() {
		dialCodexWebsocket = previous
	})
	dialCodexWebsocket = func(ctx context.Context, wsURL string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		return nil, &http.Response{
			StatusCode: http.StatusUnauthorized,
			Status:     "401 Unauthorized",
			Body:       io.NopCloser(strings.NewReader("missing token")),
		}, errors.New("websocket: bad handshake")
	}

	result, err := ProbeCodexBaseURL(context.Background(), Config{
		Codex: CodexConfig{BaseURL: "http://example.com/v1"},
	})
	if err != nil {
		t.Fatalf("ProbeCodexBaseURL() error = %v", err)
	}
	if !strings.Contains(result.Message, "auth_rejected") {
		t.Fatalf("ProbeCodexBaseURL() message = %q", result.Message)
	}
}

func TestProbeCodexBaseURLRejectsBadRequest(t *testing.T) {
	previous := dialCodexWebsocket
	t.Cleanup(func() {
		dialCodexWebsocket = previous
	})
	dialCodexWebsocket = func(ctx context.Context, wsURL string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		return nil, &http.Response{
			StatusCode: http.StatusBadRequest,
			Status:     "400 Bad Request",
			Body:       io.NopCloser(strings.NewReader("not supported")),
		}, errors.New("websocket: bad handshake")
	}

	result, err := ProbeCodexBaseURL(context.Background(), Config{
		Codex: CodexConfig{BaseURL: "http://example.com/v1"},
	})
	if err == nil {
		t.Fatalf("ProbeCodexBaseURL() error = nil, want non-nil")
	}
	if !strings.Contains(result.Message, "gateway_reachable_but_responses_websocket_unsupported") {
		t.Fatalf("ProbeCodexBaseURL() message = %q", result.Message)
	}
	if !strings.Contains(result.Message, "400 Bad Request") {
		t.Fatalf("ProbeCodexBaseURL() message = %q", result.Message)
	}
}
