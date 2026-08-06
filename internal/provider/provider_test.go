package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"mediacreator/internal/media"
)

// mockFal simulates the fal.ai queue API.
func mockFal(t *testing.T, result map[string]any) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var polls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Key test-key" {
			http.Error(w, `{"detail":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == "POST" && r.URL.Path == "/fal-ai/flux/dev":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"request_id":   "req-123",
				"response_url": srvURL(r) + "/fal-ai/flux/dev/requests/req-123",
				"status_url":   srvURL(r) + "/fal-ai/flux/dev/requests/req-123/status",
				"cancel_url":   srvURL(r) + "/fal-ai/flux/dev/requests/req-123/cancel",
			})
		case r.Method == "GET" && r.URL.Path == "/fal-ai/flux/dev/requests/req-123/status":
			n := polls.Add(1)
			if n < 2 {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"status": "IN_QUEUE", "queue_position": 3,
					"response_url": srvURL(r) + "/fal-ai/flux/dev/requests/req-123",
					"status_url":   srvURL(r) + "/fal-ai/flux/dev/requests/req-123/status",
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":       "COMPLETED",
				"response_url": srvURL(r) + "/fal-ai/flux/dev/requests/req-123",
				"status_url":   srvURL(r) + "/fal-ai/flux/dev/requests/req-123/status",
			})
		case r.Method == "GET" && r.URL.Path == "/fal-ai/flux/dev/requests/req-123":
			_ = json.NewEncoder(w).Encode(result)
		default:
			http.NotFound(w, r)
		}
	}))
	return srv, &polls
}

func srvURL(r *http.Request) string { return "http://" + r.Host }

func TestFalEndToEnd(t *testing.T) {
	result := map[string]any{
		"images": []any{
			map[string]any{"url": "https://v3.fal.media/files/abc/red-fox.png", "content_type": "image/png"},
		},
	}
	srv, _ := mockFal(t, result)
	defer srv.Close()

	t.Setenv("FAL_KEY", "test-key")
	t.Setenv("FAL_BASE_URL", srv.URL)
	p, err := NewFal("test-key")
	if err != nil {
		t.Fatal(err)
	}
	if got := p.(*falProvider).base; got != srv.URL {
		t.Fatalf("base = %q, want %q", got, srv.URL)
	}

	ctx := context.Background()
	job, err := p.Submit(ctx, "fal-ai/flux/dev", map[string]any{"prompt": "a red fox"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if job.ID != "req-123" {
		t.Fatalf("job id = %q", job.ID)
	}

	st1, err := p.Status(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	if st1.Phase != PhasePending || st1.Detail != "IN_QUEUE (position 3)" {
		t.Fatalf("first status = %+v", st1)
	}

	var final map[string]any
	for {
		st, err := p.Status(ctx, job)
		if err != nil {
			t.Fatal(err)
		}
		if st.Phase == PhaseCompleted {
			final, err = p.Result(ctx, job)
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		if st.Phase == PhaseFailed {
			t.Fatalf("unexpected failure: %+v", st)
		}
	}
	urls := media.ExtractURLs(final)
	if len(urls) != 1 || urls[0] != "https://v3.fal.media/files/abc/red-fox.png" {
		t.Fatalf("extracted %v", urls)
	}
}

func TestFalFailedJob(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"request_id": "req-bad",
				"status_url": "http://" + r.Host + "/status",
			})
		case r.URL.Path == "/status":
			w.WriteHeader(500)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "FAILED", "error": "model exploded"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	p, _ := NewFal("k")
	p.(*falProvider).base = srv.URL

	job, err := p.Submit(context.Background(), "fal-ai/flux/dev", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	st, err := p.Status(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != PhaseFailed {
		t.Fatalf("phase = %v, want failed", st.Phase)
	}
	if !contains(st.Detail, "model exploded") {
		t.Fatalf("detail = %q", st.Detail)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// mockKie simulates the kie.ai market jobs API.
func mockKie(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, `{"code":401,"msg":"no access"}`, http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/v1/jobs/createTask":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["model"] != "bytedance/seedream" {
				http.Error(w, fmt.Sprintf(`{"code":400,"msg":"unknown model %v"}`, body["model"]), 400)
				return
			}
			input, _ := body["input"].(map[string]any)
			if input["prompt"] == "" {
				http.Error(w, `{"code":422,"msg":"prompt required"}`, http.StatusUnprocessableEntity)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "success",
				"data": map[string]any{"taskId": "task_456"},
			})
		case r.Method == "GET" && r.URL.Path == "/api/v1/jobs/recordInfo":
			if r.URL.Query().Get("taskId") != "task_456" {
				http.Error(w, `{"code":404,"msg":"Task not found"}`, 404)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "success",
				"data": map[string]any{
					"taskId":     "task_456",
					"model":      "bytedance/seedream",
					"state":      "success",
					"resultJson": `{"resultUrls":["https://cdn.kie.ai/x/poster.jpg"],"seed":7}`,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	return srv
}

func TestKieMarketEndToEnd(t *testing.T) {
	srv := mockKie(t)
	defer srv.Close()

	t.Setenv("KIE_API_KEY", "test-key")
	t.Setenv("KIE_BASE_URL", srv.URL)
	p, err := NewKie("test-key", "", "auto")
	if err != nil {
		t.Fatal(err)
	}
	if !p.(*kieProvider).useMarket("bytedance/seedream") {
		t.Fatal("expected market mode for unknown family")
	}

	ctx := context.Background()
	job, err := p.Submit(ctx, "bytedance/seedream", map[string]any{"prompt": "poster of a campsite"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if job.ID != "task_456" {
		t.Fatalf("job id = %q", job.ID)
	}
	st, err := p.Status(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != PhaseCompleted {
		t.Fatalf("phase = %v (%+v)", st.Phase, st)
	}
	res, err := p.Result(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	urls := media.ExtractURLs(res)
	if len(urls) != 1 || urls[0] != "https://cdn.kie.ai/x/poster.jpg" {
		t.Fatalf("extracted %v", urls)
	}
}

// mockKieVeo simulates the kie.ai veo family endpoints (auto-detected).
func mockKieVeo(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/v1/veo/generate":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "success",
				"data": map[string]any{"taskId": "veo_task_9"},
			})
		case r.Method == "GET" && r.URL.Path == "/api/v1/veo/record-info":
			if r.URL.Query().Get("taskId") != "veo_task_9" {
				http.Error(w, `{"code":404,"msg":"not found"}`, 404)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "success",
				"data": map[string]any{
					"taskId":      "veo_task_9",
					"successFlag": 1,
					"response": map[string]any{
						"resultUrls": []any{"https://cdn.kie.ai/videos/veo9.mp4"},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	return srv
}

func TestKieVeoAutoMode(t *testing.T) {
	srv := mockKieVeo(t)
	defer srv.Close()

	p, _ := NewKie("test-key", "", "auto")
	p.(*kieProvider).base = srv.URL
	if p.(*kieProvider).useMarket("veo3") {
		t.Fatal("expected model-family mode for veo3")
	}

	ctx := context.Background()
	job, err := p.Submit(ctx, "veo3", map[string]any{"prompt": "dog playing"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if job.ID != "veo_task_9" {
		t.Fatalf("job id = %q", job.ID)
	}
	st, err := p.Status(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != PhaseCompleted {
		t.Fatalf("phase = %v", st.Phase)
	}
	res, err := p.Result(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	urls := media.ExtractURLs(res)
	if len(urls) != 1 || urls[0] != "https://cdn.kie.ai/videos/veo9.mp4" {
		t.Fatalf("extracted %v", urls)
	}
}

func TestNewFalRequiresKey(t *testing.T) {
	_ = os.Unsetenv("FAL_KEY")
	if _, err := NewFal(""); err == nil {
		t.Fatal("expected error without key")
	}
}

// mockFalCatalog simulates the official fal.ai model catalog API
// (GET /v1/models). It applies q/category/status filters and cursor
// pagination like the real API.
func mockFalCatalog(t *testing.T, seen *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if seen != nil {
			*seen = append(*seen, r.URL.RawQuery)
		}
		q := r.URL.Query()
		all := []map[string]any{
			{"endpoint_id": "fal-ai/flux/dev", "metadata": map[string]any{"display_name": "FLUX.1 [dev]", "category": "text-to-image", "status": "active"}},
			{"endpoint_id": "fal-ai/flux-2-pro", "metadata": map[string]any{"display_name": "Flux 2 Pro", "category": "text-to-image", "status": "active"}},
			{"endpoint_id": "fal-ai/kling-video/text-to-video", "metadata": map[string]any{"display_name": "Kling Video", "category": "text-to-video", "status": "active"}},
			{"endpoint_id": "fal-ai/old-model", "metadata": map[string]any{"display_name": "Old Model", "category": "text-to-image", "status": "deprecated"}},
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
		// Simulate one page of 2 items with a cursor.
		page := 1
		if c := q.Get("cursor"); c != "" {
			if b, err := base64.RawURLEncoding.DecodeString(c); err == nil {
				page, _ = strconv.Atoi(string(b))
			}
		}
		start := (page - 1) * 2
		hasMore := start+2 < len(items)
		end := start + 2
		if end > len(items) {
			end = len(items)
		}
		if start > len(items) {
			start = len(items)
		}
		pageItems := items[start:end]
		var next any
		if hasMore {
			next = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(page + 1)))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models":      pageItems,
			"has_more":    hasMore,
			"next_cursor": next,
		})
	}))
	return srv
}

func TestFalListServerSideFilters(t *testing.T) {
	var seen []string
	srv := mockFalCatalog(t, &seen)
	defer srv.Close()
	p := NewFalList()
	p.(*falProvider).catalog = srv.URL

	l, err := p.List(context.Background(), ListOptions{
		Search:   "flux",
		Category: "text-to-image",
		Status:   "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Models) != 2 || l.Models[0].ID != "fal-ai/flux/dev" || l.Models[1].ID != "fal-ai/flux-2-pro" {
		t.Fatalf("models = %+v", l.Models)
	}
	if l.Total != -1 || l.HasMore || l.NextCursor != "" {
		t.Fatalf("list = %+v", l)
	}
	if len(seen) != 1 || !strings.Contains(seen[0], "q=flux") ||
		!strings.Contains(seen[0], "category=text-to-image") ||
		!strings.Contains(seen[0], "status=active") {
		t.Fatalf("server saw %q", seen)
	}
}

func TestFalListFindMode(t *testing.T) {
	var seen []string
	srv := mockFalCatalog(t, &seen)
	defer srv.Close()
	p := NewFalList()
	p.(*falProvider).catalog = srv.URL

	l, err := p.List(context.Background(), ListOptions{EndpointID: "fal-ai/flux-2-pro"})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Models) != 1 || l.Models[0].Title != "Flux 2 Pro" {
		t.Fatalf("models = %+v", l.Models)
	}
	if len(seen) != 1 || !strings.Contains(seen[0], "endpoint_id=fal-ai%2Fflux-2-pro") {
		t.Fatalf("server saw %q", seen)
	}
}

func TestFalListCursorPagination(t *testing.T) {
	srv := mockFalCatalog(t, nil)
	defer srv.Close()
	p := NewFalList()
	p.(*falProvider).catalog = srv.URL

	// Single page: 2 items, has_more true, next cursor present.
	l, err := p.List(context.Background(), ListOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Models) != 2 || !l.HasMore || l.NextCursor == "" {
		t.Fatalf("list = %+v", l)
	}

	// All pages: follows the cursor to the end.
	l, err = p.List(context.Background(), ListOptions{Limit: 2, All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Models) != 4 || l.HasMore {
		t.Fatalf("all pages = %+v", l)
	}

	// Page 2 via --page: cursor is base64 of the page number.
	l, err = p.List(context.Background(), ListOptions{Limit: 2, Page: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Models) != 2 || l.Models[0].ID != "fal-ai/kling-video/text-to-video" {
		t.Fatalf("page 2 = %+v", l.Models)
	}
}

func TestKieList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/playground/model-paths" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200, "msg": "success",
			"data": []any{"bytedance/seedream", "veo3", "kling-1.0"},
		})
	}))
	defer srv.Close()
	p := NewKieList()
	p.(*kieProvider).base = srv.URL

	l, err := p.List(context.Background(), ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if l.Total != 3 || len(l.Models) != 3 {
		t.Fatalf("list = %+v", l)
	}
	if l.Source != "live" {
		t.Fatalf("source = %q, want live", l.Source)
	}
	if l.Models[0].ID != "bytedance/seedream" || l.Models[0].Vendor != "bytedance" {
		t.Fatalf("model 0 = %+v", l.Models[0])
	}
	if l.Models[1].Vendor != "" {
		t.Fatalf("veo3 has no vendor prefix, got %q", l.Models[1].Vendor)
	}
}

func TestKieListFallsBackToEmbeddedCatalog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"code":502,"msg":"boom"}`, http.StatusBadGateway)
	}))
	defer srv.Close()
	p := NewKieList()
	p.(*kieProvider).base = srv.URL

	l, err := p.List(context.Background(), ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if l.Source != "fallback" {
		t.Fatalf("source = %q, want fallback", l.Source)
	}
	if len(l.Models) != len(kieFallbackCatalog) || l.Total != len(kieFallbackCatalog) {
		t.Fatalf("fallback returned %d models, want %d", len(l.Models), len(kieFallbackCatalog))
	}
	if l.Models[0].ID != kieFallbackCatalog[0] {
		t.Fatalf("first fallback model = %q", l.Models[0].ID)
	}
}

func TestNewFalListAndKieListDoNotNeedKeys(t *testing.T) {
	_ = os.Unsetenv("FAL_KEY")
	_ = os.Unsetenv("KIE_API_KEY")
	_ = os.Unsetenv("KIE_KEY")
	if NewFalList() == nil || NewKieList() == nil {
		t.Fatal("list constructors should not require keys")
	}
}

func TestDoJSONRetriesOn429(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			http.Error(w, `{"detail":"rate limited"}`, http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	m, err := doJSON(context.Background(), "GET", srv.URL, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if m["ok"] != true || calls.Load() != 3 {
		t.Fatalf("calls = %d, result = %v", calls.Load(), m)
	}
}
