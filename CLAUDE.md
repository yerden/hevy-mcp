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

As of 2026-10-19 a POST with the key absent no longer reproduces that error (probed with a deliberately-invalid body, so validation-level evidence only). Keep sending explicit `null` anyway: it is what the spec documents, and it is unambiguous either way.

### Input and output shapes differ — keep them separate

Reusing one struct for request and response is tempting but breaks. Hevy rejects fields that are output-only on input.

- `index` on sets and exercises — output-only. Hevy infers order from array position.
- `title` on routine/workout exercises — output-only. Derived from the template ID.
- `created_at` / `updated_at` — output-only.
- Field renames between input and output:
  - Exercise templates: response `type`/`primary_muscle_group`/`secondary_muscle_groups` vs request `exercise_type`/`muscle_group`/`other_muscles`.
- **`superset_id` is _not_ a rename** — singular on both reads and writes. The OpenAPI spec used to document the read field as `supersets_id` (plural); the live API never did, and Hevy corrected the spec (re-verified 2026-09-19: no `supersets_id` anywhere in the spec, raw `GET /v1/workouts` and `/v1/routines` return `superset_id`). We briefly shipped a defensive decoder accepting both spellings; it was removed once the spec was fixed. Don't reintroduce the plural.

Response types live in `internal/hevy/models.go`. Request types live in `internal/hevy/requests.go` (`RoutineCreate`, `RoutineUpdate`, `WorkoutPayload`, `RoutineFolderCreate`, `ExerciseTemplateCreate`, etc.). **Do not** add output-only fields to the input types.

### `omitempty` on plain `bool` drops `false`

Go's `omitempty` treats the zero value as "empty". If a Hevy field is boolean and the user can deliberately want `false`, use `*bool` with `omitempty`:
- `nil` → field omitted (Hevy uses its default)
- `&false` → `"foo":false` sent
- `&true` → `"foo":true` sent

Same logic for any numeric field where `0` might be a real value, but in practice all Hevy numeric fields already use `*int` / `*float64`.

**`workout.is_private` on an *update* overwrites the stored value.** Since the field is always sent, a PUT that doesn't set it publishes a previously-private workout — and Hevy's read endpoints don't return `is_private` at all (not in the spec's `Workout` schema, absent from live `GET /v1/workouts`), so the tool *cannot* read the current value and preserve it the way `hevy_update_body_measurement` merges. The only defence is telling the caller: `hevy_update_workout`'s description says so, and the warning is also attached to the field itself via a `jsonschema` tag so it appears in the published schema (pinned by `TestTools_UpdateWorkout_SchemaWarnsAboutIsPrivate`).

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

### Routine UPDATE takes `folder_id` as a tri-state (changed 2026-10-19)

**This reverses the old rule.** Until some point between 2026-08-02 and 2026-10-19, `PutRoutinesRequestBody.routine` had no `folder_id` and the live API rejected the key with `HTTP 400 {"error":"Unrecognized key(s) in object: 'folder_id'"}`. The spec now documents it (nullable) and the live API honours it. Re-verified 2026-10-19 by round-tripping a throwaway routine (`PUT` then `GET` to read back the authoritative value):

| PUT body | Result |
| --- | --- |
| `folder_id` absent | placement unchanged |
| `"folder_id": N` | routine moves into folder N |
| `"folder_id": null` | routine moves to the default "My Routines" list |

Two consequences for the types:

- **`hevy.RoutineUpdate.FolderID` is a `json.RawMessage`**, set via `MoveToFolder(id)` / `MoveToMyRoutines()`, nil to keep placement. A `*int` cannot express all three states: nil serializes as an absent key, so "move to My Routines" would be unreachable. Don't "simplify" it back to `*int`.
- **The model-facing schema is a separate type**, `tools.updateRoutinePayload`, which splits the tri-state into `folder_id` (integer) plus `move_to_my_routines` (bool) and rebuilds it in `wire()`. Reason: a reflected `*int` publishes `["null","integer"]`, so the model *can* send `null`, but `encoding/json` decodes that into a nil pointer — indistinguishable from omitted by the time our code sees it. The two fields are mutually exclusive; `wire()` errors out rather than guessing. `move_to_my_routines` must never reach Hevy (unrecognized keys 400 the request) — pinned by `TestTools_UpdateRoutine_MoveToMyRoutinesSendsNull`.

Rejections are still atomic — a 400 means no partial write.

### Exercise template reads carry `equipment` and `thumbnail_url`

Both are output-only and absent from the create shape (which spells equipment
`equipment_category`). Added to `ExerciseTemplate` 2026-10-19 after a spec
re-check; before that they were silently dropped from every template read.

`exercises.Search` deliberately blanks `ThumbnailURL` on its matches: the URLs
are ~110 chars of S3 path that no model needs in search results. `Equipment` is
kept — it disambiguates titles like "Bench Press (Barbell)" vs the machine
variant.

### Exercise `type` on reads is a superset of the create enum

