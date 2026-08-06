package media

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func mustDecode(t *testing.T, s string) any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	return v
}

func TestExtractURLsFalImage(t *testing.T) {
	// Typical fal-ai/flux/dev result.
	payload := mustDecode(t, `{
		"images": [{"url": "https://v3.fal.media/files/abc/red-fox.png", "content_type": "image/png", "width": 1024, "height": 1024}],
		"timings": {"inference": 1.2}
	}`)
	got := ExtractURLs(payload)
	want := []string{"https://v3.fal.media/files/abc/red-fox.png"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestExtractURLsFalVideo(t *testing.T) {
	// Typical fal-ai video result.
	payload := mustDecode(t, `{
		"video": {"url": "https://v3.fal.media/files/abc/clip.mp4", "content_type": "video/mp4", "file_size": 1234},
		"audio": {"url": "https://v3.fal.media/files/abc/track.mp3"}
	}`)
	got := ExtractURLs(payload)
	want := []string{
		"https://v3.fal.media/files/abc/track.mp3",
		"https://v3.fal.media/files/abc/clip.mp4",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestExtractURLsKieMarket(t *testing.T) {
	// kie.ai market: resultJson string containing resultUrls.
	payload := mustDecode(t, `{
		"resultUrls": ["https://cdn.kie.ai/x/generated-content.jpg"],
		"meta": {"seed": 123}
	}`)
	got := ExtractURLs(payload)
	want := []string{"https://cdn.kie.ai/x/generated-content.jpg"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestExtractURLsKieVeo(t *testing.T) {
	// kie.ai veo family: data.response.resultUrls + fullResultUrls.
	payload := mustDecode(t, `{
		"taskId": "veo_task_x",
		"successFlag": 1,
		"response": {
			"taskId": "veo_task_x",
			"resultUrls": ["https://cdn.kie.ai/video1.mp4"],
			"fullResultUrls": ["https://cdn.kie.ai/full.mp4"],
			"resolution": "1080p"
		}
	}`)
	got := ExtractURLs(payload)
	want := []string{"https://cdn.kie.ai/full.mp4", "https://cdn.kie.ai/video1.mp4"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestExtractURLsSkipsNonMedia(t *testing.T) {
	// Callback / webhook URLs must not be collected.
	payload := mustDecode(t, `{
		"callBackUrl": "https://my-app.example/callback",
		"prompt": "a cat",
		"model": "fal-ai/flux/dev",
		"images": [{"url": "https://v3.fal.media/cat.png"}]
	}`)
	got := ExtractURLs(payload)
	want := []string{"https://v3.fal.media/cat.png"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestExtractURLsDedupe(t *testing.T) {
	payload := mustDecode(t, `{
		"images": [{"url": "https://x/y.png"}, {"url": "https://x/y.png"}]
	}`)
	got := ExtractURLs(payload)
	if len(got) != 1 {
		t.Errorf("expected dedupe, got %v", got)
	}
}

func TestBasenameExt(t *testing.T) {
	cases := []struct{ u, base, ext string }{
		{"https://v3.fal.media/files/abc/clip.mp4?token=1", "clip.mp4", ".mp4"},
		{"https://cdn.kie.ai/video1.mp4", "video1.mp4", ".mp4"},
		{"https://x/y", "y", ""},
	}
	for _, c := range cases {
		if got := Basename(c.u); got != c.base {
			t.Errorf("Basename(%s) = %q want %q", c.u, got, c.base)
		}
		if got := Ext(c.u); got != c.ext {
			t.Errorf("Ext(%s) = %q want %q", c.u, got, c.ext)
		}
	}
}

func kindOf(t *testing.T, v any, url string) Kind {
	t.Helper()
	for _, m := range ExtractMedia(v) {
		if m.URL == url {
			return m.Kind
		}
	}
	t.Fatalf("url %q not extracted from %v", url, v)
	return ""
}

func TestExtractMediaKinds(t *testing.T) {
	// fal image / video / audio shapes.
	payload := mustDecode(t, `{
		"images": [{"url": "https://x/a.png"}],
		"video": {"url": "https://x/clip.mp4"},
		"audio": {"url": "https://x/track.mp3"}
	}`)
	if got := kindOf(t, payload, "https://x/a.png"); got != KindImage {
		t.Errorf("image kind = %q", got)
	}
	if got := kindOf(t, payload, "https://x/clip.mp4"); got != KindVideo {
		t.Errorf("video kind = %q", got)
	}
	if got := kindOf(t, payload, "https://x/track.mp3"); got != KindAudio {
		t.Errorf("audio kind = %q", got)
	}
}

func TestExtractMediaKieFamilyShapes(t *testing.T) {
	// Key shapes observed across the kie.ai family endpoints.
	payload := mustDecode(t, `{
		"response": {
			"resultUrls": ["https://cdn.kie.ai/img.jpg"],
			"resultVideoUrl": "https://cdn.kie.ai/clip.mp4",
			"resultImageUrl": "https://cdn.kie.ai/frame.png",
			"originImageUrl": "https://cdn.kie.ai/orig.jpg",
			"videoUrl": "https://cdn.kie.ai/mp4.mp4",
			"audioWavUrl": "https://cdn.kie.ai/out.wav",
			"audioUrl": "https://cdn.kie.ai/music.mp3",
			"sunoData": [{"audioUrl": "https://cdn.kie.ai/s1.mp3", "imageUrl": "https://cdn.kie.ai/cov.jpg"}],
			"originUrl": "https://cdn.kie.ai/vocal.wav"
		}
	}`)
	cases := []struct {
		url  string
		kind Kind
	}{
		{"https://cdn.kie.ai/img.jpg", KindImage},   // resultUrls -> by extension
		{"https://cdn.kie.ai/clip.mp4", KindVideo},  // resultVideoUrl
		{"https://cdn.kie.ai/frame.png", KindImage}, // resultImageUrl
		{"https://cdn.kie.ai/orig.jpg", KindImage},  // originImageUrl
		{"https://cdn.kie.ai/mp4.mp4", KindVideo},   // videoUrl
		{"https://cdn.kie.ai/out.wav", KindAudio},   // audioWavUrl
		{"https://cdn.kie.ai/music.mp3", KindAudio}, // audioUrl
		{"https://cdn.kie.ai/s1.mp3", KindAudio},    // sunoData[].audioUrl
		{"https://cdn.kie.ai/cov.jpg", KindImage},   // sunoData[].imageUrl
		{"https://cdn.kie.ai/vocal.wav", KindAudio}, // originUrl
	}
	for _, c := range cases {
		if got := kindOf(t, payload, c.url); got != c.kind {
			t.Errorf("%s kind = %q want %q", c.url, got, c.kind)
		}
	}
}

func TestExtractMediaSkipsQueueMetadata(t *testing.T) {
	// fal queue responses and callback URLs must never be treated as media.
	payload := mustDecode(t, `{
		"request_id": "r1",
		"status_url": "https://queue.fal.run/x/requests/r1/status",
		"response_url": "https://queue.fal.run/x/requests/r1",
		"cancel_url": "https://queue.fal.run/x/requests/r1/cancel",
		"callBackUrl": "https://my-app.example/callback",
		"images": [{"url": "https://v3.fal.media/cat.png"}]
	}`)
	got := ExtractURLs(payload)
	want := []string{"https://v3.fal.media/cat.png"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestExtractMediaSeedanceExtraFrames(t *testing.T) {
	// Seedance market results include firstFrameUrl / lastFrameUrl.
	payload := mustDecode(t, `{
		"resultUrls": ["https://cdn.kie.ai/v.mp4"],
		"firstFrameUrl": ["https://cdn.kie.ai/f1.png"],
		"lastFrameUrl": ["https://cdn.kie.ai/l1.png"]
	}`)
	got := ExtractMedia(payload)
	if len(got) != 3 {
		t.Fatalf("got %d media, want 3: %v", len(got), got)
	}
	kinds := map[string]Kind{}
	for _, m := range got {
		kinds[m.URL] = m.Kind
	}
	if kinds["https://cdn.kie.ai/v.mp4"] != KindVideo {
		t.Errorf("v.mp4 kind = %q", kinds["https://cdn.kie.ai/v.mp4"])
	}
	if kinds["https://cdn.kie.ai/f1.png"] != KindImage {
		t.Errorf("f1.png kind = %q", kinds["https://cdn.kie.ai/f1.png"])
	}
	if kinds["https://cdn.kie.ai/l1.png"] != KindImage {
		t.Errorf("l1.png kind = %q", kinds["https://cdn.kie.ai/l1.png"])
	}
}

func TestKindByExt(t *testing.T) {
	cases := []struct {
		p    string
		want Kind
	}{
		{"x.png", KindImage}, {"x.jpg", KindImage}, {"x.webp", KindImage},
		{"x.mp4", KindVideo}, {"x.webm", KindVideo},
		{"x.mp3", KindAudio}, {"x.wav", KindAudio}, {"x.flac", KindAudio},
		{"x.unknown", KindOther},
	}
	for _, c := range cases {
		if got := KindByExt(c.p); got != c.want {
			t.Errorf("KindByExt(%s) = %q want %q", c.p, got, c.want)
		}
	}
}
