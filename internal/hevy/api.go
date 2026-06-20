package hevy

import (
	"fmt"
	"net/url"
	"strconv"
)

func pageQuery(page, pageSize int) url.Values {
	q := url.Values{}
	if page > 0 {
		q.Set("page", strconv.Itoa(page))
	}
	if pageSize > 0 {
		q.Set("pageSize", strconv.Itoa(pageSize))
	}
	return q
}

// Workouts

func (c *Client) ListWorkouts(page, pageSize int) (*PaginatedWorkouts, error) {
	out := &PaginatedWorkouts{}
	if err := c.do("GET", "/v1/workouts", pageQuery(page, pageSize), nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) CreateWorkout(req *CreateWorkoutRequest) (*Workout, error) {
	out := &Workout{}
	if err := c.doUnwrap("POST", "/v1/workouts", nil, req, "workout", out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) GetWorkout(id string) (*Workout, error) {
	out := &Workout{}
	if err := c.doUnwrap("GET", "/v1/workouts/"+url.PathEscape(id), nil, nil, "workout", out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) UpdateWorkout(id string, req *UpdateWorkoutRequest) (*Workout, error) {
	out := &Workout{}
	if err := c.doUnwrap("PUT", "/v1/workouts/"+url.PathEscape(id), nil, req, "workout", out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) GetWorkoutCount() (int, error) {
	out := &WorkoutCount{}
	if err := c.do("GET", "/v1/workouts/count", nil, nil, out); err != nil {
		return 0, err
	}
	return out.WorkoutCount, nil
}

func (c *Client) GetWorkoutEvents(page, pageSize int, since string) (*PaginatedWorkoutEvents, error) {
	q := pageQuery(page, pageSize)
	if since != "" {
		q.Set("since", since)
	}
	out := &PaginatedWorkoutEvents{}
	if err := c.do("GET", "/v1/workouts/events", q, nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

// Routines

func (c *Client) ListRoutines(page, pageSize int) (*PaginatedRoutines, error) {
	out := &PaginatedRoutines{}
	if err := c.do("GET", "/v1/routines", pageQuery(page, pageSize), nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) CreateRoutine(req *CreateRoutineRequest) (*Routine, error) {
	out := &Routine{}
	if err := c.doUnwrap("POST", "/v1/routines", nil, req, "routine", out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) GetRoutine(id string) (*Routine, error) {
	out := &Routine{}
	if err := c.doUnwrap("GET", "/v1/routines/"+url.PathEscape(id), nil, nil, "routine", out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) UpdateRoutine(id string, req *UpdateRoutineRequest) (*Routine, error) {
	out := &Routine{}
	if err := c.doUnwrap("PUT", "/v1/routines/"+url.PathEscape(id), nil, req, "routine", out); err != nil {
		return nil, err
	}
	return out, nil
}

// Exercise templates

func (c *Client) ListExerciseTemplates(page, pageSize int) (*PaginatedExerciseTemplates, error) {
	out := &PaginatedExerciseTemplates{}
	if err := c.do("GET", "/v1/exercise_templates", pageQuery(page, pageSize), nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) CreateExerciseTemplate(req *CreateExerciseTemplateRequest) (string, error) {
	out := &CreatedTemplateID{}
	if err := c.do("POST", "/v1/exercise_templates", nil, req, out); err != nil {
		return "", err
	}
	return out.ID.String(), nil
}

func (c *Client) GetExerciseTemplate(id string) (*ExerciseTemplate, error) {
	out := &ExerciseTemplate{}
	if err := c.doUnwrap("GET", "/v1/exercise_templates/"+url.PathEscape(id), nil, nil, "exercise_template", out); err != nil {
		return nil, err
	}
	return out, nil
}

// Routine folders

func (c *Client) ListRoutineFolders(page, pageSize int) (*PaginatedRoutineFolders, error) {
	out := &PaginatedRoutineFolders{}
	if err := c.do("GET", "/v1/routine_folders", pageQuery(page, pageSize), nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) CreateRoutineFolder(title string) (*RoutineFolder, error) {
	req := &CreateRoutineFolderRequest{RoutineFolder: RoutineFolderCreate{Title: title}}
	out := &RoutineFolder{}
	if err := c.doUnwrap("POST", "/v1/routine_folders", nil, req, "routine_folder", out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) GetRoutineFolder(id int) (*RoutineFolder, error) {
	out := &RoutineFolder{}
	path := fmt.Sprintf("/v1/routine_folders/%d", id)
	if err := c.doUnwrap("GET", path, nil, nil, "routine_folder", out); err != nil {
		return nil, err
	}
	return out, nil
}

// Exercise history

func (c *Client) GetExerciseHistory(templateID, startDate, endDate string) (*ExerciseHistoryResponse, error) {
	q := url.Values{}
	if startDate != "" {
		q.Set("start_date", startDate)
	}
	if endDate != "" {
		q.Set("end_date", endDate)
	}
	out := &ExerciseHistoryResponse{}
	if err := c.do("GET", "/v1/exercise_history/"+url.PathEscape(templateID), q, nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

// Body measurements

func (c *Client) ListBodyMeasurements(page, pageSize int) (*PaginatedBodyMeasurements, error) {
	out := &PaginatedBodyMeasurements{}
	if err := c.do("GET", "/v1/body_measurements", pageQuery(page, pageSize), nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) CreateBodyMeasurement(m *BodyMeasurement) error {
	return c.do("POST", "/v1/body_measurements", nil, m, nil)
}

func (c *Client) GetBodyMeasurement(date string) (*BodyMeasurement, error) {
	out := &BodyMeasurement{}
	if err := c.do("GET", "/v1/body_measurements/"+url.PathEscape(date), nil, nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) UpdateBodyMeasurement(date string, m *BodyMeasurement) error {
	return c.do("PUT", "/v1/body_measurements/"+url.PathEscape(date), nil, m, nil)
}

// User

func (c *Client) GetUserInfo() (*UserInfo, error) {
	out := &UserInfo{}
	// Hevy wraps the user info response under `data`:
	// `{"data": {"id":..., "name":..., "url":...}}`
	if err := c.doUnwrap("GET", "/v1/user/info", nil, nil, "data", out); err != nil {
		return nil, err
	}
	return out, nil
}
