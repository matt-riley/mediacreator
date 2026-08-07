package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRefImage writes a dummy local image file and returns its path.
func writeRefImage(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ref.png")
	if err := os.WriteFile(p, []byte("fake-png-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// serveUpload returns a server that records the multipart upload and replies
// with the given body.
func serveUpload(t *testing.T, body string) (*httptest.Server, *string, *string) {
	t.Helper()
	var gotAuth, gotName string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("multipart parse: %v", err)
			http.Error(w, "bad multipart", 400)
			return
		}
		files := r.MultipartForm.File["file"]
		if len(files) != 1 {
			t.Errorf("expected 1 file in field \"file\", got %d", len(files))
			http.Error(w, "no file", 400)
			return
		}
		gotName = files[0].Filename
		_, _ = io.WriteString(w, body)
	}))
	return srv, &gotAuth, &gotName
}

func TestFalUploadImage(t *testing.T) {
	srv, gotAuth, gotName := serveUpload(t, `{"url":"https://v3.fal.media/files/abc123"}`)
	defer srv.Close()
	t.Setenv("FAL_UPLOAD_URL", srv.URL)

	p, err := NewFal("test-key")
	if err != nil {
		t.Fatal(err)
	}
	up, ok := p.(Uploader)
	if !ok {
		t.Fatal("falProvider does not implement Uploader")
	}
	u, err := up.UploadImage(context.Background(), writeRefImage(t))
	if err != nil {
		t.Fatal(err)
	}
	if u != "https://v3.fal.media/files/abc123" {
		t.Fatalf("url = %q", u)
	}
	if *gotAuth != "Key test-key" {
		t.Fatalf("auth = %q", *gotAuth)
	}
	if *gotName != "ref.png" {
		t.Fatalf("filename = %q", *gotName)
	}
}

func TestKieUploadImage(t *testing.T) {
	srv, gotAuth, _ := serveUpload(t, `{"code":0,"data":{"fileUrl":"https://cdn.kie.ai/upload/xyz"}}`)
	defer srv.Close()
	t.Setenv("KIE_UPLOAD_URL", srv.URL)

	p, err := NewKie("kie-key", "", "auto")
	if err != nil {
		t.Fatal(err)
	}
	up, ok := p.(Uploader)
	if !ok {
		t.Fatal("kieProvider does not implement Uploader")
	}
	u, err := up.UploadImage(context.Background(), writeRefImage(t))
	if err != nil {
		t.Fatal(err)
	}
	if u != "https://cdn.kie.ai/upload/xyz" {
		t.Fatalf("url = %q", u)
	}
	if *gotAuth != "Bearer kie-key" {
		t.Fatalf("auth = %q", *gotAuth)
	}
}

func TestUploadImageHTTPFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"quota exceeded"}`, http.StatusForbidden)
	}))
	defer srv.Close()
	t.Setenv("FAL_UPLOAD_URL", srv.URL)

	p, _ := NewFal("test-key")
	_, err := p.(Uploader).UploadImage(context.Background(), writeRefImage(t))
	if err == nil {
		t.Fatal("expected error for HTTP 403")
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("error = %v", err)
	}
}

func TestUploadImageMissingFile(t *testing.T) {
	srv, _, _ := serveUpload(t, `{"url":"x"}`)
	defer srv.Close()
	t.Setenv("FAL_UPLOAD_URL", srv.URL)

	p, _ := NewFal("test-key")
	_, err := p.(Uploader).UploadImage(context.Background(), "/no/such/file.png")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("error = %v", err)
	}
}

func TestUploadImageResponseWithoutURL(t *testing.T) {
	srv, _, _ := serveUpload(t, `{"ok":true}`)
	defer srv.Close()
	t.Setenv("FAL_UPLOAD_URL", srv.URL)

	p, _ := NewFal("test-key")
	_, err := p.(Uploader).UploadImage(context.Background(), writeRefImage(t))
	if err == nil {
		t.Fatal("expected error when response has no url")
	}
	if !strings.Contains(err.Error(), "did not return a url") {
		t.Fatalf("error = %v", err)
	}
}
