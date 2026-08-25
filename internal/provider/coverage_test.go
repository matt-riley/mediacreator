package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// falStatusServer serves a configurable status response at a fixed path.
func falStatusServer(t *testing.T, body string, code int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFalStatusStates(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		code    int
		want    Phase
		wantSub string
		wantErr bool
	}{
		{"in queue", `{"status":"IN_QUEUE","queue_position":7}`, 200, PhasePending, "IN_QUEUE (position 7)", false},
		{"in progress", `{"status":"IN_PROGRESS"}`, 200, PhasePending, "IN_PROGRESS", false},
		{"completed", `{"status":"COMPLETED"}`, 200, PhaseCompleted, "COMPLETED", false},
		{"failed string", `{"status":"FAILED","error":"boom"}`, 200, PhaseFailed, "boom", false},
		{"failed via http error", `{"error":"internal error"}`, 500, PhaseFailed, "internal error", false},
		{"http error no body", ``, 500, PhaseFailed, "", false},
		{"unknown status", `{"status":"WEIRD"}`, 200, PhasePending, "WEIRD", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := falStatusServer(t, tc.body, tc.code)
			p := NewFalList().(*falProvider)
			p.key = "k"
			p.base = srv.URL
			st, err := p.Status(context.Background(), &Job{ID: "r1", Model: "m"})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", st)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if st.Phase != tc.want {
				t.Fatalf("phase = %s, want %s (detail %q)", st.Phase, tc.want, st.Detail)
			}
			if tc.wantSub != "" && !strings.Contains(st.Detail, tc.wantSub) {
				t.Fatalf("detail = %q, want substring %q", st.Detail, tc.wantSub)
			}
		})
	}
}

func TestFalStatusFallsBackToConstructedURL(t *testing.T) {
	// Job without StatusURL: Status must build {base}/{model}/requests/{id}/status.
	srv := falStatusServer(t, `{"status":"COMPLETED"}`, 200)
	p := NewFalList().(*falProvider)
	p.key = "k"
	p.base = srv.URL
	st, err := p.Status(context.Background(), &Job{ID: "r9", Model: "fal-ai/x"})
	if err != nil || st.Phase != PhaseCompleted {
		t.Fatalf("st = %+v err %v", st, err)
	}
}

func TestFalStatusTransportError(t *testing.T) {
	// Unreachable base -> do() returns a transport-level error.
	p := NewFalList().(*falProvider)
	p.key = "k"
	p.base = "http://127.0.0.1:1"
	_, err := p.Status(context.Background(), &Job{ID: "r1", Model: "m"})
	if err == nil {
		t.Fatal("expected transport error")
	}
}

func TestProviderNames(t *testing.T) {
	if NewFalList().Name() != "fal" {
		t.Fatal("fal name")
	}
	if NewKieList().Name() != "kie" {
		t.Fatal("kie name")
	}
}

func TestNewKieInvalidMode(t *testing.T) {
	if _, err := NewKie("k", "", "bogus"); err == nil {
		t.Fatal("expected error for invalid mode")
	}
	if _, err := NewKie("k", "", ""); err != nil {
		t.Fatalf("empty mode should default to auto: %v", err)
	}
}

func TestKieUseMarketModes(t *testing.T) {
	// market mode forces market even for a known family name.
	p := &kieProvider{key: "k", mode: "market"}
	if !p.useMarket("veo3") {
		t.Fatal("market mode should use market for veo3")
	}
	// model mode forces family endpoints.
	p.mode = "model"
	if p.useMarket("bytedance/seedream") {
		t.Fatal("model mode should not use market for seedream")
	}
	// auto mode: families by name, market otherwise.
	p.mode = "auto"
	if p.useMarket("veo3") {
		t.Fatal("auto should use family for veo3")
	}
	if !p.useMarket("bytedance/seedream") {
		t.Fatal("auto should use market for seedream")
	}
}

func TestKieCheckCode(t *testing.T) {
	if err := checkCode(map[string]any{}); err != nil {
		t.Fatalf("missing code should pass: %v", err)
	}
	if err := checkCode(map[string]any{"code": json.Number("200")}); err != nil {
		t.Fatalf("code 200 should pass: %v", err)
	}
	err := checkCode(map[string]any{"code": json.Number("401"), "msg": "no access"})
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "no access") {
		t.Fatalf("err = %v", err)
	}
}

