package hevy

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeServer spins up an httptest server that records the last request and
// replies with the supplied status and body.
type fakeServer struct {
	t           *testing.T
	server      *httptest.Server
	lastMethod  string
	lastPath    string
	lastQuery   string
	lastAPIKey  string
	lastBody    []byte
	respStatus  int
	respBody    string
	respHeaders map[string]string
}

func newFakeServer(t *testing.T, status int, body string) *fakeServer {
	t.Helper()
	fs := &fakeServer{t: t, respStatus: status, respBody: body}
	fs.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.lastMethod = r.Method
		fs.lastPath = r.URL.Path
		fs.lastQuery = r.URL.RawQuery
		fs.lastAPIKey = r.Header.Get("api-key")
		buf, _ := io.ReadAll(r.Body)
		fs.lastBody = buf
		for k, v := range fs.respHeaders {
			w.Header().Set(k, v)
		}
		w.WriteHeader(fs.respStatus)
		_, _ = io.WriteString(w, fs.respBody)
	}))
	t.Cleanup(fs.server.Close)
	return fs
}

func (fs *fakeServer) client() *Client {
	return New("test-key", fs.server.URL)
}

func TestClient_SendsAPIKeyHeader(t *testing.T) {
	fs := newFakeServer(t, 200, `{"data":{"id":"u1","name":"alice","url":"https://hevy.com/alice"}}`)
	c := fs.client()

	_, err := c.GetUserInfo()
	require.NoError(t, err)
	assert.Equal(t, "test-key", fs.lastAPIKey)
	assert.Equal(t, "/v1/user/info", fs.lastPath)
	assert.Equal(t, "GET", fs.lastMethod)
}

// Hevy returns user info wrapped under `data` per its OpenAPI spec.
func TestClient_GetUserInfo_UnwrapsDataEnvelope(t *testing.T) {
	fs := newFakeServer(t, 200, `{"data":{"id":"u1","name":"alice","url":"https://hevy.com/alice"}}`)
	got, err := fs.client().GetUserInfo()
	require.NoError(t, err)
	assert.Equal(t, &UserInfo{ID: "u1", Name: "alice", URL: "https://hevy.com/alice"}, got)
}

// Defensive: if Hevy ever returns the bare object, still decode it.
func TestClient_GetUserInfo_BareResponseStillWorks(t *testing.T) {
	fs := newFakeServer(t, 200, `{"id":"u1","name":"alice","url":"https://hevy.com/alice"}`)
	got, err := fs.client().GetUserInfo()
	require.NoError(t, err)
	assert.Equal(t, "u1", got.ID)
	assert.Equal(t, "alice", got.Name)
}

func TestClient_APIError401(t *testing.T) {
	fs := newFakeServer(t, 401, `unauthorized`)
	_, err := fs.client().GetUserInfo()
	require.Error(t, err)
	var ae *APIError
	require.True(t, errors.As(err, &ae), "expected *APIError, got %T", err)
	assert.Equal(t, 401, ae.StatusCode)
	assert.Contains(t, ae.Message, "unauthorized")
}

func TestClient_APIError404(t *testing.T) {
	fs := newFakeServer(t, 404, `{"error":"not found"}`)
	_, err := fs.client().GetWorkout("missing")
	require.Error(t, err)
	var ae *APIError
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, 404, ae.StatusCode)
}

func TestClient_DecodeError(t *testing.T) {
	fs := newFakeServer(t, 200, `not-json`)
	_, err := fs.client().GetUserInfo()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode response")
}

func TestClient_ListWorkouts(t *testing.T) {
	fs := newFakeServer(t, 200, `{"page":1,"page_count":3,"workouts":[{"id":"w1","title":"Day 1","start_time":"2024-01-01T10:00:00Z","end_time":"2024-01-01T11:00:00Z","exercises":[]}]}`)
	got, err := fs.client().ListWorkouts(1, 5)
	require.NoError(t, err)
	assert.Equal(t, "/v1/workouts", fs.lastPath)
	assert.Equal(t, "page=1&pageSize=5", fs.lastQuery)
	assert.Equal(t, 1, got.Page)
	assert.Equal(t, 3, got.PageCount)
	require.Len(t, got.Workouts, 1)
	assert.Equal(t, "w1", got.Workouts[0].ID)
}

