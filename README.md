# mediacreator

A small Go CLI for generating media with [fal.ai](https://fal.ai) or
[kie.ai](https://kie.ai) and saving the finished files to disk. It is designed
to be agent-friendly: every command prints JSON to stdout, one object per line,
so the output can be parsed directly by a shell, an agent, or a script.

## Install

Via Homebrew (published from GitHub releases):

```sh
brew install matt-riley/tools/mediacreator
```

Or build from source:

```sh
go build -o mediacreator .
```

The installed binary reports its build version (injected at release time):

```sh
mediacreator version
```

## Setup

Export an API key for the provider(s) you want to use:

```sh
export FAL_KEY=...          # https://fal.ai/dashboard/keys
export KIE_API_KEY=...      # https://kie.ai/api-key  (KIE_KEY also works)
```

Optionally, `FAL_BASE_URL` / `KIE_BASE_URL` override the API base URL (useful
for proxies). `MC_PROVIDER` sets a default `--provider`.

## Quickstart

Input is standardized: the **same flags and prompt work on every provider**.
The two examples below are identical apart from `--provider`/`--model`:

```sh
# Same prompt, same standard flags — image via fal.ai or kie.ai
./mediacreator generate --provider fal --model fal-ai/flux/dev \
    --prompt "a red fox in the snow" --output ./fox.png
./mediacreator generate --provider kie --model bytedance/seedream \
    --prompt "a red fox in the snow" --output ./fox.png

# Same prompt — video via fal.ai or kie.ai (model-family endpoint auto-selected)
./mediacreator generate --provider fal --model fal-ai/kling-video/v1/standard/text-to-video \
    --prompt "waves crashing on rocks" --output ./clips/
./mediacreator generate --provider kie --model veo3 \
    --prompt "waves crashing on rocks" --output ./clips/

# Reference image from a local file — uploaded to the provider's storage first
./mediacreator generate --provider fal --model fal-ai/flux/dev \
    --prompt "the same fox, now on a skateboard" --image-url ./ref.png --output ./out/
./mediacreator generate --provider kie --model bytedance/seedream \
    --prompt "the same fox, now on a skateboard" --image-url ./ref.png --output ./out/

# Explicit output size — FLUX-style models take image_size presets on both providers
./mediacreator generate --provider fal --model fal-ai/flux/dev \
    --prompt "a poster for a jazz festival" --image-size landscape_16_9 --output ./poster.png
./mediacreator generate --provider kie --model bytedance/seedream \
    --prompt "a poster for a jazz festival" --image-size landscape_16_9 --output ./poster.png

# Discover available models (no API key required)
./mediacreator list --provider fal --search flux --category text-to-image
./mediacreator list --provider fal --endpoint-id fal-ai/flux/dev   # find mode: details for one model
./mediacreator list --provider kie --vendor bytedance
./mediacreator list --provider fal --all --plain | wc -l   # full catalog, one id per line
```

## Commands

| Command    | What it does                                                        |
| ---------- | ------------------------------------------------------------------- |
| `generate` | Create a job, wait for completion, download the media.              |
| `submit`   | Create a job and print its id. Use `status` / `download` later.     |
| `status`   | Poll a previously created job once and print its state as JSON.     |
| `download` | Wait for an existing job to finish and download its media.          |
| `list`     | List available models for a provider (no API key needed).           |
| `version`  | Print the build version (injected at release time).                 |

```sh
# Split workflow for long jobs:
./mediacreator submit --provider fal --model fal-ai/flux/dev --prompt "a fox" \
    # -> {"provider":"fal","model":"fal-ai/flux/dev","request_id":"<id>","status":"submitted",...}
./mediacreator status --provider fal --request-id <id> --model fal-ai/flux/dev
./mediacreator download --provider fal --request-id <id> --model fal-ai/flux/dev --output ./fox.png
```

## Flags

| Flag             | Meaning                                                         | Default |
| ---------------- | --------------------------------------------------------------- | ------- |
| `--provider`     | `fal` or `kie`                                                  | `fal`   |
| `--model`        | Model/endpoint id (e.g. `fal-ai/flux/dev`, `bytedance/seedream`, `veo3`) | – |
| `--prompt`       | Generation prompt — the same standard flag on every provider    | –       |
| `--image-url`    | Reference image: URL *or local file path* (repeatable; local files are uploaded to the provider's storage first) | – |
| `--aspect-ratio` | Aspect ratio, e.g. `16:9` (fal `aspect_ratio` / kie 4o `size`; **not** FLUX-family fal models — see below) | – |
| `--image-size`   | Output size: preset (`landscape_16_9`) or `1280x720` (fal `image_size` preset/object; kie market `image_size` presets) | – |
| `--duration`     | Duration in seconds, e.g. `5` (video models)                             | – |
| `--num-images`   | Number of images to generate (fal `num_images`; kie 4o `nVariants`)     | – |
| `--seed`         | Random seed (standard flag)                                     | –       |
| `--input`        | Native input params as JSON; merged over the standard flags     | –       |
| `--output`       | Destination file or directory                                   | `.`     |
| `--webhook`      | Optional completion webhook URL                                 | –       |
| `--request-id`   | Job id from `submit` (`request_id` on fal, `taskId` on kie)     | –       |
| `--timeout`      | Max time to wait for completion                                 | `10m`   |
| `--interval`     | Poll interval                                                   | `5s`    |
| `--kie-mode`     | `market`, `model`, or `auto` (kie only)                         | `auto`  |
| `--verbose`      | Print progress and raw provider payloads to stderr              | `false` |

The standard flags (`--prompt`, `--image-url`, `--aspect-ratio`, `--image-size`,
`--duration`, `--num-images`, `--seed`) are translated per provider **using the
documented key conventions of each**:

- **fal** — `prompt`, `image_url`/`image_urls`, `aspect_ratio` (nano-banana-style
  models), `image_size` (FLUX-family models — preset string like
  `landscape_16_9` or `1280x720` which becomes a `{width,height}` object),
  `duration`, `num_images`, `seed` (per
  [Common Model Arguments](https://fal.ai/docs/documentation/model-apis/model-arguments)).
- **kie market** (`createTask` `input`) — `prompt`, `image_urls` (always an
  array, per the seedream-edit and kling docs), `image_size` (seedream uses
  fal's preset enums), `duration`, `seed`.
- **kie families** — veo/runway: `imageUrls` + `aspect_ratio`; flux-kontext:
  `inputImage` + `aspectRatio`; 4o image: `size` (`--aspect-ratio` maps to it)
  + `nVariants` (`--num-images`).

> ⚠️ `image_size` vs `aspect_ratio` is model-dependent: the fal docs warn that
> FLUX-family models use `image_size` while others (e.g. Nano Banana) use
> `aspect_ratio`, and "passing the wrong one will have no effect". Use
> `--image-size` for FLUX-style models and `--aspect-ratio` for the rest.

`--image-url` accepts either a hosted URL or a local file path: paths are uploaded first — fal via the CDN upload
two-step flow (`POST https://rest.fal.ai/storage/upload/initiate` then `PUT`
the presigned URL, returning a `v3b.fal.media` URL) and kie via the
[File Stream Upload API](https://docs.kie.ai/file-upload-api/quickstart.md)
(`POST https://kieai.redpandaai.co/api/file-stream-upload`, returning
`data.downloadUrl`) — so reference images can come straight from disk. Use
`--input` for model-specific parameters; it is merged over the standard flags.

### `list` flags

| Flag           | Meaning                                                     | Default  |
| -------------- | ----------------------------------------------------------- | -------- |
| `--provider`   | `fal` or `kie`                                              | `fal`    |
| `--page`       | Page number (fal only)                                      | `1`      |
| `--limit`      | Items per page (fal only)                                   | `50`     |
| `--all`        | Fetch the full catalog (fal only)                           | `false`  |
| `--search`     | Free-text query: name, description, or category             | –        |
| `--category`   | Filter by category (fal only)                               | –        |
| `--status`     | `active`, `deprecated`, or `all` (fal only)                 | `active` |
| `--endpoint-id`| Find mode: details for one endpoint id (fal only)           | –        |
| `--vendor`     | Filter by vendor prefix of the model id                     | –        |
| `--plain`      | Print one model id per line instead of JSON                 | `false`  |

## Output conventions

- **stdout** carries JSON only, and the shape is identical regardless of
  provider. `generate` emits two lines: the submit confirmation, then the
  completion object:

  ```json
  {"provider":"fal","model":"fal-ai/flux/dev","request_id":"req-abc","status":"submitted","status_url":"...","result_url":"..."}
  {"provider":"kie","model":"bytedance/seedream","request_id":"task-s","status":"completed","media":[{"url":"https://...","path":"/abs/fox.png","size":112358,"type":"image"}],"images":["https://..."]}
  ```

  The completion object is normalized: `media[]` lists every generated file
  with `url`, `path`, `size`, and a `type` of `image`, `video`, or `audio`,
  plus per-kind arrays (`images`, `videos`, `audios`). The raw provider
  payload is provider-specific, so it is only shown on stderr with
  `--verbose`.

- **stderr** carries diagnostics and raw payloads (only with `--verbose`).
- Exit code `0` on success, `1` on error (including failed or timed-out jobs).
- `--output` rules:
  - ends in `/` or is an existing directory → media saved inside it, named
    from the download URLs;
  - more than one file → treated as a directory;
  - otherwise → a single file path (extension filled in from the URL if
    missing).

## Providers

**fal.ai** — uses the queue API (`POST /queue.fal.run/<model>`, poll
`.../requests/<id>/status`, fetch `.../requests/<id>`). Any public fal endpoint
id works, including `fal-ai/...` and sub-accounts. Model discovery uses the
official catalog API `GET https://api.fal.ai/v1/models` (override with
`FAL_CATALOG_URL`); it works without a key, but setting `FAL_KEY` raises its
rate limits — handy when walking the full catalog with `--all`.

**kie.ai** — two API styles, chosen automatically (`--kie-mode auto`):

- *market* (default for most models): `POST /api/v1/jobs/createTask` with
  `{"model": ..., "input": {...}}`, poll `GET /api/v1/jobs/recordInfo?taskId=`.
  Works with any model listed on the [kie.ai market](https://kie.ai/market).
- *model families*: when the model name starts with a known family (`veo`,
  `runway`, `aleph`, `suno`, `mp4`, `wav`, `vocal`, `midi`, `voice`, `lyrics`,
  `gpt4o`, `flux`), the dedicated `/api/v1/<family>/generate` +
  `record-*` endpoints are used.

Model discovery for kie uses the public market catalog endpoint
(`GET /api/v1/playground/model-paths`) — the same one the kie.ai market web
app uses; kie.ai does not document a model-listing API. If that endpoint is
unreachable, `list` falls back to a snapshot of the market catalog embedded
in the binary. The JSON output includes a `"source": "live" | "fallback"`
field so callers can tell which was used.

To refresh the embedded snapshot against the live market:

```sh
go generate ./...                 # regenerates internal/provider/kie_catalog.go
# or
KIE_BASE_URL=https://api.kie.ai go run ./cmd/gen-kie-catalog
```

## Result shapes

Providers and models do **not** return the same shaped data — this is by
design; the tool normalizes everything into the canonical output above.
Verified against the fal.ai and kie.ai documentation:

- **fal.ai** (via the catalog's `expand=openapi-3.0` schemas): image models
  return `images: [{url, ...}]`, video models `video: {url, ...}`, audio
  models `audio: {url, ...}`; inputs also differ (`prompt` for image/video,
  `text` for some TTS models, `image_url` vs `image_urls` for reference
  images).
- **kie.ai market** (`jobs/recordInfo`) is unified: `state` + `resultJson`
  (a string) containing `resultUrls: [...]` — plus `firstFrameUrl` /
  `lastFrameUrl` for Seedance and `resultObject` for text/mask outputs.
- **kie.ai model families** each return their own keys: veo
  `response.resultUrls/originUrls/fullResultUrls`, aleph
  `response.resultVideoUrl/resultImageUrl`, runway `response.videoUrl`,
  suno `sunoData[].audioUrl/imageUrl`, wav `audioWavUrl`, flux-kontext
  `originImageUrl/resultImageUrl`, vocal separation `originUrl`; some (midi,
  lyrics, voice) return no media at all.

The extractor recognizes all of these shapes (and any key containing `url`,
minus queue/callback metadata) and classifies each file as `image`, `video`,
or `audio` from the key or the URL extension, so the CLI output is the same
no matter which provider or model produced the media.

Media URLs are found generically in provider results (`images[]`,
`video.url`, `audio.url`, `resultUrls`, `resultJson`, and similar shapes), so
new models generally work without code changes.

## Development

```sh
go test ./...
go vet ./...
golangci-lint run --timeout=5m   # or: mise run lint
```

## Releases

Releases are driven by [Release Please](https://github.com/googleapis/release-please)
and [GoReleaser](https://goreleaser.com): conventional commits on `main` open a
release PR; merging it creates a `vX.Y.Z` tag, release, and binaries. GoReleaser
also publishes the Homebrew formula to
[`matt-riley/homebrew-tools`](https://github.com/matt-riley/homebrew-tools).

Repository secrets required:

- `PRIVATE_KEY` (secret) + `APP_ID` (variable) — the GitHub App private key
  and app id used to mint a short-lived token for the Homebrew tap push. This
  is the same GitHub App managed by the [infra repo](https://github.com/matt-riley/infra)
  (bootstrap sets `APP_ID`; the private key lives in repo secrets). The app
  must be installed on `matt-riley/homebrew-tools` with `contents: write`.
- `RELEASE_PLEASE_TOKEN` (optional) — PAT used to create the release tag and
  trigger the publish; falls back to the default `GITHUB_TOKEN`.
- `HOMEBREW_TAP_GITHUB_TOKEN` (optional, legacy) — classic PAT with `repo`
  scope; only used as a fallback if the GitHub App token is unavailable. If
  neither is available the Homebrew publish is skipped (set
  `tap-fail-if-missing-token: true` on the workflow call to fail instead).

Local validation before pushing a release:

```sh
goreleaser check                       # validate .goreleaser.yml
HOMEBREW_TAP_GITHUB_TOKEN=test goreleaser release --snapshot --clean
./dist/mediacreator_*_darwin_arm64/mediacreator version
```
