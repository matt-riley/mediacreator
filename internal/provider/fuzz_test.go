package provider

import (
	"encoding/json"
	"testing"
)

// FuzzParseJSON feeds arbitrary bytes into the JSON decoder used for every
// provider response. It must never panic.
func FuzzParseJSON(f *testing.F) {
	for _, s := range []string{
		`{"code":200,"data":{"taskId":"t"}}`,
		`{"images":[{"url":"https://x/a.png"}]}`,
		`{"status":"IN_QUEUE","queue_position":3}`,
		`not json at all`,
		`[]`,
		`"a string"`,
		`12345`,
		`{"nested":{"a":[1,2,{"b":null}]}}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := parseJSON(data)
		if err != nil {
			return
		}
		// Chain the response consumers that run on every request path.
		_ = errorText(m)
		_ = checkCode(m)
		_ = firstURL(m)
		// Round-trip the map through JSON to catch non-serializable values.
		if _, err := json.Marshal(m); err != nil {
			t.Fatalf("map from %q does not round-trip: %v", data, err)
		}
	})
}
