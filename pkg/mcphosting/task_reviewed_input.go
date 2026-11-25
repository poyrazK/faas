package mcphosting

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
)

const taskInputReviewExtension = "io.gregale/task-input-review"

var (
	ErrTaskInputReviewUnsupported = errors.New("MCP server does not advertise reviewed Task input by ID")
	ErrTaskInputReviewStale       = errors.New("MCP Task input review no longer matches the pending forms")
	taskInputReviewRevision       = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// TaskInputReview describes the currently outstanding forms and their server revision.
type TaskInputReview struct {
	Task     Task
	Revision string
}

func (c *Client) requireTaskInputReview(ctx context.Context, taskID string) error {
	if err := validateTaskID(taskID); err != nil {
		return err
	}
	if c.Token == "" {
		return errors.New("MCP Task input review requires a client credential")
	}
	if err := c.requireTasksCapability(ctx); err != nil {
		return err
	}
	if !c.Capabilities.HasExtension(taskInputReviewExtension) {
		return ErrTaskInputReviewUnsupported
	}
	return nil
}

// GetTaskInputReview returns the retained Task's metadata and any reviewable
// pending forms, with an opaque revision for a conditional update.
func (c *Client) GetTaskInputReview(ctx context.Context, taskID string) (TaskInputReview, Exchange, error) {
	if err := c.requireTaskInputReview(ctx, taskID); err != nil {
		return TaskInputReview{}, Exchange{}, err
	}
	x, err := c.taskRequest(ctx, "gregale/tasks/input-review", taskID, nil)
	body := x.Result
	x.Result = nil
	if err != nil {
		return TaskInputReview{}, x, err
	}
	var wire struct {
		ResultType    string                      `json:"resultType"`
		Task          json.RawMessage             `json:"task"`
		Revision      string                      `json:"reviewRevision"`
		InputRequests map[string]TaskInputRequest `json:"inputRequests"`
	}
	if json.Unmarshal(body, &wire) != nil || wire.ResultType != "complete" {
		return TaskInputReview{}, x, errors.New("invalid MCP Task input review response")
	}
	task, err := decodeTaskSnapshot(wire.Task, "complete", taskID, true)
	if err != nil {
		return TaskInputReview{}, x, err
	}
	if wire.Revision != "" {
		task.InputRequests = wire.InputRequests
		if !taskInputReviewRevision.MatchString(wire.Revision) {
			return TaskInputReview{}, x, errors.New("invalid MCP Task input review revision")
		}
		if _, err := task.InputForms(); err != nil {
			return TaskInputReview{}, x, err
		}
	} else if len(wire.InputRequests) != 0 {
		return TaskInputReview{}, x, errors.New("MCP Task input review did not include a revision")
	}
	return TaskInputReview{Task: task, Revision: wire.Revision}, x, nil
}

// ApplyTaskInputReview either applies exactly the reviewed forms or reports a stale
// revision. After an uncertain acknowledgement it checks the recorded answers once.
func (c *Client) ApplyTaskInputReview(ctx context.Context, taskID, revision string, responses map[string]InputResponse) (Task, error) {
	task, outcome, err := c.reviewedTaskInput(ctx, "gregale/tasks/respond-reviewed", taskID, revision, responses)
	switch {
	case err == nil && outcome == "applied":
		return task, nil
	case err == nil:
		return task, ErrTaskInputReviewStale
	case !errors.Is(err, errTaskInputReviewUncertain):
		return Task{}, err
	}
	return c.CheckTaskInputReview(ctx, taskID, revision, responses)
}

// CheckTaskInputReview checks evidence that the reviewed answers were recorded.
// The operation never submits an update.
func (c *Client) CheckTaskInputReview(ctx context.Context, taskID, revision string, responses map[string]InputResponse) (Task, error) {
	task, outcome, err := c.reviewedTaskInput(ctx, "gregale/tasks/check-reviewed-response", taskID, revision, responses)
	if err == nil && outcome == "applied" {
		return task, nil
	}
	if task.TaskID == "" {
		task.TaskID = taskID
	}
	return Task{}, &TaskInputUpdateUnconfirmedError{Task: task, cause: err}
}

var errTaskInputReviewUncertain = errors.New("MCP Task reviewed input update acknowledgement is uncertain")

func (c *Client) reviewedTaskInput(ctx context.Context, method, taskID, revision string, responses map[string]InputResponse) (Task, string, error) {
	if !taskInputReviewRevision.MatchString(revision) || len(responses) == 0 || len(responses) > maxInteractiveInputRequests {
		return Task{}, "", errors.New("invalid MCP Task input review revision or response count")
	}
	for id, response := range responses {
		if len(id) == 0 {
			return Task{}, "", errors.New("MCP Task input response requires a form ID")
		}
		if err := validateInputResponse(response); err != nil {
			return Task{}, "", err
		}
	}
	if err := c.requireTaskInputReview(ctx, taskID); err != nil {
		return Task{}, "", err
	}
	x, err := c.taskRequest(ctx, method, taskID, map[string]any{"reviewRevision": revision, "responses": responses})
	if err != nil {
		var rpc *RPCError
		if taskResponseUncertain(x.HTTPStatus, err) || (errors.As(err, &rpc) && rpc.Code == -32603) {
			return Task{}, "", errors.Join(errTaskInputReviewUncertain, err)
		}
		return Task{}, "", err
	}
	var wire struct {
		ResultType string          `json:"resultType"`
		Task       json.RawMessage `json:"task"`
		Outcome    string          `json:"outcome"`
	}
	want := "unconfirmed"
	if method == "gregale/tasks/respond-reviewed" {
		want = "stale"
	}
	if json.Unmarshal(x.Result, &wire) != nil || wire.ResultType != "complete" ||
		(wire.Outcome != "applied" && wire.Outcome != want) {
		return Task{}, "", errTaskInputReviewUncertain
	}
	task, err := decodeTaskSnapshot(wire.Task, "complete", taskID, true)
	if err != nil {
		return Task{}, "", errors.Join(errTaskInputReviewUncertain, err)
	}
	return task, wire.Outcome, nil
}
