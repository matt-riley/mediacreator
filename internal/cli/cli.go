// Package cli implements the mediacreator command line interface.
//
// Commands are designed to be agent friendly: results are printed as JSON on
// stdout (one object per line), and all diagnostics go to stderr.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"mediacreator/internal/media"
	"mediacreator/internal/provider"
)

const usageText = `mediacreator - generate media via fal.ai or kie.ai and save it to disk

Usage:
  mediacreator generate  --provider fal|kie --model <model> [--input <json> | --prompt <text>] --output <dir-or-file> [flags]
  mediacreator submit    --provider fal|kie --model <model> [--input <json> | --prompt <text>] [flags]
  mediacreator status    --provider fal|kie --request-id <id> [--model <model>] [flags]
  mediacreator download  --provider fal|kie --request-id <id> --output <dir-or-file> [--model <model>] [flags]
  mediacreator list      --provider fal|kie [--all|--page N] [--search <text>] [--category <cat>] [--status active|deprecated|all] [--endpoint-id <id>] [--vendor <v>] [--plain]
  mediacreator help

Commands:
  generate   Create a job, wait for it to finish, download the media, print the result as JSON.
  submit     Create a job and print its id as JSON. Use status/download later.
  status     Poll a job once and print its current state as JSON.
  download   Wait for an existing job to finish and download its media.
  list       List the provider's available models (no API key required).
  version    Print the build version (injected at release time).

Common flags:
  --provider string   fal | kie                       (default "fal")
  --model string      model / endpoint id, e.g. fal-ai/flux/dev, bytedance/seedream, veo3
  --input string      native input parameters as JSON (merged over standard flags)
  --prompt string     generation prompt — the same flag works on every provider
  --image-url string  input image URL or local file path (repeatable; local files are uploaded first)
  --aspect-ratio str  aspect ratio, e.g. 16:9
  --duration string   duration in seconds, e.g. 5
  --seed int          random seed
  --output string     destination file or directory   (default ".")
  --webhook string    optional completion webhook URL
  --request-id string id returned by submit (fal: request_id, kie: taskId)
  --timeout duration  max time to wait for completion (default 10m)
  --interval duration poll interval                  (default 5s)
  --kie-mode string   market | model | auto           (default "auto", kie only)
  --verbose           log progress and raw payloads to stderr

Environment:
  FAL_KEY      fal.ai API key (https://fal.ai/dashboard/keys)
  KIE_API_KEY  kie.ai API key (https://kie.ai/api-key); KIE_KEY also works
  FAL_CATALOG_URL   override fal.ai catalog base (default https://fal.ai)

Output:
  JSON on stdout, one object per line. Exit code 0 on success, 1 on error.

Examples:
  mediacreator list --provider fal --search flux --category text-to-image
  mediacreator list --provider fal --endpoint-id fal-ai/flux/dev
  mediacreator list --provider fal --all --plain | wc -l
  mediacreator list --provider kie --vendor bytedance
  mediacreator generate --provider fal --model fal-ai/flux/dev \
      --prompt "a red fox in snow" --output ./fox.png
  mediacreator generate --provider fal --model fal-ai/flux/dev \
      --prompt "redraw this fox on a skateboard" --image-url ./ref.png --output ./out/
  mediacreator generate --provider kie --model bytedance/seedream \
      --prompt "flat vector poster of a campsite" --output ./out/
  mediacreator generate --provider kie --model veo3 \
      --prompt "a dog playing in a park" --output ./clip.mp4
  mediacreator generate --provider fal --model fal-ai/kling-video/v1/standard/text-to-video \
      --input '{"prompt":"waves crashing","duration":"5"}' --output ./videos
`

type options struct {
	provider  string
	model     string
	input     string
	prompt    string
	imageURLs []string
	aspect    string
	duration  string
	seed      int64
	hasSeed   bool
	output    string
	webhook   string
	requestID string
	timeout   time.Duration
	interval  time.Duration
	kieMode   string
	verbose   bool
}

