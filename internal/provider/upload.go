package provider

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Uploader is implemented by providers that can host local files (reference
// images) and hand back a public URL for them.
type Uploader interface {
	// UploadImage uploads the local file at path and returns its public URL.
	UploadImage(ctx context.Context, path string) (string, error)
}

// uploadFile POSTs a local file as multipart form data and returns the decoded
// JSON response. field is the multipart field name expected by the provider.
func uploadFile(ctx context.Context, u, authHeader, field, path string) (map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening reference image %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile(field, filepath.Base(path))
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return nil, fmt.Errorf("reading %q: %w", path, err)
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	req.Header.Set("User-Agent", "mediacreator/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upload to %s failed: %w", u, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading upload response from %s: %w", u, err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("upload to %s returned %d: %s", u, resp.StatusCode, truncate(strings.TrimSpace(string(raw)), 500))
	}
	m, err := parseJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("parsing upload response from %s: %s", u, truncate(strings.TrimSpace(string(raw)), 500))
	}
	return m, nil
}

// firstURL looks for a media URL in the common upload response shapes:
// fal returns {"url": "..."} and kie returns {"data": {"fileUrl": "..."}}.
// A handful of synonyms are probed so minor schema drift doesn't break it.
func firstURL(m map[string]any) string {
	for _, k := range []string{"url", "fileUrl", "file_url", "upload_url"} {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	if d, ok := m["data"].(map[string]any); ok {
		for _, k := range []string{"fileUrl", "url", "file_url", "path"} {
			if s, ok := d[k].(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

const falUploadURL = "https://rest.alpha.fal.ai/storage/upload"

// UploadImage uploads a local reference image to fal storage
// (POST /storage/upload, multipart field "file") and returns the public URL.
// FAL_UPLOAD_URL overrides the endpoint (e.g. for tests or proxies).
func (p *falProvider) UploadImage(ctx context.Context, path string) (string, error) {
	resp, err := uploadFile(ctx, envOr("FAL_UPLOAD_URL", falUploadURL), "Key "+p.key, "file", path)
	if err != nil {
		return "", err
	}
	if u := firstURL(resp); u != "" {
		return u, nil
	}
	return "", fmt.Errorf("fal upload did not return a url; response: %v", resp)
}

const kieUploadURL = "https://api.kie.ai/api/v1/media/upload"

// UploadImage uploads a local reference image to kie media storage
// (POST /api/v1/media/upload, multipart field "file") and returns the public
// URL. KIE_UPLOAD_URL overrides the endpoint (e.g. for tests or proxies).
func (p *kieProvider) UploadImage(ctx context.Context, path string) (string, error) {
	resp, err := uploadFile(ctx, envOr("KIE_UPLOAD_URL", kieUploadURL), p.auth(), "file", path)
	if err != nil {
		return "", err
	}
	if u := firstURL(resp); u != "" {
		return u, nil
	}
	return "", fmt.Errorf("kie upload did not return a file url; response: %v", resp)
}
