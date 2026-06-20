# hevy-mcp Implementation Plan

## Phase 1 — Project Bootstrap

### 1.1 Go module + directory skeleton
- `go mod init github.com/yerden/hevy-mcp`
- Create directories: `cmd/hevy-mcp/`, `internal/hevy/`, `internal/mcp/`
- Create empty placeholder files so the tree is clear from the start

### 1.2 Dependencies
```
go get github.com/mark3labs/mcp-go      # MCP protocol, stdio + HTTP/SSE transports
go get github.com/stretchr/testify      # test assertions only
```
Hevy client uses stdlib `net/http` + `encoding/json` — no extra deps.

---

## Phase 2 — Hevy API Client

### 2.1 `internal/hevy/models.go`
Define Go structs for all OpenAPI schemas:
- `WorkoutSet` (type, weight_kg, reps, distance_meters, duration_seconds, custom_metric, rpe)
- `WorkoutExercise` (exercise_template_id, superset_id, notes, sets)
- `Workout` (id, title, description, start_time, end_time, is_private, exercises)
- `CreateWorkoutRequest` / `UpdateWorkoutRequest`
- `PaginatedWorkouts` (page, page_count, workouts)
- `WorkoutEvent` (union: updated with Workout | deleted with id+deleted_at)
- `PaginatedWorkoutEvents`
- `RoutineSet` (type, weight_kg, reps, distance_meters, duration_seconds, custom_metric, rep_range)
- `RoutineExercise` (exercise_template_id, superset_id, rest_seconds, notes, sets)
- `Routine` (id, title, folder_id, notes, exercises)
- `CreateRoutineRequest` / `UpdateRoutineRequest`
- `PaginatedRoutines`
- `ExerciseTemplate` (id, title, exercise_type, equipment_category, muscle_group, other_muscles)
- `CreateExerciseTemplateRequest`
- `PaginatedExerciseTemplates`
- `RoutineFolder` (id, title)
- `CreateRoutineFolderRequest`
- `PaginatedRoutineFolders`
- `ExerciseHistoryEntry`
- `ExerciseHistoryResponse`
- `BodyMeasurement` (date + all measurement fields)
- `PaginatedBodyMeasurements`
- `UserInfo` (id, name, url)
- Enum types as `string` type aliases with constants (set type, exercise type, equipment, muscle group)

### 2.2 `internal/hevy/client.go`
```go
type Client struct {
    baseURL    string
    apiKey     string
    httpClient *http.Client
}

func New(apiKey string) *Client
func (c *Client) do(method, path string, body, out any) error
```
- Sets `api-key` header on every request
- Reads and decodes JSON response body into `out`
- Maps non-2xx responses to a typed `APIError{StatusCode, Message}`

### 2.3 `internal/hevy/api.go`
One exported method per endpoint:
```go
// Workouts
func (c *Client) ListWorkouts(page, pageSize int) (*PaginatedWorkouts, error)
func (c *Client) CreateWorkout(req *CreateWorkoutRequest) (*Workout, error)
func (c *Client) GetWorkout(id string) (*Workout, error)
func (c *Client) UpdateWorkout(id string, req *UpdateWorkoutRequest) (*Workout, error)
func (c *Client) GetWorkoutCount() (int, error)
func (c *Client) GetWorkoutEvents(page, pageSize int, since string) (*PaginatedWorkoutEvents, error)

// Routines
func (c *Client) ListRoutines(page, pageSize int) (*PaginatedRoutines, error)
func (c *Client) CreateRoutine(req *CreateRoutineRequest) (*Routine, error)
func (c *Client) GetRoutine(id string) (*Routine, error)
func (c *Client) UpdateRoutine(id string, req *UpdateRoutineRequest) (*Routine, error)

// Exercise templates
func (c *Client) ListExerciseTemplates(page, pageSize int) (*PaginatedExerciseTemplates, error)
func (c *Client) CreateExerciseTemplate(req *CreateExerciseTemplateRequest) (int, error)
func (c *Client) GetExerciseTemplate(id string) (*ExerciseTemplate, error)

// Routine folders
func (c *Client) ListRoutineFolders(page, pageSize int) (*PaginatedRoutineFolders, error)
func (c *Client) CreateRoutineFolder(title string) (*RoutineFolder, error)
func (c *Client) GetRoutineFolder(id int) (*RoutineFolder, error)

// Exercise history
func (c *Client) GetExerciseHistory(templateID, startDate, endDate string) (*ExerciseHistoryResponse, error)

// Body measurements
func (c *Client) ListBodyMeasurements(page, pageSize int) (*PaginatedBodyMeasurements, error)
func (c *Client) CreateBodyMeasurement(m *BodyMeasurement) error
func (c *Client) GetBodyMeasurement(date string) (*BodyMeasurement, error)
func (c *Client) UpdateBodyMeasurement(date string, m *BodyMeasurement) error

// User
func (c *Client) GetUserInfo() (*UserInfo, error)
```