// Build-time version info, injected via -ldflags (see .goreleaser.yml).
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// Run dispatches a command and returns an error (exit code 1) on failure.
func Run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usageText)
		return nil
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "generate":
		return runGenerate(rest)
	case "submit":
		return runSubmit(rest)
	case "status":
		return runStatus(rest)
	case "download":
		return runDownload(rest)
	case "list":
		return runList(rest)
	case "version", "-v", "--version":
		fmt.Printf("mediacreator version %s (%s, %s)\n", version, commit, date)
		return nil
	case "help", "-h", "--help":
		fmt.Print(usageText)
		return nil
	default:
		return fmt.Errorf("unknown command %q (try \"mediacreator help\")", cmd)
	}
}

func newFlagSet(name string) (*flag.FlagSet, *options) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	o := &options{}
	fs.StringVar(&o.provider, "provider", os.Getenv("MC_PROVIDER"), "fal | kie")
	fs.StringVar(&o.model, "model", "", "model / endpoint id")
	fs.StringVar(&o.input, "input", "", "native input parameters as JSON (merged over the standard flags)")
	fs.StringVar(&o.prompt, "prompt", "", "generation prompt (standard input, any provider)")
	fs.Var((*stringSlice)(&o.imageURLs), "image-url", "input image URL or local file path (repeatable; local files are uploaded first)")
	fs.StringVar(&o.aspect, "aspect-ratio", "", "aspect ratio, e.g. 16:9 (standard input)")
	fs.StringVar(&o.duration, "duration", "", "duration in seconds, e.g. 5 (standard input)")
	fs.Int64Var(&o.seed, "seed", 0, "random seed (standard input)")
	fs.StringVar(&o.output, "output", ".", "destination file or directory")
	fs.StringVar(&o.webhook, "webhook", "", "completion webhook URL")
	fs.StringVar(&o.requestID, "request-id", "", "job id returned by submit")
	fs.DurationVar(&o.timeout, "timeout", 10*time.Minute, "max wait for completion")
	fs.DurationVar(&o.interval, "interval", 5*time.Second, "poll interval")
	fs.StringVar(&o.kieMode, "kie-mode", "auto", "market | model | auto")
	fs.BoolVar(&o.verbose, "verbose", false, "log progress to stderr")
	return fs, o
}

// buildProvider constructs the configured provider, reading keys from env.
func buildProvider(o *options) (provider.Provider, error) {
	switch strings.ToLower(o.provider) {
	case "", "fal":
		return provider.NewFal(os.Getenv("FAL_KEY"))
	case "kie":
		return provider.NewKie(os.Getenv("KIE_API_KEY"), os.Getenv("KIE_KEY"), o.kieMode)
	default:
		return nil, fmt.Errorf("unknown provider %q (want fal or kie)", o.provider)
	}
}

// stringSlice is a repeatable string flag.
type stringSlice []string

func (s *stringSlice) String() string { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// resolveImageURLs turns --image-url values into URLs. Values that are not
// http(s) URLs are treated as local files and uploaded to the provider's
// storage first, so reference images can come straight from disk.
func resolveImageURLs(ctx context.Context, p provider.Provider, urls []string) ([]string, error) {
	out := make([]string, len(urls))
	for i, v := range urls {
		if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
			out[i] = v
			continue
		}
		if _, err := os.Stat(v); err != nil {
			return nil, fmt.Errorf("--image-url %q is not a URL and no such file exists (pass an http(s) URL or a local image path)", v)
		}
		up, ok := p.(provider.Uploader)
		if !ok {
			return nil, fmt.Errorf("provider %s does not support uploading local reference images; pass an http(s) URL instead", p.Name())
		}
		u, err := up.UploadImage(ctx, v)
		if err != nil {
			return nil, fmt.Errorf("uploading reference image %q: %w", v, err)
		}
		logf(nil, "uploaded reference image %s -> %s", v, u)
		out[i] = u
	}
	return out, nil
}

// buildNativeInput translates the standard flags into the provider's native
// request body, then merges the raw --input JSON on top (it wins on conflict).
func buildNativeInput(p provider.Provider, o *options) (map[string]any, error) {
	std := provider.StandardInput{
		Prompt:      o.prompt,
		ImageURLs:   o.imageURLs,
		AspectRatio: o.aspect,
		Duration:    o.duration,
		Seed:        o.seed,
		HasSeed:     o.hasSeed,
	}
	native := map[string]any{}
	if n, ok := p.(provider.InputNormalizer); ok {
		native = n.NormalizeInput(o.model, std)
	}
	if o.input != "" {
		var extra map[string]any
		dec := json.NewDecoder(strings.NewReader(o.input))
		dec.UseNumber()
		if err := dec.Decode(&extra); err != nil {
			return nil, fmt.Errorf("invalid --input JSON: %w", err)
		}
		for k, v := range extra {
			native[k] = v
		}
	}
	return native, nil
}

