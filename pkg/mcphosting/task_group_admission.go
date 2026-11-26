package mcphosting

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

const (
	taskGroupOwnerExtension = "io.gregale/task-group-owner"
	taskGroupOwnerMethod    = "gregale/tasks/group-owner"
	taskGroupSubmitMethod   = "gregale/tasks/submit-bound"
)

var mcpTaskGroupDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// TaskGroupOwner is an endpoint-issued binding to the caller's OAuth identity.
type TaskGroupOwner struct {
	GroupID string `json:"groupId"`
	Binding string `json:"binding"`
}

// TaskSubmissionCapabilities is the capabilities object carried by bound
// admission. Durable clients also include it in the request fingerprint.
func TaskSubmissionCapabilities(deferFormInput bool) map[string]any {
	caps := map[string]any{"extensions": map[string]any{TasksExtensionID: map[string]any{}}}
	if deferFormInput {
		caps["elicitation"] = map[string]any{"form": map[string]any{}}
	}
	return caps
}

func (c *Client) requireTaskGroupAdmission(ctx context.Context) error {
	if c.Token == "" {
		return errors.New("bound MCP Task submission requires a client token")
	}
	if err := c.requireTasksCapability(ctx); err != nil {
		return err
	}
	if !c.Capabilities.HasExtension(taskGroupOwnerExtension) ||
		!c.Capabilities.HasExtension(taskSubmissionExtension) ||
		!c.Capabilities.HasExtension("io.gregale/task-submission-lookup") {
		return errors.New("MCP server does not advertise recoverable owner-bound Task submission")
	}
	return nil
}

// BindTaskGroup obtains the caller's private owner binding for a new group.
func (c *Client) BindTaskGroup(ctx context.Context, groupID string) (TaskGroupOwner, error) {
	binding, err := c.readTaskGroupBinding(ctx, groupID, nil)
	if err != nil {
		return TaskGroupOwner{}, err
	}
	return TaskGroupOwner{GroupID: groupID, Binding: binding}, nil
}

// VerifyTaskGroupTool checks the current owner's permission before a first send.
func (c *Client) VerifyTaskGroupTool(ctx context.Context, owner TaskGroupOwner, toolName string) error {
	if !mcpTaskGroupDigest.MatchString(owner.Binding) || strings.TrimSpace(toolName) == "" {
		return errors.New("invalid MCP Task group binding or tool")
	}
	binding, err := c.readTaskGroupBinding(ctx, owner.GroupID, map[string]any{"binding": owner.Binding, "toolName": toolName})
	if err != nil {
		return err
	}
	if binding != owner.Binding {
		return errors.New("MCP server did not confirm the saved group owner binding")
	}
	return nil
}

func (c *Client) readTaskGroupBinding(ctx context.Context, groupID string, params map[string]any) (string, error) {
	if !mcpTaskGroupDigest.MatchString(groupID) {
		return "", errors.New("invalid MCP Task group ID")
	}
	if err := c.requireTaskGroupAdmission(ctx); err != nil {
		return "", err
	}
	if params == nil {
		params = make(map[string]any)
	}
	params["groupId"] = groupID
	x, err := c.request(ctx, taskGroupOwnerMethod, params, nil, false)
	if err != nil {
		return "", err
	}
	var result struct {
		ResultType string `json:"resultType"`
		Binding    string `json:"binding"`
	}
	if x.SessionID != "" || json.Unmarshal(x.Result, &result) != nil ||
		result.ResultType != "complete" || !mcpTaskGroupDigest.MatchString(result.Binding) {
		return "", errors.New("invalid MCP Task group owner acknowledgement")
	}
	return result.Binding, nil
}

// SubmitBoundTask sends a handle-only admission using a caller-retained key.
// Callers persist the attempt before invoking this method.
func (c *Client) SubmitBoundTask(ctx context.Context, owner TaskGroupOwner, key, tool string, args map[string]any, deferFormInput bool) (Task, Exchange, error) {
	if err := validateTaskSubmissionKey(key); err != nil {
		return Task{}, Exchange{}, err
	}
	if !mcpTaskGroupDigest.MatchString(owner.GroupID) || !mcpTaskGroupDigest.MatchString(owner.Binding) || strings.TrimSpace(tool) == "" {
		return Task{}, Exchange{}, errors.New("invalid MCP Task group binding or tool")
	}
	if err := c.requireTaskGroupAdmission(ctx); err != nil {
		return Task{}, Exchange{}, err
	}
	x, err := c.request(ctx, taskGroupSubmitMethod, map[string]any{
		"name": tool, "arguments": args, "_meta": map[string]any{
			taskGroupOwnerExtension:                      owner,
			taskSubmissionKeyMeta:                        key,
			"io.modelcontextprotocol/clientCapabilities": TaskSubmissionCapabilities(deferFormInput),
		},
	}, nil, false)
	if err != nil {
		return Task{}, x, err
	}
	var result struct {
		IsError bool `json:"isError"`
	}
	if x.SessionID != "" || json.Unmarshal(x.Result, &result) != nil || result.IsError {
		return Task{}, x, errors.New("MCP server did not confirm bound Task admission")
	}
	task, err := decodeTask(x.Result, "task", "")
	return task, x, err
}
