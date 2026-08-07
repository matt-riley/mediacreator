package media

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("hello-media"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "sub", "out")
	n, err := Download(context.Background(), srv.URL+"/files/out.png", dest)
	if err != nil {
		t.Fatal(err)
	}
	if n != 11 {
		t.Fatalf("size = %d", n)
	}
	b, err := os.ReadFile(dest + ".png") // extension inferred from Content-Type
	if err != nil || string(b) != "hello-media" {
		t.Fatalf("content = %q (err %v)", b, err)
	}
	// No ".part" temp file left behind.
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatalf("temp file left behind: %v", err)
	}
}

func TestDownloadInfersExtensionFromContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4; charset=binary")
		_, _ = w.Write([]byte("clip"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "clip") // no extension
	n, err := Download(context.Background(), srv.URL+"/clip", dest)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("size = %d", n)
	}
	if _, err := os.Stat(dest + ".mp4"); err != nil {
		t.Fatalf("expected clip.mp4: %v", err)
	}
}

func TestDownloadHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusGone)
	}))
	defer srv.Close()
	_, err := Download(context.Background(), srv.URL+"/x", filepath.Join(t.TempDir(), "x.png"))
	if err == nil || !strings.Contains(err.Error(), "410") {
		t.Fatalf("err = %v", err)
	}
}

func TestDownloadBadURL(t *testing.T) {
	_, err := Download(context.Background(), "://bad url", filepath.Join(t.TempDir(), "x"))
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestExtFromContentType(t *testing.T) {
	cases := map[string]string{
		"image/png":                ".png",
		"image/jpeg":               ".jpg",
		"image/jpg":                ".jpg",
		"image/webp":               ".webp",
		"image/gif":                ".gif",
		"image/bmp":                ".bmp",
		"image/svg+xml":            ".svg",
		"image/avif":               ".avif",
		"video/mp4":                ".mp4",
		"video/webm":               ".webm",
		"video/quicktime":          ".mov",
		"audio/mpeg":               ".mp3",
		"audio/mp3":                ".mp3",
		"audio/wav":                ".wav",
		"audio/x-wav":              ".wav",
		"audio/flac":               ".flac",
		"audio/ogg":                ".ogg",
		"audio/aac":                ".aac",
		"IMAGE/PNG":                ".png", // case-insensitive
		"video/mp4; codec=h2":      ".mp4",
		"application/octet-stream": ".bin",
		"":                         ".bin",
	}
	for ct, want := range cases {
		if got := extFromContentType(ct); got != want {
			t.Errorf("extFromContentType(%q) = %q, want %q", ct, got, want)
		}
	}
}

func TestIsMediaURLKey(t *testing.T) {
	// isMediaURLKey receives lowercased keys (ExtractMedia lowercases them
	// while walking); call it the same way here.
	media := []string{"url", "image_url", "videourl", "resulturls", "audiourl", "originimageurl"}
	for _, k := range media {
		if !isMediaURLKey(k) {
			t.Errorf("isMediaURLKey(%q) = false, want true", k)
		}
	}
	// Queue/callback metadata keys with "url" in them must be excluded.
	excluded := []string{"status_url", "response_url", "cancel_url", "webhook_url", "callbackurl"}
	for _, k := range excluded {
		if isMediaURLKey(k) {
			t.Errorf("isMediaURLKey(%q) = true, want false", k)
		}
	}
	if isMediaURLKey("prompt") {
		t.Error("isMediaURLKey(prompt) = true, want false")
	}
}