// printJSON writes v as a single JSON object line to stdout.
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func logf(o *options, format string, a ...any) {
	if o == nil || o.verbose {
		fmt.Fprintf(os.Stderr, format+"\n", a...)
	}
}

// runGenerate submits a job, waits, and downloads the media.
func runGenerate(args []string) error {
	fs, o := newFlagSet("generate")
	if err := fs.Parse(args); err != nil {
		return err
	}
	o.hasSeed = flagWasSet(fs, "seed")
	if o.model == "" {
		return fmt.Errorf("--model is required")
	}
	p, err := buildProvider(o)
	if err != nil {
		return err
	}
	ctx := context.Background()
	o.imageURLs, err = resolveImageURLs(ctx, p, o.imageURLs)
	if err != nil {
		return err
	}
	input, err := buildNativeInput(p, o)
	if err != nil {
		return err
	}

	job, err := p.Submit(ctx, o.model, input, o.webhook)
	if err != nil {
		return err
	}
	emitSubmit(p, o, job)

	result, err := waitForCompleted(ctx, p, o, job)
	if err != nil {
		return err
	}
	return emitDone(p, o, job, result)
}

// runSubmit creates a job and returns immediately.
func runSubmit(args []string) error {
	fs, o := newFlagSet("submit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	o.hasSeed = flagWasSet(fs, "seed")
	if o.model == "" {
		return fmt.Errorf("--model is required")
	}
	p, err := buildProvider(o)
	if err != nil {
		return err
	}
	ctx := context.Background()
	o.imageURLs, err = resolveImageURLs(ctx, p, o.imageURLs)
	if err != nil {
		return err
	}
	input, err := buildNativeInput(p, o)
	if err != nil {
		return err
	}
	job, err := p.Submit(ctx, o.model, input, o.webhook)
	if err != nil {
		return err
	}
	emitSubmit(p, o, job)
	return nil
}

// flagWasSet reports whether the named flag was explicitly provided.
func flagWasSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

// runList prints the model catalog of a provider.
func runList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		providerName string
		page, limit  int
		all          bool
		search       string
		category     string
		status       string
		endpointID   string
		vendor       string
		plain        bool
	)
	fs.StringVar(&providerName, "provider", os.Getenv("MC_PROVIDER"), "fal | kie")
	fs.IntVar(&page, "page", 1, "page number (fal only)")
	fs.IntVar(&limit, "limit", 50, "items per page (fal only)")
	fs.BoolVar(&all, "all", false, "fetch the full catalog (fal only)")
	fs.StringVar(&search, "search", "", "filter by free-text query (model name, description, category)")
	fs.StringVar(&category, "category", "", "filter by category, e.g. text-to-image (fal only)")
	fs.StringVar(&status, "status", "active", "active | deprecated | all (fal only)")
	fs.StringVar(&endpointID, "endpoint-id", "", "fetch a specific endpoint id (find mode, fal only)")
	fs.StringVar(&vendor, "vendor", "", "filter by vendor prefix of the model id")
	fs.BoolVar(&plain, "plain", false, "print one model id per line instead of JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var p provider.Provider
	switch strings.ToLower(providerName) {
	case "", "fal":
		p = provider.NewFalList()
	case "kie":
		p = provider.NewKieList()
	default:
		return fmt.Errorf("unknown provider %q (want fal or kie)", providerName)
	}

	list, err := p.List(context.Background(), provider.ListOptions{
		Page:       page,
		Limit:      limit,
		All:        all,
		Search:     search,
		Category:   category,
		Status:     status,
		EndpointID: endpointID,
	})
	if err != nil {
		return err
	}
	models := filterModels(list.Models, search, category, vendor, endpointID)

	if plain {
		for _, m := range models {
			fmt.Println(m.ID)
		}
		return nil
	}

	out := map[string]any{
		"provider": p.Name(),
		"returned": len(models),
		"models":   models,
	}
	if list.Total >= 0 {
		out["total"] = list.Total
	}
	if list.HasMore {
		out["has_more"] = true
		out["next_cursor"] = list.NextCursor
	}
	if list.Source != "" {
		out["source"] = list.Source
	}
	return printJSON(out)
}

