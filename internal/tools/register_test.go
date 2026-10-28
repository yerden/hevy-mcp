package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	t        *testing.T
	server   *server.MCPServer
	upstream *httptest.Server
	lastReq  *http.Request
	lastBody []byte
	handler  func(w http.ResponseWriter, r *http.Request)
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
	u.reply(200, `{"data":{"id":"u1","name":"alice","url":"https://hevy.com/alice"}}`)

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

// Hevy rejects create/update with `"workout.is_private" is required` if the
// field is missing, so it must always be serialized. When the caller omits
// it, we send the zero value (false = public), matching Hevy's app default.
func TestTools_CreateWorkout_IsPrivateDefaultsToFalseWhenUnset(t *testing.T) {
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

	var sent struct {
		Workout map[string]any `json:"workout"`
	}
	require.NoError(t, json.Unmarshal(u.lastBody, &sent))
	v, present := sent.Workout["is_private"]
	require.True(t, present, "is_private must always be present in the body; body=%s", string(u.lastBody))
	assert.Equal(t, false, v, "is_private must default to false when caller omits it")
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

// The search walks the live catalog at Hevy's 100-per-page ceiling and filters
// server-side; only matches (custom ones included) reach the result.
func TestTools_SearchExerciseTemplates_PagesLiveCatalogAndFilters(t *testing.T) {
	u := newUpstream(t)
	var pages []string
	u.handler = func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Query().Get("page"))
		assert.Equal(t, "/v1/exercise_templates", r.URL.Path)
		assert.Equal(t, "100", r.URL.Query().Get("pageSize"))
		switch r.URL.Query().Get("page") {
		case "1":
			_, _ = w.Write([]byte(`{"page":1,"page_count":2,"exercise_templates":[
				{"id":"AAA","title":"Back Squat (Barbell)","type":"weight_reps","primary_muscle_group":"quadriceps","thumbnail_url":"https://s3/a.jpg"},
				{"id":"CCC","title":"Bench Press (Barbell)","type":"weight_reps","primary_muscle_group":"chest"}]}`))
		default:
			_, _ = w.Write([]byte(`{"page":2,"page_count":2,"exercise_templates":[
				{"id":"c-1","title":"My Goblet Squat","type":"weight_reps","primary_muscle_group":"quadriceps","is_custom":true}]}`))
		}
	}

	r := u.callTool("hevy_search_exercise_templates", map[string]any{"query": "squat"})
	require.False(t, r.IsError, resultText(t, r))
	assert.Equal(t, []string{"1", "2"}, pages)

	var got struct {
		Count             int                     `json:"count"`
		Truncated         bool                    `json:"truncated"`
		ExerciseTemplates []hevy.ExerciseTemplate `json:"exercise_templates"`
	}
	require.NoError(t, json.Unmarshal([]byte(resultText(t, r)), &got))
	assert.Equal(t, 2, got.Count)
	assert.False(t, got.Truncated)
	require.Len(t, got.ExerciseTemplates, 2)
	assert.Equal(t, "AAA", got.ExerciseTemplates[0].ID)
	assert.Empty(t, got.ExerciseTemplates[0].ThumbnailURL, "thumbnails are stripped from search results")
	assert.Equal(t, "c-1", got.ExerciseTemplates[1].ID)
	assert.True(t, got.ExerciseTemplates[1].IsCustom)
}

