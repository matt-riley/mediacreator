package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

const kieBase = "https://api.kie.ai"

// NewKie builds a kie.ai provider. The API key is read from KIE_API_KEY
// (falling back to KIE_KEY). mode is "auto", "market", or "model":
//   - market: unified jobs API (/api/v1/jobs/createTask + recordInfo), works
//     with any model listed on the kie.ai market.
//   - model: model-family endpoints (/api/v1/<family>/generate + record-*),
//     e.g. veo, runway, aleph, suno music.
//   - auto: pick model-family endpoints when the model name matches a known
//     family, otherwise use the market jobs API.
func NewKie(key, fallbackKey, mode string) (Provider, error) {
	if key == "" {
		key = fallbackKey
	}
	if key == "" {
		return nil, fmt.Errorf("provider kie requires an API key; set the KIE_API_KEY environment variable")
	}
	switch mode {
	case "", "auto", "market", "model":
	default:
		return nil, fmt.Errorf("invalid --kie-mode %q (want auto, market, or model)", mode)
	}
	return &kieProvider{key: key, mode: mode, base: envOr("KIE_BASE_URL", kieBase)}, nil
}

// NewKieList returns a kie provider for catalog listing. The market model
// list is public, so no API key is required.
func NewKieList() Provider {
	return &kieProvider{key: "", mode: "auto", base: envOr("KIE_BASE_URL", kieBase)}
}

type kieProvider struct {
	key  string
	mode string
	base string
}

func (p *kieProvider) Name() string { return "kie" }

func (p *kieProvider) auth() string { return "Bearer " + p.key }

// NormalizeInput maps the provider-agnostic StandardInput onto kie's native
// request schema, per the model-family docs:
//
//   - Market models (createTask input): image_urls (an array — seedream-edit
//     and kling image-to-video both document arrays, even for one image),
//     image_size (seedream uses fal's preset enums), duration, seed.
//   - Family endpoints use their own conventions: veo/runway take imageUrls +
//     aspect_ratio, flux-kontext takes inputImage + aspectRatio, and 4o image
//     takes size (a ratio string) + nVariants.
func (p *kieProvider) NormalizeInput(model string, std StandardInput) map[string]any {
	in := map[string]any{}
	if std.Prompt != "" {
		in["prompt"] = std.Prompt
	}
	if p.useMarket(model) {
		if len(std.ImageURLs) > 0 {
			in["image_urls"] = std.ImageURLs
		}
		if std.AspectRatio != "" {
			in["aspect_ratio"] = std.AspectRatio
		}
		if std.ImageSize != "" {
			in["image_size"] = std.ImageSize
		}
		if std.Duration != "" {
			in["duration"] = std.Duration
		}
		if std.HasSeed {
			in["seed"] = std.Seed
		}
		return in
	}

	switch familyName(model) {
	case "flux":
		// flux-kontext family: single reference image + camelCase aspectRatio.
		if len(std.ImageURLs) == 1 {
			in["inputImage"] = std.ImageURLs[0]
		} else if len(std.ImageURLs) > 0 {
			in["imageUrls"] = std.ImageURLs
		}
		if std.AspectRatio != "" {
			in["aspectRatio"] = std.AspectRatio
		}
	case "gpt4o":
		// 4o image family: size is a ratio string ("1:1"), nVariants an int.
		if len(std.ImageURLs) > 0 {
			in["imageUrls"] = std.ImageURLs
		}
		if std.AspectRatio != "" {
			in["size"] = std.AspectRatio
		}
		if std.NumImages > 0 {
			in["nVariants"] = std.NumImages
		}
	default:
		// veo, runway, aleph, suno, ...: imageUrls + snake_case keys.
		if len(std.ImageURLs) > 0 {
			in["imageUrls"] = std.ImageURLs
		}
		if std.AspectRatio != "" {
			in["aspect_ratio"] = std.AspectRatio
		}
	}
	if std.Duration != "" {
		in["duration"] = std.Duration
	}
	if std.HasSeed {
		in["seed"] = std.Seed
	}
	return in
}

// endpointPair describes the generate/poll endpoints of a model family.
type endpointPair struct {
	gen string // path to create a job
	rec string // path to poll a job (taskId passed as ?taskId=)
}

