package provider

import (
	"context"
	"encoding/json"
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

func TestFalUploadImage(t *testing.T) {
	var gotAuth, gotInitBody string
	var putOK bool
	var putBytes []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/storage/upload/initiate":
			gotAuth = r.Header.Get("Authorization")
			b, _ := io.ReadAll(r.Body)
			gotInitBody = string(b)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"upload_url": "http://" + r.Host + "/presigned/ref.png",
				"file_url":   "https://v3b.fal.media/files/b/abc/ref.png",
			})
		case r.Method == http.MethodPut && r.URL.Path == "/presigned/ref.png":
			putOK = true
			putBytes, _ = io.ReadAll(r.Body)
			_, _ = io.WriteString(w, "ok")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAL_UPLOAD_URL", srv.URL+"/storage/upload/initiate")

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
	if u != "https://v3b.fal.media/files/b/abc/ref.png" {
		t.Fatalf("url = %q", u)
	}
	if gotAuth != "Key test-key" {
		t.Fatalf("initiate auth = %q", gotAuth)
	}
	var initBody map[string]any
	if err := json.Unmarshal([]byte(gotInitBody), &initBody); err != nil {
		t.Fatalf("initiate body %q: %v", gotInitBody, err)
	}
	if initBody["content_type"] != "image/png" || initBody["file_name"] != "ref.png" {
		t.Fatalf("initiate body = %v", initBody)
	}
	if !putOK {
		t.Fatal("expected PUT to the presigned upload_url")
	}
	if string(putBytes) != "fake-png-bytes" {
		t.Fatalf("PUT body = %q", putBytes)
	}
}

func TestFalUploadImageContentTypes(t *testing.T) {
	for _, tc := range []struct {
		file, want string
	}{
		{"a.png", "image/png"},
		{"a.JPG", "image/jpeg"},
		{"a.webp", "image/webp"},
		{"a.mp4", "video/mp4"},
		{"a.xyz", "application/octet-stream"},
	} {
		if got := contentTypeOf(tc.file); got != tc.want {
			t.Errorf("contentTypeOf(%q) = %q, want %q", tc.file, got, tc.want)
		}
	}
}

func TestKieUploadImage(t *testing.T) {
	var gotAuth string
	var gotFields map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("multipart parse: %v", err)
			http.Error(w, "bad multipart", 400)
			return
		}
		gotFields = r.MultipartForm.Value
		if len(r.MultipartForm.File["file"]) != 1 {
			t.Errorf("expected 1 file in field \"file\", got %d", len(r.MultipartForm.File["file"]))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"code":    200,
			"msg":     "File uploaded successfully",
			"data": map[string]any{
				"fileName":    "ref.png",
				"filePath":    "images/user-uploads/ref.png",
				"downloadUrl": "https://tempfile.redpandaai.co/x/images/user-uploads/ref.png",
				"fileSize":    15,
				"mimeType":    "image/png",
			},
		})
	}))
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
	if u != "https://tempfile.redpandaai.co/x/images/user-uploads/ref.png" {
		t.Fatalf("url = %q", u)
	}
	if gotAuth != "Bearer kie-key" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if len(gotFields["uploadPath"]) != 1 || gotFields["uploadPath"][0] != "images/user-uploads" {
		t.Fatalf("uploadPath = %q", gotFields["uploadPath"])
	}
}

func TestUploadImageHTTPFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"quota exceeded"}}`, http.StatusForbidden)
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"upload_url": "http://" + r.Host + "/presigned/x",
			"file_url":   "https://v3b.fal.media/files/x",
		})
	}))
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()
	t.Setenv("FAL_UPLOAD_URL", srv.URL)

	p, _ := NewFal("test-key")
	_, err := p.(Uploader).UploadImage(context.Background(), writeRefImage(t))
	if err == nil {
		t.Fatal("expected error when response has no url")
	}
	if !strings.Contains(err.Error(), "upload_url/file_url") {
		t.Fatalf("error = %v", err)
	}
}
