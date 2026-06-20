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

func TestClient_GetExerciseHistory(t *testing.T) {
	fs := newFakeServer(t, 200, `{"exercise_template_id":"e1","history":[]}`)
	_, err := fs.client().GetExerciseHistory("e1", "2024-01-01", "2024-02-01")
	require.NoError(t, err)
	assert.Equal(t, "/v1/exercise_history/e1", fs.lastPath)
	assert.Contains(t, fs.lastQuery, "start_date=2024-01-01")
	assert.Contains(t, fs.lastQuery, "end_date=2024-02-01")
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
	err := fs.client().CreateBodyMeasurement(&BodyMeasurement{Date: "2024-01-01"})
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
	err := fs.client().UpdateBodyMeasurement("2024-01-01", &BodyMeasurement{Date: "2024-01-01"})
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