func TestClient_CreateWorkout(t *testing.T) {
	fs := newFakeServer(t, 201, `{"id":"w-new","title":"New","start_time":"2024-01-01T10:00:00Z","end_time":"2024-01-01T11:00:00Z","exercises":[]}`)
	req := &CreateWorkoutRequest{Workout: WorkoutPayload{Title: "New", StartTime: "2024-01-01T10:00:00Z", EndTime: "2024-01-01T11:00:00Z"}}
	got, err := fs.client().CreateWorkout(req)
	require.NoError(t, err)
	assert.Equal(t, "POST", fs.lastMethod)
	assert.Equal(t, "/v1/workouts", fs.lastPath)
	var decoded CreateWorkoutRequest
	require.NoError(t, json.Unmarshal(fs.lastBody, &decoded))
	assert.Equal(t, "New", decoded.Workout.Title)
	assert.Equal(t, "w-new", got.ID)
}

func TestClient_GetWorkout(t *testing.T) {
	fs := newFakeServer(t, 200, `{"id":"w1","title":"x","start_time":"","end_time":"","exercises":[]}`)
	got, err := fs.client().GetWorkout("w1")
	require.NoError(t, err)
	assert.Equal(t, "/v1/workouts/w1", fs.lastPath)
	assert.Equal(t, "w1", got.ID)
}

func TestClient_UpdateWorkout(t *testing.T) {
	fs := newFakeServer(t, 200, `{"id":"w1","title":"x","start_time":"","end_time":"","exercises":[]}`)
	got, err := fs.client().UpdateWorkout("w1", &UpdateWorkoutRequest{Workout: WorkoutPayload{Title: "x"}})
	require.NoError(t, err)
	assert.Equal(t, "PUT", fs.lastMethod)
	assert.Equal(t, "/v1/workouts/w1", fs.lastPath)
	assert.Equal(t, "w1", got.ID)
}

func TestClient_GetWorkoutCount(t *testing.T) {
	fs := newFakeServer(t, 200, `{"workout_count":42}`)
	got, err := fs.client().GetWorkoutCount()
	require.NoError(t, err)
	assert.Equal(t, "/v1/workouts/count", fs.lastPath)
	assert.Equal(t, 42, got)
}

func TestClient_GetWorkoutEvents(t *testing.T) {
	fs := newFakeServer(t, 200, `{"page":1,"page_count":1,"events":[{"type":"updated","workout":{"id":"w1","title":"x","start_time":"","end_time":"","exercises":[]}}]}`)
	got, err := fs.client().GetWorkoutEvents(1, 5, "2024-01-01T00:00:00Z")
	require.NoError(t, err)
	assert.Equal(t, "/v1/workouts/events", fs.lastPath)
	assert.Contains(t, fs.lastQuery, "since=2024-01-01T00%3A00%3A00Z")
	require.Len(t, got.Events, 1)
	assert.Equal(t, "updated", got.Events[0].Type)
}

func TestClient_ListRoutines(t *testing.T) {
	fs := newFakeServer(t, 200, `{"page":1,"page_count":1,"routines":[{"id":"r1","title":"PPL","exercises":[]}]}`)
	got, err := fs.client().ListRoutines(1, 5)
	require.NoError(t, err)
	assert.Equal(t, "/v1/routines", fs.lastPath)
	require.Len(t, got.Routines, 1)
}

func TestClient_CreateRoutine(t *testing.T) {
	fs := newFakeServer(t, 201, `{"id":"r-new","title":"PPL","exercises":[]}`)
	got, err := fs.client().CreateRoutine(&CreateRoutineRequest{Routine: RoutineCreate{Title: "PPL"}})
	require.NoError(t, err)
	assert.Equal(t, "POST", fs.lastMethod)
	assert.Equal(t, "r-new", got.ID)
}

