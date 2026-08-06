package media

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Download fetches rawURL and writes it to dest, creating parent directories.
// If dest has no extension, one is inferred from the URL or Content-Type.
// Returns the number of bytes written.
func Download(ctx context.Context, rawURL, dest string) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "mediacreator/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	if filepath.Ext(dest) == "" {
		dest += extFromContentType(resp.Header.Get("Content-Type"))
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return 0, err
	}

	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return 0, err
	}
	n, copyErr := io.Copy(f, resp.Body)
	if closeErr := f.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		_ = os.Remove(tmp)
		return 0, copyErr
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	return n, nil
}

// extFromContentType maps a MIME type to a file extension.
func extFromContentType(ct string) string {
	ct = strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0]))
	switch ct {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/bmp":
		return ".bmp"
	case "image/svg+xml":
		return ".svg"
	case "image/avif":
		return ".avif"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "video/quicktime":
		return ".mov"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/flac":
		return ".flac"
	case "audio/ogg":
		return ".ogg"
	case "audio/aac":
		return ".aac"
	default:
		return ".bin"
	}
}
