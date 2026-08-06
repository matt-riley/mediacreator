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

```sh
# Image via fal.ai (saved to a single file)
./mediacreator generate --provider fal --model fal-ai/flux/dev \
    --prompt "a red fox in the snow" --output ./fox.png

# Video via fal.ai (saved into a directory)
./mediacreator generate --provider fal --model fal-ai/kling-video/v1/standard/text-to-video \
    --input '{"prompt":"waves crashing on rocks","duration":"5"}' --output ./clips/

# Image via kie.ai market (any model listed on https://kie.ai/market)
./mediacreator generate --provider kie --model bytedance/seedream \
    --prompt "flat vector poster of a mountain campsite" --output ./poster.png

# Video via kie.ai veo (model-family endpoint used automatically)
./mediacreator generate --provider kie --model veo3 \
    --prompt "a dog playing in a park" --output ./clip.mp4

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
| `--input`        | Input parameters as a JSON object                               | –       |
| `--prompt`       | Shorthand for `--input '{"prompt":"..."}'`                      | –       |
| `--output`       | Destination file or directory                                   | `.`     |
| `--webhook`      | Optional completion webhook URL                                 | –       |
| `--request-id`   | Job id from `submit` (`request_id` on fal, `taskId` on kie)     | –       |
| `--timeout`      | Max time to wait for completion                                 | `10m`   |
| `--interval`     | Poll interval                                                   | `5s`    |
| `--kie-mode`     | `market`, `model`, or `auto` (kie only)                         | `auto`  |
| `--verbose`      | Print progress to stderr                                        | `false` |

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

- **stdout** carries JSON only. `generate` emits two lines: the submit
  confirmation, then the completion object:

  ```json
  {"provider":"fal","model":"fal-ai/flux/dev","request_id":"req-abc","status":"submitted","status_url":"...","result_url":"..."}
  {"provider":"fal","model":"fal-ai/flux/dev","request_id":"req-abc","status":"completed","media":[{"url":"https://...","path":"/abs/fox.png","size":112358}],"result":{...}}
  ```

- **stderr** carries diagnostics (only with `--verbose`).
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

- `HOMEBREW_TAP_GITHUB_TOKEN` — classic PAT with `repo` scope to push the
  formula to the tap (fine-grained tokens cannot push to other repos).
- `RELEASE_PLEASE_TOKEN` (optional) — PAT used to create the release tag and
  trigger the publish; falls back to the default `GITHUB_TOKEN`.

Local validation before pushing a release:

```sh
goreleaser check                       # validate .goreleaser.yml
HOMEBREW_TAP_GITHUB_TOKEN=test goreleaser release --snapshot --clean
./dist/mediacreator_*_darwin_arm64/mediacreator version
```
