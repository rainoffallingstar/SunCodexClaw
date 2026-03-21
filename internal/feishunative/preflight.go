package feishunative

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

var dialCodexWebsocket = func(ctx context.Context, wsURL string, headers http.Header) (*websocket.Conn, *http.Response, error) {
	dialer := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
	}
	return dialer.DialContext(ctx, wsURL, headers)
}

type CodexBaseURLProbeResult struct {
	Enabled bool
	Skipped bool
	WSURL   string
	Message string
}

func ProbeCodexBaseURL(ctx context.Context, cfg Config) (CodexBaseURLProbeResult, error) {
	baseURL := strings.TrimSpace(cfg.Codex.BaseURL)
	if baseURL == "" {
		return CodexBaseURLProbeResult{Skipped: true, Message: "base_url_not_configured"}, nil
	}

	wsURL, skipped, err := codexResponsesWebsocketURL(baseURL)
	if err != nil {
		return CodexBaseURLProbeResult{
			Enabled: true,
			Message: compactText(err.Error(), 300),
		}, err
	}
	if skipped {
		return CodexBaseURLProbeResult{
			Skipped: true,
			WSURL:   wsURL,
			Message: "official_openai_base_url",
		}, nil
	}

	dialCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		dialCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}

	headers := http.Header{}
	if strings.TrimSpace(cfg.Codex.APIKey) != "" {
		headers.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.Codex.APIKey))
	}

	conn, resp, err := dialCodexWebsocket(dialCtx, wsURL, headers)
	if err == nil {
		if conn != nil {
			_ = conn.Close()
		}
		return CodexBaseURLProbeResult{
			Enabled: true,
			WSURL:   wsURL,
			Message: "ok",
		}, nil
	}

	statusCode := 0
	statusText := ""
	bodyText := ""
	if resp != nil {
		statusCode = resp.StatusCode
		statusText = strings.TrimSpace(resp.Status)
		bodyText = compactText(readHTTPBodySnippet(resp.Body), 240)
	}
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		message := compactText(fmt.Sprintf("auth_rejected status=%s body=%s", emptyFallback(statusText, fmt.Sprintf("%d", statusCode)), emptyFallback(bodyText, "(empty)")), 300)
		return CodexBaseURLProbeResult{
			Enabled: true,
			WSURL:   wsURL,
			Message: message,
		}, nil
	}
	if message, ok := classifyUnsupportedResponsesWebsocket(statusCode, statusText, bodyText, err); ok {
		return CodexBaseURLProbeResult{
			Enabled: true,
			WSURL:   wsURL,
			Message: message,
		}, fmt.Errorf("codex responses websocket probe failed: %s", message)
	}

	message := compactText(buildCodexBaseURLProbeErrorMessage(statusText, bodyText, err), 300)
	return CodexBaseURLProbeResult{
		Enabled: true,
		WSURL:   wsURL,
		Message: message,
	}, fmt.Errorf("codex responses websocket probe failed: %s", message)
}

func codexResponsesWebsocketURL(baseURL string) (string, bool, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", false, fmt.Errorf("invalid codex base_url %q: %w", baseURL, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", false, fmt.Errorf("invalid codex base_url %q: missing scheme or host", baseURL)
	}
	if strings.EqualFold(parsed.Hostname(), "api.openai.com") {
		return "", true, nil
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", false, fmt.Errorf("invalid codex base_url %q: unsupported scheme %q", baseURL, parsed.Scheme)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/responses"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), false, nil
}

func readHTTPBodySnippet(body io.ReadCloser) string {
	if body == nil {
		return ""
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, 512))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func buildCodexBaseURLProbeErrorMessage(statusText, bodyText string, err error) string {
	parts := []string{}
	if strings.TrimSpace(statusText) != "" {
		parts = append(parts, statusText)
	}
	if strings.TrimSpace(bodyText) != "" {
		parts = append(parts, "body="+strings.TrimSpace(bodyText))
	}
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		errText := strings.TrimSpace(err.Error())
		if len(parts) == 0 || !strings.Contains(errText, strings.TrimSpace(statusText)) {
			parts = append(parts, errText)
		}
	}
	if len(parts) == 0 {
		return "unknown probe error"
	}
	return strings.Join(parts, " ")
}

func classifyUnsupportedResponsesWebsocket(statusCode int, statusText, bodyText string, err error) (string, bool) {
	if statusCode != http.StatusBadRequest && statusCode != http.StatusMethodNotAllowed {
		return "", false
	}
	text := strings.ToLower(strings.Join([]string{
		strings.TrimSpace(statusText),
		strings.TrimSpace(bodyText),
		probeErrorText(err),
	}, " "))
	if !strings.Contains(text, "websocket") && !strings.Contains(text, "upgrade") && !strings.Contains(text, "handshake") {
		return "", false
	}
	message := fmt.Sprintf(
		"gateway_reachable_but_responses_websocket_unsupported status=%s body=%s",
		emptyFallback(statusText, fmt.Sprintf("%d", statusCode)),
		emptyFallback(bodyText, "(empty)"),
	)
	if errText := probeErrorText(err); errText != "" && !strings.Contains(strings.ToLower(message), strings.ToLower(errText)) {
		message += " error=" + errText
	}
	return compactText(message, 300), true
}

func probeErrorText(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}
