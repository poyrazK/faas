package mcphosting

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
)

const (
	taskInboxExtension    = "io.gregale/task-inbox"
	DefaultTaskInboxLimit = 20
	MaxTaskInboxLimit     = 50
)

var taskInboxCursorPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}\.[A-Za-z0-9_-]{43}$`)

// TaskInboxQuery requests one page of retained metadata for the current caller.
type TaskInboxQuery struct {
	Status string
	Limit  int
	Cursor string
}

func (q TaskInboxQuery) Validate() error {
	switch q.Status {
	case "", "working", "input_required", "completed", "failed", "cancelled":
	default:
		return errors.New("MCP Task inbox status must be working, input_required, completed, failed or cancelled")
	}
	if q.Limit < 1 || q.Limit > MaxTaskInboxLimit {
		return errors.New("MCP Task inbox limit must be between 1 and 50")
	}
	if q.Cursor != "" && !taskInboxCursorPattern.MatchString(q.Cursor) {
		return errors.New("invalid MCP Task inbox cursor")
	}
	return nil
}

// TaskInboxPage carries metadata only. A nonempty cursor starts the next page.
type TaskInboxPage struct {
	Tasks      []Task
	NextCursor string
}

func (c *Client) ListTaskInbox(ctx context.Context, query TaskInboxQuery) (TaskInboxPage, Exchange, error) {
	if err := query.Validate(); err != nil {
		return TaskInboxPage{}, Exchange{}, err
	}
	if c.Token == "" {
		return TaskInboxPage{}, Exchange{}, errors.New("MCP Task inbox requires a client credential")
	}
	if err := c.requireTasksCapability(ctx); err != nil {
		return TaskInboxPage{}, Exchange{}, err
	}
	if !c.Capabilities.HasExtension(taskInboxExtension) {
		return TaskInboxPage{}, Exchange{}, errors.New("MCP server does not advertise a Task inbox")
	}
	params := map[string]any{"limit": query.Limit}
	if query.Status != "" {
		params["status"] = query.Status
	}
	if query.Cursor != "" {
		params["cursor"] = query.Cursor
	}
	x, err := c.request(ctx, "gregale/tasks/inbox", params, nil, false)
	body := x.Result
	x.Result = nil
	if err != nil {
		return TaskInboxPage{}, x, err
	}
	page, err := decodeTaskInbox(body, query)
	return page, x, err
}

func decodeTaskInbox(body json.RawMessage, query TaskInboxQuery) (TaskInboxPage, error) {
	var wire struct {
		ResultType string            `json:"resultType"`
		Tasks      []json.RawMessage `json:"tasks"`
		NextCursor string            `json:"nextCursor"`
	}
	if json.Unmarshal(body, &wire) != nil || wire.ResultType != "complete" ||
		wire.Tasks == nil || len(wire.Tasks) > query.Limit {
		return TaskInboxPage{}, errors.New("invalid MCP Task inbox page")
	}
	if wire.NextCursor != "" && (len(wire.Tasks) != query.Limit || wire.NextCursor == query.Cursor ||
		!taskInboxCursorPattern.MatchString(wire.NextCursor)) {
		return TaskInboxPage{}, errors.New("invalid MCP Task inbox continuation")
	}
	page := TaskInboxPage{Tasks: make([]Task, 0, len(wire.Tasks)), NextCursor: wire.NextCursor}
	seen := make(map[string]bool, len(wire.Tasks))
	for _, raw := range wire.Tasks {
		task, err := decodeTaskSnapshot(raw, "complete", "", true)
		if err != nil || seen[task.TaskID] || (query.Status != "" && query.Status != task.Status) {
			return TaskInboxPage{}, errors.New("invalid MCP Task inbox metadata")
		}
		seen[task.TaskID] = true
		page.Tasks = append(page.Tasks, task)
	}
	return page, nil
}
