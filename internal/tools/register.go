// Package tools wires the Hevy client to MCP tool definitions.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/yerden/hevy-mcp/internal/exercises"
	"github.com/yerden/hevy-mcp/internal/hevy"
)

// ClientFactory returns a *hevy.Client for one tool call. In stdio mode the
// returned client is constant; in HTTP mode it carries the per-session API key
// pulled out of context by middleware.
type ClientFactory func(ctx context.Context) (*hevy.Client, error)

// StaticFactory returns a factory that always yields c. Use for stdio mode.
func StaticFactory(c *hevy.Client) ClientFactory {
	return func(context.Context) (*hevy.Client, error) { return c, nil }
}

// RegisterAll attaches every Hevy tool to the MCP server.
func RegisterAll(s *server.MCPServer, factory ClientFactory) {
	registerWorkouts(s, factory)
	registerRoutines(s, factory)
	registerExerciseTemplates(s, factory)
	registerExerciseSearch(s, factory)
	registerRoutineFolders(s, factory)
	registerExerciseHistory(s, factory)
	registerBodyMeasurements(s, factory)
	registerUser(s, factory)
}

// wrap resolves the per-request Hevy client and invokes fn. Resolution failures
// (e.g. missing per-session API key) are surfaced as tool error results so the
// model sees a clear message rather than a transport error.
func wrap(factory ClientFactory, fn func(c *hevy.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error)) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		c, err := factory(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return fn(c, req)
	}
}

// jsonResult marshals v as JSON text and wraps it in an MCP tool result.
func jsonResult(v any) (*mcp.CallToolResult, error) {
	buf, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}
	return mcp.NewToolResultText(string(buf)), nil
}

// hevyResult converts a Hevy client call result/err into an MCP tool result.
// API errors are surfaced as MCP tool error results (visible to the LLM)
// rather than transport errors.
func hevyResult(v any, err error) (*mcp.CallToolResult, error) {
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(v)
}

// rawResult returns Hevy's actual response body verbatim to the tool caller.
// We use this for create/update endpoints because Hevy's response shape on
// POST is unreliable (sometimes wrapped, sometimes empty), and forwarding the
// raw bytes lets the model see exactly what Hevy returned rather than a
// possibly-zeroed struct.
func rawResult(raw []byte, err error) (*mcp.CallToolResult, error) {
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if len(raw) == 0 {
		return jsonResult(map[string]string{
			"status": "created",
			"note":   "Hevy returned an empty response body. Use the corresponding list/get tool to confirm.",
		})
	}
	return mcp.NewToolResultText(string(raw)), nil
}

func pageArgs(req mcp.CallToolRequest) (int, int) {
	return req.GetInt("page", 1), req.GetInt("pageSize", 10)
}

// paginationOpts returns page/pageSize options capped at 10, Hevy's limit on
// every list endpoint except /v1/exercise_templates — see paginationOptsMax.
func paginationOpts() []mcp.ToolOption {
	return paginationOptsMax(10)
}

// paginationOptsMax is paginationOpts with an explicit pageSize ceiling. The
// default stays 10 regardless of the ceiling so a list call can't dump a huge
// page into the model's context unless it asks for one.
func paginationOptsMax(max int) []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithNumber("page",
			mcp.Description("1-based page number"),
			mcp.DefaultNumber(1),
			mcp.Min(1),
		),
		mcp.WithNumber("pageSize",
			mcp.Description(fmt.Sprintf("Results per page (Hevy max is %d)", max)),
			mcp.DefaultNumber(10),
			mcp.Min(1),
			mcp.Max(float64(max)),
		),
	}
}