func TestClient_GetRoutine(t *testing.T) {
	fs := newFakeServer(t, 200, `{"id":"r1","title":"PPL","exercises":[]}`)
	got, err := fs.client().GetRoutine("r1")
	require.NoError(t, err)
	assert.Equal(t, "/v1/routines/r1", fs.lastPath)
	assert.Equal(t, "r1", got.ID)
}

func TestClient_UpdateRoutine(t *testing.T) {
	fs := newFakeServer(t, 200, `{"id":"r1","title":"PPL","exercises":[]}`)
	got, err := fs.client().UpdateRoutine("r1", &UpdateRoutineRequest{Routine: RoutineUpdate{Title: "PPL"}})
	require.NoError(t, err)
	assert.Equal(t, "PUT", fs.lastMethod)
	assert.Equal(t, "r1", got.ID)
}

func TestClient_ListExerciseTemplates(t *testing.T) {
	fs := newFakeServer(t, 200, `{"page":1,"page_count":1,"exercise_templates":[{"id":"e1","title":"Squat","type":"weight_reps","primary_muscle_group":"quadriceps"}]}`)
	got, err := fs.client().ListExerciseTemplates(1, 5)
	require.NoError(t, err)
	assert.Equal(t, "/v1/exercise_templates", fs.lastPath)
	require.Len(t, got.ExerciseTemplates, 1)
	assert.Equal(t, "Squat", got.ExerciseTemplates[0].Title)
}

func TestClient_CreateExerciseTemplate(t *testing.T) {
	fs := newFakeServer(t, 201, `{"id":"e-new"}`)
	id, err := fs.client().CreateExerciseTemplate(&CreateExerciseTemplateRequest{Exercise: ExerciseTemplateCreate{Title: "Squat"}})
	require.NoError(t, err)
	assert.Equal(t, "POST", fs.lastMethod)
	assert.Equal(t, "e-new", id)
}

func TestClient_GetExerciseTemplate(t *testing.T) {
	fs := newFakeServer(t, 200, `{"id":"e1","title":"Squat","type":"weight_reps","primary_muscle_group":"quadriceps"}`)
	got, err := fs.client().GetExerciseTemplate("e1")
	require.NoError(t, err)
	assert.Equal(t, "/v1/exercise_templates/e1", fs.lastPath)
	assert.Equal(t, "Squat", got.Title)
}

func TestClient_ListRoutineFolders(t *testing.T) {
	fs := newFakeServer(t, 200, `{"page":1,"page_count":1,"routine_folders":[{"id":1,"title":"Main"}]}`)
	got, err := fs.client().ListRoutineFolders(1, 5)
	require.NoError(t, err)
	assert.Equal(t, "/v1/routine_folders", fs.lastPath)
	require.Len(t, got.RoutineFolders, 1)
}

func TestClient_CreateRoutineFolder(t *testing.T) {
	fs := newFakeServer(t, 201, `{"id":7,"title":"Main"}`)
	got, err := fs.client().CreateRoutineFolder("Main")
	require.NoError(t, err)
	assert.Contains(t, string(fs.lastBody), `"title":"Main"`)
	assert.Equal(t, 7, got.ID)
}

func TestClient_GetRoutineFolder(t *testing.T) {
	fs := newFakeServer(t, 200, `{"id":3,"title":"Cardio"}`)
	got, err := fs.client().GetRoutineFolder(3)
	require.NoError(t, err)
	assert.Equal(t, "/v1/routine_folders/3", fs.lastPath)
	assert.Equal(t, 3, got.ID)
}

func TestClient_ListBodyMeasurements(t *testing.T) {
	fs := newFakeServer(t, 200, `{"page":1,"page_count":1,"body_measurements":[{"date":"2024-01-01"}]}`)
	got, err := fs.client().ListBodyMeasurements(1, 5)
	require.NoError(t, err)
	assert.Equal(t, "/v1/body_measurements", fs.lastPath)
	require.Len(t, got.BodyMeasurements, 1)
}

