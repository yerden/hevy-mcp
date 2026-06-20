package tools

import "github.com/yerden/hevy-mcp/internal/hevy"

// Argument types for the create/update tools. These mirror the Hevy
// request-body schemas so the JSON schema we publish to the LLM matches what
// Hevy will actually accept (no output-only fields, correct field names for
// exercise templates, folder_id as nullable on routine create, etc.).

type createWorkoutArgs struct {
	Workout hevy.WorkoutPayload `json:"workout"`
}

type updateWorkoutArgs struct {
	ID      string              `json:"id"`
	Workout hevy.WorkoutPayload `json:"workout"`
}

type createRoutineArgs struct {
	Routine hevy.RoutineCreate `json:"routine"`
}

type updateRoutineArgs struct {
	ID      string             `json:"id"`
	Routine hevy.RoutineUpdate `json:"routine"`
}

type createExerciseTemplateArgs struct {
	Exercise hevy.ExerciseTemplateCreate `json:"exercise"`
}

type createBodyMeasurementArgs struct {
	Measurement hevy.BodyMeasurement `json:"measurement"`
}

type updateBodyMeasurementArgs struct {
	Date        string               `json:"date"`
	Measurement hevy.BodyMeasurement `json:"measurement"`
}

// indexHint is no longer needed — input-only request types omit the `index`
// field entirely. Kept as a noop so existing descriptions can still reference
// it, but contains no warning text.
const indexHint = ""
