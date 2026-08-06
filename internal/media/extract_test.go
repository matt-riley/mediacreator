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
