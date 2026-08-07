package provider

import (
	"bytes"
	"context"
	"encoding/json"
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

// uploadMultipart POSTs a local file plus optional extra form fields and
// returns the decoded JSON response.
func uploadMultipart(ctx context.Context, u, authHeader, fileField, path string, fields map[string]string) (map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening reference image %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			return nil, err
		}
	}
	fw, err := mw.CreateFormFile(fileField, filepath.Base(path))
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

// putFile streams a local file to a (presigned) URL with an HTTP PUT — the
// second half of fal's two-step upload.
func putFile(ctx context.Context, u, path, contentType string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening reference image %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, f)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("User-Agent", "mediacreator/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("PUT %s failed: %w", u, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading PUT response from %s: %w", u, err)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("PUT %s returned %d: %s", u, resp.StatusCode, truncate(strings.TrimSpace(string(raw)), 500))
	}
	return nil
}

// contentTypeOf guesses a MIME type from the file extension (used for fal's
// initiate payload).
func contentTypeOf(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".bmp":
		return "image/bmp"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	default:
		return "application/octet-stream"
	}
}

// firstURL looks for a media URL in the documented upload response shapes:
// fal returns {"url": ...} from the SDK endpoints, while kie's file upload
// returns {"data": {"downloadUrl": ...}}. A few synonyms are probed so minor
// schema drift doesn't break it.
func firstURL(m map[string]any) string {
	for _, k := range []string{"url", "fileUrl", "file_url", "upload_url"} {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	if d, ok := m["data"].(map[string]any); ok {
		for _, k := range []string{"downloadUrl", "fileUrl", "url", "file_url", "path"} {
			if s, ok := d[k].(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

const falUploadInitiateURL = "https://rest.fal.ai/storage/upload/initiate?storage_type=fal-cdn-v3"

// UploadImage uploads a local reference image to the fal CDN using the same
// two-step flow as the official SDKs (see fal.ai/docs/documentation/model-apis/fal-cdn):
//
//	POST {rest.fal.ai}/storage/upload/initiate?storage_type=fal-cdn-v3
//	  {"content_type": ..., "file_name": ...}   -> {"upload_url", "file_url"}
//	PUT {upload_url}                             (presigned, raw file bytes)
//
// FAL_UPLOAD_URL overrides the initiate endpoint (e.g. for tests or proxies).
func (p *falProvider) UploadImage(ctx context.Context, path string) (string, error) {
	initiate := envOr("FAL_UPLOAD_URL", falUploadInitiateURL)
	ct := contentTypeOf(path)
	body, err := json.Marshal(map[string]any{
		"content_type": ct,
		"file_name":    filepath.Base(path),
	})
	if err != nil {
		return "", err
	}
	resp, err := doJSON(ctx, http.MethodPost, initiate, "Key "+p.key, body)
	if err != nil {
		return "", err
	}
	uploadURL, _ := resp["upload_url"].(string)
	fileURL, _ := resp["file_url"].(string)
	if uploadURL == "" || fileURL == "" {
		return "", fmt.Errorf("fal upload initiate did not return upload_url/file_url; response: %v", resp)
	}
	if err := putFile(ctx, uploadURL, path, ct); err != nil {
		return "", fmt.Errorf("uploading file body: %w", err)
	}
	return fileURL, nil
}

const kieUploadURL = "https://kieai.redpandaai.co/api/file-stream-upload"

// kieUploadPath is the required uploadPath field from kie's File Upload API
// docs (docs.kie.ai/file-upload-api) — uploaded files are temporary.
const kieUploadPath = "images/user-uploads"

// UploadImage uploads a local reference image via kie's File Stream Upload API
// (POST /api/file-stream-upload, Bearer auth, multipart fields file +
// uploadPath) and returns data.downloadUrl. KIE_UPLOAD_URL overrides the
// endpoint (e.g. for tests or proxies).
func (p *kieProvider) UploadImage(ctx context.Context, path string) (string, error) {
	resp, err := uploadMultipart(ctx, envOr("KIE_UPLOAD_URL", kieUploadURL), p.auth(), "file", path, map[string]string{
		"uploadPath": kieUploadPath,
	})
	if err != nil {
		return "", err
	}
	if u := firstURL(resp); u != "" {
		return u, nil
	}
	return "", fmt.Errorf("kie upload did not return a download url; response: %v", resp)
}
