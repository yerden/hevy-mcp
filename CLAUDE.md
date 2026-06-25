# Agent guidelines for hevy-mcp

Notes for an AI agent (Claude or otherwise) working on this codebase. The user-facing overview lives in `README.md`; this file is for things that aren't obvious from the code and would otherwise be re-learned the hard way.

## Build, test, lint

- Tests are run **in Docker** (`docker compose run --rm test`). The user wants the host toolchain out of the equation, and this is the canonical way to verify CI-ready state. Local `go test ./...` is fine while iterating, but always re-run in Docker before declaring done.
- Go version: the `go.mod` directive is `1.25.5` (mcp-go's minimum). The Docker test/build stages use `golang:1.25-alpine`.
- Final image: `Dockerfile` is multi-stage; the runtime image is `alpine:3.20` with the static binary and `ca-certificates`. Don't add runtime deps without a reason.

## Architecture in one paragraph

`cmd/hevy-mcp/main.go` reads `HEVY_API_KEY` from env (the only secret), parses CLI flags (`--transport`, `--port`, `--base-url`), builds a `*hevy.Client`, wraps it in a `tools.ClientFactory`, registers tools on an `mcp-go` server, then starts stdio or streamable HTTP. The factory pattern is the key seam: stdio uses a static factory (one key for the whole process); HTTP uses `tools.HeaderFactory` which pulls a per-request `X-Hevy-Api-Key` header out of context (set by `tools.HTTPHeaderInjector` via `server.WithHTTPContextFunc`). That makes the same binary usable as either a single-tenant subprocess or a multi-tenant HTTP daemon.

## Hevy API quirks (HARD-WON)

These are real footguns from the Hevy public API. Read before changing wire types.

### `folder_id` on POST /v1/routines must be `null`, not absent

Hevy's error: `"Invalid routine folder id: undefined"` when the field is missing. The schema documents it as nullable with description *"Pass null to insert the routine into default 'My Routines' folder."* So:

- `*int` with `json:"folder_id"` — **no `omitempty`**. Nil pointer must serialize as `null`.
- Pinned by `TestClient_CreateRoutine_FolderIDNilSerializesAsNull`.

### Input and output shapes differ — keep them separate

Reusing one struct for request and response is tempting but breaks. Hevy rejects fields that are output-only on input.

- `index` on sets and exercises — output-only. Hevy infers order from array position.
- `title` on routine/workout exercises — output-only. Derived from the template ID.
- `created_at` / `updated_at` — output-only.
- Field renames between input and output:
  - Exercise templates: response `type`/`primary_muscle_group`/`secondary_muscle_groups` vs request `exercise_type`/`muscle_group`/`other_muscles`.
  - Exercises: response `supersets_id` (plural) vs request `superset_id` (singular).

Response types live in `internal/hevy/models.go`. Request types live in `internal/hevy/requests.go` (`RoutineCreate`, `RoutineUpdate`, `WorkoutPayload`, `RoutineFolderCreate`, `ExerciseTemplateCreate`, etc.). **Do not** add output-only fields to the input types.

### `omitempty` on plain `bool` drops `false`

Go's `omitempty` treats the zero value as "empty". If a Hevy field is boolean and the user can deliberately want `false`, use `*bool` with `omitempty`:
- `nil` → field omitted (Hevy uses its default)
- `&false` → `"foo":false` sent
- `&true` → `"foo":true` sent

Same logic for any numeric field where `0` might be a real value, but in practice all Hevy numeric fields already use `*int` / `*float64`.

**Exception: `workout.is_private` must always be sent.** Hevy rejects the request with `"workout.is_private" is required` if the field is missing — so `WorkoutPayload.IsPrivate` is a plain `bool` with no `omitempty`. Default zero value is `false` (public), matching the Hevy app default. Pinned by `TestTools_CreateWorkout_IsPrivateDefaultsToFalseWhenUnset` plus the `IsPrivateFalseSurvives` / `IsPrivateTrueSurvives` pair.

### Some GET responses are wrapped under a key

Not just POSTs — some GETs wrap too. Known ones:

- `GET /v1/user/info` → `{"data": {...UserInfo}}`
- `GET /v1/routines/{id}` → `{"routine": {...}}`

When adding a new typed GET, default to `doUnwrap(method, path, query, body, key, out)` rather than `do`. It tries the envelope first and falls back to bare, so future shape changes don't silently zero out the result.

### POST/PUT responses are inconsistent — return raw bytes

Hevy's OpenAPI spec says POST endpoints return the created object, but in practice some return wrapped envelopes (`{"routine": {...}}`), some return empty bodies, and the shape varies by endpoint. Decoding into a typed struct lost data silently.

**Pattern**: for create/update tools, the handler calls `Client.DoRaw(method, path, query, body)` and forwards Hevy's bytes verbatim via `rawResult(...)`. Empty body becomes a `{"status":"created","note":"..."}` synthetic response so the model isn't confused. This sidesteps all wrapping/empty-body issues at once.

For GET endpoints, `Client.doUnwrap(method, path, query, body, key, out)` tries `{"<key>": {...}}` first, falls back to bare. Use this for `GET /v1/routines/{id}` (documented wrapping), and apply it as a precaution to other single-resource GETs.

### Routine UPDATE schema has no `folder_id`

Per Hevy spec, `PutRoutinesRequestBody.routine` does not include `folder_id`. Folder placement can't be changed through update. Use a different flow if you need to move a routine. `RoutineUpdate` reflects this deliberately.

### Exercise template create returns `{"id": ...}` with ambiguous typing

The spec says integer; real catalog uses hex strings like `"05293BCA"`. `flexibleID` (in `requests.go`) parses both and stringifies. If you change it, keep both code paths working.

## MCP / mcp-go conventions

- Tool definitions use **`mcp.WithInputSchema[T]()`** for create/update tools — it reflects on a Go struct (request type) and emits a precise JSON schema. The model needs this to populate fields like `folder_id` it would otherwise have to guess.
- Simple read tools use explicit `mcp.WithString`/`mcp.WithNumber` etc. — keep them flat; no need to reach for typed schema for one-field tools.
- Tool **error semantics**: API errors are surfaced as `mcp.NewToolResultError(...)` (tool result with `IsError=true`), not as transport-level errors. The model can read the message and try again. Reserve transport errors for genuinely broken requests.
- Pagination: every list tool exposes `page` (default 1) and `pageSize` (default 10, max 10 — Hevy's limit). The model controls paging so Hevy traffic stays bounded; never auto-walk all pages.

## Authentication

Two distinct auth paths, one per transport. There is no `X-Hevy-Api-Key` header anymore; it was removed in the OAuth cutover.

- **Stdio mode**: `HEVY_API_KEY` env var, required at startup. One key for the process lifetime. Tools are wired via `tools.StaticFactory(client)`.
- **HTTP mode**: OAuth 2.1 per the MCP authorization spec. Users paste their Hevy API key on the `/oauth/authorize` consent page; the server returns an access token wrapping the key in a signed (HS256) + encrypted (AES-256-GCM) JWT. Claude presents that token as `Authorization: Bearer ...` on every MCP request. Tools are wired via `tools.ContextFactory(base)`, which reads the per-request key from context populated by `oauth.Config.BearerMiddleware`.
- Required HTTP-mode env vars: `OAUTH_SIGNING_KEY` (base64-encoded 32 bytes for HMAC-256) and `OAUTH_ENCRYPTION_KEY` (base64-encoded 32 bytes for AES-256-GCM). Required HTTP-mode flag: `--issuer` (the canonical https URL of the deployment, e.g. `https://hevy.fly.dev`).
- `Client.WithAPIKey(key)` returns a shallow copy that shares the underlying `*http.Client` (and its connection pool). Used by `ContextFactory` to clone the base per request.

### OAuth design quirks worth knowing

- **`mcp-go`'s `WithProtectedResourceMetadata` does not enforce bearer tokens.** It only serves the metadata document. We implement bearer validation + WWW-Authenticate emission ourselves in `internal/oauth/middleware.go`. Don't add `WithProtectedResourceMetadata` to the streamable server — we mount `NewProtectedResourceMetadataHandler` directly on our own mux instead, since the library's auto-mux path doesn't compose with our extra OAuth endpoints.
- **The protected-resource metadata MUST list `authorization_servers` and `bearer_methods_supported`.** Both fields use `omitempty` in the library config, so a Config with empty defaults serves a non-conformant document. Always populate them.
- **PKCE S256 is required.** `code_challenge_method=plain` is rejected at `/oauth/authorize`.
- **Single allowed redirect_uri.** Hardcoded to `https://claude.ai/api/mcp/auth_callback` (Claude web's hosted callback). Any other redirect_uri is rejected before any user interaction.
- **No DCR.** Claude web's "Advanced settings" lets users enter a static client_id. Since this is a public client with PKCE, we don't even validate the client_id — anything non-empty works.
- **Resource indicator (RFC 8707) is required and audience-bound.** The authorize/token endpoints require `resource` to match the issuer; bearer middleware rejects tokens whose `aud` claim doesn't match the canonical resource URL.

## Testing patterns

- Hevy client tests use `httptest.NewServer` and decode the captured request body — see `internal/hevy/client_test.go`. New tests should follow the `newFakeServer(t, status, body)` pattern.
- Tool tests drive the server via `server.HandleMessage(...)` with a JSON-RPC `tools/call` payload — see `internal/tools/register_test.go`. Use the existing `upstream` helper.
- The MCP server's `tools/call` result is `*mcp.CallToolResult` (pointer); `tools/list` is `*mcp.ListToolsResult`. Both helpers in `register_test.go` accept either pointer or value form, defensively.
- When adding tests for wire-shape behavior, decode `u.lastBody` into a typed struct rather than substring-matching — JSON field order is not guaranteed.

## When in doubt

- **API behavior questions**: the Hevy OpenAPI spec lives at `https://api.hevyapp.com/docs`. It's served as Swagger UI; the JSON spec is embedded in `swagger-ui-init.js` as a `swaggerDoc` object. Brace-match it out, parse, inspect. The user has confirmed the spec is mostly accurate, but POST response shapes drift — trust the raw-bytes pattern over the spec.
- **Don't trust the spec for POST response shapes.** Trust user reports + `DoRaw` round-trips.
- **Don't add `omitempty` to anything where the zero value carries meaning** (`false`, `0`, empty string for required fields).
- **Don't merge input and output types.** It will appear to work until Hevy rejects a stray `title` or `index`.
