package hevy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

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
//
// Hevy's PUT accepts `folder_id` and distinguishes three states (live-verified
// 2026-10-19 by round-tripping a throwaway routine; the endpoint used to
// reject the key outright with HTTP 400 "Unrecognized key(s)"):
//
//   - key absent          → placement unchanged
//   - `"folder_id": N`    → routine moves into folder N
//   - `"folder_id": null` → routine moves to the default "My Routines" list
//
// A `*int` can only express two of those — nil omits the key, so "move to My
// Routines" would be unreachable — hence the raw message, set through
// MoveToFolder / MoveToMyRoutines. Leave it nil to keep the current folder.
//
// This type is not model-facing: the update tool publishes its own schema that
// splits the tri-state into two plain fields. See internal/tools/args.go.
type RoutineUpdate struct {
	Title     string                  `json:"title"`
	Notes     string                  `json:"notes,omitempty"`
	FolderID  json.RawMessage         `json:"folder_id,omitempty"`
	Exercises []RoutineCreateExercise `json:"exercises"`
}

// MoveToFolder makes the update move the routine into folder id.
func (r *RoutineUpdate) MoveToFolder(id int) {
	r.FolderID = json.RawMessage(strconv.Itoa(id))
}

// MoveToMyRoutines makes the update move the routine out of whatever folder it
// is in, into the default "My Routines" list.
func (r *RoutineUpdate) MoveToMyRoutines() {
	r.FolderID = json.RawMessage("null")
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
// IsPrivate is a plain bool with no `omitempty`: Hevy rejects the request
// with `"workout.is_private" is required` if the field is missing, so we
// always serialize it. Default zero value `false` means public, which
// matches Hevy's app default. Callers that want a private workout must set
// it to true explicitly.
type WorkoutPayload struct {
	Title       string                   `json:"title"`
	Description string                   `json:"description,omitempty"`
	StartTime   string                   `json:"start_time"`
	EndTime     string                   `json:"end_time"`
	IsPrivate   bool                     `json:"is_private" jsonschema:"Whether the workout is hidden from followers. Always sent, so on an update this overwrites the stored value: pass true to keep a private workout private. Hevy's read endpoints do not return the current value."`
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

// -- Body measurements -----------------------------------------------------

// BodyMeasurementInput is the body for POST /v1/body_measurements and
// PUT /v1/body_measurements/{date}.
//
// Separate from BodyMeasurement (the response type) because reads carry
// server-owned `id` and `created_at`; echoing those back on a write is exactly
// the kind of output-only leakage the rest of this file avoids.
//
// Every metric is *float64 + omitempty, so "not provided" is distinguishable
// from a value — which is what makes MergeBodyMeasurement possible. The flip
// side: there is no way to express "clear this field" through this type.
type BodyMeasurementInput struct {
	Date           string   `json:"date"`
	WeightKg       *float64 `json:"weight_kg,omitempty"`
	LeanMassKg     *float64 `json:"lean_mass_kg,omitempty"`
	FatPercent     *float64 `json:"fat_percent,omitempty"`
	NeckCm         *float64 `json:"neck_cm,omitempty"`
	ShoulderCm     *float64 `json:"shoulder_cm,omitempty"`
	ChestCm        *float64 `json:"chest_cm,omitempty"`
	LeftBicepCm    *float64 `json:"left_bicep_cm,omitempty"`
	RightBicepCm   *float64 `json:"right_bicep_cm,omitempty"`
	LeftForearmCm  *float64 `json:"left_forearm_cm,omitempty"`
	RightForearmCm *float64 `json:"right_forearm_cm,omitempty"`
	Abdomen        *float64 `json:"abdomen,omitempty"`
	Waist          *float64 `json:"waist,omitempty"`
	Hips           *float64 `json:"hips,omitempty"`
	LeftThigh      *float64 `json:"left_thigh,omitempty"`
	RightThigh     *float64 `json:"right_thigh,omitempty"`
	LeftCalf       *float64 `json:"left_calf,omitempty"`
	RightCalf      *float64 `json:"right_calf,omitempty"`
}

// bodyMeasurementOutputOnlyKeys are server-owned fields that come back on
// reads and must never be echoed into a write body.
var bodyMeasurementOutputOnlyKeys = []string{"id", "created_at"}

// MergeBodyMeasurement builds the body for PUT /v1/body_measurements/{date}
// by overlaying the caller-provided fields in update onto the row as Hevy
// currently has it.
//
// Why this exists: that PUT is a **full replace**, not a patch. Any metric
// missing from the body is cleared server-side, and the endpoint *ignores*
// unrecognized keys instead of rejecting them, so there is no error to notice
// — a partial update silently destroys the rest of the row. (Learned the hard
// way on 2026-10-19: a two-key PUT wiped a real row's weight, lean mass and
// fat percentage.) Read-modify-write is the only safe shape.
//
// current is the raw GET response for that date. Working from the raw bytes
// rather than a decoded struct means any field Hevy adds that we don't model
// yet survives the round-trip instead of being dropped on the next update.
func MergeBodyMeasurement(current []byte, update *BodyMeasurementInput) ([]byte, error) {
	if update == nil {
		return nil, errors.New("merge body measurement: nil update")
	}

	merged := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(current)) > 0 {
		if err := json.Unmarshal(current, &merged); err != nil {
			return nil, fmt.Errorf("merge body measurement: decode current row: %w", err)
		}
	}
	for _, k := range bodyMeasurementOutputOnlyKeys {
		delete(merged, k)
	}

	buf, err := json.Marshal(update)
	if err != nil {
		return nil, fmt.Errorf("merge body measurement: encode update: %w", err)
	}
	var provided map[string]json.RawMessage
	if err := json.Unmarshal(buf, &provided); err != nil {
		return nil, fmt.Errorf("merge body measurement: decode update: %w", err)
	}
	// omitempty means only the fields the caller actually set appear here.
	for k, v := range provided {
		merged[k] = v
	}

	return json.Marshal(merged)
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