func TestTools_SearchExerciseTemplates_HevyErrorIsToolError(t *testing.T) {
	u := newUpstream(t)
	u.reply(429, `{"error":"rate limited"}`)

	r := u.callTool("hevy_search_exercise_templates", map[string]any{"query": "squat"})
	assert.True(t, r.IsError)
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
	// Hevy's real response wraps a flat array of per-set entries under
	// `exercise_history`. The tool forwards the raw body verbatim so the
	// model sees Hevy's actual shape without any lossy decode step.
	body := `{"exercise_history":[{"workout_id":"w1","workout_title":"Push","exercise_template_id":"e1","weight_kg":57,"reps":15,"set_type":"normal"}]}`
	u := newUpstream(t)
	u.reply(200, body)

	r := u.callTool("hevy_get_exercise_history", map[string]any{
		"id":         "e1",
		"start_date": "2024-01-01",
		"end_date":   "2024-02-01",
	})
	assert.False(t, r.IsError)
	assert.Equal(t, "/v1/exercise_history/e1", u.lastReq.URL.Path)
	assert.Contains(t, u.lastReq.URL.RawQuery, "start_date=2024-01-01")
	assert.Contains(t, u.lastReq.URL.RawQuery, "end_date=2024-02-01")
	assert.Equal(t, body, resultText(t, r))
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
		"hevy_search_exercise_templates",
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
	assert.Len(t, expected, 23, "22 Hevy endpoints + 1 search tool filtering the template catalog")

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

// toolSchema pulls one tool's published JSON schema out of tools/list.
func toolSchema(t *testing.T, u *upstream, name string) map[string]any {
	t.Helper()
	msg, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	require.NoError(t, err)
	resp := u.server.HandleMessage(context.Background(), msg)
	jr, ok := resp.(mcp.JSONRPCResponse)
	require.True(t, ok, "expected JSONRPCResponse, got %T", resp)

	var tools []mcp.Tool
	switch v := jr.Result.(type) {
	case *mcp.ListToolsResult:
		tools = v.Tools
	case mcp.ListToolsResult:
		tools = v.Tools
	default:
		t.Fatalf("expected ListToolsResult, got %T", jr.Result)
	}
	for _, tl := range tools {
		if tl.Name != name {
			continue
		}
		b, err := json.Marshal(tl)
		require.NoError(t, err)
		var schema map[string]any
		require.NoError(t, json.Unmarshal(b, &schema))
		return schema
	}
	t.Fatalf("tool %s not found", name)
	return nil
}

func pageSizeMax(t *testing.T, u *upstream, tool string) float64 {
	t.Helper()
	schema := toolSchema(t, u, tool)
	props, ok := schema["inputSchema"].(map[string]any)["properties"].(map[string]any)
	require.True(t, ok, "no properties in %s schema", tool)
	ps, ok := props["pageSize"].(map[string]any)
	require.True(t, ok, "no pageSize in %s schema", tool)
	max, ok := ps["maximum"].(float64)
	require.True(t, ok, "no maximum on %s pageSize: %v", tool, ps)
	return max
}

// /v1/exercise_templates allows pageSize up to 100 (every other Hevy list
// endpoint caps at 10), so the templates tool must publish the higher ceiling
// while the rest stay at 10.
func TestTools_ExerciseTemplatesPageSizeCeilingIs100(t *testing.T) {
	u := newUpstream(t)
	assert.Equal(t, float64(100), pageSizeMax(t, u, "hevy_list_exercise_templates"))
	assert.Equal(t, float64(10), pageSizeMax(t, u, "hevy_list_workouts"))
	assert.Equal(t, float64(10), pageSizeMax(t, u, "hevy_list_routines"))
	assert.Equal(t, float64(10), pageSizeMax(t, u, "hevy_list_routine_folders"))
	assert.Equal(t, float64(10), pageSizeMax(t, u, "hevy_list_body_measurements"))
}

func TestTools_ListExerciseTemplates_ForwardsLargePageSize(t *testing.T) {
	u := newUpstream(t)
	u.reply(200, `{"page":1,"page_count":5,"exercise_templates":[]}`)

	r := u.callTool("hevy_list_exercise_templates", map[string]any{"page": 1, "pageSize": 100})
	assert.False(t, r.IsError)
	assert.Equal(t, "/v1/exercise_templates", u.lastReq.URL.Path)
	assert.Equal(t, "page=1&pageSize=100", u.lastReq.URL.RawQuery)
}

// Reads expose `equipment` and `thumbnail_url`; they must reach the model
// rather than being dropped by our response struct.
func TestTools_ListExerciseTemplates_KeepsEquipmentAndThumbnail(t *testing.T) {
	u := newUpstream(t)
	u.reply(200, `{"page":1,"page_count":1,"exercise_templates":[{"id":"e1","title":"Bench Press (Barbell)","type":"weight_reps","primary_muscle_group":"chest","equipment":"barbell","thumbnail_url":"https://example.invalid/bench.jpg"}]}`)

	r := u.callTool("hevy_list_exercise_templates", nil)
	assert.False(t, r.IsError)

	var got hevy.PaginatedExerciseTemplates
	require.NoError(t, json.Unmarshal([]byte(resultText(t, r)), &got))
	require.Len(t, got.ExerciseTemplates, 1)
	assert.Equal(t, hevy.EquipmentCategory("barbell"), got.ExerciseTemplates[0].Equipment)
	assert.Equal(t, "https://example.invalid/bench.jpg", got.ExerciseTemplates[0].ThumbnailURL)
}

// The user info tool must surface the full response, not just id/name/url.
func TestTools_GetUserInfo_IncludesUnitsAndProFlag(t *testing.T) {
	u := newUpstream(t)
	u.reply(200, `{"data":{"id":"u1","username":"alice","name":"Alice","url":"https://hevy.com/alice","weight_unit":"kg","distance_unit":"kilometers","is_pro":true}}`)

	r := u.callTool("hevy_get_user_info", nil)
	assert.False(t, r.IsError)

	var got hevy.UserInfo
	require.NoError(t, json.Unmarshal([]byte(resultText(t, r)), &got))
	assert.Equal(t, "alice", got.Username)
	assert.Equal(t, "kg", got.WeightUnit)
	assert.Equal(t, "kilometers", got.DistanceUnit)
	assert.True(t, got.IsPro)
}

// routineBody decodes the `routine` sub-object of the last captured PUT body.
func routineBody(t *testing.T, u *upstream) map[string]any {
	t.Helper()
	var body struct {
		Routine map[string]any `json:"routine"`
	}
	require.NoError(t, json.Unmarshal(u.lastBody, &body), "body: %s", u.lastBody)
	require.NotNil(t, body.Routine, "no routine object in body: %s", u.lastBody)
	return body.Routine
}

func updateRoutineArgsFor(extra map[string]any) map[string]any {
	routine := map[string]any{
		"title": "PPL",
		"exercises": []any{map[string]any{
			"exercise_template_id": "e1",
			"sets":                 []any{map[string]any{"type": "normal", "reps": 10}},
		}},
	}
	for k, v := range extra {
		routine[k] = v
	}
	return map[string]any{"id": "r1", "routine": routine}
}

// Default behaviour: an update that says nothing about folders must not send
// folder_id at all, which is how Hevy preserves the routine's placement.
func TestTools_UpdateRoutine_OmitsFolderIDByDefault(t *testing.T) {
	u := newUpstream(t)
	u.reply(200, `{"routine":{"id":"r1"}}`)

	r := u.callTool("hevy_update_routine", updateRoutineArgsFor(nil))
	assert.False(t, r.IsError)
	assert.Equal(t, "PUT", u.lastReq.Method)
	assert.Equal(t, "/v1/routines/r1", u.lastReq.URL.Path)
	_, present := routineBody(t, u)["folder_id"]
	assert.False(t, present, "folder_id must be absent so Hevy keeps the current folder")
}

// folder_id moves the routine into that folder (Hevy accepts this on PUT as of
// 2026-10-19; it used to 400).
func TestTools_UpdateRoutine_ForwardsFolderID(t *testing.T) {
	u := newUpstream(t)
	u.reply(200, `{"routine":{"id":"r1"}}`)

	r := u.callTool("hevy_update_routine", updateRoutineArgsFor(map[string]any{"folder_id": 3101367}))
	assert.False(t, r.IsError)
	assert.Equal(t, float64(3101367), routineBody(t, u)["folder_id"])
}

// move_to_my_routines must reach Hevy as an explicit null — the only way to
// move a routine out of a folder.
func TestTools_UpdateRoutine_MoveToMyRoutinesSendsNull(t *testing.T) {
	u := newUpstream(t)
	u.reply(200, `{"routine":{"id":"r1"}}`)

	r := u.callTool("hevy_update_routine", updateRoutineArgsFor(map[string]any{"move_to_my_routines": true}))
	assert.False(t, r.IsError)
	body := routineBody(t, u)
	val, present := body["folder_id"]
	assert.True(t, present, "folder_id must be present as null, got body %s", u.lastBody)
	assert.Nil(t, val)
	_, leaked := body["move_to_my_routines"]
	assert.False(t, leaked, "move_to_my_routines is a tool-level flag; Hevy rejects unrecognized keys")
}

// The two placement fields contradict each other; fail before touching Hevy
// rather than guessing which one the model meant.
func TestTools_UpdateRoutine_RejectsConflictingPlacement(t *testing.T) {
	u := newUpstream(t)

	r := u.callTool("hevy_update_routine", updateRoutineArgsFor(map[string]any{
		"folder_id":           3101367,
		"move_to_my_routines": true,
	}))
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(t, r), "mutually exclusive")
	assert.Nil(t, u.lastReq, "no request should reach Hevy")
}

