package clawhub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultBaseURL = "https://clawhub.ai"

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

func NewClient(baseURL string, httpClient *http.Client) (*Client, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid clawhub base url %q", baseURL)
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{
		BaseURL:    strings.TrimRight(parsed.String(), "/"),
		HTTPClient: httpClient,
	}, nil
}

func (c *Client) Search(ctx context.Context, query string, limit int) (map[string]any, error) {
	params := url.Values{}
	params.Set("q", strings.TrimSpace(query))
	if limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", limit))
	}
	return c.getJSON(ctx, "/api/v1/search", params)
}

func (c *Client) List(ctx context.Context, sortBy string, limit int, cursor string) (map[string]any, error) {
	params := url.Values{}
	if strings.TrimSpace(sortBy) != "" {
		params.Set("sort", strings.TrimSpace(sortBy))
	}
	if limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", limit))
	}
	if strings.TrimSpace(cursor) != "" {
		params.Set("cursor", strings.TrimSpace(cursor))
	}
	return c.getJSON(ctx, "/api/v1/skills", params)
}

func (c *Client) Show(ctx context.Context, slug string) (map[string]any, error) {
	slug = strings.Trim(strings.TrimSpace(slug), "/")
	if slug == "" {
		return nil, fmt.Errorf("skill slug is required")
	}
	return c.getJSON(ctx, "/api/v1/skills/"+url.PathEscape(slug), nil)
}

func (c *Client) File(ctx context.Context, slug string, skillPath string, version string) ([]byte, error) {
	slug = strings.Trim(strings.TrimSpace(slug), "/")
	if slug == "" {
		return nil, fmt.Errorf("skill slug is required")
	}
	skillPath = strings.TrimSpace(skillPath)
	if skillPath == "" {
		return nil, fmt.Errorf("skill file path is required")
	}
	params := url.Values{}
	params.Set("path", skillPath)
	if strings.TrimSpace(version) != "" {
		params.Set("version", strings.TrimSpace(version))
	}
	return c.getBytes(ctx, "/api/v1/skills/"+url.PathEscape(slug)+"/file", params)
}

func (c *Client) getJSON(ctx context.Context, requestPath string, params url.Values) (map[string]any, error) {
	body, err := c.getBytes(ctx, requestPath, params)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode clawhub json failed: %w", err)
	}
	return payload, nil
}

func (c *Client) getBytes(ctx context.Context, requestPath string, params url.Values) ([]byte, error) {
	endpoint, err := c.resolveURL(requestPath, params)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, text/plain;q=0.9, */*;q=0.8")
	req.Header.Set("User-Agent", "suncodexclawd/clawhub")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("clawhub GET %s failed: %s", requestPath, summarizeHTTPResponse(resp.Status, body))
	}
	return body, nil
}

func (c *Client) resolveURL(requestPath string, params url.Values) (string, error) {
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(requestPath)
	if err != nil {
		return "", err
	}
	full := base.ResolveReference(ref)
	if len(params) > 0 {
		full.RawQuery = params.Encode()
	}
	return full.String(), nil
}

func summarizeHTTPResponse(status string, body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return status
	}
	if len(text) > 300 {
		text = text[:300]
	}
	return fmt.Sprintf("%s body=%s", status, text)
}
