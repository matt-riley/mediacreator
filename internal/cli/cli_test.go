package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"mediacreator/internal/provider"
)

// captureStdout redirects os.Stdout for the duration of fn and returns what
// was written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	fn()
	_ = w.Close()
	os.Stdout = old
	b, _ := io.ReadAll(r)
	return string(b)
}

// splitLines splits trimmed non-empty lines.
func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func mustJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("invalid json %q: %v", s, err)
	}
	return m
}

// fakeUploadProvider is a Provider stub whose only behavior is uploading
// local reference images via UploadImage (the other methods are never used
// by resolveImageURLs).
type fakeUploadProvider struct {
	provider.Provider
	uploads map[string]string
}

func (f *fakeUploadProvider) Name() string { return "fake" }

func (f *fakeUploadProvider) UploadImage(ctx context.Context, path string) (string, error) {
	u, ok := f.uploads[path]
	if !ok {
		return "", fmt.Errorf("no upload configured for %s", path)
	}
	return u, nil
}

// fakeListOnlyProvider is a Provider without UploadImage.
type fakeListOnlyProvider struct{ provider.Provider }

func (f *fakeListOnlyProvider) Name() string { return "list-only" }

func TestResolveImageURLs(t *testing.T) {
	ref := filepath.Join(t.TempDir(), "ref.png")
	if err := os.WriteFile(ref, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("urls pass through", func(t *testing.T) {
		in := []string{"https://example.com/a.png", "http://example.com/b.jpg"}
		out, err := resolveImageURLs(context.Background(), &fakeUploadProvider{}, in)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(out, in) {
			t.Fatalf("out = %v", out)
		}
	})

	t.Run("local file is uploaded", func(t *testing.T) {
		p := &fakeUploadProvider{uploads: map[string]string{ref: "https://v3.fal.media/files/abc"}}
		out, err := resolveImageURLs(context.Background(), p, []string{ref})
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 1 || out[0] != "https://v3.fal.media/files/abc" {
			t.Fatalf("out = %v", out)
		}
	})

	t.Run("missing file errors", func(t *testing.T) {
		_, err := resolveImageURLs(context.Background(), &fakeUploadProvider{}, []string{"/no/such.png"})
		if err == nil || !strings.Contains(err.Error(), "no such file") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("provider without uploader errors", func(t *testing.T) {
		p := &fakeListOnlyProvider{}
		_, err := resolveImageURLs(context.Background(), p, []string{ref})
		if err == nil || !strings.Contains(err.Error(), "does not support uploading") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestGenerateFalLocalReferenceImage(t *testing.T) {
	mediaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	}))
	defer mediaSrv.Close()

	var submitBody string
	falSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/storage/upload":
			_ = json.NewEncoder(w).Encode(map[string]any{"url": mediaSrv.URL + "/files/ref-uploaded.png"})
		case r.Method == "POST":
			b, _ := io.ReadAll(r.Body)
			submitBody = string(b)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"request_id":   "req-up-1",
				"response_url": "http://" + r.Host + "/fal-ai/flux/dev/requests/req-up-1",
				"status_url":   "http://" + r.Host + "/fal-ai/flux/dev/requests/req-up-1/status",
			})
		case r.URL.Path == "/fal-ai/flux/dev/requests/req-up-1/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "COMPLETED"})
		case r.URL.Path == "/fal-ai/flux/dev/requests/req-up-1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"images": []any{map[string]any{"url": mediaSrv.URL + "/files/out.png"}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer falSrv.Close()
	t.Setenv("FAL_KEY", "test-key")
	t.Setenv("FAL_BASE_URL", falSrv.URL)
	t.Setenv("FAL_UPLOAD_URL", falSrv.URL+"/storage/upload")

	ref := filepath.Join(t.TempDir(), "ref.png")
	if err := os.WriteFile(ref, []byte("fake-png"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out")

	var err error
	outStr := captureStdout(t, func() {
		err = runGenerate([]string{
			"--provider", "fal", "--model", "fal-ai/flux/dev",
			"--prompt", "redraw this", "--image-url", ref,
			"--output", out + "/", "--interval", "1s",
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := splitLines(outStr)
	if len(lines) != 2 {
		t.Fatalf("expected 2 json lines, got %d: %s", len(lines), outStr)
	}
	done := mustJSON(t, lines[1])
	if done["status"] != "completed" || done["request_id"] != "req-up-1" {
		t.Fatalf("done line = %v", done)
	}

	// The submitted request must reference the uploaded (public) URL, not the
	// local path.
	var submitted map[string]any
	if err := json.Unmarshal([]byte(submitBody), &submitted); err != nil {
		t.Fatalf("submit body %q: %v", submitBody, err)
	}
	if submitted["image_url"] != mediaSrv.URL+"/files/ref-uploaded.png" {
		t.Fatalf("image_url = %v (submit body %q)", submitted["image_url"], submitBody)
	}
	if submitted["prompt"] != "redraw this" {
		t.Fatalf("prompt = %v", submitted["prompt"])
	}

	files, err := os.ReadDir(out + "/")
	if err != nil || len(files) != 1 {
		t.Fatalf("output dir = %v (err %v)", files, err)
	}
}

func TestGenerateFalFileMode(t *testing.T) {
	mediaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	}))
	defer mediaSrv.Close()

	falSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"request_id":   "req-1",
				"response_url": "http://" + r.Host + "/fal-ai/flux/dev/requests/req-1",
				"status_url":   "http://" + r.Host + "/fal-ai/flux/dev/requests/req-1/status",
			})
		case r.URL.Path == "/fal-ai/flux/dev/requests/req-1/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "COMPLETED"})
		case r.URL.Path == "/fal-ai/flux/dev/requests/req-1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"images": []any{map[string]any{"url": mediaSrv.URL + "/files/fox.png"}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer falSrv.Close()
	t.Setenv("FAL_KEY", "test-key")
	t.Setenv("FAL_BASE_URL", falSrv.URL)

	out := filepath.Join(t.TempDir(), "fox.png")
	var err error
	outStr := captureStdout(t, func() {
		err = runGenerate([]string{
			"--provider", "fal", "--model", "fal-ai/flux/dev",
			"--prompt", "a red fox", "--output", out, "--interval", "1s",
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := splitLines(outStr)
	if len(lines) != 2 {
		t.Fatalf("expected 2 json lines, got %d: %s", len(lines), outStr)
	}
	sub := mustJSON(t, lines[0])
	done := mustJSON(t, lines[1])
	if sub["status"] != "submitted" || sub["request_id"] != "req-1" {
		t.Fatalf("submit line = %v", sub)
	}
	if done["status"] != "completed" || done["request_id"] != "req-1" {
		t.Fatalf("done line = %v", done)
	}
	media, _ := done["media"].([]any)
	if len(media) != 1 {
		t.Fatalf("media = %v", done["media"])
	}
	first := media[0].(map[string]any)
	if first["path"] != out {
		t.Fatalf("path = %v", first["path"])
	}
	if fi, err := os.Stat(out); err != nil {
		t.Fatalf("output file missing: %v", err)
	} else if fi.Size() != 8 {
		t.Fatalf("output size = %d", fi.Size())
	}
}

func TestGenerateFalDirModeMultipleFiles(t *testing.T) {
	mediaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("data"))
	}))
	defer mediaSrv.Close()

	falSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"request_id":   "req-2",
				"response_url": "http://" + r.Host + "/m/requests/req-2",
				"status_url":   "http://" + r.Host + "/m/requests/req-2/status",
			})
		case r.URL.Path == "/m/requests/req-2/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "COMPLETED"})
		case r.URL.Path == "/m/requests/req-2":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"images": []any{
					map[string]any{"url": mediaSrv.URL + "/files/a.png"},
					map[string]any{"url": mediaSrv.URL + "/files/b.png"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer falSrv.Close()
	t.Setenv("FAL_KEY", "test-key")
	t.Setenv("FAL_BASE_URL", falSrv.URL)

	outDir := t.TempDir()
	var err error
	outStr := captureStdout(t, func() {
		err = runGenerate([]string{
			"--provider", "fal", "--model", "fal-ai/flux/dev",
			"--input", `{"prompt":"two images"}`, "--output", outDir + "/",
			"--interval", "1s",
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	done := mustJSON(t, splitLines(outStr)[1])
	media, _ := done["media"].([]any)
	if len(media) != 2 {
		t.Fatalf("media = %v", done["media"])
	}
	// Both files must exist with distinct names inside the directory.
	seen := map[string]bool{}
	for _, m := range media {
		p := m.(map[string]any)["path"].(string)
		if filepath.Dir(p) != outDir {
			t.Fatalf("file %q not inside %q", p, outDir)
		}
		if seen[p] {
			t.Fatalf("duplicate path %q", p)
		}
		seen[p] = true
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("file %q missing: %v", p, err)
		}
	}
}

func TestGenerateKieEndToEnd(t *testing.T) {
	mediaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("jpegdata"))
	}))
	defer mediaSrv.Close()

	kieSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/v1/jobs/createTask":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "success",
				"data": map[string]any{"taskId": "task_456"},
			})
		case r.Method == "GET" && r.URL.Path == "/api/v1/jobs/recordInfo":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "success",
				"data": map[string]any{
					"taskId":     "task_456",
					"state":      "success",
					"resultJson": `{"resultUrls":["` + mediaSrv.URL + `/x/poster.jpg"]}`,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer kieSrv.Close()
	t.Setenv("KIE_API_KEY", "test-key")
	t.Setenv("KIE_BASE_URL", kieSrv.URL)

	out := filepath.Join(t.TempDir(), "poster.jpg")
	var err error
	outStr := captureStdout(t, func() {
		err = runGenerate([]string{
			"--provider", "kie", "--model", "bytedance/seedream",
			"--prompt", "a campsite poster", "--output", out, "--interval", "1s",
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	done := mustJSON(t, splitLines(outStr)[1])
	if done["request_id"] != "task_456" || done["status"] != "completed" {
		t.Fatalf("done = %v", done)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("output missing: %v", err)
	}
}

func TestVersionCommand(t *testing.T) {
	// Version vars are injected at release time; the default must still exit 0
	// (the Homebrew formula test runs `mediacreator version`).
	outStr := captureStdout(t, func() {
		if err := Run([]string{"version"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(outStr, "mediacreator version") {
		t.Fatalf("version output = %q", outStr)
	}
}

func TestStatusAndDownloadCommands(t *testing.T) {
	falSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/fal-ai/flux/dev/requests/req-9/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "IN_QUEUE", "queue_position": 2})
		default:
			http.NotFound(w, r)
		}
	}))
	defer falSrv.Close()
	t.Setenv("FAL_KEY", "test-key")
	t.Setenv("FAL_BASE_URL", falSrv.URL)

	outStr := captureStdout(t, func() {
		if err := runStatus([]string{"--provider", "fal", "--request-id", "req-9", "--model", "fal-ai/flux/dev"}); err != nil {
			t.Fatal(err)
		}
	})
	st := mustJSON(t, splitLines(outStr)[0])
	if st["status"] != "pending" || st["detail"] != "IN_QUEUE (position 2)" {
		t.Fatalf("status = %v", st)
	}
}

// mockCatalog simulates provider catalog APIs with a few known models.
// The fal branch honors q/category/status filters and cursor pagination.
func mockCatalog(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models": // fal official catalog
			q := r.URL.Query()
			all := []map[string]any{
				{"endpoint_id": "fal-ai/flux/dev", "metadata": map[string]any{"display_name": "FLUX.1 [dev]", "category": "text-to-image", "status": "active"}},
				{"endpoint_id": "fal-ai/flux-2-pro", "metadata": map[string]any{"display_name": "Flux 2 Pro", "category": "text-to-image", "status": "active"}},
				{"endpoint_id": "fal-ai/kling-video/text-to-video", "metadata": map[string]any{"display_name": "Kling Video", "category": "text-to-video", "status": "active"}},
				{"endpoint_id": "fal-ai/legacy-model", "metadata": map[string]any{"display_name": "Legacy", "category": "text-to-image", "status": "deprecated"}},
			}
			var items []any
			for _, m := range all {
				id := m["endpoint_id"].(string)
				meta := m["metadata"].(map[string]any)
				if s := q.Get("q"); s != "" && !strings.Contains(strings.ToLower(id), strings.ToLower(s)) {
					continue
				}
				if c := q.Get("category"); c != "" && meta["category"] != c {
					continue
				}
				if s := q.Get("status"); s != "" && meta["status"] != s {
					continue
				}
				if e := q.Get("endpoint_id"); e != "" && id != e {
					continue
				}
				items = append(items, m)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"models": items, "has_more": false, "next_cursor": nil})
		case "/api/v1/playground/model-paths": // kie market
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "success",
				"data": []any{"bytedance/seedream", "bytedance/seedance-2", "veo3"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	return srv
}

func TestListFalJSONAndFilters(t *testing.T) {
	srv := mockCatalog(t)
	defer srv.Close()
	t.Setenv("FAL_CATALOG_URL", srv.URL)

	// Search is applied server-side; the mock filters on q.
	outStr := captureStdout(t, func() {
		if err := runList([]string{"--provider", "fal", "--search", "kling"}); err != nil {
			t.Fatal(err)
		}
	})
	out := mustJSON(t, splitLines(outStr)[0])
	if out["provider"] != "fal" || out["returned"] != float64(1) {
		t.Fatalf("list = %v", out)
	}
	models := out["models"].([]any)
	m := models[0].(map[string]any)
	if m["id"] != "fal-ai/kling-video/text-to-video" || m["title"] != "Kling Video" {
		t.Fatalf("model = %v", m)
	}

	// Category + default status=active excludes the deprecated model.
	outStr = captureStdout(t, func() {
		if err := runList([]string{"--provider", "fal", "--category", "text-to-image"}); err != nil {
			t.Fatal(err)
		}
	})
	out = mustJSON(t, splitLines(outStr)[0])
	if out["returned"] != float64(2) {
		t.Fatalf("category+active returned %v", out["returned"])
	}

	// --status deprecated only shows the deprecated model.
	outStr = captureStdout(t, func() {
		if err := runList([]string{"--provider", "fal", "--status", "deprecated"}); err != nil {
			t.Fatal(err)
		}
	})
	out = mustJSON(t, splitLines(outStr)[0])
	if out["returned"] != float64(1) {
		t.Fatalf("deprecated returned %v", out["returned"])
	}
}

func TestListFalFindMode(t *testing.T) {
	srv := mockCatalog(t)
	defer srv.Close()
	t.Setenv("FAL_CATALOG_URL", srv.URL)

	outStr := captureStdout(t, func() {
		if err := runList([]string{"--provider", "fal", "--endpoint-id", "fal-ai/flux-2-pro"}); err != nil {
			t.Fatal(err)
		}
	})
	out := mustJSON(t, splitLines(outStr)[0])
	models := out["models"].([]any)
	if len(models) != 1 {
		t.Fatalf("find mode returned %v", out)
	}
	m := models[0].(map[string]any)
	if m["id"] != "fal-ai/flux-2-pro" || m["title"] != "Flux 2 Pro" {
		t.Fatalf("model = %v", m)
	}
}

func TestListFalPlain(t *testing.T) {
	srv := mockCatalog(t)
	defer srv.Close()
	t.Setenv("FAL_CATALOG_URL", srv.URL)

	outStr := captureStdout(t, func() {
		if err := runList([]string{"--provider", "fal", "--plain"}); err != nil {
			t.Fatal(err)
		}
	})
	lines := splitLines(outStr)
	want := []string{"fal-ai/flux/dev", "fal-ai/flux-2-pro", "fal-ai/kling-video/text-to-video"}
	if len(lines) != len(want) {
		t.Fatalf("plain output = %q", lines)
	}
	for i, w := range want {
		if lines[i] != w {
			t.Fatalf("plain output = %q", lines)
		}
	}
}

func TestListKieVendorFilter(t *testing.T) {
	srv := mockCatalog(t)
	defer srv.Close()
	t.Setenv("KIE_BASE_URL", srv.URL)

	outStr := captureStdout(t, func() {
		if err := runList([]string{"--provider", "kie", "--vendor", "bytedance"}); err != nil {
			t.Fatal(err)
		}
	})
	out := mustJSON(t, splitLines(outStr)[0])
	if out["provider"] != "kie" || out["total"] != float64(3) || out["returned"] != float64(2) {
		t.Fatalf("list = %v", out)
	}
	models := out["models"].([]any)
	m := models[0].(map[string]any)
	if m["id"] != "bytedance/seedream" || m["vendor"] != "bytedance" {
		t.Fatalf("model = %v", m)
	}
}

func TestListNoKeyRequired(t *testing.T) {
	srv := mockCatalog(t)
	defer srv.Close()
	// No FAL_KEY / KIE_API_KEY set: list must still work.
	t.Setenv("FAL_CATALOG_URL", srv.URL)
	if err := runList([]string{"--provider", "fal", "--plain"}); err != nil {
		t.Fatalf("list without key failed: %v", err)
	}
}

// TestStandardInputAcrossProviders verifies that identical standard flags are
// translated into each provider's native schema and that the completion JSON
// is the same shape regardless of provider.
func TestStandardInputAcrossProviders(t *testing.T) {
	mediaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("img"))
	}))
	defer mediaSrv.Close()

	var falBody, kieBody string
	falSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			b, _ := io.ReadAll(r.Body)
			falBody = string(b)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"request_id":   "req-s",
				"response_url": "http://" + r.Host + "/m/requests/req-s",
				"status_url":   "http://" + r.Host + "/m/requests/req-s/status"})
			return
		}
		switch r.URL.Path {
		case "/m/requests/req-s/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "COMPLETED"})
		case "/m/requests/req-s":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"images": []any{map[string]any{"url": mediaSrv.URL + "/a.png"}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer falSrv.Close()

	kieSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			b, _ := io.ReadAll(r.Body)
			kieBody = string(b)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "success",
				"data": map[string]any{"taskId": "task-s"},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200, "msg": "success",
			"data": map[string]any{
				"taskId": "task-s", "state": "success",
				"resultJson": `{"resultUrls":["` + mediaSrv.URL + `/b.png"]}`,
			},
		})
	}))
	defer kieSrv.Close()

	t.Setenv("FAL_KEY", "k")
	t.Setenv("FAL_BASE_URL", falSrv.URL)
	t.Setenv("KIE_API_KEY", "k")
	t.Setenv("KIE_BASE_URL", kieSrv.URL)

	args := []string{
		"--prompt", "a red fox in the snow",
		"--image-url", "https://x/ref.png",
		"--aspect-ratio", "16:9",
		"--duration", "5",
		"--seed", "42",
		"--output", t.TempDir() + "/",
		"--interval", "1s",
	}

	var falOut, kieOut string
	falOut = captureStdout(t, func() {
		if err := runGenerate(append([]string{"--provider", "fal", "--model", "fal-ai/flux/dev/image-to-image"}, args...)); err != nil {
			t.Fatal(err)
		}
	})
	kieOut = captureStdout(t, func() {
		if err := runGenerate(append([]string{"--provider", "kie", "--model", "bytedance/seedream"}, args...)); err != nil {
			t.Fatal(err)
		}
	})

	// Same prompt reached both providers with the standard params.
	var falReq map[string]any
	if err := json.Unmarshal([]byte(falBody), &falReq); err != nil {
		t.Fatalf("fal body: %v", err)
	}
	if falReq["prompt"] != "a red fox in the snow" || falReq["image_url"] != "https://x/ref.png" {
		t.Fatalf("fal request = %v", falReq)
	}
	var kieReq struct {
		Model string         `json:"model"`
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal([]byte(kieBody), &kieReq); err != nil {
		t.Fatalf("kie body: %v", err)
	}
	if kieReq.Input["prompt"] != "a red fox in the snow" || kieReq.Input["image_url"] != "https://x/ref.png" {
		t.Fatalf("kie request = %v", kieReq)
	}

	falDone := mustJSON(t, splitLines(falOut)[1])
	kieDone := mustJSON(t, splitLines(kieOut)[1])

	// Canonical shape: same keys on both providers, no raw provider payload.
	if len(falDone) != len(kieDone) {
		t.Fatalf("shape mismatch:\nfal: %v\nkie: %v", falDone, kieDone)
	}
	for k := range falDone {
		if _, ok := kieDone[k]; !ok {
			t.Fatalf("key %q missing from kie output", k)
		}
	}
	if _, ok := falDone["result"]; ok {
		t.Fatal("raw result must not appear in canonical output")
	}
	mediaArr := falDone["media"].([]any)
	item := mediaArr[0].(map[string]any)
	if item["type"] != "image" || item["path"] == "" {
		t.Fatalf("media item = %v", item)
	}
	if !reflect.DeepEqual(falDone["images"], []any{item["url"]}) {
		t.Fatalf("images = %v", falDone["images"])
	}
}