// filterModels applies client-side search/category/vendor/endpoint filters.
// Providers apply some of these server-side already; the local pass is a
// safety net (and the only filter for kie).
func filterModels(models []provider.Model, search, category, vendor, endpointID string) []provider.Model {
	search = strings.ToLower(strings.TrimSpace(search))
	category = strings.ToLower(strings.TrimSpace(category))
	vendor = strings.ToLower(strings.TrimSpace(vendor))
	endpointID = strings.ToLower(strings.TrimSpace(endpointID))
	if search == "" && category == "" && vendor == "" && endpointID == "" {
		return models
	}
	out := make([]provider.Model, 0, len(models))
	for _, m := range models {
		if search != "" &&
			!strings.Contains(strings.ToLower(m.ID), search) &&
			!strings.Contains(strings.ToLower(m.Title), search) {
			continue
		}
		if category != "" && strings.ToLower(m.Category) != category {
			continue
		}
		if vendor != "" && strings.ToLower(m.Vendor) != vendor {
			continue
		}
		if endpointID != "" && strings.ToLower(m.ID) != endpointID {
			continue
		}
		out = append(out, m)
	}
	return out
}

// runStatus polls a previously submitted job once.
func runStatus(args []string) error {
	fs, o := newFlagSet("status")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if o.requestID == "" {
		return fmt.Errorf("--request-id is required")
	}
	p, err := buildProvider(o)
	if err != nil {
		return err
	}
	st, err := p.Status(context.Background(), &provider.Job{ID: o.requestID, Model: o.model})
	if err != nil {
		return err
	}
	if o.verbose {
		raw, _ := json.Marshal(st.Raw)
		fmt.Fprintf(os.Stderr, "raw status: %s\n", raw)
	}
	return printJSON(map[string]any{
		"provider":   p.Name(),
		"model":      o.model,
		"request_id": o.requestID,
		"status":     st.Phase,
		"detail":     st.Detail,
	})
}

// runDownload waits for an existing job and downloads its media.
func runDownload(args []string) error {
	fs, o := newFlagSet("download")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if o.requestID == "" {
		return fmt.Errorf("--request-id is required")
	}
	p, err := buildProvider(o)
	if err != nil {
		return err
	}
	ctx := context.Background()
	job := &provider.Job{ID: o.requestID, Model: o.model}
	if st, err := p.Status(ctx, job); err == nil && st.Phase == provider.PhasePending {
		logf(o, "job %s is %s", o.requestID, st.Detail)
	}
	result, err := waitForCompleted(ctx, p, o, job)
	if err != nil {
		return err
	}
	return emitDone(p, o, job, result)
}

