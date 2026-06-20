package hevy

// Request types — what we POST/PUT to Hevy. These are deliberately separate
// from the response types in models.go because Hevy rejects fields that are
// output-only (`index`, exercise `title`, `created_at`, etc.) and uses
// different names for some fields between input and output (notably exercise
// templates).
//
// Field tagging rules:
//   - Output-only fields are omitted entirely (not even in the struct).
//   - `folder_id` on RoutineCreate has no `omitempty`: nil must serialize as
//     `null`, because Hevy interprets a missing key as undefined and rejects
//     it ("Invalid routine folder id: undefined").
//   - All other optional fields use *T + omitempty so we send only what the
//     caller provided.

// -- Routines --------------------------------------------------------------

// RoutineCreate is the routine sub-object inside POST /v1/routines.
type RoutineCreate struct {
	Title     string                  `json:"title"`
	FolderID  *int                    `json:"folder_id"`
	Notes     string                  `json:"notes,omitempty"`
	Exercises []RoutineCreateExercise `json:"exercises"`
}

// RoutineCreateExercise is one exercise within a routine create payload.
type RoutineCreateExercise struct {
	ExerciseTemplateID string             `json:"exercise_template_id"`
	SupersetID         *int               `json:"superset_id,omitempty"`
	RestSeconds        *int               `json:"rest_seconds,omitempty"`
	Notes              string             `json:"notes,omitempty"`
	Sets               []RoutineCreateSet `json:"sets"`
}

// RoutineCreateSet is one set within a routine create payload.
type RoutineCreateSet struct {
	Type            SetType   `json:"type"`
	WeightKg        *float64  `json:"weight_kg,omitempty"`
	Reps            *int      `json:"reps,omitempty"`
	DistanceMeters  *int      `json:"distance_meters,omitempty"`
	DurationSeconds *int      `json:"duration_seconds,omitempty"`
	CustomMetric    *float64  `json:"custom_metric,omitempty"`
	RepRange        *RepRange `json:"rep_range,omitempty"`
}

// RoutineUpdate is the routine sub-object inside PUT /v1/routines/{id}.
// Hevy's PUT schema deliberately omits folder_id — folder placement is not
// changed by update; use a separate flow if you need to move a routine.
type RoutineUpdate struct {
	Title     string                  `json:"title"`
	Notes     string                  `json:"notes,omitempty"`
	Exercises []RoutineCreateExercise `json:"exercises"`
}

// CreateRoutineRequest is the body for POST /v1/routines.
type CreateRoutineRequest struct {
	Routine RoutineCreate `json:"routine"`
}

// UpdateRoutineRequest is the body for PUT /v1/routines/{id}.
type UpdateRoutineRequest struct {
	Routine RoutineUpdate `json:"routine"`
}

// -- Workouts --------------------------------------------------------------

// WorkoutPayload is the workout sub-object for both POST and PUT (Hevy uses
// the same schema for create and update).
//
// IsPrivate is *bool, not bool, because `omitempty` on a plain bool would
// drop a deliberate `false` (it can't distinguish "user wants public" from
// "user didn't specify"). With *bool: nil → field omitted, &false → "false"
// sent, &true → "true" sent.
type WorkoutPayload struct {
	Title       string                   `json:"title"`
	Description string                   `json:"description,omitempty"`
	StartTime   string                   `json:"start_time"`
	EndTime     string                   `json:"end_time"`
	IsPrivate   *bool                    `json:"is_private,omitempty"`
	Exercises   []WorkoutPayloadExercise `json:"exercises"`
}

// WorkoutPayloadExercise is one exercise inside a workout create/update body.
type WorkoutPayloadExercise struct {
	ExerciseTemplateID string              `json:"exercise_template_id"`
	SupersetID         *int                `json:"superset_id,omitempty"`
	Notes              string              `json:"notes,omitempty"`
	Sets               []WorkoutPayloadSet `json:"sets"`
}

// WorkoutPayloadSet is one set inside a workout create/update body.
type WorkoutPayloadSet struct {
	Type            SetType  `json:"type"`
	WeightKg        *float64 `json:"weight_kg,omitempty"`
	Reps            *int     `json:"reps,omitempty"`
	DistanceMeters  *int     `json:"distance_meters,omitempty"`
	DurationSeconds *int     `json:"duration_seconds,omitempty"`
	CustomMetric    *float64 `json:"custom_metric,omitempty"`
	RPE             *float64 `json:"rpe,omitempty"`
}

// CreateWorkoutRequest is the body for POST /v1/workouts.
type CreateWorkoutRequest struct {
	Workout WorkoutPayload `json:"workout"`
}

// UpdateWorkoutRequest is the body for PUT /v1/workouts/{id}.
type UpdateWorkoutRequest struct {
	Workout WorkoutPayload `json:"workout"`
}

// -- Routine folders -------------------------------------------------------

// CreateRoutineFolderRequest is the body for POST /v1/routine_folders.
type CreateRoutineFolderRequest struct {
	RoutineFolder RoutineFolderCreate `json:"routine_folder"`
}

// RoutineFolderCreate is the routine_folder sub-object on POST.
type RoutineFolderCreate struct {
	Title string `json:"title"`
}

// -- Exercise templates ----------------------------------------------------

// ExerciseTemplateCreate is the exercise sub-object inside POST
// /v1/exercise_templates. Field names differ from ExerciseTemplate (the
// response type): see models.go.
type ExerciseTemplateCreate struct {
	Title             string            `json:"title"`
	ExerciseType      ExerciseType      `json:"exercise_type"`
	EquipmentCategory EquipmentCategory `json:"equipment_category"`
	MuscleGroup       MuscleGroup       `json:"muscle_group"`
	OtherMuscles      []MuscleGroup     `json:"other_muscles,omitempty"`
}

// CreateExerciseTemplateRequest is the body for POST /v1/exercise_templates.
type CreateExerciseTemplateRequest struct {
	Exercise ExerciseTemplateCreate `json:"exercise"`
}

// CreatedTemplateID is the success response of POST /v1/exercise_templates.
// The spec types `id` as integer but the catalog uses hex string IDs, so we
// use json.Number to accept either.
//
// Use ID.String() to get a stable string form.
type CreatedTemplateID struct {
	ID flexibleID `json:"id"`
}

// flexibleID parses both `"abc"` and `123` as a string-typed ID.
type flexibleID string

func (f *flexibleID) UnmarshalJSON(data []byte) error {
	if len(data) >= 2 && data[0] == '"' {
		*f = flexibleID(data[1 : len(data)-1])
		return nil
	}
	*f = flexibleID(data)
	return nil
}

func (f flexibleID) String() string { return string(f) }