// Tool annotations. mcp.NewTool defaults every tool to readOnlyHint=false and
// destructiveHint=true, so unless a tool says otherwise clients like claude.ai
// file it under "write/delete" — reads included. Every tool must carry one of
// these (pinned by TestTools_AnnotationsMatchToolKind).
var (
	// readsHevy: a GET against the Hevy API.
	readsHevy = mcp.WithToolAnnotation(mcp.ToolAnnotation{
		ReadOnlyHint:    mcp.ToBoolPtr(true),
		DestructiveHint: mcp.ToBoolPtr(false),
		IdempotentHint:  mcp.ToBoolPtr(true),
		OpenWorldHint:   mcp.ToBoolPtr(true),
	})
	// createsInHevy: a POST. Additive, but each call makes another object,
	// and Hevy has no DELETE endpoints to undo it.
	createsInHevy = mcp.WithToolAnnotation(mcp.ToolAnnotation{
		ReadOnlyHint:    mcp.ToBoolPtr(false),
		DestructiveHint: mcp.ToBoolPtr(false),
		IdempotentHint:  mcp.ToBoolPtr(false),
		OpenWorldHint:   mcp.ToBoolPtr(true),
	})
	// overwritesInHevy: a PUT. Stored values are replaced (even the merging
	// body-measurement update overwrites the metrics it is given).
	overwritesInHevy = mcp.WithToolAnnotation(mcp.ToolAnnotation{
		ReadOnlyHint:    mcp.ToBoolPtr(false),
		DestructiveHint: mcp.ToBoolPtr(true),
		IdempotentHint:  mcp.ToBoolPtr(true),
		OpenWorldHint:   mcp.ToBoolPtr(true),
	})
)