Live catalog reads return `bodyweight_weighted`, `bodyweight_assisted`,
`floors_duration` and `steps_duration`; the spec's `CustomExerciseType` (create
input) accepts none of those, and offers `bodyweight_reps` /
`bodyweight_assisted_reps`, which reads never return. Treat `ExerciseType` as an
open string on the read side — don't validate reads against the create enum.

### `GET /v1/user/info` returns seven fields

`id`, `username`, `name`, `url`, `weight_unit`, `distance_unit`, `is_pro`. We
modelled only the first, third and fourth until 2026-10-19. `IsPro` has no
`omitempty` — `false` means "not a Pro subscriber" and must not vanish.

### Body measurement reads include `id` and `created_at`

Undocumented in the spec but present live. `BodyMeasurement` is currently shared
between request and response, so adding them would need an input/output split
per the rule below — not done yet, so those two fields are dropped on reads.

### Body measurement UPDATE is a destructive full replace

`PUT /v1/body_measurements/{date}` replaces the row: **every metric absent from the body is cleared**, and the endpoint *ignores* unrecognized keys instead of rejecting them, so it answers `HTTP 200` either way. There is no error to notice. Discovered 2026-10-19 the hard way — a two-key probe PUT wiped a real row's `weight_kg`, `lean_mass_kg` and `fat_percent` (restored from a read taken minutes earlier).

Consequences:

- `hevy_update_body_measurement` is **read-modify-write**: GET the row, `hevy.MergeBodyMeasurement(current, update)`, PUT the merged body. Pinned by `TestTools_UpdateBodyMeasurement_MergesInsteadOfClearing`. Never turn it back into a blind PUT.
- The merge works from the **raw GET bytes**, not a decoded struct, so a field Hevy adds before we model it survives the round-trip instead of being erased by the next update.
- A GET that 404s short-circuits with a message pointing at `hevy_create_body_measurement` — no write is attempted.
- `hevy.Client.UpdateBodyMeasurement` still sends what you give it (replace semantics). It is test-only today; if you ever call it from a tool, merge first.
- Clearing a metric is deliberately not expressible: `BodyMeasurementInput` uses `*float64` + `omitempty`, so nil means "leave alone". Needs an explicit mechanism if it ever comes up.

Reads also carry server-owned `id` and `created_at` (neither in the spec). They live on `BodyMeasurement` (response) and are stripped by the merge; `BodyMeasurementInput` (request) must never grow them.

### Exercise template create returns `{"id": ...}` with ambiguous typing

The spec says integer; real catalog uses hex strings like `"05293BCA"`. `flexibleID` (in `requests.go`) parses both and stringifies. If you change it, keep both code paths working.

## MCP / mcp-go conventions

- Tool definitions use **`mcp.WithInputSchema[T]()`** for create/update tools — it reflects on a Go struct (request type) and emits a precise JSON schema. The model needs this to populate fields like `folder_id` it would otherwise have to guess.
- Simple read tools use explicit `mcp.WithString`/`mcp.WithNumber` etc. — keep them flat; no need to reach for typed schema for one-field tools.
- **Every tool carries an annotation option** (`readsHevy`, `createsInHevy`, `overwritesInHevy` in `register.go`). `mcp.NewTool` defaults to `readOnlyHint=false, destructiveHint=true`, and claude.ai files any tool with those defaults under "Write/Delete tools", reads included. Pinned by `TestTools_AnnotationsMatchToolKind`, which fails for any new tool name it doesn't recognise.
- Tool **error semantics**: API errors are surfaced as `mcp.NewToolResultError(...)` (tool result with `IsError=true`), not as transport-level errors. The model can read the message and try again. Reserve transport errors for genuinely broken requests.
- Pagination: every list tool exposes `page` (default 1) and `pageSize` (default 10). The max is 10 — Hevy's limit — **except `/v1/exercise_templates`, which allows 100** (spec default *is* 100; live-verified 2026-10-19). `paginationOpts()` emits the 10 ceiling, `paginationOptsMax(100)` the templates one; the default stays 10 everywhere so a list call can't dump a huge page into context unasked. The model controls paging so Hevy traffic stays bounded; never auto-walk all pages.
- **The one auto-walk: `hevy_search_exercise_templates`.** It pages `/v1/exercise_templates` at 100 per page (~5 requests for the ~460-entry catalog), filters server-side, and returns only matches. It stops as soon as `limit` matches are found and is hard-capped at `maxPages` (20) whatever `page_count` says. Any page error fails the whole search — a partial result would read as "no such exercise". This replaced an embedded build-time catalog snapshot (2026-10-28); there is no `go generate` step or `hevy_key` build secret any more.

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
- **Exact-match redirect_uri allowlist, with a loopback carve-out.** Configured via `OAUTH_ALLOWED_REDIRECT_URIS` (comma-separated), or defaults to `https://claude.ai/api/mcp/auth_callback` when unset. Entries match verbatim except for `http://127.0.0.1/…` and `http://[::1]/…` entries, whose port floats per RFC 8252 §7.3 (native MCP clients bind ephemeral ports) — scheme, host, path, and query still must match exactly. `localhost` is deliberately not treated as loopback (RFC 8252 §8.3). No per-client binding, no prefix matching, no wildcards elsewhere. A flat (client-agnostic) list is safe here because PKCE and the RFC 8707 audience binding neutralise cross-client code-swap attacks.
- **No DCR.** Claude web's "Advanced settings" lets users enter a static client_id. Since this is a public client with PKCE, we don't even validate the client_id — anything non-empty works.
- **Resource indicator (RFC 8707) is required and audience-bound.** The authorize/token endpoints require `resource` to match the issuer; bearer middleware rejects tokens whose `aud` claim doesn't match the canonical resource URL.

