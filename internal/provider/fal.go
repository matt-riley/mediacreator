package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const falBase = "https://queue.fal.run"
const falCatalog = "https://api.fal.ai"

// NewFal builds a fal.ai queue provider. The API key is read from FAL_KEY.
// FAL_BASE_URL may override the API base (e.g. for proxies or tests).
func NewFal(key string) (Provider, error) {
	if key == "" {
		return nil, fmt.Errorf("provider fal requires an API key; set the FAL_KEY environment variable")
	}
	return &falProvider{
		key:     key,
		base:    envOr("FAL_BASE_URL", falBase),
		catalog: envOr("FAL_CATALOG_URL", falCatalog),
	}, nil
}

// NewFalList returns a fal provider for catalog listing. The catalog API
// (GET https://api.fal.ai/v1/models) is public; FAL_KEY is sent when present
// for higher rate limits.
func NewFalList() Provider {
	return &falProvider{
		key:     os.Getenv("FAL_KEY"),
		base:    envOr("FAL_BASE_URL", falBase),
		catalog: envOr("FAL_CATALOG_URL", falCatalog),
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

type falProvider struct {
	key     string
	base    string
	catalog string

	mu      sync.Mutex
	imgKeys map[string]string // model -> "image_url", "image_urls", or "" (unknown)
}

func (p *falProvider) Name() string { return "fal" }

// NormalizeInput maps the provider-agnostic StandardInput onto fal's native
// request schema. The single-image key defaults to "image_url"; Submit then
// refines it against the model's published schema, because some models
// (e.g. openai/gpt-image-2/edit, fal-ai/nano-banana/edit) require the
// "image_urls" array even for one image. Image models use "prompt" while some
// audio models expect "text" — pass those via --input.
func (p *falProvider) NormalizeInput(model string, std StandardInput) map[string]any {
	in := map[string]any{}
	if std.Prompt != "" {
		in["prompt"] = std.Prompt
	}
	switch len(std.ImageURLs) {
	case 1:
		in["image_url"] = std.ImageURLs[0]
	case 2, 3, 4, 5, 6, 7, 8, 9, 10:
		in["image_urls"] = std.ImageURLs
	}
	if std.AspectRatio != "" {
		in["aspect_ratio"] = std.AspectRatio
	}
	if std.ImageSize != "" {
		in["image_size"] = imageSizeValue(std.ImageSize)
	}
	if std.Duration != "" {
		in["duration"] = std.Duration
	}
	if std.NumImages > 0 {
		in["num_images"] = std.NumImages
	}
	if std.HasSeed {
		in["seed"] = std.Seed
	}
	return in
}

// imageSizeValue normalizes the --image-size flag into fal's image_size
// schema: a preset string ("landscape_16_9") or a {"width","height"} object
// when given as "1280x720" (both forms are documented; see
// fal.ai/docs/documentation/model-apis/model-arguments).
func imageSizeValue(v string) any {
	if w, h, ok := parseDims(v); ok {
		return map[string]any{"width": w, "height": h}
	}
	return v
}

// parseDims splits a "WxH" string (case-insensitive separator) into integers.
func parseDims(v string) (int, int, bool) {
	lower := strings.ToLower(v)
	i := strings.IndexByte(lower, 'x')
	if i <= 0 || i == len(lower)-1 {
		return 0, 0, false
	}
	w, err1 := strconv.Atoi(lower[:i])
	h, err2 := strconv.Atoi(lower[i+1:])
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

func (p *falProvider) Submit(ctx context.Context, model string, input map[string]any, webhook string) (*Job, error) {
	if model == "" {
		return nil, fmt.Errorf("provider fal requires --model (e.g. fal-ai/flux/dev)")
	}
	p.useSchemaImageKey(ctx, model, input)
	u := p.base + "/" + strings.Trim(model, "/")
	if webhook != "" {
		u += "?fal_webhook=" + url.QueryEscape(webhook)
	}
	body, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("encoding input: %w", err)
	}
	resp, err := doJSON(ctx, "POST", u, "Key "+p.key, body)
	if err != nil {
		return nil, err
	}

	job := &Job{Model: model}
	if s, ok := resp["request_id"].(string); ok {
		job.ID = s
	}
	if s, ok := resp["response_url"].(string); ok {
		job.ResultURL = s
	}
	if s, ok := resp["status_url"].(string); ok {
		job.StatusURL = s
	}
	if job.ID == "" {
		return nil, fmt.Errorf("fal did not return a request_id; response: %v", resp)
	}
	return job, nil
}

// useSchemaImageKey rewrites a single string image_url into an image_urls
// array when the model's schema declares the array. fal accepts the wrong key
// at submit time but fails the result fetch with 422 "body.image_urls
// required", so the key is corrected before the request is queued.
func (p *falProvider) useSchemaImageKey(ctx context.Context, model string, input map[string]any) {
	v, ok := input["image_url"].(string)
	if !ok {
		return
	}
	if _, ok := input["image_urls"]; ok {
		return // --input already supplied the array form; it wins
	}
	if p.imageKey(ctx, model) != "image_urls" {
		return
	}
	input["image_urls"] = []string{v}
	delete(input, "image_url")
}

// imageKey reports which reference-image key the model's published schema
// expects ("image_url" or "image_urls"), or "" when the catalog cannot say.
// Results are cached per model; an unreachable catalog falls back to the
// normalized key rather than failing the submission.
func (p *falProvider) imageKey(ctx context.Context, model string) string {
	p.mu.Lock()
	k, ok := p.imgKeys[model]
	p.mu.Unlock()
	if ok {
		return k
	}

	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	k = p.fetchImageKey(lookupCtx, model)

	p.mu.Lock()
	if p.imgKeys == nil {
		p.imgKeys = map[string]string{}
	}
	p.imgKeys[model] = k
	p.mu.Unlock()
	return k
}

// fetchImageKey reads the endpoint's OpenAPI schema from the catalog and
// returns the reference-image key it declares.
func (p *falProvider) fetchImageKey(ctx context.Context, model string) string {
	u := p.catalog + "/v1/models?expand=openapi-3.0&endpoint_id=" + url.QueryEscape(model)
	resp, err := doJSON(ctx, "GET", u, p.listAuth(), nil)
	if err != nil {
		return ""
	}
	models, _ := resp["models"].([]any)
	if len(models) == 0 {
		return ""
	}
	entry, _ := models[0].(map[string]any)
	openapi, _ := entry["openapi"].(map[string]any)
	props := inputSchemaProperties(openapi)
	if _, ok := props["image_urls"]; ok {
		return "image_urls"
	}
	if _, ok := props["image_url"]; ok {
		return "image_url"
	}
	return ""
}

// inputSchemaProperties returns the properties of the schema bound to the
// endpoint's POST request body.
func inputSchemaProperties(openapi map[string]any) map[string]any {
	components, _ := openapi["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	paths, _ := openapi["paths"].(map[string]any)
	for _, raw := range paths {
		path, _ := raw.(map[string]any)
		post, _ := path["post"].(map[string]any)
		body, _ := post["requestBody"].(map[string]any)
		content, _ := body["content"].(map[string]any)
		media, _ := content["application/json"].(map[string]any)
		schema, _ := media["schema"].(map[string]any)
		ref, _ := schema["$ref"].(string)
		name := strings.TrimPrefix(ref, "#/components/schemas/")
		if name == "" {
			continue
		}
		s, _ := schemas[name].(map[string]any)
		if props, ok := s["properties"].(map[string]any); ok {
			return props
		}
	}
	return nil
}

func (p *falProvider) Status(ctx context.Context, job *Job) (*Status, error) {
	u := job.StatusURL
	if u == "" {
		u = p.base + "/" + strings.Trim(job.Model, "/") + "/requests/" + job.ID + "/status"
	}
	code, resp, err := do(ctx, "GET", u, "Key "+p.key, nil)
	if err != nil {
		return nil, err
	}

	st := &Status{Raw: resp}
	status, _ := resp["status"].(string)
	if code >= 400 {
		// fal reports failed jobs via error responses; surface them as a
		// failed status rather than a generic request error.
		if status == "FAILED" || errorText(resp) != "" {
			st.Phase = PhaseFailed
			st.Detail = errorText(resp)
			if st.Detail == "" {
				st.Detail = "FAILED"
			}
			return st, nil
		}
		return nil, fmt.Errorf("%s returned %d: %s", u, code, errorText(resp))
	}
	switch status {
	case "COMPLETED":
		st.Phase = PhaseCompleted
		st.Detail = "COMPLETED"
	case "FAILED":
		st.Phase = PhaseFailed
		st.Detail = errorText(resp)
		if st.Detail == "" {
			st.Detail = "FAILED"
		}
	case "IN_QUEUE":
		st.Phase = PhasePending
		pos, _ := resp["queue_position"].(json.Number)
		st.Detail = "IN_QUEUE (position " + pos.String() + ")"
	case "IN_PROGRESS":
		st.Phase = PhasePending
		st.Detail = "IN_PROGRESS"
	default:
		st.Phase = PhasePending
		st.Detail = status
		if st.Detail == "" {
			st.Detail = "pending"
		}
	}
	return st, nil
}

func (p *falProvider) Result(ctx context.Context, job *Job) (map[string]any, error) {
	u := job.ResultURL
	if u == "" {
		u = p.base + "/" + strings.Trim(job.Model, "/") + "/requests/" + job.ID
	}
	return doJSON(ctx, "GET", u, "Key "+p.key, nil)
}

// List queries the official fal.ai model catalog API
// (GET {catalog}/v1/models). Search, category, and status filters are applied
// server-side; pagination uses cursors (base64-encoded page numbers).
func (p *falProvider) List(ctx context.Context, opts ListOptions) (*ModelList, error) {
	limit := opts.Limit
	if limit < 1 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	build := func(cursor string) string {
		q := url.Values{}
		q.Add("limit", strconv.Itoa(limit))
		if opts.EndpointID != "" {
			q.Add("endpoint_id", opts.EndpointID)
		}
		if opts.Search != "" {
			q.Add("q", opts.Search)
		}
		if opts.Category != "" {
			q.Add("category", opts.Category)
		}
		if opts.Status != "" && opts.Status != "all" {
			q.Add("status", opts.Status)
		}
		if cursor != "" {
			q.Add("cursor", cursor)
		}
		return p.catalog + "/v1/models?" + q.Encode()
	}

	cursor := opts.Cursor
	if cursor == "" && opts.Page > 1 {
		// The API encodes the next page number in the cursor (base64).
		cursor = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(opts.Page)))
	}

	var models []Model
	for {
		resp, err := doJSON(ctx, "GET", build(cursor), p.listAuth(), nil)
		if err != nil {
			return nil, err
		}
		if items, ok := resp["models"].([]any); ok {
			for _, it := range items {
				m, ok := it.(map[string]any)
				if !ok {
					continue
				}
				id, _ := m["endpoint_id"].(string)
				meta, _ := m["metadata"].(map[string]any)
				title, _ := meta["display_name"].(string)
				cat, _ := meta["category"].(string)
				status, _ := meta["status"].(string)
				models = append(models, Model{
					ID:         id,
					Title:      title,
					Category:   cat,
					Vendor:     vendorOf(id),
					Deprecated: status == "deprecated",
				})
			}
		}
		hasMore, _ := resp["has_more"].(bool)
		next, _ := resp["next_cursor"].(string)
		if !opts.All || !hasMore || next == "" {
			return &ModelList{Models: models, Total: -1, HasMore: hasMore, NextCursor: next}, nil
		}
		cursor = next
		// Be polite to the catalog API when walking many pages.
		if !sleepCtx(ctx, 200*time.Millisecond) {
			return nil, ctx.Err()
		}
	}
}

// listAuth sends the API key when available (optional for the catalog API).
func (p *falProvider) listAuth() string {
	if p.key == "" {
		return ""
	}
	return "Key " + p.key
}
