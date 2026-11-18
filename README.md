# hcloud-mcp

An MCP server for the HCloud API. Its tools are generated at build time from the
OpenAPI specs of the microservices listed in [services.yaml](services.yaml).
Builds are CGO-free for macOS, Linux and Windows on amd64 and arm64, and are
packaged as MCP Bundles (`.mcpb`).

## Usage

```sh
export HCLOUD_API_SERVER=https://api.example.com
export HCLOUD_API_TOKEN=...

hcloud-mcp stdio                           # MCP over stdin/stdout
hcloud-mcp http --addr :8080               # streamable HTTP at /mcp (+ /healthz)
hcloud-mcp tools                           # list compiled-in services and tools
```

### Token modes

| Mode | Flag / env | Token used for API calls | Transports |
|---|---|---|---|
| `env` (default) | `--token-mode env` / `apiTokenMode=env` | `HCLOUD_API_TOKEN` from the environment, shared by all callers | stdio, http |
| `external` | `--token-mode external` / `apiTokenMode=external` | the token each MCP client sends with its HTTP request | http only |

In `external` mode, `HCLOUD_API_TOKEN` is not needed and is ignored. Clients send their
own token, by default as `Authorization: Bearer <token>`. Use `--token-header`
(env `apiTokenHeader`) to read it from a different header. The token is read on
every call, not once per session, so each call runs with the credentials of
whoever made it. A request to `/mcp` without a token gets `401 Unauthorized`.

```sh
HCLOUD_API_SERVER=https://api.example.com hcloud-mcp http --token-mode external
```

Client config example (`.mcp.json` / Claude Code):

```json
{ "mcpServers": { "hcloud": {
    "type": "http",
    "url": "https://mcp.example.com/mcp",
    "headers": { "Authorization": "Bearer ${HCLOUD_TOKEN}" } } } }
```

### Outgoing auth

The token is sent to the API as `Authorization: Bearer <token>`. To use a different
header or scheme, set `--auth-header` / `--auth-scheme` (or the env vars
`apiAuthHeader` / `apiAuthScheme`).

## How tools are generated

```
services.yaml ──cmd/gen──▶ internal/catalog/generated/catalog.json ──go:embed──▶ binary
```

* The catalog is a build artifact and is gitignored. A binary built without one
  starts with an error asking you to run `make generate`.
* Each operation becomes a tool named `<service>_<operationId>`. Names are cut
  to 64 characters.
* Path, query and header parameters become top-level arguments. The request body
  goes in `body`.
* Each call goes to `<HCLOUD_API_SERVER><prefix><path>`.
* `$ref`s are inlined into `$defs`. OpenAPI 3.0 `nullable` is converted to JSON
  Schema. `readOnly` properties are removed from inputs. Deprecated operations
  are skipped unless `includeDeprecated: true`.
* Supports OpenAPI 3.0, OpenAPI 3.1 and Swagger 2.0, from JSON or YAML.

### Adding or updating a microservice

1. Add an entry to `services.yaml`. The spec can be a local file, a full URL, or
   a path such as `/api/cosmo/v1/docs/spec.yaml`. Paths are resolved against
   the spec base URL: `SPEC_BASE_URL`, then `specBaseUrl`, then `HCLOUD_API_SERVER`.
2. Run `HCLOUD_API_SERVER=https://api.example.com make all VERSION=x.y.z`.

Specs are downloaded with `SPEC_TOKEN` as a bearer token, falling back to
`HCLOUD_API_TOKEN` if it's set.

## Build

```sh
make generate   # regenerate the catalog from services.yaml (needs HCLOUD_API_SERVER)
make build      # build/hcloud-mcp_<os>_<arch>[.exe]
make mcpb       # build/*.mcpb
make all        # all three, in order
make lint
make test
make snapshot   # full goreleaser dry run into dist/, nothing is published
```

`make mcpb` produces:

* `hcloud-mcp_<version>.mcpb`: one universal bundle. It contains a macOS
  universal binary (amd64 + arm64), Windows amd64 (which also runs on Windows
  arm64) and Linux amd64.
* `hcloud-mcp_<version>_<os>_<arch>.mcpb`: one smaller bundle per platform.

When a bundle is installed, Claude Desktop asks for the API server and the API
token. The token is stored as a sensitive value.

## Release

`goreleaser release` builds everything from a git tag:

1. regenerates the catalog from the live specs (`scripts/generate-catalog.sh`)
2. builds binaries for darwin, linux and windows on amd64 and arm64, plus a
   macOS universal binary
3. creates archives (`tar.gz`, `zip` for Windows), the `.mcpb` bundles and
   checksums for all of them
4. builds and pushes the multi-arch image `ghcr.io/moovit-sp-gmbh/hcloud-mcp`
5. creates a draft GitHub release with all files attached

Set `HCLOUD_API_SERVER` in CI, plus `SPEC_TOKEN` or `HCLOUD_API_TOKEN` if the
spec endpoints need authentication. Without `HCLOUD_API_SERVER`, the release
reuses an existing local catalog. If there is none, it fails rather than
shipping a binary without tools.

## Docker

The image runs `hcloud-mcp http` on port 8080. It is based on
`distroless/static` and runs as non-root.

```sh
# shared token
docker run -p 8080:8080 -e HCLOUD_API_SERVER=https://api.example.com \
  -e HCLOUD_API_TOKEN=... ghcr.io/moovit-sp-gmbh/hcloud-mcp

# every client sends its own token
docker run -p 8080:8080 -e HCLOUD_API_SERVER=https://api.example.com \
  -e apiTokenMode=external ghcr.io/moovit-sp-gmbh/hcloud-mcp
```

The container health check calls `hcloud-mcp healthcheck`, which probes
`/healthz` on `ADDR` (default `:8080`).