## Testing patterns

- Hevy client tests use `httptest.NewServer` and decode the captured request body — see `internal/hevy/client_test.go`. New tests should follow the `newFakeServer(t, status, body)` pattern.
- Tool tests drive the server via `server.HandleMessage(...)` with a JSON-RPC `tools/call` payload — see `internal/tools/register_test.go`. Use the existing `upstream` helper.
- The MCP server's `tools/call` result is `*mcp.CallToolResult` (pointer); `tools/list` is `*mcp.ListToolsResult`. Both helpers in `register_test.go` accept either pointer or value form, defensively.
- When adding tests for wire-shape behavior, decode `u.lastBody` into a typed struct rather than substring-matching — JSON field order is not guaranteed.

## CI / deploy (GitHub Actions)

The workflow lives at `.github/workflows/fly-deploy.yml`. Two jobs:

- **`test`** runs on every push to `main` (and on manual dispatch). Executes `docker compose run --rm test` — same command CLAUDE.md's testing rule specifies. If you change the test invocation, keep the workflow and `docker-compose.yml` `test` service in sync.
- **`deploy`** is `workflow_dispatch`-gated (`if: github.event_name == 'workflow_dispatch'`). Push does not ship — you must click Run workflow. It `needs: test`, verifies the `FLY_API_TOKEN` secret is set (fails fast if not), then runs `flyctl deploy --remote-only`.

Things to remember when touching build/deploy code:

- **The build takes no secrets.** If you ever add a BuildKit secret, wire all three sites (Dockerfile `--mount=type=secret`, workflow `--build-secret` flag, GitHub Actions secret) or the build will silently use empty input.
- **Runtime env vars are separate from build secrets.** Public values (like `OAUTH_ALLOWED_REDIRECT_URIS`) go in `fly.toml`'s `[env]`. Private values (`OAUTH_SIGNING_KEY`, `OAUTH_ENCRYPTION_KEY`) are set via `flyctl secrets set` and are *not* touched by the workflow. Adding a new required runtime secret means updating this list and telling the operator to run `flyctl secrets set` — the workflow won't do it for them.
- **`setup-flyctl` is pinned to a specific tag** (`@1.5`), not `@master` or `@v1`. Bump deliberately, not accidentally.
- **Deploys don't auto-fire.** Any advice about "just push and it'll deploy" is wrong here — the user chose manual gating on purpose. Suggest the Actions UI or `gh workflow run fly-deploy.yml` for deploys, not a git push.

## When in doubt

- **API behavior questions**: the Hevy OpenAPI spec lives at `https://api.hevyapp.com/docs`. It's served as Swagger UI; the JSON spec is embedded in `swagger-ui-init.js` as a `swaggerDoc` object. Brace-match it out, parse, inspect. The user has confirmed the spec is mostly accurate, but POST response shapes drift — trust the raw-bytes pattern over the spec.
- **Don't trust the spec for POST response shapes.** Trust user reports + `DoRaw` round-trips.
- **Probing what Hevy accepts without writing: only where rejection is proven.** On `/v1/routines` and `/v1/workouts`, Hevy validates the whole body, concatenates all complaints into one `error` string, and rejects atomically — so a request carrying a *deliberate* fatal error (`"exercises": []` → `"Array must contain at least 1 element(s)"`) can never write, yet still reports whether the key under test is tolerated. A junk key (`"zzz_bogus": 1`) is the required control: `Unrecognized key(s) in object: 'zzz_bogus'` proves the unrecognized-key check ran in that same response.
  **This technique is endpoint-specific and has already destroyed data.** `PUT /v1/body_measurements/{date}` ignores unknown keys and returns `HTTP 200`, so the "safe" probe was a successful destructive write (2026-10-19). Before probing a write endpoint, establish that it *does* reject unknown keys — on a resource you can afford to lose. Treat any endpoint whose rejection behaviour is unverified as write-unsafe, and **ask the user first**: Hevy has **no DELETE endpoints**, so neither throwaway objects nor mistakes can be cleaned up afterwards.
  Validation-level evidence never settles write *semantics* (does the field actually change anything?) — that needs a real round-trip plus a read-back, on a throwaway resource, with the user's go-ahead.
- **Don't add `omitempty` to anything where the zero value carries meaning** (`false`, `0`, empty string for required fields).
- **Don't merge input and output types.** It will appear to work until Hevy rejects a stray `title` or `index`.