// waitForCompleted polls a job until it completes, fails, or times out.
func waitForCompleted(ctx context.Context, p provider.Provider, o *options, job *provider.Job) (map[string]any, error) {
	deadline := time.Now().Add(o.timeout)
	ticker := time.NewTicker(o.interval)
	defer ticker.Stop()

	last := ""
	for {
		st, err := p.Status(ctx, job)
		if err != nil {
			return nil, err
		}
		if st.Detail != last {
			logf(o, "job %s: %s", job.ID, st.Detail)
			last = st.Detail
		}
		switch st.Phase {
		case provider.PhaseCompleted:
			logf(o, "job %s completed", job.ID)
			return p.Result(ctx, job)
		case provider.PhaseFailed:
			return nil, fmt.Errorf("job %s failed: %s", job.ID, st.Detail)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out after %s waiting for job %s (last status: %s)", o.timeout, job.ID, st.Detail)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// emitSubmit prints the job id line after a successful submit.
func emitSubmit(p provider.Provider, o *options, job *provider.Job) {
	info := map[string]any{
		"provider":   p.Name(),
		"model":      o.model,
		"request_id": job.ID,
		"status":     "submitted",
		"status_url": job.StatusURL,
		"result_url": job.ResultURL,
	}
	if o.output != "." {
		info["output"] = o.output
	}
	_ = printJSON(info)
}

// emitDone downloads the media and prints the completion line. The output
// shape is identical regardless of provider: media is normalized to a list of
// {url, path, size, type} plus per-kind URL arrays.
func emitDone(p provider.Provider, o *options, job *provider.Job, result map[string]any) error {
	found := media.ExtractMedia(result)
	if len(found) == 0 {
		raw, _ := json.Marshal(result)
		return fmt.Errorf("no media URLs found in the result; raw result:\n%s", raw)
	}
	logf(o, "found %d media file(s)", len(found))

	urls := make([]string, len(found))
	for i, m := range found {
		urls[i] = m.URL
	}
	paths, err := planOutputs(o.output, urls)
	if err != nil {
		return err
	}
	type fileInfo struct {
		URL  string `json:"url"`
		Path string `json:"path"`
		Size int64  `json:"size"`
		Type string `json:"type"`
	}
	files := make([]fileInfo, 0, len(found))
	var images, videos, audios []string
	for i, m := range found {
		logf(o, "downloading %s -> %s", m.URL, paths[i])
		size, err := media.Download(context.Background(), m.URL, paths[i])
		if err != nil {
			return fmt.Errorf("downloading %s: %w", m.URL, err)
		}
		kind := m.Kind
		if kind == media.KindOther {
			kind = media.KindByExt(paths[i])
		}
		files = append(files, fileInfo{URL: m.URL, Path: paths[i], Size: size, Type: string(kind)})
		switch kind {
		case media.KindImage:
			images = append(images, m.URL)
		case media.KindVideo:
			videos = append(videos, m.URL)
		case media.KindAudio:
			audios = append(audios, m.URL)
		}
	}
	if o.verbose {
		raw, _ := json.Marshal(result)
		fmt.Fprintf(os.Stderr, "raw result: %s\n", raw)
	}

	out := map[string]any{
		"provider":   p.Name(),
		"model":      o.model,
		"request_id": job.ID,
		"status":     "completed",
		"media":      files,
	}
	if len(images) > 0 {
		out["images"] = images
	}
	if len(videos) > 0 {
		out["videos"] = videos
	}
	if len(audios) > 0 {
		out["audios"] = audios
	}
	return printJSON(out)
}

// planOutputs maps each media URL to a destination path.
//
//   - If output is a directory (existing, or ending in "/"), or there is more
//     than one file, media is saved inside it using URL-derived names.
//   - Otherwise output is treated as a single file path; a missing extension
//     is filled in from the first URL.
func planOutputs(output string, urls []string) ([]string, error) {
	if output == "" {
		output = "."
	}
	dirMode := strings.HasSuffix(output, "/") || isDir(output)
	if !dirMode && len(urls) > 1 {
		dirMode = true
	}

	if dirMode {
		if err := os.MkdirAll(output, 0o755); err != nil {
			return nil, err
		}
		used := map[string]bool{}
		paths := make([]string, len(urls))
		for i, u := range urls {
			name := sanitizeName(media.Basename(u))
			if name == "" {
				name = fmt.Sprintf("output_%d%s", i+1, media.Ext(u))
			}
			name = uniqueName(used, name)
			paths[i] = filepath.Join(output, name)
		}
		return paths, nil
	}

	if len(urls) == 1 {
		if filepath.Ext(output) == "" {
			output += media.Ext(urls[0])
		}
		if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
			return nil, err
		}
		return []string{output}, nil
	}
	return nil, fmt.Errorf("internal: unexpected planOutputs state")
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func sanitizeName(name string) string {
	name = unsafeChars.ReplaceAllString(name, "_")
	if len(name) > 200 {
		name = name[len(name)-200:]
	}
	return strings.Trim(name, "._")
}

// uniqueName returns name, deduplicating against used by inserting -2, -3, ...
// before the extension.
func uniqueName(used map[string]bool, name string) string {
	if !used[name] {
		used[name] = true
		return name
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d%s", base, i, ext)
		if !used[candidate] {
			used[candidate] = true
			return candidate
		}
	}
}