// The model can only use the placement fields if the published schema shows
// them.
func TestTools_UpdateRoutine_SchemaPublishesPlacementFields(t *testing.T) {
	u := newUpstream(t)
	schema := toolSchema(t, u, "hevy_update_routine")
	props, ok := schema["inputSchema"].(map[string]any)["properties"].(map[string]any)["routine"].(map[string]any)["properties"].(map[string]any)
	require.True(t, ok, "routine properties not found: %v", schema)
	assert.Contains(t, props, "folder_id")
	assert.Contains(t, props, "move_to_my_routines")
}

// Regression test for a real data loss: Hevy's PUT /v1/body_measurements/{date}
// replaces the whole row and ignores unknown keys, so a partial update used to
// silently wipe every metric the caller didn't mention. The tool must now
// read-modify-write.
func TestTools_UpdateBodyMeasurement_MergesInsteadOfClearing(t *testing.T) {
	u := newUpstream(t)
	var methods []string
	u.handler = func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"id":73949311,"date":"2026-10-16","weight_kg":75.61,"lean_mass_kg":64.65,"fat_percent":14.5,"created_at":"2026-10-16T01:51:40.082Z"}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}

	r := u.callTool("hevy_update_body_measurement", map[string]any{
		"date": "2026-10-16",
		"measurement": map[string]any{
			"date":      "2026-10-16",
			"weight_kg": 76.2,
		},
	})
	assert.False(t, r.IsError)
	assert.Equal(t, []string{"GET", "PUT"}, methods, "must read the row before replacing it")
	assert.Equal(t, "/v1/body_measurements/2026-10-16", u.lastReq.URL.Path)

	var put map[string]any
	require.NoError(t, json.Unmarshal(u.lastBody, &put), "body: %s", u.lastBody)
	assert.Equal(t, 76.2, put["weight_kg"], "the caller's value must win")
	assert.Equal(t, 64.65, put["lean_mass_kg"], "unmentioned metric must survive the update")
	assert.Equal(t, 14.5, put["fat_percent"], "unmentioned metric must survive the update")
	assert.NotContains(t, put, "id", "server-owned field must not be echoed back")
	assert.NotContains(t, put, "created_at", "server-owned field must not be echoed back")
}