### 2.4 Tests for Hevy client
- `internal/hevy/client_test.go`
- Use `httptest.NewServer` to mock the Hevy REST API
- One test per method covering: success path, 401 error, 404 error, malformed JSON

---

## Phase 3 — MCP Tool Registration

### 3.1 `internal/tools/register.go`
Single exported function:
```go
func RegisterAll(s *server.MCPServer, h *hevy.Client)
```

For each tool:
```go
tool := mcp.NewTool("hevy_list_workouts",
    mcp.WithDescription("List workouts, paginated. Use page/pageSize to walk through results without fetching everything at once."),
    mcp.WithNumber("page",     mcp.WithDescription("1-based page number"), mcp.DefaultNumber(1)),
    mcp.WithNumber("pageSize", mcp.WithDescription("Results per page (max 10)"), mcp.DefaultNumber(10)),
)
s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    page     := int(req.GetFloat("page", 1))
    pageSize := int(req.GetFloat("pageSize", 10))
    result, err := h.ListWorkouts(page, pageSize)
    if err != nil { return nil, err }
    b, _ := json.Marshal(result)
    return mcp.NewToolResultText(string(b)), nil
})
```

Pagination is exposed as explicit `page`/`pageSize` parameters on every list tool. The AI controls paging so requests to the Hevy API are bounded.

### 3.2 Tests for tool handlers
- `internal/tools/register_test.go`
- Construct a real `mcp.MCPServer`, call `RegisterAll` with a mock `hevy.Client` (interface or `httptest` server)
- Issue `tools/call` requests and assert the JSON result matches the expected Hevy response

---

## Phase 4 — Entry Point

### 4.1 `cmd/hevy-mcp/main.go`
```go
func main() {
    apiKey := mustEnv("HEVY_API_KEY")
    baseURL := os.Getenv("HEVY_BASE_URL") // optional, defaults to https://api.hevyapp.com

    hevyClient := hevy.New(apiKey, baseURL)

    s := server.NewMCPServer("hevy-mcp", "0.1.0", server.WithToolCapabilities(true))
    tools.RegisterAll(s, hevyClient)

    switch transport := os.Getenv("MCP_TRANSPORT"); transport {
    case "http":
        port := os.Getenv("MCP_PORT")
        if port == "" { port = "8080" }
        h := server.NewStreamableHTTPServer(s)
        log.Printf("hevy-mcp HTTP/SSE listening on :%s", port)
        log.Fatal(http.ListenAndServe(":"+port, h))
    default: // "stdio"
        if err := server.ServeStdio(s); err != nil {
            log.Fatal(err)
        }
    }
}
```

---

## Phase 5 — Docker

### 5.1 `Dockerfile`
```dockerfile
# Stage 1: build
FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /hevy-mcp ./cmd/hevy-mcp

# Stage 2: run
FROM alpine:3.20
RUN adduser -D hevy
USER hevy
COPY --from=builder /hevy-mcp /usr/local/bin/hevy-mcp
ENTRYPOINT ["/usr/local/bin/hevy-mcp"]
```

### 5.2 `docker-compose.yml`
```yaml
services:
  test:
    image: golang:1.23-alpine
    working_dir: /src
    volumes:
      - .:/src
    environment:
      - HEVY_API_KEY=test-key
    command: go test ./...
```

Run tests: `docker compose run --rm test`

---

## Implementation Order

1. `go.mod` + directory skeleton + `go get` dependencies
2. `internal/hevy/models.go`
3. `internal/hevy/client.go`
4. `internal/hevy/api.go`
5. `internal/hevy/client_test.go`
6. `internal/tools/register.go`
7. `internal/tools/register_test.go`
8. `cmd/hevy-mcp/main.go`
9. `Dockerfile`
10. `docker-compose.yml`
11. End-to-end smoke test (run binary locally against real API, call `hevy_get_user_info`)

---

## Resolved Decisions

| Question | Decision |
|----------|----------|
| Go module path | `github.com/yerden/hevy-mcp` |
| MCP library | `github.com/mark3labs/mcp-go` |
| Pagination | Explicit `page`/`pageSize` params on every list tool; AI controls paging |
| Transports | Both stdio (default) and HTTP/SSE via `MCP_TRANSPORT=http` + `MCP_PORT` |
