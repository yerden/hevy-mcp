# hevy-mcp

A Model Context Protocol server that fronts the [Hevy](https://www.hevyapp.com/) workout-tracking REST API. Lets an AI assistant (Claude, etc.) read your workouts, manage routines, log measurements, and so on through MCP tools.

Written in Go. Single binary. Two transports: stdio (subprocess mode, single-user) and streamable HTTP with OAuth (daemon mode, multi-user).

## Quick start

### Stdio mode (local subprocess, single user)

```bash
docker build -t hevy-mcp .
docker run --rm -i -e HEVY_API_KEY=pk_... hevy-mcp
```

Or natively (Go 1.25+):

```bash
go build -o hevy-mcp ./cmd/hevy-mcp
HEVY_API_KEY=pk_... ./hevy-mcp
```

### HTTP mode (remote, multi-user, OAuth)

HTTP mode authenticates each user through the MCP OAuth 2.1 flow. The server hosts a consent page where the user pastes their Hevy API key; the server returns a bearer token that wraps the key (signed HMAC-SHA256, encrypted AES-256-GCM). Claude carries that token on every MCP request.

Two server-side secrets are required (32 random bytes each, base64-encoded). Generate once:

```bash
openssl rand -base64 32   # OAUTH_SIGNING_KEY
openssl rand -base64 32   # OAUTH_ENCRYPTION_KEY
```

Run:

```bash
docker run --rm -p 8080:8080 \
  -e OAUTH_SIGNING_KEY=... \
  -e OAUTH_ENCRYPTION_KEY=... \
  hevy-mcp --transport=http --issuer=https://your-host.example
```

### Deploy on fly.io

```bash
fly launch
fly secrets set OAUTH_SIGNING_KEY="$(openssl rand -base64 32)"
fly secrets set OAUTH_ENCRYPTION_KEY="$(openssl rand -base64 32)"
fly secrets set OAUTH_ISSUER="https://<your-app>.fly.dev"
fly deploy
```

`OAUTH_ISSUER` is the fallback for the `--issuer` flag, so the Dockerfile entrypoint doesn't need to be edited per-deployment.

### Connect from Claude (web, desktop, mobile)

1. In Claude → Settings → Connectors → **Add custom connector**.
2. URL: `https://your-host.example/mcp` — the MCP endpoint path is `/mcp`, and Claude treats whatever URL you paste here as the canonical resource. The `/mcp` suffix is required.
3. **Advanced settings → OAuth Client ID**: enter any non-empty value, e.g. `hevy-mcp`. Leave Client Secret blank.
4. Click Connect. Claude redirects to the server's consent page.
5. Paste your Hevy API key (find it in the Hevy app → Settings → Developer).
6. Connect. Tools are now available in the conversation.

To disconnect, regenerate your Hevy API key in the Hevy app — that immediately invalidates all outstanding tokens.

### Stdio with Claude Desktop

```json
{
  "mcpServers": {
    "hevy": {
      "command": "docker",
      "args": ["run", "--rm", "-i", "-e", "HEVY_API_KEY", "hevy-mcp"],
      "env": { "HEVY_API_KEY": "pk_..." }
    }
  }
}
```

## Configuration

| Variable | Mode | Notes |
|---|---|---|
| `HEVY_API_KEY` | stdio | Required. Single user for the process lifetime. |
| `OAUTH_SIGNING_KEY` | http | Required. Base64-encoded 32 bytes (HMAC-SHA256). |
| `OAUTH_ENCRYPTION_KEY` | http | Required. Base64-encoded 32 bytes (AES-256-GCM). |
| `OAUTH_ISSUER` | http | Fallback for `--issuer`. Canonical https URL of this deployment. |

| Flag | Default | Notes |
|---|---|---|
| `--transport` | `stdio` | `stdio` or `http`. |
| `--port` | `8080` | HTTP listen port (HTTP mode only). |
| `--issuer` | — | Canonical https URL of this deployment (required in HTTP mode). |
| `--base-url` | `https://api.hevyapp.com` | Override only for testing. |

Rotating either OAuth secret invalidates every outstanding access token; users must reconnect through the consent flow.

## Tools

22 tools, all prefixed `hevy_`. Pagination is exposed via explicit `page`/`pageSize` arguments on every list tool; the model controls paging so Hevy calls stay bounded.

| Domain | Tools |
|---|---|
| Workouts | `list_workouts`, `create_workout`, `get_workout`, `update_workout`, `get_workout_count`, `get_workout_events` |
| Routines | `list_routines`, `create_routine`, `get_routine`, `update_routine` |
| Exercise templates | `list_exercise_templates`, `create_exercise_template`, `get_exercise_template` |
| Routine folders | `list_routine_folders`, `create_routine_folder`, `get_routine_folder` |
| Exercise history | `get_exercise_history` |
| Body measurements | `list_body_measurements`, `create_body_measurement`, `get_body_measurement`, `update_body_measurement` |
| User | `get_user_info` |

Schemas for create/update tools are derived from the Hevy OpenAPI spec, so the model sees field-level structure (e.g. `folder_id`, set types, rep ranges) rather than free-form objects.

## Tests

Tests run in Docker via Compose:

```bash
docker compose run --rm test
```

This mounts the source tree into `golang:1.25-alpine` and runs `go test ./...`. No host Go toolchain required.

## Troubleshooting

Hevy client failures are logged via `slog` to stderr with a stable `event` tag, so you can grep for them in `fly logs` (or any tailer):

| Event | Level | When | Fields |
|---|---|---|---|
| `hevy_api_error` | warn | Hevy returned a non-2xx | `method`, `path`, `status`, `body` (≤512 B) |
| `hevy_decode_error` | error | Response body didn't parse into the expected Go type | `method`, `path`, `err`, `body` (≤1024 B); plus `key` for envelope-wrapped GETs |

Tail on fly.io:

```bash
fly logs -a <your-app> | grep -E 'hevy_(api|decode)_error'
```

A `hevy_decode_error` is the main signal that Hevy's API drifted (renamed field, envelope shape changed, new required field). The truncated body in the log line is usually enough to identify what changed.

Fly retains logs for a rolling window only (hours, not days). If you need longer history for post-mortem, ship them via the [Fly Log Shipper](https://github.com/superfly/fly-log-shipper) to Axiom / Datadog / Loki.

## Layout

```
cmd/hevy-mcp/          binary entry point (flag parsing, transport selection)
internal/hevy/         REST client, request/response types
internal/tools/        MCP tool registration, context-based key plumbing
internal/oauth/        OAuth 2.1 authorization server + bearer middleware
Dockerfile             multi-stage build → ~17 MB image
docker-compose.yml     test runner + sample HTTP service
```

See `CLAUDE.md` for agent guidelines (Hevy API quirks, OAuth design notes, conventions).

## License

Personal project, no warranty.
