package hevy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultBaseURL = "https://api.hevyapp.com"

// Client talks to the Hevy REST API.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// New creates a Client. If baseURL is empty, DefaultBaseURL is used.
func New(apiKey, baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// WithAPIKey returns a shallow copy of c that uses the given API key.
// The HTTP client and base URL are shared, so the connection pool is reused.
// Useful when multiplexing per-session credentials in one process.
func (c *Client) WithAPIKey(apiKey string) *Client {
	clone := *c
	clone.apiKey = apiKey
	return &clone
}

// APIError is returned when the Hevy API responds with a non-2xx status.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("hevy API error: status %d: %s", e.StatusCode, e.Message)
}

// DoRaw is the exported wrapper around the internal doRaw. It executes any
// Hevy request and returns the raw response body, letting callers (notably
// the MCP tools layer) surface Hevy's actual response verbatim instead of
// risking lossy decoding when the wire shape is unclear.
func (c *Client) DoRaw(method, path string, query url.Values, body any) ([]byte, error) {
	return c.doRaw(method, path, query, body)
}

// doRaw executes a request and returns the raw response body. Non-2xx
// responses become a typed *APIError.
func (c *Client) doRaw(method, path string, query url.Values, body any) ([]byte, error) {
	full := c.baseURL + path
	if len(query) > 0 {
		full += "?" + query.Encode()
	}

	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
		reqBody = bytes.NewReader(buf)
	}

	req, err := http.NewRequest(method, full, reqBody)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("api-key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(raw))}
	}
	return raw, nil
}

// do executes a request and decodes the response JSON into out. If out is nil
// or the response body is empty, no decode is attempted.
func (c *Client) do(method, path string, query url.Values, body, out any) error {
	raw, err := c.doRaw(method, path, query, body)
	if err != nil {
		return err
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// doUnwrap executes a request and decodes the response into out, tolerating
// two shapes:
//   - the bare object (`{"id": ..., "title": ...}`)
//   - a single-key envelope (`{"<key>": {"id": ..., "title": ...}}`)
//
// This handles the Hevy convention where some endpoints wrap responses under
// a key (e.g. GET /v1/routines/{id} returns `{"routine": ...}`) while others
// document a bare response but in practice may wrap as well.
func (c *Client) doUnwrap(method, path string, query url.Values, body any, key string, out any) error {
	raw, err := c.doRaw(method, path, query, body)
	if err != nil {
		return err
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	// Try the envelope first: if the top-level object has exactly the
	// expected key, decode its value. We use json.RawMessage so we don't
	// double-parse the inner object.
	var env map[string]json.RawMessage
	if err := json.Unmarshal(raw, &env); err == nil {
		if inner, ok := env[key]; ok && len(inner) > 0 && inner[0] == '{' {
			if err := json.Unmarshal(inner, out); err == nil {
				return nil
			}
		}
	}
	// Fall back to the bare shape.
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