func registerWorkouts(s *server.MCPServer, factory ClientFactory) {
	listOpts := append([]mcp.ToolOption{
		mcp.WithDescription("List the authenticated user's completed workouts, ordered by start time descending. Use page/pageSize to paginate."),
		readsHevy,
	}, paginationOpts()...)
	s.AddTool(mcp.NewTool("hevy_list_workouts", listOpts...),
		wrap(factory, func(c *hevy.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			page, size := pageArgs(req)
			return hevyResult(c.ListWorkouts(page, size))
		}))

	s.AddTool(mcp.NewTool("hevy_create_workout",
		mcp.WithDescription("Create a new completed workout. Returns Hevy's raw response body."+indexHint),
		createsInHevy,
		mcp.WithInputSchema[createWorkoutArgs](),
	),
		wrap(factory, func(c *hevy.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args createWorkoutArgs
			if err := req.BindArguments(&args); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return rawResult(c.DoRaw("POST", "/v1/workouts", nil, &hevy.CreateWorkoutRequest{Workout: args.Workout}))
		}))

	s.AddTool(mcp.NewTool("hevy_get_workout",
		mcp.WithDescription("Get a single workout by ID."),
		readsHevy,
		mcp.WithString("id", mcp.Required(), mcp.Description("Workout ID (UUID).")),
	),
		wrap(factory, func(c *hevy.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			id, err := req.RequireString("id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return hevyResult(c.GetWorkout(id))
		}))

	s.AddTool(mcp.NewTool("hevy_update_workout",
		mcp.WithDescription("Update an existing workout by ID. The update replaces the whole workout, so send the complete exercise and set list — anything you leave out is gone. Note `is_private`: it is always sent, so a workout that was private becomes public unless you pass is_private=true (Hevy's read endpoints don't return the current value, so this tool cannot preserve it for you). Returns Hevy's raw response body."+indexHint),
		overwritesInHevy,
		mcp.WithInputSchema[updateWorkoutArgs](),
	),
		wrap(factory, func(c *hevy.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args updateWorkoutArgs
			if err := req.BindArguments(&args); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			path := "/v1/workouts/" + args.ID
			return rawResult(c.DoRaw("PUT", path, nil, &hevy.UpdateWorkoutRequest{Workout: args.Workout}))
		}))

	s.AddTool(mcp.NewTool("hevy_get_workout_count",
		mcp.WithDescription("Return the total number of workouts logged by the authenticated user."),
		readsHevy,
	),
		wrap(factory, func(c *hevy.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			n, err := c.GetWorkoutCount()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return jsonResult(map[string]int{"workout_count": n})
		}))

	eventOpts := append([]mcp.ToolOption{
		mcp.WithDescription("List workout updated/deleted events, useful for incremental sync. `since` is an ISO 8601 timestamp to filter from."),
		readsHevy,
		mcp.WithString("since",
			mcp.Description("ISO 8601 timestamp; only events at or after this time are returned."),
		),
	}, paginationOpts()...)
	s.AddTool(mcp.NewTool("hevy_get_workout_events", eventOpts...),
		wrap(factory, func(c *hevy.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			page, size := pageArgs(req)
			since := req.GetString("since", "")
			return hevyResult(c.GetWorkoutEvents(page, size, since))
		}))
}

func registerRoutines(s *server.MCPServer, factory ClientFactory) {
	listOpts := append([]mcp.ToolOption{
		mcp.WithDescription("List the authenticated user's routines (workout templates)."),
		readsHevy,
	}, paginationOpts()...)
	s.AddTool(mcp.NewTool("hevy_list_routines", listOpts...),
		wrap(factory, func(c *hevy.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			page, size := pageArgs(req)
			return hevyResult(c.ListRoutines(page, size))
		}))

	s.AddTool(mcp.NewTool("hevy_create_routine",
		mcp.WithDescription("Create a new routine. Set `folder_id` to place the routine in a folder, or null to use the default 'My Routines' folder. Returns Hevy's raw response body."+indexHint),
		createsInHevy,
		mcp.WithInputSchema[createRoutineArgs](),
	),
		wrap(factory, func(c *hevy.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args createRoutineArgs
			if err := req.BindArguments(&args); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return rawResult(c.DoRaw("POST", "/v1/routines", nil, &hevy.CreateRoutineRequest{Routine: args.Routine}))
		}))

	s.AddTool(mcp.NewTool("hevy_get_routine",
		mcp.WithDescription("Get a single routine by ID."),
		readsHevy,
		mcp.WithString("id", mcp.Required(), mcp.Description("Routine ID.")),
	),
		wrap(factory, func(c *hevy.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			id, err := req.RequireString("id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return hevyResult(c.GetRoutine(id))
		}))

	s.AddTool(mcp.NewTool("hevy_update_routine",
		mcp.WithDescription("Update an existing routine. The update replaces the routine's exercises wholesale, so send the full exercise list. Folder placement is left untouched unless you ask: set `folder_id` to move the routine into a folder, or `move_to_my_routines: true` to move it out into the default 'My Routines' list. Returns Hevy's raw response body."+indexHint),
		overwritesInHevy,
		mcp.WithInputSchema[updateRoutineArgs](),
	),
		wrap(factory, func(c *hevy.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args updateRoutineArgs
			if err := req.BindArguments(&args); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			body, err := args.Routine.wire()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			path := "/v1/routines/" + args.ID
			return rawResult(c.DoRaw("PUT", path, nil, &hevy.UpdateRoutineRequest{Routine: body}))
		}))
}

func registerExerciseTemplates(s *server.MCPServer, factory ClientFactory) {
	// This endpoint is the one exception to Hevy's 10-per-page limit: it
	// documents and accepts pageSize up to 100 (verified against the live
	// API 2026-10-19). Handy for walking the ~460-entry public catalog.
	listOpts := append([]mcp.ToolOption{
		mcp.WithDescription("List Hevy exercise templates (the catalog of exercises). Includes both built-in and user custom exercises. Unlike the other list tools, pageSize goes up to 100 here."),
		readsHevy,
	}, paginationOptsMax(100)...)
	s.AddTool(mcp.NewTool("hevy_list_exercise_templates", listOpts...),
		wrap(factory, func(c *hevy.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			page, size := pageArgs(req)
			return hevyResult(c.ListExerciseTemplates(page, size))
		}))

	s.AddTool(mcp.NewTool("hevy_create_exercise_template",
		mcp.WithDescription("Create a custom exercise template. Returns Hevy's raw response body."),
		createsInHevy,
		mcp.WithInputSchema[createExerciseTemplateArgs](),
	),
		wrap(factory, func(c *hevy.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args createExerciseTemplateArgs
			if err := req.BindArguments(&args); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return rawResult(c.DoRaw("POST", "/v1/exercise_templates", nil, &hevy.CreateExerciseTemplateRequest{Exercise: args.Exercise}))
		}))

	s.AddTool(mcp.NewTool("hevy_get_exercise_template",
		mcp.WithDescription("Get a single exercise template by ID."),
		readsHevy,
		mcp.WithString("id", mcp.Required(), mcp.Description("Exercise template ID.")),
	),
		wrap(factory, func(c *hevy.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			id, err := req.RequireString("id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return hevyResult(c.GetExerciseTemplate(id))
		}))
}

// registerExerciseSearch adds a search tool over Hevy's exercise-template
// catalog. Each call pages through /v1/exercise_templates (100 per page, ~5
// requests for the full catalog) and filters server-side, so only the matches
// reach the model's context.
func registerExerciseSearch(s *server.MCPServer, factory ClientFactory) {
	s.AddTool(mcp.NewTool("hevy_search_exercise_templates",
		mcp.WithDescription("Search Hevy exercise templates — built-in and the user's custom ones — by title substring, muscle group, or exercise type. Filtering happens server-side, so prefer this over paging hevy_list_exercise_templates to find an exercise. `truncated: true` means the limit was hit and more matches may exist."),
		readsHevy,
		mcp.WithString("query",
			mcp.Description("Case-insensitive substring match on exercise title (e.g. 'squat', 'pendlay').")),
		mcp.WithString("muscle_group",
			mcp.Description("Match against Hevy's muscle-group enum (e.g. chest, lats, quadriceps, biceps, cardio). Matches primary OR secondary by default; set muscle_group_primary_only=true to restrict to the primary.")),
		mcp.WithBoolean("muscle_group_primary_only",
			mcp.Description("When true, muscle_group only matches an exercise's primary muscle group. Ignored when muscle_group is unset."),
			mcp.DefaultBool(false),
		),
		mcp.WithString("exercise_type",
			mcp.Description("Match against Hevy's exercise-type enum (e.g. weight_reps, reps_only, duration, distance_duration, bodyweight_weighted, bodyweight_assisted).")),
		mcp.WithNumber("limit",
			mcp.Description("Max results to return."),
			mcp.DefaultNumber(20),
			mcp.Min(1),
			mcp.Max(200),
		),
	),
		wrap(factory, func(c *hevy.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			res, err := exercises.Search(c, exercises.SearchParams{
				Query:                  req.GetString("query", ""),
				MuscleGroup:            req.GetString("muscle_group", ""),
				MuscleGroupPrimaryOnly: req.GetBool("muscle_group_primary_only", false),
				ExerciseType:           req.GetString("exercise_type", ""),
				Limit:                  req.GetInt("limit", 20),
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return jsonResult(map[string]any{
				"count":              len(res.Templates),
				"truncated":          res.Truncated,
				"exercise_templates": res.Templates,
			})
		}))
}

func registerRoutineFolders(s *server.MCPServer, factory ClientFactory) {
	listOpts := append([]mcp.ToolOption{
		mcp.WithDescription("List routine folders for organizing routines."),
		readsHevy,
	}, paginationOpts()...)
	s.AddTool(mcp.NewTool("hevy_list_routine_folders", listOpts...),
		wrap(factory, func(c *hevy.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			page, size := pageArgs(req)
			return hevyResult(c.ListRoutineFolders(page, size))
		}))

	s.AddTool(mcp.NewTool("hevy_create_routine_folder",
		mcp.WithDescription("Create a routine folder. Returns Hevy's raw response body."),
		createsInHevy,
		mcp.WithString("title", mcp.Required(), mcp.Description("Folder title.")),
	),
		wrap(factory, func(c *hevy.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			title, err := req.RequireString("title")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			body := &hevy.CreateRoutineFolderRequest{RoutineFolder: hevy.RoutineFolderCreate{Title: title}}
			return rawResult(c.DoRaw("POST", "/v1/routine_folders", nil, body))
		}))

	s.AddTool(mcp.NewTool("hevy_get_routine_folder",
		mcp.WithDescription("Get a single routine folder by numeric ID."),
		readsHevy,
		mcp.WithNumber("id", mcp.Required(), mcp.Description("Folder ID (integer).")),
	),
		wrap(factory, func(c *hevy.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			id, err := req.RequireInt("id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return hevyResult(c.GetRoutineFolder(id))
		}))
}

func registerExerciseHistory(s *server.MCPServer, factory ClientFactory) {
	s.AddTool(mcp.NewTool("hevy_get_exercise_history",
		mcp.WithDescription("Get historical set data for one exercise template, optionally bounded by start_date / end_date (ISO 8601). Returns Hevy's raw response body."),
		readsHevy,
		mcp.WithString("id", mcp.Required(), mcp.Description("Exercise template ID.")),
		mcp.WithString("start_date", mcp.Description("Inclusive lower bound (ISO 8601, e.g. 2024-01-01).")),
		mcp.WithString("end_date", mcp.Description("Inclusive upper bound (ISO 8601).")),
	),
		wrap(factory, func(c *hevy.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			id, err := req.RequireString("id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			q := url.Values{}
			if start := req.GetString("start_date", ""); start != "" {
				q.Set("start_date", start)
			}
			if end := req.GetString("end_date", ""); end != "" {
				q.Set("end_date", end)
			}
			return rawResult(c.DoRaw("GET", "/v1/exercise_history/"+url.PathEscape(id), q, nil))
		}))
}

func registerBodyMeasurements(s *server.MCPServer, factory ClientFactory) {
	listOpts := append([]mcp.ToolOption{
		mcp.WithDescription("List logged body measurements."),
		readsHevy,
	}, paginationOpts()...)
	s.AddTool(mcp.NewTool("hevy_list_body_measurements", listOpts...),
		wrap(factory, func(c *hevy.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			page, size := pageArgs(req)
			return hevyResult(c.ListBodyMeasurements(page, size))
		}))

	s.AddTool(mcp.NewTool("hevy_create_body_measurement",
		mcp.WithDescription("Create a body measurement entry for a date that has none yet. Hevy returns HTTP 409 if an entry already exists for that date — use hevy_update_body_measurement to change an existing one. Returns Hevy's raw response body."),
		createsInHevy,
		mcp.WithInputSchema[createBodyMeasurementArgs](),
	),
		wrap(factory, func(c *hevy.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args createBodyMeasurementArgs
			if err := req.BindArguments(&args); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return rawResult(c.DoRaw("POST", "/v1/body_measurements", nil, &args.Measurement))
		}))

	s.AddTool(mcp.NewTool("hevy_get_body_measurement",
		mcp.WithDescription("Get a body measurement by date (YYYY-MM-DD)."),
		readsHevy,
		mcp.WithString("date", mcp.Required(), mcp.Description("Date in YYYY-MM-DD.")),
	),
		wrap(factory, func(c *hevy.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			date, err := req.RequireString("date")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return hevyResult(c.GetBodyMeasurement(date))
		}))

	s.AddTool(mcp.NewTool("hevy_update_body_measurement",
		mcp.WithDescription("Update the body measurement entry for an existing date. Send only the metrics you want to change: Hevy's endpoint is a destructive full replace, so this tool reads the current row first and merges your fields over it. Clearing a metric is not possible through this tool. Use hevy_create_body_measurement for a date that has no entry yet. Returns Hevy's raw response body."),
		overwritesInHevy,
		mcp.WithInputSchema[updateBodyMeasurementArgs](),
	),
		wrap(factory, func(c *hevy.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args updateBodyMeasurementArgs
			if err := req.BindArguments(&args); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			path := "/v1/body_measurements/" + url.PathEscape(args.Date)

			// Read-modify-write: PUT replaces the whole row and silently
			// ignores unknown keys, so sending the caller's fields alone
			// would wipe every metric they didn't mention.
			current, err := c.DoRaw("GET", path, nil, nil)
			if err != nil {
				var apiErr *hevy.APIError
				if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
					return mcp.NewToolResultError(fmt.Sprintf("no body measurement exists for %s, so there is nothing to merge into — use hevy_create_body_measurement to add one", args.Date)), nil
				}
				return mcp.NewToolResultError(err.Error()), nil
			}
			body, err := hevy.MergeBodyMeasurement(current, &args.Measurement)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return rawResult(c.DoRaw("PUT", path, nil, json.RawMessage(body)))
		}))
}

func registerUser(s *server.MCPServer, factory ClientFactory) {
	s.AddTool(mcp.NewTool("hevy_get_user_info",
		mcp.WithDescription("Return profile information about the authenticated Hevy user."),
		readsHevy,
	),
		wrap(factory, func(c *hevy.Client, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return hevyResult(c.GetUserInfo())
		}))
}

// decode round-trips raw JSON-decoded data into a typed struct.
func decode(raw any, out any) error {
	if raw == nil {
		return fmt.Errorf("missing required object argument")
	}
	buf, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("encode argument: %w", err)
	}
	if err := json.Unmarshal(buf, out); err != nil {
		return fmt.Errorf("decode argument: %w", err)
	}
	return nil
}