// Nothing to merge into means nothing to write — a blind PUT here would create
// the illusion of an update while Hevy 404s, or worse.
func TestTools_UpdateBodyMeasurement_MissingDateDoesNotWrite(t *testing.T) {
	u := newUpstream(t)
	var methods []string
	u.handler = func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Body measurement not found"}`))
	}

	r := u.callTool("hevy_update_body_measurement", map[string]any{
		"date":        "1990-01-01",
		"measurement": map[string]any{"date": "1990-01-01", "weight_kg": 70},
	})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(t, r), "hevy_create_body_measurement")
	assert.Equal(t, []string{"GET"}, methods, "must not PUT when the row doesn't exist")
}

// The is_private hazard only reaches the model if the generated schema carries
// its description: an update always sends the field, so a private workout
// silently goes public unless the caller sets it, and Hevy's reads don't
// return the current value for us to preserve.
func TestTools_UpdateWorkout_SchemaWarnsAboutIsPrivate(t *testing.T) {
	u := newUpstream(t)
	schema := toolSchema(t, u, "hevy_update_workout")
	props, ok := schema["inputSchema"].(map[string]any)["properties"].(map[string]any)["workout"].(map[string]any)["properties"].(map[string]any)
	require.True(t, ok, "workout properties not found: %v", schema)
	isPrivate, ok := props["is_private"].(map[string]any)
	require.True(t, ok, "is_private not published: %v", props)
	desc, _ := isPrivate["description"].(string)
	assert.Contains(t, desc, "keep a private workout private", "is_private must carry its warning")
}

// mcp.NewTool defaults to readOnlyHint=false, destructiveHint=true, which made
// claude.ai list every tool — reads included — as "write/delete". Each tool
// must state its kind explicitly.
func TestTools_AnnotationsMatchToolKind(t *testing.T) {
	u := newUpstream(t)

	msg, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	require.NoError(t, err)
	jr, ok := u.server.HandleMessage(context.Background(), msg).(mcp.JSONRPCResponse)
	require.True(t, ok)
	var tools []mcp.Tool
	switch v := jr.Result.(type) {
	case *mcp.ListToolsResult:
		tools = v.Tools
	case mcp.ListToolsResult:
		tools = v.Tools
	default:
		t.Fatalf("expected ListToolsResult, got %T", jr.Result)
	}
	require.Len(t, tools, 23)

	for _, tl := range tools {
		a := tl.Annotations
		require.NotNil(t, a.ReadOnlyHint, tl.Name)
		require.NotNil(t, a.DestructiveHint, tl.Name)
		switch {
		case strings.HasPrefix(tl.Name, "hevy_get_"),
			strings.HasPrefix(tl.Name, "hevy_list_"),
			strings.HasPrefix(tl.Name, "hevy_search_"):
			assert.True(t, *a.ReadOnlyHint, "%s should be read-only", tl.Name)
			assert.False(t, *a.DestructiveHint, "%s should not be destructive", tl.Name)
		case strings.HasPrefix(tl.Name, "hevy_create_"):
			assert.False(t, *a.ReadOnlyHint, "%s writes", tl.Name)
			assert.False(t, *a.DestructiveHint, "%s is additive", tl.Name)
		case strings.HasPrefix(tl.Name, "hevy_update_"):
			assert.False(t, *a.ReadOnlyHint, "%s writes", tl.Name)
			assert.True(t, *a.DestructiveHint, "%s overwrites stored values", tl.Name)
		default:
			t.Errorf("tool %s has no expected annotation kind; extend this test", tl.Name)
		}
	}
}
