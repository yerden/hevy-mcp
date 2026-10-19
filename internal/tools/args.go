package tools

import (
	"errors"

	"github.com/yerden/hevy-mcp/internal/hevy"
)

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
	ID      string               `json:"id"`
	Routine updateRoutinePayload `json:"routine"`
}

// updateRoutinePayload is the model-facing shape for hevy_update_routine. It
// deliberately differs from hevy.RoutineUpdate (the wire type): Hevy's PUT
// treats `folder_id` as a tri-state (absent = keep placement, N = move into
// folder N, null = move to "My Routines"), and a reflected `*int` collapses
// two of those — once the model's `null` reaches Go it is indistinguishable
// from an omitted key, and nil then serializes as an omitted key again. So we
// publish the "move out" case as its own boolean and rebuild the tri-state in
// wire().
type updateRoutinePayload struct {
	Title            string                       `json:"title"`
	Notes            string                       `json:"notes,omitempty"`
	FolderID         *int                         `json:"folder_id,omitempty" jsonschema:"Move the routine into this folder. Omit to leave the routine where it is."`
	MoveToMyRoutines bool                         `json:"move_to_my_routines,omitempty" jsonschema:"Set true to move the routine out of its folder, into the default 'My Routines' list. Mutually exclusive with folder_id."`
	Exercises        []hevy.RoutineCreateExercise `json:"exercises"`
}

// wire maps the payload onto Hevy's PUT body, translating the two placement
// fields into the single tri-state folder_id Hevy expects.
func (p updateRoutinePayload) wire() (hevy.RoutineUpdate, error) {
	out := hevy.RoutineUpdate{
		Title:     p.Title,
		Notes:     p.Notes,
		Exercises: p.Exercises,
	}
	switch {
	case p.FolderID != nil && p.MoveToMyRoutines:
		return out, errors.New("folder_id and move_to_my_routines are mutually exclusive: pass folder_id to move the routine into a folder, or move_to_my_routines=true to move it out of its folder")
	case p.FolderID != nil:
		out.MoveToFolder(*p.FolderID)
	case p.MoveToMyRoutines:
		out.MoveToMyRoutines()
	}
	return out, nil
}

type createExerciseTemplateArgs struct {
	Exercise hevy.ExerciseTemplateCreate `json:"exercise"`
}

type createBodyMeasurementArgs struct {
	Measurement hevy.BodyMeasurementInput `json:"measurement"`
}

type updateBodyMeasurementArgs struct {
	Date        string                    `json:"date"`
	Measurement hevy.BodyMeasurementInput `json:"measurement"`
}

// indexHint is no longer needed — input-only request types omit the `index`
// field entirely. Kept as a noop so existing descriptions can still reference
// it, but contains no warning text.
const indexHint = ""
