package media

import (
	"encoding/json"
	"strings"
	"testing"
)

// FuzzExtractMedia feeds arbitrary JSON into the media extraction walker.
// It must never panic, must not return duplicate URLs, and every kind must be
// a known value. Seeds include the documented fal/kie response shapes.
func FuzzExtractMedia(f *testing.F) {
	seeds := []string{
		`{"images":[{"url":"https://v3.fal.media/files/a.png"}]}`,
		`{"video":{"url":"https://v3.fal.media/files/v.mp4","content_type":"video/mp4"}}`,
		`{"data":{"resultJson":"{\"resultUrls\":[\"https://cdn.kie.ai/x/a.jpg\"]}"}}`,
		`{"sunoData":[{"audioUrl":"https://x/audio.mp3"}]}`,
		`{"status_url":"https://x/status","response_url":"https://x/response","images":[{"url":"https://x/i.png"}]}`,
		`{"nested":{"deeply":{"urls":["https://x/1.png","https://x/2.png"]}}}`,
		`[]`,
		`{"url":"not-http","prompt":"hello","seed":1}`,
		`{"arr":[1,2,3,{"url":"https://x/a.png"}]}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			return // invalid JSON is parseJSON's business, not ours
		}
		media := ExtractMedia(v)
		seen := map[string]bool{}
		for _, m := range media {
			if seen[m.URL] {
				t.Fatalf("duplicate media url %q", m.URL)
			}
			seen[m.URL] = true
			switch m.Kind {
			case KindImage, KindVideo, KindAudio, KindOther:
			default:
				t.Fatalf("unknown kind %q for %q", m.Kind, m.URL)
			}
			if m.URL == "" {
				t.Fatal("empty media url")
			}
		}
	})
}

// FuzzBasename ensures URL basename extraction never panics and always
// returns something safe to use in a file path.
func FuzzBasename(f *testing.F) {
	for _, s := range []string{
		"https://v3.fal.media/files/b/abc/out.png",
		"https://x/a%20b.png",
		"",
		"http://x/",
		"relative/path.mp4?q=1",
		"https://x/a.png#frag",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, u string) {
		name := Basename(u)
		// A basename must never contain a path separator; anything else is
		// neutralized downstream by sanitizeName (covered by its own fuzz,
		// which also enforces the 200-char cap).
		if strings.ContainsRune(name, '/') {
			t.Fatalf("unsafe basename %q", name)
		}
	})
}
