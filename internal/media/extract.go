// Package media finds media URLs inside arbitrary provider result payloads
// and downloads them to disk.
package media

import (
	"net/url"
	"path"
	"sort"
	"strings"
)

// Kind classifies a generated media file.
type Kind string

const (
	KindImage Kind = "image"
	KindVideo Kind = "video"
	KindAudio Kind = "audio"
	KindOther Kind = "other"
)

// Media is a single generated file URL and its classification.
type Media struct {
	URL  string
	Kind Kind
}

// kindByKey maps lowercased object keys to a media kind. Ambiguous keys
// (e.g. "resultUrls", used for both images and videos) are left unclassified
// and resolved from the URL extension instead.
var kindByKey = map[string]Kind{
	"image": KindImage, "images": KindImage,
	"imageurl": KindImage, "imageurls": KindImage,
	"image_url": KindImage, "image_urls": KindImage,
	"cover": KindImage, "covers": KindImage,
	"coverurl": KindImage, "cover_url": KindImage,
	"thumbnail": KindImage, "thumbnails": KindImage,
	"thumbnailurl": KindImage, "thumbnail_url": KindImage,
	"mask": KindImage, "masks": KindImage,
	"maskurl": KindImage, "maskurls": KindImage,
	"mask_url": KindImage, "mask_urls": KindImage,
	"originimageurl": KindImage, "origin_image_url": KindImage,
	"resultimageurl": KindImage, "result_image_url": KindImage,
	"firstframeurl": KindImage, "first_frame_url": KindImage,
	"lastframeurl": KindImage, "last_frame_url": KindImage,
	"frames": KindImage, "keyframes": KindImage,

	"video": KindVideo, "videos": KindVideo,
	"videourl": KindVideo, "videourls": KindVideo,
	"video_url": KindVideo, "video_urls": KindVideo,
	"resultvideourl": KindVideo, "result_video_url": KindVideo,
	"originurls": KindVideo, "origin_urls": KindVideo,
	"fullresulturls": KindVideo, "full_result_urls": KindVideo,

	"audio": KindAudio, "audios": KindAudio,
	"audiourl": KindAudio, "audiourls": KindAudio,
	"audio_url": KindAudio, "audio_urls": KindAudio,
	"audiowavurl": KindAudio, "audio_wav_url": KindAudio,
	"streamaudiourl": KindAudio, "stream_audio_url": KindAudio,
	"originurl":  KindAudio, // vocal separation "original audio" URL
	"origindata": KindAudio, "sunodata": KindAudio,
}

// containerKeys mark subtrees that may hold media: nested objects and array
// items are scanned for URLs even without a media key of their own.
var containerKeys = map[string]bool{
	"images": true, "image": true, "video": true, "audio": true,
	"videos": true, "audios": true, "resulturls": true, "result_urls": true,
	"resulturl": true, "urls": true, "files": true, "media": true,
	"items": true, "output": true, "result": true, "response": true,
	"data": true, "masks": true, "mask_urls": true, "covers": true,
	"frames": true, "keyframes": true, "resultobject": true,
	"imageurls": true, "image_urls": true, "videourls": true,
	"audio_urls": true, "audiourls": true, "firstframeurl": true,
	"lastframeurl": true, "originurls": true, "fullresulturls": true,
	"origindata": true, "sunodata": true, "maskurls": true,
	"thumbnailurl": true,
}

// mediaURLKeyExcluded lists lowercased keys that contain "url" but are not
// generated media (callbacks, queue metadata, model links).
var mediaURLKeyExcluded = map[string]bool{
	"callbackurl": true, "call_back_url": true, "callback": true,
	"webhookurl": true, "webhook_url": true, "webhook": true,
	"statusurl": true, "status_url": true,
	"responseurl": true, "response_url": true,
	"cancelurl": true, "cancel_url": true,
	"modelurl": true, "model_url": true,
	"sourceurl": true, "source_url": true,
	"homepage": true, "referrerurl": true, "referrer_url": true,
	"fal_webhook": true,
}

// ExtractMedia walks an arbitrary JSON-decoded payload and returns every media
// URL it can find (images, videos, audio, ...) with a best-effort kind, in a
// deterministic order: object keys are visited alphabetically while array
// order is preserved. Duplicate URLs are dropped.
func ExtractMedia(v any) []Media {
	var out []Media
	walk(v, false, KindOther, &out, 0)
	return dedupe(out)
}

// ExtractURLs returns just the URLs from ExtractMedia.
func ExtractURLs(v any) []string {
	ms := ExtractMedia(v)
	urls := make([]string, len(ms))
	for i, m := range ms {
		urls[i] = m.URL
	}
	return urls
}

func walk(v any, inContainer bool, containerKind Kind, out *[]Media, depth int) {
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
				if isHTTP(s) && (inContainer || isMediaURLKey(lk)) {
					*out = append(*out, Media{URL: s, Kind: classify(lk, containerKind, s)})
				}
				continue
			}
			childKind := containerKind
			if kb, ok := kindByKey[lk]; ok {
				childKind = kb
			}
			walk(val, inContainer || containerKeys[lk], childKind, out, depth+1)
		}
	case []any:
		for _, val := range t {
			if s, ok := val.(string); ok {
				if isHTTP(s) && inContainer {
					*out = append(*out, Media{URL: s, Kind: classify("", containerKind, s)})
				}
				continue
			}
			walk(val, inContainer, containerKind, out, depth+1)
		}
	}
}

// classify picks a kind from the key, the enclosing container, then the URL
// extension, in that order.
func classify(key string, containerKind Kind, u string) Kind {
	if k, ok := kindByKey[key]; ok {
		return k
	}
	if containerKind != KindOther {
		return containerKind
	}
	return kindByExt(u)
}

func isMediaURLKey(lk string) bool {
	if mediaURLKeyExcluded[lk] {
		return false
	}
	return strings.Contains(lk, "url")
}

func isHTTP(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

func dedupe(in []Media) []Media {
	seen := map[string]bool{}
	out := make([]Media, 0, len(in))
	for _, m := range in {
		if !seen[m.URL] {
			seen[m.URL] = true
			out = append(out, m)
		}
	}
	return out
}

// kindByExt classifies a URL from its file extension.
func kindByExt(u string) Kind {
	p, err := url.Parse(u)
	if err != nil {
		return KindOther
	}
	return KindByExt(p.Path)
}

// KindByExt classifies a file path or URL path by its extension.
func KindByExt(p string) Kind {
	switch strings.ToLower(path.Ext(p)) {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif", ".bmp", ".svg", ".avif", ".jfif", ".tiff":
		return KindImage
	case ".mp4", ".webm", ".mov", ".mkv", ".avi", ".m4v", ".ts":
		return KindVideo
	case ".mp3", ".wav", ".flac", ".ogg", ".aac", ".m4a", ".opus", ".mid", ".midi", ".wma":
		return KindAudio
	}
	return KindOther
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
