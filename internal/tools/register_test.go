package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yerden/hevy-mcp/internal/hevy"
)

// upstream pairs the registered MCP server with an httptest server standing in
// for Hevy. Reused across all tool tests.
type upstream struct {
	t         *testing.T
	server    *server.MCPServer
	upstream  *httptest.Server
	lastReq   *http.Request
	lastBody  []byte
	handler   func(w http.ResponseWriter, r *http.Request)
}

func newUpstream(t *testing.T) *upstream {
	t.Helper()
	u := &upstream{t: t}
	u.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.lastReq = r
		buf := make([]byte, r.ContentLength)
		if r.ContentLength > 0 {
			_, _ = r.Body.Read(buf)
		}
		u.lastBody = buf
		if u.handler != nil {
			u.handler(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(u.upstream.Close)

	hc := hevy.New("test-key", u.upstream.URL)
	u.server = server.NewMCPServer("hevy-mcp-test", "0.0.0", server.WithToolCapabilities(true))
	RegisterAll(u.server, StaticFactory(hc))
	return u
}

// reply sets the next upstream response.
func (u *upstream) reply(status int, body string) {
	u.handler = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

// callTool issues a JSON-RPC tools/call against the server and returns the
// decoded tool result. fails the test on transport-level errors.
func (u *upstream) callTool(name string, args map[string]any) *mcp.CallToolResult {
	u.t.Helper()
	params := map[string]any{"name": name}
	if args != nil {
		params["arguments"] = args
	}
	msg, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  params,
	})
	require.NoError(u.t, err)

	resp := u.server.HandleMessage(context.Background(), msg)
	require.NotNil(u.t, resp)

	jr, ok := resp.(mcp.JSONRPCResponse)
	require.True(u.t, ok, "expected JSONRPCResponse, got %T", resp)
	switch v := jr.Result.(type) {
	case *mcp.CallToolResult:
		return v
	case mcp.CallToolResult:
		return &v
	default:
		u.t.Fatalf("expected CallToolResult, got %T", jr.Result)
		return nil
	}
}

// resultText pulls the first text content payload out of a tool result.
func resultText(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	require.Len(t, r.Content, 1, "expected exactly one content block")
	tc, ok := r.Content[0].(mcp.TextContent)
	require.True(t, ok, "expected TextContent, got %T", r.Content[0])
	return tc.Text
}

func TestTools_ListWorkouts(t *testing.T) {
	u := newUpstream(t)
	u.reply(200, `{"page":2,"page_count":3,"workouts":[{"id":"w1","title":"Pull","start_time":"","end_time":"","exercises":[]}]}`)

	r := u.callTool("hevy_list_workouts", map[string]any{"page": 2, "pageSize": 5})
	assert.False(t, r.IsError)
	text := resultText(t, r)

	var got hevy.PaginatedWorkouts
	require.NoError(t, json.Unmarshal([]byte(text), &got))
	assert.Equal(t, 2, got.Page)
	assert.Equal(t, "/v1/workouts", u.lastReq.URL.Path)
	assert.Equal(t, "page=2&pageSize=5", u.lastReq.URL.RawQuery)
}

func TestTools_ListWorkoutsDefaults(t *testing.T) {
	u := newUpstream(t)
	u.reply(200, `{"page":1,"page_count":1,"workouts":[]}`)

	_ = u.callTool("hevy_list_workouts", nil)
	assert.Equal(t, "page=1&pageSize=10", u.lastReq.URL.RawQuery)
}

func TestTools_GetUserInfo(t *testing.T) {
	u := newUpstream(t)
	u.reply(200, `{"id":"u1","name":"alice","url":"https://hevy.com/alice"}`)

	r := u.callTool("hevy_get_user_info", nil)
	assert.False(t, r.IsError)
	text := resultText(t, r)
	assert.Contains(t, text, `"name":"alice"`)
	assert.Equal(t, "/v1/user/info", u.lastReq.URL.Path)
}

func TestTools_GetWorkout(t *testing.T) {
	u := newUpstream(t)
	u.reply(200, `{"id":"w1","title":"x","start_time":"","end_time":"","exercises":[]}`)

	r := u.callTool("hevy_get_workout", map[string]any{"id": "w1"})
	assert.False(t, r.IsError)
	assert.Equal(t, "/v1/workouts/w1", u.lastReq.URL.Path)
	assert.Contains(t, resultText(t, r), `"id":"w1"`)
}

func TestTools_GetWorkoutMissingID(t *testing.T) {
	u := newUpstream(t)
	r := u.callTool("hevy_get_workout", map[string]any{})
	assert.True(t, r.IsError, "missing required arg should return tool error")
	assert.Contains(t, resultText(t, r), "id")
}

func TestTools_GetWorkoutCount(t *testing.T) {
	u := newUpstream(t)
	u.reply(200, `{"workout_count":7}`)

	r := u.callTool("hevy_get_workout_count", nil)
	assert.False(t, r.IsError)
	assert.Contains(t, resultText(t, r), `"workout_count":7`)
}

func TestTools_GetWorkoutEvents(t *testing.T) {
	u := newUpstream(t)
	u.reply(200, `{"page":1,"page_count":1,"events":[]}`)

	r := u.callTool("hevy_get_workout_events", map[string]any{"since": "2024-01-01T00:00:00Z"})
	assert.False(t, r.IsError)
	assert.Equal(t, "/v1/workouts/events", u.lastReq.URL.Path)
	assert.Contains(t, u.lastReq.URL.RawQuery, "since=2024-01-01T00%3A00%3A00Z")
}

func TestTools_CreateWorkout(t *testing.T) {
	u := newUpstream(t)
	u.reply(201, `{"id":"new-w","title":"Day 1","start_time":"","end_time":"","exercises":[]}`)

	args := map[string]any{
		"workout": map[string]any{
			"title":      "Day 1",
			"start_time": "2024-01-01T10:00:00Z",
			"end_time":   "2024-01-01T11:00:00Z",
			"exercises":  []any{},
		},
	}
	r := u.callTool("hevy_create_workout", args)
	assert.False(t, r.IsError)
	assert.Equal(t, "POST", u.lastReq.Method)

	var sent hevy.CreateWorkoutRequest
	require.NoError(t, json.Unmarshal(u.lastBody, &sent))
	assert.Equal(t, "Day 1", sent.Workout.Title)
}

// Regression: is_private:false used to be silently dropped because the field
// was `bool` with json:"omitempty". With *bool, an explicit false survives the
// round-trip; an unset value is still omitted.
func TestTools_CreateWorkout_IsPrivateFalseSurvives(t *testing.T) {
	u := newUpstream(t)
	u.reply(201, ``)

	args := map[string]any{
		"workout": map[string]any{
			"title":      "Public",
			"start_time": "2024-01-01T10:00:00Z",
			"end_time":   "2024-01-01T11:00:00Z",
			"is_private": false,
			"exercises":  []any{},
		},
	}
	r := u.callTool("hevy_create_workout", args)
	assert.False(t, r.IsError)

	var sent struct {
		Workout map[string]any `json:"workout"`
	}
	require.NoError(t, json.Unmarshal(u.lastBody, &sent))
	v, present := sent.Workout["is_private"]
	require.True(t, present, "is_private must be in the body when explicitly false; body=%s", string(u.lastBody))
	assert.Equal(t, false, v, "is_private must travel through as false")
}

func TestTools_CreateWorkout_IsPrivateTrueSurvives(t *testing.T) {
	u := newUpstream(t)
	u.reply(201, ``)

	args := map[string]any{
		"workout": map[string]any{
			"title":      "Private",
			"start_time": "2024-01-01T10:00:00Z",
			"end_time":   "2024-01-01T11:00:00Z",
			"is_private": true,
			"exercises":  []any{},
		},
	}
	r := u.callTool("hevy_create_workout", args)
	assert.False(t, r.IsError)
	assert.Contains(t, string(u.lastBody), `"is_private":true`)
}

// When the LLM omits is_private entirely, the body should also omit it (so
// Hevy uses its default rather than us picking one).
func TestTools_CreateWorkout_IsPrivateAbsentWhenUnset(t *testing.T) {
	u := newUpstream(t)
	u.reply(201, ``)

	args := map[string]any{
		"workout": map[string]any{
			"title":      "Default",
			"start_time": "2024-01-01T10:00:00Z",
			"end_time":   "2024-01-01T11:00:00Z",
			"exercises":  []any{},
		},
	}
	r := u.callTool("hevy_create_workout", args)
	assert.False(t, r.IsError)
	assert.NotContains(t, string(u.lastBody), `"is_private"`, "must omit is_private when caller didn't set it; body=%s", string(u.lastBody))
}

func TestTools_UpdateWorkout(t *testing.T) {
	u := newUpstream(t)
	u.reply(200, `{"id":"w1","title":"Updated","start_time":"","end_time":"","exercises":[]}`)

	args := map[string]any{
		"id": "w1",
		"workout": map[string]any{
			"title":     "Updated",
			"exercises": []any{},
		},
	}
	r := u.callTool("hevy_update_workout", args)
	assert.False(t, r.IsError)
	assert.Equal(t, "PUT", u.lastReq.Method)
	assert.Equal(t, "/v1/workouts/w1", u.lastReq.URL.Path)
}

func TestTools_ListRoutines(t *testing.T) {
	u := newUpstream(t)
	u.reply(200, `{"page":1,"page_count":1,"routines":[]}`)

	r := u.callTool("hevy_list_routines", map[string]any{"page": 1, "pageSize": 5})
	assert.False(t, r.IsError)
	assert.Equal(t, "/v1/routines", u.lastReq.URL.Path)
}

func TestTools_CreateRoutine(t *testing.T) {
	u := newUpstream(t)
	u.reply(201, `{"id":"r-new","title":"PPL","exercises":[]}`)

	r := u.callTool("hevy_create_routine", map[string]any{
		"routine": map[string]any{"title": "PPL", "exercises": []any{}},
	})
	assert.False(t, r.IsError)
	assert.Contains(t, string(u.lastBody), `"title":"PPL"`)
}

// When Hevy returns an empty body on a successful create, the tool surfaces
// a clear status note rather than a misleading zero-value object.
func TestTools_CreateRoutine_EmptyResponseSurfacesNote(t *testing.T) {
	u := newUpstream(t)
	u.reply(201, ``) // empty body, common in practice

	r := u.callTool("hevy_create_routine", map[string]any{
		"routine": map[string]any{"title": "PPL", "exercises": []any{}},
	})
	assert.False(t, r.IsError)
	text := resultText(t, r)
	assert.Contains(t, text, "created")
	assert.Contains(t, text, "Hevy returned an empty response body")
}

// When Hevy returns a non-empty body (wrapped or bare), forward it verbatim.
func TestTools_CreateRoutine_ForwardsRawBody(t *testing.T) {
	u := newUpstream(t)
	body := `{"routine":{"id":"r-1","title":"PPL","folder_id":42,"exercises":[]}}`
	u.reply(201, body)

	r := u.callTool("hevy_create_routine", map[string]any{
		"routine": map[string]any{"title": "PPL", "exercises": []any{}},
	})
	assert.False(t, r.IsError)
	assert.Equal(t, body, resultText(t, r))
}

func TestTools_CreateRoutineFolder_ForwardsRawBody(t *testing.T) {
	u := newUpstream(t)
	body := `{"routine_folder":{"id":3081154,"title":"Cardio"}}`
	u.reply(201, body)

	r := u.callTool("hevy_create_routine_folder", map[string]any{"title": "Cardio"})
	assert.False(t, r.IsError)
	assert.Equal(t, body, resultText(t, r))
}

// Bug #2: the published schema for hevy_create_routine must not list `title`
// inside the exercises array. Title is an output-only field that Hevy rejects
// on input.
func TestTools_CreateRoutine_SchemaOmitsTitleOnExercises(t *testing.T) {
	u := newUpstream(t)
	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	resp := u.server.HandleMessage(context.Background(), msg)
	jr := resp.(mcp.JSONRPCResponse)

	var tools []mcp.Tool
	switch v := jr.Result.(type) {
	case *mcp.ListToolsResult:
		tools = v.Tools
	case mcp.ListToolsResult:
		tools = v.Tools
	}
	var schema map[string]any
	for _, tl := range tools {
		if tl.Name == "hevy_create_routine" {
			b, _ := json.Marshal(tl)
			require.NoError(t, json.Unmarshal(b, &schema))
			break
		}
	}
	require.NotNil(t, schema, "create_routine tool not found")

	// Walk: inputSchema.properties.routine.properties.exercises.items.properties
	dig := func(keys ...string) map[string]any {
		var cur any = schema
		for _, k := range keys {
			m, ok := cur.(map[string]any)
			require.True(t, ok, "expected object at %s", k)
			cur = m[k]
		}
		out, _ := cur.(map[string]any)
		return out
	}
	exerciseProps := dig("inputSchema", "properties", "routine", "properties", "exercises", "items", "properties")
	require.NotNil(t, exerciseProps, "exercises[].properties not found in schema")
	_, hasTitle := exerciseProps["title"]
	assert.False(t, hasTitle, "schema for exercises[] must NOT advertise `title` (Hevy rejects it). props=%v", exerciseProps)
	_, hasIndex := exerciseProps["index"]
	assert.False(t, hasIndex, "schema for exercises[] must NOT advertise `index`. props=%v", exerciseProps)
	// Sanity: it should still advertise fields Hevy accepts.
	_, hasTemplateID := exerciseProps["exercise_template_id"]
	assert.True(t, hasTemplateID, "exercise_template_id should be advertised. props=%v", exerciseProps)
}

// Regression: folder_id must round-trip from tool args to the upstream body.
func TestTools_CreateRoutine_PropagatesFolderID(t *testing.T) {
	u := newUpstream(t)
	u.reply(201, `{"id":"r","title":"PPL","exercises":[]}`)

	r := u.callTool("hevy_create_routine", map[string]any{
		"routine": map[string]any{
			"title":     "PPL",
			"folder_id": 5,
			"exercises": []any{},
		},
	})
	assert.False(t, r.IsError, "tool error: %s", resultText(t, r))
	assert.Contains(t, string(u.lastBody), `"folder_id":5`, "body=%s", string(u.lastBody))
}

// Regression: Hevy rejects routine payloads that include `index` on sets or
// exercises. The struct must omit it unless the caller set it explicitly.
func TestTools_CreateRoutine_OmitsIndexWhenAbsent(t *testing.T) {
	u := newUpstream(t)
	u.reply(201, `{"id":"r-new","title":"PPL","exercises":[]}`)

	r := u.callTool("hevy_create_routine", map[string]any{
		"routine": map[string]any{
			"title": "PPL",
			"exercises": []any{
				map[string]any{
					"exercise_template_id": "tmpl-1",
					"sets": []any{
						map[string]any{"type": "normal", "reps": 8, "weight_kg": 60.0},
					},
				},
			},
		},
	})
	assert.False(t, r.IsError)
	body := string(u.lastBody)
	assert.NotContains(t, body, `"index"`, "must not inject index field; body=%s", body)
}

func TestTools_GetRoutine(t *testing.T) {
	u := newUpstream(t)
	u.reply(200, `{"id":"r1","title":"PPL","exercises":[]}`)

	r := u.callTool("hevy_get_routine", map[string]any{"id": "r1"})
	assert.False(t, r.IsError)
	assert.Equal(t, "/v1/routines/r1", u.lastReq.URL.Path)
}

func TestTools_UpdateRoutine(t *testing.T) {
	u := newUpstream(t)
	u.reply(200, `{"id":"r1","title":"x","exercises":[]}`)

	r := u.callTool("hevy_update_routine", map[string]any{
		"id":      "r1",
		"routine": map[string]any{"title": "x", "exercises": []any{}},
	})
	assert.False(t, r.IsError)
	assert.Equal(t, "PUT", u.lastReq.Method)
}

func TestTools_ListExerciseTemplates(t *testing.T) {
	u := newUpstream(t)
	u.reply(200, `{"page":1,"page_count":1,"exercise_templates":[]}`)

	r := u.callTool("hevy_list_exercise_templates", nil)
	assert.False(t, r.IsError)
	assert.Equal(t, "/v1/exercise_templates", u.lastReq.URL.Path)
}

func TestTools_CreateExerciseTemplate(t *testing.T) {
	u := newUpstream(t)
	u.reply(201, `{"id":"e-new"}`)

	r := u.callTool("hevy_create_exercise_template", map[string]any{
		"exercise": map[string]any{
			"title":              "Custom",
			"exercise_type":      "weight_reps",
			"equipment_category": "barbell",
			"muscle_group":       "quadriceps",
		},
	})
	assert.False(t, r.IsError)
	assert.Contains(t, resultText(t, r), `"id":"e-new"`)
}

func TestTools_GetExerciseTemplate(t *testing.T) {
	u := newUpstream(t)
	u.reply(200, `{"id":"e1","title":"Squat","type":"weight_reps","primary_muscle_group":"quadriceps"}`)

	r := u.callTool("hevy_get_exercise_template", map[string]any{"id": "e1"})
	assert.False(t, r.IsError)
	assert.Equal(t, "/v1/exercise_templates/e1", u.lastReq.URL.Path)
}

func TestTools_ListRoutineFolders(t *testing.T) {
	u := newUpstream(t)
	u.reply(200, `{"page":1,"page_count":1,"routine_folders":[]}`)

	r := u.callTool("hevy_list_routine_folders", nil)
	assert.False(t, r.IsError)
	assert.Equal(t, "/v1/routine_folders", u.lastReq.URL.Path)
}

func TestTools_CreateRoutineFolder(t *testing.T) {
	u := newUpstream(t)
	// Real Hevy responses wrap under `routine_folder`; the unwrap helper
	// surfaces the inner object so the tool sees a populated ID/title.
	u.reply(201, `{"routine_folder":{"id":5,"title":"Main"}}`)

	r := u.callTool("hevy_create_routine_folder", map[string]any{"title": "Main"})
	assert.False(t, r.IsError)
	assert.Contains(t, string(u.lastBody), `"title":"Main"`)
	assert.Contains(t, resultText(t, r), `"id":5`)
}

func TestTools_GetRoutineFolder(t *testing.T) {
	u := newUpstream(t)
	u.reply(200, `{"id":3,"title":"Cardio"}`)

	r := u.callTool("hevy_get_routine_folder", map[string]any{"id": 3})
	assert.False(t, r.IsError)
	assert.Equal(t, "/v1/routine_folders/3", u.lastReq.URL.Path)
}

func TestTools_GetExerciseHistory(t *testing.T) {
	u := newUpstream(t)
	u.reply(200, `{"exercise_template_id":"e1","history":[]}`)

	r := u.callTool("hevy_get_exercise_history", map[string]any{
		"id":         "e1",
		"start_date": "2024-01-01",
		"end_date":   "2024-02-01",
	})
	assert.False(t, r.IsError)
	assert.Equal(t, "/v1/exercise_history/e1", u.lastReq.URL.Path)
	assert.Contains(t, u.lastReq.URL.RawQuery, "start_date=2024-01-01")
}

func TestTools_ListBodyMeasurements(t *testing.T) {
	u := newUpstream(t)
	u.reply(200, `{"page":1,"page_count":1,"body_measurements":[]}`)

	r := u.callTool("hevy_list_body_measurements", nil)
	assert.False(t, r.IsError)
	assert.Equal(t, "/v1/body_measurements", u.lastReq.URL.Path)
}

func TestTools_CreateBodyMeasurement(t *testing.T) {
	u := newUpstream(t)
	u.reply(201, ``)

	r := u.callTool("hevy_create_body_measurement", map[string]any{
		"measurement": map[string]any{"date": "2024-01-01", "weight_kg": 80.0},
	})
	assert.False(t, r.IsError)
	assert.Contains(t, string(u.lastBody), `"date":"2024-01-01"`)
}

func TestTools_GetBodyMeasurement(t *testing.T) {
	u := newUpstream(t)
	u.reply(200, `{"date":"2024-01-01"}`)

	r := u.callTool("hevy_get_body_measurement", map[string]any{"date": "2024-01-01"})
	assert.False(t, r.IsError)
	assert.Equal(t, "/v1/body_measurements/2024-01-01", u.lastReq.URL.Path)
}

func TestTools_UpdateBodyMeasurement(t *testing.T) {
	u := newUpstream(t)
	u.reply(200, ``)

	r := u.callTool("hevy_update_body_measurement", map[string]any{
		"date":        "2024-01-01",
		"measurement": map[string]any{"date": "2024-01-01"},
	})
	assert.False(t, r.IsError)
	assert.Equal(t, "PUT", u.lastReq.Method)
}

func TestTools_APIErrorSurfacedAsToolError(t *testing.T) {
	u := newUpstream(t)
	u.reply(401, `unauthorized`)

	r := u.callTool("hevy_get_user_info", nil)
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(t, r), "401")
}

