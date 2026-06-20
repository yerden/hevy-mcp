// Package hevy contains the wire types and HTTP client for the Hevy public API.
//
// We use separate types for request vs response payloads where they differ.
// Hevy's API has fields that are present in responses but rejected on input
// (e.g. `index` on sets, `title` on routine exercises, `created_at`), and a
// few fields that are renamed between the two directions (e.g. the request
// uses `muscle_group`/`other_muscles` while the response uses
// `primary_muscle_group`/`secondary_muscle_groups`).
package hevy

// SetType is the kind of set logged in a workout or routine.
type SetType string

const (
	SetTypeWarmup  SetType = "warmup"
	SetTypeNormal  SetType = "normal"
	SetTypeFailure SetType = "failure"
	SetTypeDropset SetType = "dropset"
)

// ExerciseType describes how an exercise records its metrics.
type ExerciseType string

// EquipmentCategory categorizes the equipment used by an exercise template.
type EquipmentCategory string

// MuscleGroup is a primary or secondary muscle category for an exercise.
type MuscleGroup string

// -- Response types --------------------------------------------------------

// WorkoutSet is one logged set inside a workout exercise (response shape).
type WorkoutSet struct {
	Index           *int     `json:"index,omitempty"`
	Type            SetType  `json:"type"`
	WeightKg        *float64 `json:"weight_kg,omitempty"`
	Reps            *int     `json:"reps,omitempty"`
	DistanceMeters  *float64 `json:"distance_meters,omitempty"`
	DurationSeconds *int     `json:"duration_seconds,omitempty"`
	CustomMetric    *float64 `json:"custom_metric,omitempty"`
	RPE             *float64 `json:"rpe,omitempty"`
}

// WorkoutExercise represents one exercise within a workout (response shape).
//
// Hevy responses use `supersets_id` (plural), while the create/update request
// schema uses `superset_id`. We use a single `SupersetID` field with the
// response key on output types and a different name on the input types.
type WorkoutExercise struct {
	Index              *int         `json:"index,omitempty"`
	Title              string       `json:"title,omitempty"`
	Notes              string       `json:"notes,omitempty"`
	ExerciseTemplateID string       `json:"exercise_template_id"`
	SupersetID         *int         `json:"supersets_id,omitempty"`
	Sets               []WorkoutSet `json:"sets"`
}

// Workout is a completed workout record (response shape).
type Workout struct {
	ID          string            `json:"id,omitempty"`
	Title       string            `json:"title"`
	RoutineID   string            `json:"routine_id,omitempty"`
	Description string            `json:"description,omitempty"`
	StartTime   string            `json:"start_time"`
	EndTime     string            `json:"end_time"`
	UpdatedAt   string            `json:"updated_at,omitempty"`
	CreatedAt   string            `json:"created_at,omitempty"`
	IsPrivate   bool              `json:"is_private,omitempty"`
	Exercises   []WorkoutExercise `json:"exercises"`
}

// PaginatedWorkouts wraps a workouts page response.
type PaginatedWorkouts struct {
	Page      int       `json:"page"`
	PageCount int       `json:"page_count"`
	Workouts  []Workout `json:"workouts"`
}

// WorkoutEvent is a union: either an updated workout or a deletion marker.
type WorkoutEvent struct {
	Type      string   `json:"type"`
	Workout   *Workout `json:"workout,omitempty"`
	ID        string   `json:"id,omitempty"`
	DeletedAt string   `json:"deleted_at,omitempty"`
}

// PaginatedWorkoutEvents wraps an events page response.
type PaginatedWorkoutEvents struct {
	Page      int            `json:"page"`
	PageCount int            `json:"page_count"`
	Events    []WorkoutEvent `json:"events"`
}

// WorkoutCount is the response of GET /v1/workouts/count.
type WorkoutCount struct {
	WorkoutCount int `json:"workout_count"`
}

// RepRange is the optional rep range hint on a routine set.
type RepRange struct {
	Start *int `json:"start,omitempty"`
	End   *int `json:"end,omitempty"`
}