// kieFamilies maps model-name prefixes to their dedicated endpoint pair.
var kieFamilies = map[string]endpointPair{
	"veo":    {"/api/v1/veo/generate", "/api/v1/veo/record-info"},
	"runway": {"/api/v1/runway/generate", "/api/v1/runway/record-detail"},
	"aleph":  {"/api/v1/aleph/generate", "/api/v1/aleph/record-info"},
	"suno":   {"/api/v1/generate", "/api/v1/generate/record-info"},
	"gpt4o":  {"/api/v1/gpt4o-image/generate", "/api/v1/gpt4o-image/record-info"},
	"flux":   {"/api/v1/flux/kontext/generate", "/api/v1/flux/kontext/record-info"},
	"mp4":    {"/api/v1/mp4/generate", "/api/v1/mp4/record-info"},
	"wav":    {"/api/v1/wav/generate", "/api/v1/wav/record-info"},
	"vocal":  {"/api/v1/vocal-removal/generate", "/api/v1/vocal-removal/record-info"},
	"midi":   {"/api/v1/midi/generate", "/api/v1/midi/record-info"},
	"voice":  {"/api/v1/voice/generate", "/api/v1/voice/record-info"},
	"lyrics": {"/api/v1/lyrics", "/api/v1/lyrics/record-info"},
}

// familyName returns the matched family prefix for a model name, or "".
func familyName(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	for prefix := range kieFamilies {
		if strings.HasPrefix(m, prefix) {
			return prefix
		}
	}
	return ""
}

// familyFor returns the endpoint pair for a model when its name matches a
// known family prefix.
func familyFor(model string) (endpointPair, bool) {
	pair, ok := kieFamilies[familyName(model)]
	return pair, ok
}

// useMarket reports whether the unified market jobs API should be used.
func (p *kieProvider) useMarket(model string) bool {
	switch p.mode {
	case "market":
		return true
	case "model":
		return false
	default: // auto
		_, known := familyFor(model)
		return !known
	}
}

// num extracts a numeric value from a decoded JSON map (json.Number).
func num(m map[string]any, key string) (float64, bool) {
	n, ok := m[key].(json.Number)
	if !ok {
		return 0, false
	}
	v, err := n.Float64()
	if err != nil {
		return 0, false
	}
	return v, true
}

// checkCode fails when the response carries a non-200 code.
func checkCode(resp map[string]any) error {
	if c, ok := resp["code"].(json.Number); ok {
		if v, err := c.Float64(); err == nil && v != 200 {
			msg, _ := resp["msg"].(string)
			return fmt.Errorf("kie returned code %s: %s", c.String(), msg)
		}
	}
	return nil
}

func (p *kieProvider) Submit(ctx context.Context, model string, input map[string]any, webhook string) (*Job, error) {
	if model == "" {
		return nil, fmt.Errorf("provider kie requires --model (e.g. bytedance/seedream or veo3)")
	}
	var (
		path string
		body map[string]any
	)
	if p.useMarket(model) {
		path = "/api/v1/jobs/createTask"
		body = map[string]any{"model": model, "input": input}
		if webhook != "" {
			body["callBackUrl"] = webhook
		}
	} else {
		pair, _ := familyFor(model)
		path = pair.gen
		body = map[string]any{}
		for k, v := range input {
			body[k] = v
		}
		if webhook != "" {
			body["callBackUrl"] = webhook
		}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encoding input: %w", err)
	}
	resp, err := doJSON(ctx, "POST", p.base+path, p.auth(), payload)
	if err != nil {
		return nil, err
	}
	if err := checkCode(resp); err != nil {
		return nil, err
	}
	data, _ := resp["data"].(map[string]any)
	id, _ := data["taskId"].(string)
	if id == "" {
		return nil, fmt.Errorf("kie did not return a taskId; response: %v", resp)
	}
	return &Job{ID: id, Model: model}, nil
}

func (p *kieProvider) recordPath(model string) string {
	if p.useMarket(model) {
		return "/api/v1/jobs/recordInfo"
	}
	pair, _ := familyFor(model)
	return pair.rec
}

