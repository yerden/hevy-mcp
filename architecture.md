# hevy-mcp Architecture

## Overview

A Model Context Protocol (MCP) server written in Go that wraps the Hevy workout-tracking REST API and exposes it as MCP tools consumable by AI assistants (Claude, etc.).

```
┌─────────────────┐   stdio/JSON-RPC   ┌──────────────────┐   HTTPS   ┌──────────────────────┐
│  MCP Client     │◄──────────────────►│  hevy-mcp        │◄─────────►│  Hevy REST API       │
│  (Claude, etc.) │                    │  (Go binary)     │           │  api.hevyapp.com/v1  │
└─────────────────┘                    └──────────────────┘           └──────────────────────┘
```

Authentication: the binary reads `HEVY_API_KEY` from the environment and forwards it as an `api-key` header on every Hevy request. No state is stored locally.

---

## Directory Layout

```
hevy-mcp/
├── cmd/
│   └── hevy-mcp/
│       └── main.go           # wires everything, parses flags, starts chosen transport
├── internal/
│   ├── hevy/
│   │   ├── client.go         # HTTP client (base URL, api-key header, error handling)
│   │   ├── models.go         # all request/response structs matching the OpenAPI spec
│   │   └── api.go            # one method per Hevy endpoint
│   └── tools/
│       └── register.go       # builds mcp-go tool definitions and registers handlers
├── Dockerfile                # multi-stage production image
├── docker-compose.yml        # test runner (go test ./...)
├── go.mod
├── go.sum
└── task.md
```

---

## Component Responsibilities

### `cmd/hevy-mcp/main.go`
Reads `HEVY_API_KEY` from the environment (the only secret), parses CLI flags (`--transport`, `--port`, `--base-url`), constructs a `hevy.Client`, builds the `mcp-go` server via `tools.RegisterAll`, then starts either transport based on `--transport` (`stdio` default, or `http` for HTTP/SSE on `--port`).

### `internal/hevy/`

**`client.go`**
- Wraps `net/http.Client`
- Injects `api-key` header on every request
- Decodes JSON responses; maps non-2xx HTTP status codes to typed Go errors

**`models.go`**
- Go structs for every request and response schema in the OpenAPI spec:
  - `Workout`, `WorkoutExercise`, `WorkoutSet`
  - `Routine`, `RoutineExercise`, `RoutineSet`, `RepRange`
  - `ExerciseTemplate`
  - `RoutineFolder`
  - `ExerciseHistoryEntry`
  - `BodyMeasurement`
  - `UserInfo`
  - Paginated wrappers: `PaginatedWorkouts`, `PaginatedRoutines`, etc.
  - Event types: `WorkoutEvent` (updated | deleted)

**`api.go`**
One exported method per Hevy endpoint, e.g.:
```
ListWorkouts(page, pageSize int) (*PaginatedWorkouts, error)
CreateWorkout(req *CreateWorkoutRequest) (*Workout, error)
GetWorkout(id string) (*Workout, error)
...
```

### `internal/tools/register.go`
Uses `github.com/mark3labs/mcp-go`:
- Calls `mcp.NewTool(name, mcp.WithDescription(...), mcp.WithNumber(...), ...)` for each of the 21 tools
- Calls `s.AddTool(tool, handlerFunc)` to attach a typed handler
- Each handler: pulls args via `req.GetString/GetInt/...`, calls `hevy.Client`, marshals result to JSON text, returns `mcp.NewToolResultText(...)`

---

## MCP Tools Exposed

All tool names are prefixed with `hevy_` to avoid collisions in multi-server setups.

### Workouts
| Tool | Hevy endpoint | Notes |
|------|--------------|-------|
| `hevy_list_workouts` | GET /v1/workouts | `page`, `pageSize` params |
| `hevy_create_workout` | POST /v1/workouts | full exercise+set structure |
| `hevy_get_workout` | GET /v1/workouts/{id} | |
| `hevy_update_workout` | PUT /v1/workouts/{id} | |
| `hevy_get_workout_count` | GET /v1/workouts/count | returns integer |
| `hevy_get_workout_events` | GET /v1/workouts/events | `since` ISO 8601 filter |

### Routines
| Tool | Hevy endpoint |
|------|--------------|
| `hevy_list_routines` | GET /v1/routines |
| `hevy_create_routine` | POST /v1/routines |
| `hevy_get_routine` | GET /v1/routines/{id} |
| `hevy_update_routine` | PUT /v1/routines/{id} |

### Exercise Templates
| Tool | Hevy endpoint |
|------|--------------|
| `hevy_list_exercise_templates` | GET /v1/exercise_templates |
| `hevy_create_exercise_template` | POST /v1/exercise_templates |
| `hevy_get_exercise_template` | GET /v1/exercise_templates/{id} |

### Routine Folders
| Tool | Hevy endpoint |
|------|--------------|
| `hevy_list_routine_folders` | GET /v1/routine_folders |
| `hevy_create_routine_folder` | POST /v1/routine_folders |
| `hevy_get_routine_folder` | GET /v1/routine_folders/{id} |

### Exercise History
| Tool | Hevy endpoint | Notes |
|------|--------------|-------|
| `hevy_get_exercise_history` | GET /v1/exercise_history/{id} | `start_date`, `end_date` optional |

### Body Measurements
| Tool | Hevy endpoint |
|------|--------------|
| `hevy_list_body_measurements` | GET /v1/body_measurements |
| `hevy_create_body_measurement` | POST /v1/body_measurements |
| `hevy_get_body_measurement` | GET /v1/body_measurements/{date} |
| `hevy_update_body_measurement` | PUT /v1/body_measurements/{date} |

### User
| Tool | Hevy endpoint |
|------|--------------|
| `hevy_get_user_info` | GET /v1/user/info |

Total: **21 tools**.

---

## Data Flow (single tool call)

```
stdin → [JSON-RPC request]
        server.go: decode → route to handler
        handlers.go: unmarshal args → call hevy.Client method
        hevy/api.go: build HTTP request → send
        Hevy REST API: respond with JSON
        hevy/api.go: decode → return Go struct
        handlers.go: marshal to JSON
        server.go: wrap in JSON-RPC response
stdout ← [JSON-RPC response]
```

---

## MCP Protocol Summary

- Library: **`github.com/mark3labs/mcp-go`** (handles JSON-RPC 2.0, lifecycle, capability negotiation)
- MCP version: **2024-11-05**
- Capabilities declared: `tools` (list + call)
- No resources, prompts, or sampling implemented (not needed)

### Transports

Both are operational; selected at startup via `--transport`:

| `--transport` | How it starts | Use case |
|---|---|---|
| `stdio` (default) | `server.ServeStdio(s)` | Claude Desktop / subprocess mode |
| `http` | `server.NewStreamableHTTPServer(s)` on `--port` (default `8080`) | Daemon / multi-client mode via HTTP+SSE |

---

## Docker

### Production image (`Dockerfile`)
Multi-stage build:
1. `golang:1.23-alpine` builder stage — compiles static binary
2. `alpine:3.20` final stage — copies binary only (~10 MB image)

The container is started with `HEVY_API_KEY` injected at runtime (e.g. via `-e` flag or Compose env file).

### Test runner (`docker-compose.yml`)
A single `test` service using the `golang:1.23-alpine` image that mounts the source tree and runs `go test ./...`. This keeps the host Go toolchain out of the equation.

---

## Security Notes

- API key is never hardcoded; read only from environment
- No local database or file system writes
- All Hevy traffic is over TLS (HTTPS)
- The MCP server only exposes what the Hevy API exposes — no elevation of privilege possible