func TestClient_CreateBodyMeasurement(t *testing.T) {
	fs := newFakeServer(t, 201, ``)
	err := fs.client().CreateBodyMeasurement(&BodyMeasurementInput{Date: "2024-01-01"})
	require.NoError(t, err)
	assert.Equal(t, "POST", fs.lastMethod)
	assert.Equal(t, "/v1/body_measurements", fs.lastPath)
	assert.True(t, strings.Contains(string(fs.lastBody), `"date":"2024-01-01"`))
}

func TestClient_GetBodyMeasurement(t *testing.T) {
	fs := newFakeServer(t, 200, `{"date":"2024-01-01"}`)
	got, err := fs.client().GetBodyMeasurement("2024-01-01")
	require.NoError(t, err)
	assert.Equal(t, "/v1/body_measurements/2024-01-01", fs.lastPath)
	assert.Equal(t, "2024-01-01", got.Date)
}

func TestClient_UpdateBodyMeasurement(t *testing.T) {
	fs := newFakeServer(t, 200, ``)
	err := fs.client().UpdateBodyMeasurement("2024-01-01", &BodyMeasurementInput{Date: "2024-01-01"})
	require.NoError(t, err)
	assert.Equal(t, "PUT", fs.lastMethod)
	assert.Equal(t, "/v1/body_measurements/2024-01-01", fs.lastPath)
}

func TestClient_New_DefaultBaseURL(t *testing.T) {
	c := New("key", "")
	assert.Equal(t, DefaultBaseURL, c.baseURL)
}

// -- Regression tests for bugs reported by users ---------------------------

// Bug 1: omitting folder_id from a RoutineCreate must serialize as null, not
// drop the field — Hevy interprets a missing key as undefined.
func TestClient_CreateRoutine_FolderIDNilSerializesAsNull(t *testing.T) {
	fs := newFakeServer(t, 201, `{"id":"r","title":"P","exercises":[]}`)
	_, err := fs.client().CreateRoutine(&CreateRoutineRequest{
		Routine: RoutineCreate{Title: "P", FolderID: nil, Exercises: []RoutineCreateExercise{}},
	})
	require.NoError(t, err)
	assert.Contains(t, string(fs.lastBody), `"folder_id":null`, "body=%s", string(fs.lastBody))
}

// Bug 2: title on exercises must not appear in the request body. The input
// type RoutineCreateExercise has no Title field, so it physically cannot be
// emitted — this test pins that.
func TestClient_CreateRoutine_NoTitleOnExercises(t *testing.T) {
	fs := newFakeServer(t, 201, `{"id":"r","title":"P","exercises":[]}`)
	_, err := fs.client().CreateRoutine(&CreateRoutineRequest{
		Routine: RoutineCreate{
			Title: "P",
			Exercises: []RoutineCreateExercise{
				{
					ExerciseTemplateID: "tmpl-1",
					Sets: []RoutineCreateSet{
						{Type: SetTypeNormal},
					},
				},
			},
		},
	})
	require.NoError(t, err)
	body := string(fs.lastBody)
	// The routine itself does carry a title; the exercise object must not.
	// Decode and inspect the structure instead of string-matching.
	var parsed struct {
		Routine struct {
			Title     string                   `json:"title"`
			Exercises []map[string]interface{} `json:"exercises"`
		} `json:"routine"`
	}
	require.NoError(t, json.Unmarshal(fs.lastBody, &parsed))
	assert.Equal(t, "P", parsed.Routine.Title)
	require.Len(t, parsed.Routine.Exercises, 1)
	_, hasTitle := parsed.Routine.Exercises[0]["title"]
	assert.False(t, hasTitle, "exercise must not include title key; body=%s", body)
	_, hasIndex := parsed.Routine.Exercises[0]["index"]
	assert.False(t, hasIndex, "exercise must not include index key; body=%s", body)
}

// Bug 3: POST /v1/routine_folders may wrap the response under `routine_folder`
// in practice — must unwrap.
func TestClient_CreateRoutineFolder_UnwrapsEnvelope(t *testing.T) {
	fs := newFakeServer(t, 201, `{"routine_folder":{"id":3081154,"title":"Cardio"}}`)
	got, err := fs.client().CreateRoutineFolder("Cardio")
	require.NoError(t, err)
	assert.Equal(t, 3081154, got.ID)
	assert.Equal(t, "Cardio", got.Title)
}