// The LLM only knows what's in the tool schema. Verify that the published
// schema for the routine-create tool advertises folder_id (and a few other
// critical fields) so a model that follows the schema will populate them.
func TestTools_CreateRoutine_SchemaAdvertisesFields(t *testing.T) {
	u := newUpstream(t)
	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	})
	resp := u.server.HandleMessage(context.Background(), msg)
	jr := resp.(mcp.JSONRPCResponse)

	var tools []mcp.Tool
	switch v := jr.Result.(type) {
	case *mcp.ListToolsResult:
		tools = v.Tools
	case mcp.ListToolsResult:
		tools = v.Tools
	}
	var schema string
	for _, tl := range tools {
		if tl.Name == "hevy_create_routine" {
			b, _ := json.Marshal(tl)
			schema = string(b)
			break
		}
	}
	require.NotEmpty(t, schema, "create_routine tool not found")
	for _, field := range []string{"folder_id", "title", "exercises", "exercise_template_id", "weight_kg", "reps"} {
		assert.Contains(t, schema, field, "schema should advertise %q", field)
	}
}

func TestTools_AllRegistered(t *testing.T) {
	u := newUpstream(t)
	u.reply(200, `{"page":1,"page_count":1,"workouts":[]}`)

	expected := []string{
		"hevy_list_workouts",
		"hevy_create_workout",
		"hevy_get_workout",
		"hevy_update_workout",
		"hevy_get_workout_count",
		"hevy_get_workout_events",
		"hevy_list_routines",
		"hevy_create_routine",
		"hevy_get_routine",
		"hevy_update_routine",
		"hevy_list_exercise_templates",
		"hevy_create_exercise_template",
		"hevy_get_exercise_template",
		"hevy_list_routine_folders",
		"hevy_create_routine_folder",
		"hevy_get_routine_folder",
		"hevy_get_exercise_history",
		"hevy_list_body_measurements",
		"hevy_create_body_measurement",
		"hevy_get_body_measurement",
		"hevy_update_body_measurement",
		"hevy_get_user_info",
	}
	assert.Len(t, expected, 22, "should match plan total of 22 tools (21 endpoints + user info already counted)")

	// Issue tools/list to verify each tool is present.
	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	})
	resp := u.server.HandleMessage(context.Background(), msg)
	jr, ok := resp.(mcp.JSONRPCResponse)
	require.True(t, ok)
	var listResult mcp.ListToolsResult
	switch v := jr.Result.(type) {
	case *mcp.ListToolsResult:
		listResult = *v
	case mcp.ListToolsResult:
		listResult = v
	default:
		t.Fatalf("expected ListToolsResult, got %T", jr.Result)
	}

	got := make(map[string]bool)
	for _, tool := range listResult.Tools {
		got[tool.Name] = true
	}
	for _, name := range expected {
		assert.True(t, got[name], "tool %s should be registered", name)
	}
}
