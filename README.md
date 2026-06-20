# hevy-mcp

A Model Context Protocol server that fronts the [Hevy](https://www.hevyapp.com/) workout-tracking REST API. Lets an AI assistant (Claude, etc.) read your workouts, manage routines, log measurements, and so on through MCP tools.

Written in Go. Single binary. Two transports: stdio (subprocess mode) and streamable HTTP (daemon mode, multi-user).

## Quick start

### Run locally with Docker

```bash
docker build -t hevy-mcp .

# stdio mode (for Claude Desktop / subprocess clients):
docker run --rm -i -e HEVY_API_KEY=pk_... hevy-mcp

# HTTP mode (for HTTP MCP clients / multi-user):
docker run --rm -p 8080:8080 \
  -e MCP_TRANSPORT=http \
  -e HEVY_API_KEY=pk_... \
  hevy-mcp
```

You can also build natively if you have Go 1.25+:
```bash
go build -o hevy-mcp ./cmd/hevy-mcp
HEVY_API_KEY=pk_... ./hevy-mcp
```

### Configure your MCP client

**Claude Code (CLI):**
```bash
claude mcp add --transport http hevy http://localhost:8080/mcp \
  --header "X-Hevy-Api-Key: pk_..."
```

**Claude Desktop / other clients** (`claude_desktop_config.json`):
```json
{
  "mcpServers": {
    "hevy": {
      "type": "http",
      "url": "http://localhost:8080/mcp",
      "headers": { "X-Hevy-Api-Key": "pk_..." }
    }
  }
}
```

Or stdio mode:
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

| Variable | Default | Notes |
|---|---|---|
| `HEVY_API_KEY` | (required for stdio; fallback for HTTP) | Get one from your Hevy account settings. |
| `HEVY_BASE_URL` | `https://api.hevyapp.com` | Override only for testing. |
| `MCP_TRANSPORT` | `stdio` | Set to `http` for HTTP streamable transport. |
| `MCP_PORT` | `8080` | HTTP listen port. |

### Authentication modes

- **stdio** — one process per user. The key must be in `HEVY_API_KEY` at startup.
- **HTTP** — one process can serve multiple users. Each MCP client sends its own key via the `X-Hevy-Api-Key` request header. `HEVY_API_KEY` is used as a fallback when the header is absent (useful for single-user HTTP deployments).

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

## Layout

```
cmd/hevy-mcp/          binary entry point (env, transport selection)
internal/hevy/         REST client, request/response types
internal/tools/        MCP tool registration, per-session auth
Dockerfile             multi-stage build → ~17 MB image
docker-compose.yml     test runner + sample HTTP service
```

See `architecture.md` for a full design overview and `CLAUDE.md` for agent guidelines (Hevy API quirks, conventions, gotchas).

## License

Personal project, no warranty.