// Same response shape with bare object — both shapes must work.
func TestClient_CreateRoutineFolder_BareResponseStillWorks(t *testing.T) {
	fs := newFakeServer(t, 201, `{"id":3081154,"title":"Cardio"}`)
	got, err := fs.client().CreateRoutineFolder("Cardio")
	require.NoError(t, err)
	assert.Equal(t, 3081154, got.ID)
	assert.Equal(t, "Cardio", got.Title)
}

// Bug 4: POST /v1/routines may wrap response under `routine` — must unwrap.
func TestClient_CreateRoutine_UnwrapsEnvelope(t *testing.T) {
	fs := newFakeServer(t, 201, `{"routine":{"id":"r-1","title":"PPL","exercises":[]}}`)
	got, err := fs.client().CreateRoutine(&CreateRoutineRequest{Routine: RoutineCreate{Title: "PPL"}})
	require.NoError(t, err)
	assert.Equal(t, "r-1", got.ID)
	assert.Equal(t, "PPL", got.Title)
}

// GET /v1/routines/{id} is documented as wrapped — confirm we unwrap.
func TestClient_GetRoutine_UnwrapsEnvelope(t *testing.T) {
	fs := newFakeServer(t, 200, `{"routine":{"id":"r1","title":"PPL","exercises":[]}}`)
	got, err := fs.client().GetRoutine("r1")
	require.NoError(t, err)
	assert.Equal(t, "r1", got.ID)
}

// Bug 5: the superset field is `superset_id` (singular) on reads and writes
// alike. Hevy's OpenAPI spec once documented the read field as `supersets_id`
// (plural); the spec was corrected on their side (verified 2026-09-19), and
// the live API never used the plural form. The field must also not be
// `omitempty` on output — callers need to see `null` explicitly to
// distinguish "not in a superset" from "field was dropped".
func TestClient_GetWorkout_SupersetIDSingularOnResponse(t *testing.T) {
	body := `{"id":"w1","title":"x","start_time":"","end_time":"","exercises":[{"exercise_template_id":"t1","superset_id":7,"sets":[]},{"exercise_template_id":"t2","superset_id":null,"sets":[]}]}`
	fs := newFakeServer(t, 200, body)
	got, err := fs.client().GetWorkout("w1")
	require.NoError(t, err)
	require.Len(t, got.Exercises, 2)
	require.NotNil(t, got.Exercises[0].SupersetID)
	assert.Equal(t, 7, *got.Exercises[0].SupersetID)
	assert.Nil(t, got.Exercises[1].SupersetID)
}

// Marshaling a WorkoutExercise must always include superset_id, even when nil,
// so downstream consumers can tell "unpaired" from "unknown".
func TestWorkoutExercise_MarshalsSupersetIDEvenWhenNil(t *testing.T) {
	buf, err := json.Marshal(WorkoutExercise{ExerciseTemplateID: "t1", Sets: []WorkoutSet{}})
	require.NoError(t, err)
	assert.Contains(t, string(buf), `"superset_id":null`, "body=%s", string(buf))
	assert.NotContains(t, string(buf), "supersets_id")
}

func TestWorkoutExercise_MarshalsSupersetIDWhenSet(t *testing.T) {
	id := 7
	buf, err := json.Marshal(WorkoutExercise{ExerciseTemplateID: "t1", SupersetID: &id, Sets: []WorkoutSet{}})
	require.NoError(t, err)
	assert.Contains(t, string(buf), `"superset_id":7`, "body=%s", string(buf))
}