// RoutineSet is one set in a routine template (response shape).
type RoutineSet struct {
	Index           *int      `json:"index,omitempty"`
	Type            SetType   `json:"type"`
	WeightKg        *float64  `json:"weight_kg,omitempty"`
	Reps            *int      `json:"reps,omitempty"`
	DistanceMeters  *float64  `json:"distance_meters,omitempty"`
	DurationSeconds *int      `json:"duration_seconds,omitempty"`
	CustomMetric    *float64  `json:"custom_metric,omitempty"`
	RPE             *float64  `json:"rpe,omitempty"`
	RepRange        *RepRange `json:"rep_range,omitempty"`
}

// RoutineExercise represents one exercise in a routine (response shape).
type RoutineExercise struct {
	Index              *int         `json:"index,omitempty"`
	Title              string       `json:"title,omitempty"`
	Notes              string       `json:"notes,omitempty"`
	RestSeconds        *int         `json:"rest_seconds,omitempty"`
	ExerciseTemplateID string       `json:"exercise_template_id"`
	SupersetID         *int         `json:"supersets_id,omitempty"`
	Sets               []RoutineSet `json:"sets"`
}

// Routine is a workout template (response shape).
type Routine struct {
	ID        string            `json:"id,omitempty"`
	Title     string            `json:"title"`
	FolderID  *int              `json:"folder_id"`
	UpdatedAt string            `json:"updated_at,omitempty"`
	CreatedAt string            `json:"created_at,omitempty"`
	Exercises []RoutineExercise `json:"exercises"`
}

// PaginatedRoutines wraps a routines page response.
type PaginatedRoutines struct {
	Page      int       `json:"page"`
	PageCount int       `json:"page_count"`
	Routines  []Routine `json:"routines"`
}

// ExerciseTemplate describes a Hevy exercise definition (response shape).
//
// Field names differ between input and output for this resource: the response
// uses `type`/`primary_muscle_group`/`secondary_muscle_groups`, while the
// create-request uses `exercise_type`/`muscle_group`/`other_muscles`.
type ExerciseTemplate struct {
	ID                    string        `json:"id"`
	Title                 string        `json:"title"`
	Type                  ExerciseType  `json:"type"`
	PrimaryMuscleGroup    MuscleGroup   `json:"primary_muscle_group"`
	SecondaryMuscleGroups []MuscleGroup `json:"secondary_muscle_groups,omitempty"`
	IsCustom              bool          `json:"is_custom,omitempty"`
}

// PaginatedExerciseTemplates wraps an exercise templates page response.
type PaginatedExerciseTemplates struct {
	Page              int                `json:"page"`
	PageCount         int                `json:"page_count"`
	ExerciseTemplates []ExerciseTemplate `json:"exercise_templates"`
}

// RoutineFolder is a grouping of routines (response shape).
type RoutineFolder struct {
	ID        int    `json:"id"`
	Title     string `json:"title"`
	Index     *int   `json:"index,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// PaginatedRoutineFolders wraps a routine folders page response.
type PaginatedRoutineFolders struct {
	Page           int             `json:"page"`
	PageCount      int             `json:"page_count"`
	RoutineFolders []RoutineFolder `json:"routine_folders"`
}

// ExerciseHistoryEntry is one historical instance of an exercise.
type ExerciseHistoryEntry struct {
	WorkoutID string       `json:"workout_id"`
	StartTime string       `json:"start_time"`
	EndTime   string       `json:"end_time"`
	Sets      []WorkoutSet `json:"sets"`
}

// ExerciseHistoryResponse wraps GET /v1/exercise_history/{id} output.
type ExerciseHistoryResponse struct {
	ExerciseTemplateID string                 `json:"exercise_template_id"`
	History            []ExerciseHistoryEntry `json:"history"`
}

// BodyMeasurement is a snapshot of body metrics for a given date.
//
// Note Hevy's mixed naming: upper-body circumferences carry a `_cm` suffix
// while lower-body ones don't (`abdomen`, `waist`, `hips`, `left_thigh`,
// etc.). We follow the API verbatim.
type BodyMeasurement struct {
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

// PaginatedBodyMeasurements wraps a body measurements page response.
type PaginatedBodyMeasurements struct {
	Page             int               `json:"page"`
	PageCount        int               `json:"page_count"`
	BodyMeasurements []BodyMeasurement `json:"body_measurements"`
}

// UserInfo represents the authenticated Hevy user.
type UserInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}
