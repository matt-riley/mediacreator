// Package media finds media URLs inside arbitrary provider result payloads
// and downloads them to disk.
package media

import (
	"net/url"
	"sort"
	"strings"
)

// urlKeys are object keys whose string values are media URLs.
var urlKeys = map[string]bool{
	"url": true, "image_url": true, "imageurl": true, "video_url": true,
	"videourl": true, "audio_url": true, "audiourl": true, "result_url": true,
	"cover_url": true, "thumbnail_url": true, "content_url": true,
	"file_url": true, "fileurl": true, "mask_url": true,
	"urls": true, // some APIs return {urls: ["..."]}
}

// containerKeys are object keys whose descendants may hold media URLs.
var containerKeys = map[string]bool{
	"images": true, "image": true, "video": true, "audio": true,
	"videos": true, "audios": true, "resulturls": true, "result_urls": true,
	"files": true, "media": true, "items": true, "output": true,
	"result": true, "response": true, "data": true, "masks": true,
	"mask_urls": true, "covers": true, "frames": true, "keyframes": true,
	"resultobject": true, "resulturl": true,
}

// ExtractURLs walks an arbitrary JSON-decoded payload and returns every media
// URL it can find (images, videos, audio, etc.) in a deterministic order:
// object keys are visited alphabetically, while array order is preserved.
func ExtractURLs(v any) []string {
	var out []string
	walk(v, false, &out, 0)
	return dedupe(out)
}

func walk(v any, inContainer bool, out *[]string, depth int) {
	if depth > 12 {
		return
	}
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			val := t[k]
			lk := strings.ToLower(k)
			if s, ok := val.(string); ok {
				if isHTTP(s) && (inContainer || urlKeys[lk]) {
					*out = append(*out, s)
				}
				continue
			}
			walk(val, inContainer || containerKeys[lk], out, depth+1)
		}
	case []any:
		for _, val := range t {
			if s, ok := val.(string); ok {
				if isHTTP(s) && inContainer {
					*out = append(*out, s)
				}
				continue
			}
			walk(val, inContainer, out, depth+1)
		}
	}
}

func isHTTP(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// Basename returns the file name portion of a URL (query/fragment stripped).
func Basename(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	segments := strings.Split(p.Path, "/")
	name := segments[len(segments)-1]
	if i := strings.Index(name, "?"); i >= 0 {
		name = name[:i]
	}
	return name
}

// Ext returns the file extension (including the dot) of a URL path.
func Ext(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	segments := strings.Split(p.Path, "/")
	name := segments[len(segments)-1]
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i:]
	}
	return ""
}