// Same behavior for RoutineExercise.
func TestRoutineExercise_ParsesAndMarshalsSupersetID(t *testing.T) {
	var ex RoutineExercise
	require.NoError(t, json.Unmarshal([]byte(`{"exercise_template_id":"t1","superset_id":3,"sets":[]}`), &ex))
	require.NotNil(t, ex.SupersetID)
	assert.Equal(t, 3, *ex.SupersetID)

	// marshal always present, even when nil
	buf, err := json.Marshal(RoutineExercise{ExerciseTemplateID: "t1", Sets: []RoutineSet{}})
	require.NoError(t, err)
	assert.Contains(t, string(buf), `"superset_id":null`, "body=%s", string(buf))
}

// CreateExerciseTemplate accepts integer or string IDs from Hevy.
func TestClient_CreateExerciseTemplate_IntegerID(t *testing.T) {
	fs := newFakeServer(t, 201, `{"id":12345}`)
	id, err := fs.client().CreateExerciseTemplate(&CreateExerciseTemplateRequest{Exercise: ExerciseTemplateCreate{Title: "X"}})
	require.NoError(t, err)
	assert.Equal(t, "12345", id)
}

func TestClient_CreateExerciseTemplate_StringID(t *testing.T) {
	fs := newFakeServer(t, 201, `{"id":"abc-123"}`)
	id, err := fs.client().CreateExerciseTemplate(&CreateExerciseTemplateRequest{Exercise: ExerciseTemplateCreate{Title: "X"}})
	require.NoError(t, err)
	assert.Equal(t, "abc-123", id)
}

// Hevy's user info response carries more than id/name/url: username, the
// account's unit preferences, and the Pro flag (live-verified 2026-10-19).
func TestClient_GetUserInfo_DecodesUnitsAndProFlag(t *testing.T) {
	fs := newFakeServer(t, 200, `{"data":{"id":"u1","username":"alice","name":"Alice","url":"https://hevy.com/alice","weight_unit":"kg","distance_unit":"kilometers","is_pro":true}}`)
	got, err := fs.client().GetUserInfo()
	require.NoError(t, err)
	assert.Equal(t, &UserInfo{
		ID:           "u1",
		Username:     "alice",
		Name:         "Alice",
		URL:          "https://hevy.com/alice",
		WeightUnit:   "kg",
		DistanceUnit: "kilometers",
		IsPro:        true,
	}, got)
}

// `is_pro` must survive as an explicit false — a non-Pro account has to be
// distinguishable from a response that omitted the field.
func TestClient_UserInfo_IsProFalseSerializes(t *testing.T) {
	buf, err := json.Marshal(UserInfo{ID: "u1"})
	require.NoError(t, err)
	assert.Contains(t, string(buf), `"is_pro":false`)
}

// Exercise templates carry `equipment` and `thumbnail_url` on reads; neither
// is part of the create request shape.
func TestClient_GetExerciseTemplate_DecodesEquipmentAndThumbnail(t *testing.T) {
	fs := newFakeServer(t, 200, `{"id":"e1","title":"Bench Press (Barbell)","type":"weight_reps","primary_muscle_group":"chest","secondary_muscle_groups":["triceps"],"equipment":"barbell","thumbnail_url":"https://example.invalid/bench.jpg","is_custom":false}`)
	got, err := fs.client().GetExerciseTemplate("e1")
	require.NoError(t, err)
	assert.Equal(t, EquipmentCategory("barbell"), got.Equipment)
	assert.Equal(t, "https://example.invalid/bench.jpg", got.ThumbnailURL)
}

// Hevy's routine PUT distinguishes "folder_id absent" (keep placement) from
// "folder_id: null" (move to My Routines) from "folder_id: N" (move into N).
// All three must survive serialization.
func TestRoutineUpdate_FolderPlacementTriState(t *testing.T) {
	t.Run("absent keeps placement", func(t *testing.T) {
		buf, err := json.Marshal(RoutineUpdate{Title: "PPL"})
		require.NoError(t, err)
		assert.NotContains(t, string(buf), "folder_id")
	})

	t.Run("MoveToFolder sends the id", func(t *testing.T) {
		r := RoutineUpdate{Title: "PPL"}
		r.MoveToFolder(3101367)
		buf, err := json.Marshal(r)
		require.NoError(t, err)
		assert.Contains(t, string(buf), `"folder_id":3101367`)
	})

	t.Run("MoveToMyRoutines sends null", func(t *testing.T) {
		r := RoutineUpdate{Title: "PPL"}
		r.MoveToMyRoutines()
		buf, err := json.Marshal(r)
		require.NoError(t, err)
		assert.Contains(t, string(buf), `"folder_id":null`)
	})
}

