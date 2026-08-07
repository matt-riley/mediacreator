package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// FuzzBuildNativeInput feeds arbitrary --input JSON through the standard-flag
// merge path used by generate/submit. It must never panic and must only ever
// produce JSON-serializable values.
func FuzzBuildNativeInput(f *testing.F) {
	seeds := []string{
		`{"prompt":"a fox","num_images":2}`,
		`{"image_url":"https://x/a.png"}`,
		`{"guidance_scale":2.5,"steps":30}`,
		`{"nested":{"a":[1,2,3]}}`,
		`[1,2,3]`,
		`"not an object"`,
		`{"image_url":12345}`,
		`{}`,
		`{"prompt":"p","aspect_ratio":"16:9","seed":7}`,
		`{"duration":"5"}`,
		`null`,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		t.Setenv("FAL_KEY", "test-key")
		o := &options{
			provider:  "fal",
			model:     "fal-ai/flux/dev",
			prompt:    "a fox",
			imageSize: "landscape_16_9",
			numImages: 1,
			input:     raw,
		}
		p, err := buildProvider(o)
		if err != nil {
			t.Fatal(err)
		}
		in, err := buildNativeInput(p, o)
		if err != nil {
			return // invalid JSON is rejected with an error, which is fine
		}
		if _, err := json.Marshal(in); err != nil {
			t.Fatalf("input %q not serializable: %v", raw, err)
		}
	})
}

// FuzzSanitizeName feeds arbitrary strings into the output-name sanitizer.
func FuzzSanitizeName(f *testing.F) {
	for _, s := range []string{
		"out.png",
		"a b/c:d?e.png",
		"....png",
		"",
		strings.Repeat("x", 300) + ".png",
		"https://x/a.png",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		name := sanitizeName(s)
		if len(name) > 200 {
			t.Fatalf("sanitized name too long: %d", len(name))
		}
		// Sanitized names must be safe path components.
		if strings.ContainsAny(name, "/\\:?*\"<>|") {
			t.Fatalf("unsafe sanitized name %q", name)
		}
		// uniqueName must not panic and must produce distinct names.
		used := map[string]bool{}
		_ = uniqueName(used, name)
		if name != "" {
			second := uniqueName(used, name)
			if second == name {
				t.Fatalf("uniqueName did not dedupe %q", name)
			}
		}
	})
}