func TestKieStatusStates(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		want    Phase
		wantSub string
	}{
		{"market success", `{"code":200,"data":{"state":"success","taskId":"t"}}`, PhaseCompleted, "success"},
		{"market fail with msg", `{"code":200,"data":{"state":"fail","failMsg":"out of credits","taskId":"t"}}`, PhaseFailed, "out of credits"},
		{"market fail with code", `{"code":200,"data":{"state":"fail","failCode":"E42","taskId":"t"}}`, PhaseFailed, "E42"},
		{"market pending", `{"code":200,"data":{"state":"generating","taskId":"t"}}`, PhasePending, "generating"},
		{"family successFlag", `{"code":200,"data":{"successFlag":1,"taskId":"t"}}`, PhaseCompleted, "success"},
		{"family failed", `{"code":200,"data":{"successFlag":2,"errorMessage":"nope","taskId":"t"}}`, PhaseFailed, "nope"},
		{"family generating", `{"code":200,"data":{"successFlag":0,"taskId":"t"}}`, PhasePending, "generating"},
		{"resultJson means completed", `{"code":200,"data":{"resultJson":"{\"a\":1}","taskId":"t"}}`, PhaseCompleted, "completed"},
		{"unknown shape pending", `{"code":200,"data":{"foo":"bar","taskId":"t"}}`, PhasePending, "pending"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			p := NewKieList().(*kieProvider)
			p.key = "k"
			p.base = srv.URL
			st, err := p.Status(context.Background(), &Job{ID: "t", Model: "bytedance/seedream"})
			if err != nil {
				t.Fatal(err)
			}
			if st.Phase != tc.want {
				t.Fatalf("phase = %s, want %s (detail %q)", st.Phase, tc.want, st.Detail)
			}
			if tc.wantSub != "" && !strings.Contains(st.Detail, tc.wantSub) {
				t.Fatalf("detail = %q, want substring %q", st.Detail, tc.wantSub)
			}
		})
	}
}

func TestKieStatusRejectsNon200Code(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":401,"msg":"unauthorized"}`))
	}))
	defer srv.Close()
	p := NewKieList().(*kieProvider)
	p.key = "k"
	p.base = srv.URL
	if _, err := p.Status(context.Background(), &Job{ID: "t", Model: "bytedance/seedream"}); err == nil {
		t.Fatal("expected error for code 401")
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Fatalf("short = %q", got)
	}
	long := strings.Repeat("a", 600)
	got := truncate(long, 500)
	if len(got) != 503 || !strings.HasSuffix(got, "...") {
		t.Fatalf("long = %q", got)
	}
}

func TestListAuth(t *testing.T) {
	t.Setenv("FAL_KEY", "")
	p := NewFalList().(*falProvider)
	if p.listAuth() != "" {
		t.Fatal("empty key -> empty auth")
	}
	p.key = "k"
	if p.listAuth() != "Key k" {
		t.Fatal("key -> Key auth")
	}
}

func TestUploadImagePutFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"upload_url": "http://" + r.Host + "/presigned/x",
				"file_url":   "https://v3b.fal.media/files/x",
			})
		case http.MethodPut:
			http.Error(w, `{"error":"signature expired"}`, http.StatusForbidden)
		}
	}))
	defer srv.Close()
	t.Setenv("FAL_UPLOAD_URL", srv.URL)

	p, _ := NewFal("test-key")
	_, err := p.(Uploader).UploadImage(context.Background(), writeRefImage(t))
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v", err)
	}
}

func TestUploadImageContentTypeExtras(t *testing.T) {
	cases := map[string]string{
		"a.jpg":  "image/jpeg",
		"a.gif":  "image/gif",
		"a.bmp":  "image/bmp",
		"a.mp3":  "audio/mpeg",
		"a.wav":  "audio/wav",
		"a.mp4":  "video/mp4",
		"a.webm": "video/webm",
		"a.xyz":  "application/octet-stream",
	}
	for f, want := range cases {
		if got := contentTypeOf(f); got != want {
			t.Errorf("contentTypeOf(%q) = %q, want %q", f, got, want)
		}
	}
}