// PUT /v1/body_measurements/{date} is a destructive full replace that ignores
// unknown keys instead of rejecting them (live-verified 2026-10-19, after it
// silently wiped a real row). MergeBodyMeasurement is what keeps a partial
// update from clearing the rest of the row.
func TestMergeBodyMeasurement(t *testing.T) {
	const current = `{"id":73949311,"date":"2026-10-16","weight_kg":75.61,"lean_mass_kg":64.65,"fat_percent":14.5,"created_at":"2026-10-16T01:51:40.082Z"}`
	f := func(v float64) *float64 { return &v }

	t.Run("unset metrics keep their stored values", func(t *testing.T) {
		buf, err := MergeBodyMeasurement([]byte(current), &BodyMeasurementInput{
			Date:     "2026-10-16",
			WeightKg: f(76.2),
		})
		require.NoError(t, err)

		var got map[string]any
		require.NoError(t, json.Unmarshal(buf, &got))
		assert.Equal(t, 76.2, got["weight_kg"], "provided field wins")
		assert.Equal(t, 64.65, got["lean_mass_kg"], "unset field must survive")
		assert.Equal(t, 14.5, got["fat_percent"], "unset field must survive")
		assert.Equal(t, "2026-10-16", got["date"])
	})

	t.Run("server-owned fields are stripped", func(t *testing.T) {
		buf, err := MergeBodyMeasurement([]byte(current), &BodyMeasurementInput{Date: "2026-10-16"})
		require.NoError(t, err)
		assert.NotContains(t, string(buf), "created_at")
		assert.NotContains(t, string(buf), `"id"`)
	})

	// Working from raw bytes rather than a decoded struct means a field Hevy
	// adds before we model it isn't destroyed by the next update.
	t.Run("unmodelled fields survive", func(t *testing.T) {
		buf, err := MergeBodyMeasurement([]byte(`{"date":"2026-10-16","future_metric":42}`), &BodyMeasurementInput{
			Date:     "2026-10-16",
			WeightKg: f(76.2),
		})
		require.NoError(t, err)
		assert.Contains(t, string(buf), `"future_metric":42`)
	})

	t.Run("empty current row is not an error", func(t *testing.T) {
		buf, err := MergeBodyMeasurement(nil, &BodyMeasurementInput{Date: "2026-10-16", WeightKg: f(76.2)})
		require.NoError(t, err)
		assert.JSONEq(t, `{"date":"2026-10-16","weight_kg":76.2}`, string(buf))
	})

	t.Run("garbage current row is an error", func(t *testing.T) {
		_, err := MergeBodyMeasurement([]byte(`not json`), &BodyMeasurementInput{Date: "2026-10-16"})
		require.Error(t, err)
	})
}

// The input type must not carry the server-owned fields that reads include.
func TestBodyMeasurementInput_OmitsOutputOnlyFields(t *testing.T) {
	buf, err := json.Marshal(BodyMeasurementInput{Date: "2026-10-16"})
	require.NoError(t, err)
	assert.Equal(t, `{"date":"2026-10-16"}`, string(buf))
}

// Reads carry id and created_at; both are absent from the spec but present on
// every live row.
func TestBodyMeasurement_DecodesServerOwnedFields(t *testing.T) {
	fs := newFakeServer(t, 200, `{"id":73949311,"date":"2026-10-16","weight_kg":75.61,"created_at":"2026-10-16T01:51:40.082Z"}`)
	got, err := fs.client().GetBodyMeasurement("2026-10-16")
	require.NoError(t, err)
	require.NotNil(t, got.ID)
	assert.Equal(t, 73949311, *got.ID)
	assert.Equal(t, "2026-10-16T01:51:40.082Z", got.CreatedAt)
}
