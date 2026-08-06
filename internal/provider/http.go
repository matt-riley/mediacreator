package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// errorText picks the most useful message out of an error response body.
func errorText(m map[string]any) string {
	for _, k := range []string{"error", "detail", "message", "msg", "failMsg", "errorMessage"} {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	if s, ok := m["msg"].(string); ok && s != "" {
		return s
	}
	b, _ := json.Marshal(m)
	return truncate(string(b), 500)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// httpClient is the shared HTTP client. Generations can take a while, but
// each individual request should be quick.
var httpClient = &http.Client{}

// do performs a request and returns the HTTP status code and decoded JSON
// body. Only transport-level failures produce a non-nil error; non-2xx
// responses are returned to the caller to interpret.
func do(ctx context.Context, method, url, authHeader string, body []byte) (int, map[string]any, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return 0, nil, err
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "mediacreator/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("request to %s failed: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("reading response from %s: %w", url, err)
	}
	m, err := parseJSON(raw)
	if err != nil && resp.StatusCode >= 400 {
		return resp.StatusCode, map[string]any{}, fmt.Errorf("%s returned %d: %s", url, resp.StatusCode, truncate(strings.TrimSpace(string(raw)), 500))
	}
	return resp.StatusCode, m, nil
}

func parseJSON(raw []byte) (map[string]any, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return map[string]any{}, nil
	}
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

// doJSON performs a request and returns the decoded JSON response body.
// Non-2xx responses are turned into errors using the body's error fields.
// HTTP 429 responses are retried a few times with backoff.
func doJSON(ctx context.Context, method, url, authHeader string, body []byte) (map[string]any, error) {
	for attempt := 0; ; attempt++ {
		code, m, err := do(ctx, method, url, authHeader, body)
		if err != nil {
			return nil, err
		}
		if code >= 400 {
			if code == http.StatusTooManyRequests && attempt < 3 {
				if !sleepCtx(ctx, time.Duration(1<<attempt)*time.Second) {
					return nil, ctx.Err()
				}
				continue
			}
			msg := errorText(m)
			if msg == "" {
				msg = fmt.Sprintf("HTTP %d", code)
			}
			return nil, fmt.Errorf("%s returned %d: %s", url, code, msg)
		}
		return m, nil
	}
}

// sleepCtx sleeps for d or until ctx is done, reporting whether the full
// duration elapsed.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