func (p *kieProvider) Status(ctx context.Context, job *Job) (*Status, error) {
	u := p.base + p.recordPath(job.Model) + "?taskId=" + url.QueryEscape(job.ID)
	resp, err := doJSON(ctx, "GET", u, p.auth(), nil)
	if err != nil {
		return nil, err
	}
	if err := checkCode(resp); err != nil {
		return nil, err
	}
	data, _ := resp["data"].(map[string]any)
	if data == nil {
		data = resp
	}

	st := &Status{Raw: data}
	// Family endpoints use successFlag (0 generating, 1 success, 2/3 failed).
	if v, ok := num(data, "successFlag"); ok {
		switch int(v) {
		case 1:
			st.Phase = PhaseCompleted
			st.Detail = "success"
		case 2, 3:
			st.Phase = PhaseFailed
			st.Detail = fmt.Sprintf("failed (successFlag=%d)", int(v))
			if m, _ := data["errorMessage"].(string); m != "" {
				st.Detail += ": " + m
			}
		default:
			st.Phase = PhasePending
			st.Detail = "generating"
		}
		return st, nil
	}
	// Market API uses state (waiting|queuing|generating|success|fail).
	if state, _ := data["state"].(string); state != "" {
		switch state {
		case "success":
			st.Phase = PhaseCompleted
			st.Detail = "success"
		case "fail":
			st.Phase = PhaseFailed
			st.Detail = "failed"
			if m, _ := data["failMsg"].(string); m != "" {
				st.Detail += ": " + m
			} else if c, _ := data["failCode"].(string); c != "" {
				st.Detail += ": " + c
			}
		default:
			st.Phase = PhasePending
			st.Detail = state
		}
		return st, nil
	}
	// Unknown shape: if we have a result payload, assume completed.
	if _, ok := data["resultJson"]; ok {
		st.Phase = PhaseCompleted
		st.Detail = "completed"
		return st, nil
	}
	st.Phase = PhasePending
	st.Detail = "pending"
	return st, nil
}

func (p *kieProvider) Result(ctx context.Context, job *Job) (map[string]any, error) {
	u := p.base + p.recordPath(job.Model) + "?taskId=" + url.QueryEscape(job.ID)
	resp, err := doJSON(ctx, "GET", u, p.auth(), nil)
	if err != nil {
		return nil, err
	}
	if err := checkCode(resp); err != nil {
		return nil, err
	}
	data, _ := resp["data"].(map[string]any)
	if data == nil {
		return resp, nil
	}
	// Market API wraps the result in a JSON string.
	if s, ok := data["resultJson"].(string); ok && s != "" {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(s), &parsed); err == nil {
			return parsed, nil
		}
		return map[string]any{"resultJson": s}, nil
	}
	return data, nil
}

// List returns the kie.ai market model catalog. kie.ai documents no model
// listing endpoint, so the primary source is the market web app's endpoint
// (GET /api/v1/playground/model-paths). If that is unreachable, a snapshot of
// the market catalog embedded in the binary is used instead.
func (p *kieProvider) List(ctx context.Context, opts ListOptions) (*ModelList, error) {
	resp, err := doJSON(ctx, "GET", p.base+"/api/v1/playground/model-paths", "", nil)
	if err == nil {
		if err := checkCode(resp); err == nil {
			if models, ok := modelsFromPaths(resp["data"]); ok && len(models) > 0 {
				return &ModelList{Models: models, Total: len(models), Source: "live"}, nil
			}
		}
	}
	models := make([]Model, 0, len(kieFallbackCatalog))
	for _, id := range kieFallbackCatalog {
		models = append(models, Model{ID: id, Title: id, Vendor: vendorOf(id)})
	}
	return &ModelList{Models: models, Total: len(models), Source: "fallback"}, nil
}

// modelsFromPaths converts the market "data" payload (an array of model id
// strings) into catalog models.
func modelsFromPaths(raw any) ([]Model, bool) {
	items, ok := raw.([]any)
	if !ok {
		return nil, false
	}
	models := make([]Model, 0, len(items))
	for _, r := range items {
		if id, ok := r.(string); ok && id != "" {
			models = append(models, Model{ID: id, Title: id, Vendor: vendorOf(id)})
		}
	}
	return models, len(models) > 0
}
