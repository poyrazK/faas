package mcphosting

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	TasksExtensionID        = "io.modelcontextprotocol/tasks"
	defaultTaskPollInterval = 2 * time.Second
	minTaskPollInterval     = 250 * time.Millisecond
	maxTaskPollInterval     = 24 * time.Hour
	maxTaskIDBytes          = 256
	taskStreamReconnectBase = 250 * time.Millisecond
	taskStreamReconnectMax  = 5 * time.Second
	maxTaskStreamReconnects = 3
	taskReconcileInterval   = 30 * time.Second
)

var (
	// ErrTaskSubscriptionsUnsupported means the endpoint declined the requested Task subscription.
	ErrTaskSubscriptionsUnsupported = errors.New("MCP server does not support task status subscriptions")
	// ErrTaskSubscriptionClosed means the server ended the subscription.
	ErrTaskSubscriptionClosed = errors.New("MCP task subscription stream closed")
	// ErrTaskSubscriptionInterrupted means the stream ended unexpectedly or could not connect.
	ErrTaskSubscriptionInterrupted = errors.New("MCP task subscription stream interrupted")
)

// CallOptions controls opt-in behavior for a single MCP tool call. Tasks are
// never requested unless EnableTasks or WaitForTask is set.
type CallOptions struct {
	Progress    bool
	Responder   InputResponder
	EnableTasks bool
	WaitForTask bool
	// TaskSubmissionKey is a caller-retained UUIDv7 for a handle-only Task call.
	TaskSubmissionKey string
	// DeferFormInput lets a keyed, handle-only Task request bounded forms.
	// The caller supplies responses in a later request.
	DeferFormInput bool
	// CancelOnTimeout requests cooperative cancellation when the wait ends
	// because its context expires or is cancelled. It is opt-in so a resumed
	// wait can stop observing without stopping durable work.
	CancelOnTimeout bool
	OnTask          func(Task)
}

// Task is the Tasks extension's durable view of a tool call. Result and Error
// retain their JSON shape because the extension embeds the original result.
type Task struct {
	ResultType    string                      `json:"resultType,omitempty"`
	TaskID        string                      `json:"taskId"`
	Status        string                      `json:"status"`
	StatusMessage string                      `json:"statusMessage,omitempty"`
	CreatedAt     string                      `json:"createdAt"`
	LastUpdatedAt string                      `json:"lastUpdatedAt"`
	TTLMS         json.RawMessage             `json:"ttlMs"`
	PollInterval  *int64                      `json:"pollIntervalMs,omitempty"`
	InputRequests map[string]TaskInputRequest `json:"inputRequests,omitempty"`
	Result        json.RawMessage             `json:"result,omitempty"`
	Error         json.RawMessage             `json:"error,omitempty"`
}

// TaskInputUpdateUnconfirmedError carries metadata for a Task whose input
// update could not be confirmed by a subsequent read.
type TaskInputUpdateUnconfirmedError struct {
	Task  Task
	cause error
}

func (e *TaskInputUpdateUnconfirmedError) Error() string {
	return "MCP task input update outcome is unclear"
}

func (e *TaskInputUpdateUnconfirmedError) Unwrap() error { return e.cause }

// TaskCancellationUnconfirmedError means the cancellation request may have
// reached the server without a reliable acknowledgement. Its cause retains the original error.
type TaskCancellationUnconfirmedError struct {
	cause error
}

func (e *TaskCancellationUnconfirmedError) Error() string { return e.cause.Error() }

func (e *TaskCancellationUnconfirmedError) Unwrap() error { return e.cause }

// PollDelay returns the bounded delay before reading this task again.
func (t Task) PollDelay() time.Duration {
	if t.PollInterval == nil {
		return defaultTaskPollInterval
	}
	milliseconds := min(max(*t.PollInterval, minTaskPollInterval.Milliseconds()), maxTaskPollInterval.Milliseconds())
	return time.Duration(milliseconds) * time.Millisecond
}

// TaskInputRequest is a server-to-client request surfaced while a task waits
// for user input. The CLI currently supports form-mode elicitation only.
type TaskInputRequest struct {
	Method string `json:"method"`
	Params struct {
		Mode            string         `json:"mode"`
		Message         string         `json:"message"`
		RequestedSchema map[string]any `json:"requestedSchema"`
	} `json:"params"`
}

func (request *TaskInputRequest) UnmarshalJSON(data []byte) error {
	type plain TaskInputRequest
	var decoded plain
	decoded.Params.Mode = "form"
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	var fields struct {
		Params map[string]json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if mode, present := fields.Params["mode"]; present && bytes.Equal(bytes.TrimSpace(mode), []byte("null")) {
		return errors.New("MCP elicitation mode must be a string")
	}
	*request = TaskInputRequest(decoded)
	return nil
}

// InputForms returns the supported forms in the current Task snapshot.
func (t Task) InputForms() ([]InputRequest, error) {
	if t.Status != "input_required" {
		return nil, errors.New("MCP Task input review requires a Task waiting for form input")
	}
	return parseTaskInputRequests("task", t.InputRequests)
}

// GetTask reads one task. Streamable HTTP requests carry taskId in Mcp-Name
// as required by the Tasks extension routing contract.
func (c *Client) GetTask(ctx context.Context, taskID string) (Task, Exchange, error) {
	if err := validateTaskID(taskID); err != nil {
		return Task{}, Exchange{}, err
	}
	if err := c.requireTasksCapability(ctx); err != nil {
		return Task{}, Exchange{}, err
	}
	x, err := c.taskRequest(ctx, "tasks/get", taskID, nil)
	if err != nil {
		return Task{}, x, err
	}
	task, err := decodeTask(x.Result, "complete", taskID)
	if err != nil {
		return Task{}, x, err
	}
	return task, x, nil
}

// GetTaskBySubmissionKey reads metadata for a retained Task using its saved key.
func (c *Client) GetTaskBySubmissionKey(ctx context.Context, key string) (Task, Exchange, error) {
	if err := validateTaskSubmissionKey(key); err != nil {
		return Task{}, Exchange{}, err
	}
	if c.Token == "" {
		return Task{}, Exchange{}, errors.New("MCP Task submission lookup requires a client token")
	}
	if err := c.requireTasksCapability(ctx); err != nil {
		return Task{}, Exchange{}, err
	}
	if !c.Capabilities.HasExtension("io.gregale/task-submission-lookup") {
		return Task{}, Exchange{}, errors.New("MCP server does not advertise Task submission lookup")
	}
	x, err := c.request(ctx, "gregale/tasks/lookup", map[string]any{"submissionKey": key}, nil, false)
	body := x.Result
	x.Result = nil
	if err != nil {
		return Task{}, x, err
	}
	task, err := decodeTaskSnapshot(body, "complete", "", true)
	return task, x, err
}

// IsTaskSubmissionNotFound identifies a definitive lookup response. The Node
// starter uses a server-defined code because the modern MCP codec rewrites
// the older resource-not-found code used by the Go and Python starters.
func IsTaskSubmissionNotFound(err error) bool {
	var rpc *RPCError
	return errors.As(err, &rpc) && (rpc.Code == -32002 || rpc.Code == -32004)
}

func validateTaskSubmissionKey(value string) error {
	key, err := uuid.Parse(value)
	if err != nil || key.Version() != 7 || key.Variant() != uuid.RFC4122 || key.String() != value {
		return errors.New("MCP Task submission key must be a lowercase UUIDv7")
	}
	return nil
}

// WaitTask resumes polling for a task created by an earlier call. It uses the
// same bounded form responder and status callback as CallWithOptions.
func (c *Client) WaitTask(ctx context.Context, taskID string, options CallOptions) (Task, error) {
	if err := validateTaskID(taskID); err != nil {
		return Task{}, err
	}
	if c.Version != ProtocolVersion {
		return Task{}, fmt.Errorf("MCP Tasks requires protocol %s", ProtocolVersion)
	}
	if err := c.requireTasksCapability(ctx); err != nil {
		if options.CancelOnTimeout && (ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded)) {
			c.bestEffortCancelTask(ctx, taskID)
		}
		return Task{}, err
	}
	task, _, err := c.GetTask(ctx, taskID)
	if err != nil {
		if options.CancelOnTimeout && (ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded)) {
			c.bestEffortCancelTask(ctx, taskID)
		}
		return Task{}, err
	}
	return c.waitForTask(ctx, "task", task, options)
}

// RespondToTask answers the forms in a freshly retrieved Task in one update.
// It reads the Task once after the update to verify whether the forms are
// still pending, including when the update reply is lost.
func (c *Client) RespondToTask(ctx context.Context, task Task, responder InputResponder) (Task, error) {
	if task.Status != "input_required" {
		return task, nil
	}
	if responder == nil {
		return Task{}, errors.New("MCP task input update requires a responder")
	}
	forms, err := task.InputForms()
	if err != nil {
		return Task{}, err
	}
	responses := make(map[string]InputResponse, len(forms))
	for _, form := range forms {
		response, err := responder(ctx, form)
		if err != nil {
			return Task{}, err
		}
		if err := validateInputResponse(response); err != nil {
			return Task{}, err
		}
		responses[form.ID] = response
	}
	if uncertain, err := c.updateTask(ctx, task.TaskID, responses); err != nil && !uncertain {
		return Task{}, err
	}
	return c.reconcileTaskInputUpdate(ctx, task.TaskID, responses)
}

// CancelTask requests cooperative cancellation. The acknowledgement does not
// imply that the task has already reached its cancelled state.
func (c *Client) CancelTask(ctx context.Context, taskID string) (Exchange, error) {
	if err := validateTaskID(taskID); err != nil {
		return Exchange{}, err
	}
	if err := c.requireTasksCapability(ctx); err != nil {
		return Exchange{}, err
	}
	x, err := c.taskRequest(ctx, "tasks/cancel", taskID, nil)
	if err != nil {
		if taskResponseUncertain(x.HTTPStatus, err) {
			return x, &TaskCancellationUnconfirmedError{cause: err}
		}
		return x, err
	}
	var result struct {
		ResultType string `json:"resultType"`
	}
	if err := json.Unmarshal(x.Result, &result); err != nil || result.ResultType != "complete" {
		return x, &TaskCancellationUnconfirmedError{cause: errors.New("MCP task cancellation returned an invalid acknowledgement")}
	}
	return x, nil
}

func (c *Client) updateTask(ctx context.Context, taskID string, responses map[string]InputResponse) (uncertain bool, err error) {
	if len(responses) == 0 {
		return false, errors.New("MCP task update requires at least one input response")
	}
	if err := validateTaskID(taskID); err != nil {
		return false, err
	}
	if err := c.requireTasksCapability(ctx); err != nil {
		return false, err
	}
	x, err := c.taskRequest(ctx, "tasks/update", taskID, map[string]any{"inputResponses": responses})
	if err != nil {
		return taskResponseUncertain(x.HTTPStatus, err), err
	}
	var result struct {
		ResultType string `json:"resultType"`
	}
	if err := json.Unmarshal(x.Result, &result); err != nil || result.ResultType != "complete" {
		return true, errors.New("MCP task update returned an invalid acknowledgement")
	}
	return false, nil
}

func taskResponseUncertain(status int, err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	var transport *url.Error
	var rpc *RPCError
	return errors.As(err, &transport) || status == http.StatusRequestTimeout || status >= 500 ||
		status >= 200 && status < 300 && !errors.As(err, &rpc)
}

// Inspect the durable state once after an update that may have committed.
// The caller decides how to handle an answer that is still pending.
func (c *Client) reconcileTaskInputUpdate(ctx context.Context, taskID string, responses map[string]InputResponse) (Task, error) {
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.Canceled) {
			return Task{}, err
		}
		return Task{}, &TaskInputUpdateUnconfirmedError{Task: Task{TaskID: taskID}, cause: err}
	}
	current, exchange, err := c.GetTask(ctx, taskID)
	if err != nil {
		if taskResponseUncertain(exchange.HTTPStatus, err) || exchange.HTTPStatus == http.StatusTooManyRequests {
			return Task{}, &TaskInputUpdateUnconfirmedError{Task: Task{TaskID: taskID}, cause: err}
		}
		return Task{}, err
	}
	for id := range responses {
		if _, pending := current.InputRequests[id]; pending {
			snapshot := Task{TaskID: taskID, Status: current.Status, LastUpdatedAt: current.LastUpdatedAt}
			return current, &TaskInputUpdateUnconfirmedError{Task: snapshot}
		}
	}
	return current, nil
}

// listenTaskUpdate opens a fresh Tasks subscription and returns the first
// status snapshot that differs from current. Re-listening always includes a
// current snapshot, so callers can reconnect without a race-prone catch-up
// cursor.
func (c *Client) listenTaskUpdate(ctx context.Context, current Task) (Task, error) {
	var update Task
	err := c.listenTaskNotifications(ctx, []string{current.TaskID}, func(next Task) (bool, error) {
		if next.Status == current.Status && next.LastUpdatedAt == current.LastUpdatedAt {
			return false, nil
		}
		update = next
		return true, nil
	})
	return update, err
}

func (c *Client) listenTaskNotifications(ctx context.Context, taskIDs []string, notify func(Task) (bool, error)) error {
	requested := make(map[string]struct{}, len(taskIDs))
	for _, id := range taskIDs {
		if err := validateTaskID(id); err != nil {
			return err
		}
		requested[id] = struct{}{}
	}
	if c.Version != ProtocolVersion {
		return ErrTaskSubscriptionsUnsupported
	}
	if err := c.requireTasksCapability(ctx); err != nil {
		return err
	}

	c.nextID++
	id := c.nextID
	params := map[string]any{
		"notifications": map[string]any{"taskIds": taskIDs},
		"_meta": map[string]any{
			"io.modelcontextprotocol/protocolVersion": c.Version,
			"io.modelcontextprotocol/clientInfo":      map[string]string{"name": "gregale-cli", "version": "1.0.0"},
			"io.modelcontextprotocol/clientCapabilities": map[string]any{
				"extensions": map[string]any{TasksExtensionID: map[string]any{}},
			},
		},
	}
	encoded, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "subscriptions/listen", "params": params})
	if err != nil {
		return fmt.Errorf("encode MCP task subscription: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("create MCP task subscription: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", c.Version)
	req.Header.Set("Mcp-Method", "subscriptions/listen")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	// The request context, rather than http.Client.Timeout, bounds this long-
	// lived stream. This lets library callers wait longer than the default
	// per-request timeout while preserving any custom transport settings.
	client := *c.HTTP
	client.Timeout = 0
	res, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %w", ErrTaskSubscriptionInterrupted, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		switch res.StatusCode {
		case http.StatusBadRequest, http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotAcceptable, http.StatusUnsupportedMediaType, http.StatusNotImplemented:
			return ErrTaskSubscriptionsUnsupported
		case http.StatusRequestTimeout, http.StatusTooManyRequests:
			return fmt.Errorf("%w: %w", ErrTaskSubscriptionInterrupted, httpResponseError(res))
		default:
			if res.StatusCode >= 500 {
				return fmt.Errorf("%w: %w", ErrTaskSubscriptionInterrupted, httpResponseError(res))
			}
			return httpResponseError(res)
		}
	}
	if res.Header.Get("Mcp-Session-Id") != "" {
		return errors.New("stateful MCP session detected")
	}
	contentType, _, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil {
		return ErrTaskSubscriptionsUnsupported
	}
	if contentType == "application/json" {
		body, readErr := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
		if readErr != nil {
			return fmt.Errorf("%w: %w", ErrTaskSubscriptionInterrupted, readErr)
		}
		if len(body) > maxResponseBytes {
			return errors.New("MCP task subscription response exceeds diagnostic limit")
		}
		var message struct {
			Error *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &message) != nil {
			return errors.New("MCP task subscription returned invalid JSON")
		}
		if message.Error != nil {
			if unsupportedTaskSubscriptionCode(message.Error.Code) {
				return ErrTaskSubscriptionsUnsupported
			}
			return &RPCError{Code: message.Error.Code}
		}
		return ErrTaskSubscriptionsUnsupported
	}
	if contentType != "text/event-stream" {
		return ErrTaskSubscriptionsUnsupported
	}

	acknowledged := false
	done, err := scanTaskSSE(res.Body, func(data []byte) (bool, error) {
		var message struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
			Result  json.RawMessage `json:"result"`
			Error   *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &message); err != nil || message.JSONRPC != "2.0" {
			return true, errors.New("MCP task subscription contains an invalid JSON-RPC message")
		}
		if len(message.ID) > 0 && !bytes.Equal(message.ID, []byte("null")) {
			var responseID int
			if err := json.Unmarshal(message.ID, &responseID); err != nil || responseID != id {
				return true, errors.New("MCP task subscription response ID does not match request")
			}
			if message.Error != nil {
				if unsupportedTaskSubscriptionCode(message.Error.Code) {
					return true, ErrTaskSubscriptionsUnsupported
				}
				if message.Error.Code == -32603 {
					return true, fmt.Errorf("%w: %w", ErrTaskSubscriptionInterrupted, &RPCError{Code: message.Error.Code})
				}
				return true, &RPCError{Code: message.Error.Code}
			}
			if len(message.Result) == 0 || bytes.Equal(message.Result, []byte("null")) {
				return true, errors.New("MCP task subscription ended with an empty response")
			}
			if !acknowledged {
				return true, ErrTaskSubscriptionsUnsupported
			}
			var result struct {
				ResultType string `json:"resultType"`
			}
			if json.Unmarshal(message.Result, &result) != nil || result.ResultType != "complete" {
				return true, errors.New("MCP task subscription ended with an invalid response")
			}
			return true, ErrTaskSubscriptionClosed
		}

		switch message.Method {
		case "notifications/subscriptions/acknowledged":
			var ack struct {
				Notifications struct {
					TaskIDs []string `json:"taskIds"`
				} `json:"notifications"`
			}
			if err := json.Unmarshal(message.Params, &ack); err != nil {
				return true, ErrTaskSubscriptionsUnsupported
			}
			for id := range requested {
				if !slices.Contains(ack.Notifications.TaskIDs, id) {
					return true, ErrTaskSubscriptionsUnsupported
				}
			}
			acknowledged = true
		case "notifications/tasks":
			if !acknowledged {
				return true, errors.New("MCP task notification arrived before subscription acknowledgement")
			}
			var identity struct {
				TaskID string `json:"taskId"`
			}
			if err := json.Unmarshal(message.Params, &identity); err != nil {
				return true, err
			}
			if _, ok := requested[identity.TaskID]; !ok {
				return false, nil
			}
			update, err := decodeTaskNotification(message.Params, identity.TaskID)
			if err != nil {
				return true, err
			}
			return notify(update)
		}
		return false, nil
	})
	if done || err != nil {
		return err
	}
	if !acknowledged {
		return ErrTaskSubscriptionsUnsupported
	}
	return ErrTaskSubscriptionClosed
}

func unsupportedTaskSubscriptionCode(code int) bool {
	return code == -32601 || code == -32602 || code == -32021
}

func decodeTaskNotification(body json.RawMessage, expectedID string) (Task, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return Task{}, errors.New("MCP task notification must contain an object")
	}
	if existing, ok := fields["resultType"]; ok && !bytes.Equal(existing, []byte(`"complete"`)) {
		return Task{}, errors.New("MCP task notification contains an invalid resultType")
	}
	fields["resultType"] = json.RawMessage(`"complete"`)
	encoded, err := json.Marshal(fields)
	if err != nil {
		return Task{}, errors.New("MCP task notification could not be decoded")
	}
	return decodeTask(encoded, "complete", expectedID)
}

func scanTaskSSE(reader io.Reader, handle func([]byte) (bool, error)) (bool, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), maxResponseBytes+1)
	var data []string
	frameBytes := 0
	dispatch := func() (bool, error) {
		if len(data) == 0 {
			return false, nil
		}
		return handle([]byte(strings.Join(data, "\n")))
	}
	for scanner.Scan() {
		line := scanner.Text()
		frameBytes += len(line) + 1
		if frameBytes > maxResponseBytes {
			return false, errors.New("MCP task notification exceeds diagnostic limit")
		}
		if line == "" {
			done, err := dispatch()
			if done || err != nil {
				return done, err
			}
			data = nil
			frameBytes = 0
		} else if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("%w: %w", ErrTaskSubscriptionInterrupted, err)
	}
	return dispatch()
}

func (c *Client) requireTasksCapability(ctx context.Context) error {
	if c.Version != ProtocolVersion {
		return fmt.Errorf("MCP Tasks requires protocol %s", ProtocolVersion)
	}
	if !c.Capabilities.HasExtension(TasksExtensionID) {
		if _, _, err := c.Discover(ctx); err != nil {
			return fmt.Errorf("discover MCP Tasks support: %w", err)
		}
	}
	if !c.Capabilities.HasExtension(TasksExtensionID) {
		return fmt.Errorf("MCP server does not advertise %s", TasksExtensionID)
	}
	return nil
}

func (c *Client) taskRequest(ctx context.Context, method, taskID string, extra map[string]any) (Exchange, error) {
	params := make(map[string]any, len(extra)+2)
	for key, value := range extra {
		params[key] = value
	}
	params["taskId"] = taskID
	params["_meta"] = map[string]any{
		"io.modelcontextprotocol/clientCapabilities": map[string]any{
			"extensions": map[string]any{TasksExtensionID: map[string]any{}},
		},
	}
	return c.request(ctx, method, params, nil, false)
}

func decodeTask(body json.RawMessage, wantType, expectedID string) (Task, error) {
	return decodeTaskSnapshot(body, wantType, expectedID, false)
}

func decodeTaskSnapshot(body json.RawMessage, wantType, expectedID string, metadataOnly bool) (Task, error) {
	var task Task
	if err := json.Unmarshal(body, &task); err != nil {
		return Task{}, fmt.Errorf("decode MCP task: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return Task{}, fmt.Errorf("decode MCP task: %w", err)
	}
	if metadataOnly {
		for field := range fields {
			switch strings.ToLower(field) {
			case "result", "error", "inputrequests", "statusmessage":
				return Task{}, errors.New("MCP Task lookup included a payload field")
			}
		}
	}
	if task.ResultType != wantType {
		return Task{}, fmt.Errorf("MCP task resultType must be %q", wantType)
	}
	if err := validateTaskID(task.TaskID); err != nil {
		return Task{}, err
	}
	if expectedID != "" && task.TaskID != expectedID {
		return Task{}, errors.New("MCP task response ID does not match request")
	}
	for _, field := range []string{"createdAt", "lastUpdatedAt", "ttlMs"} {
		if _, ok := fields[field]; !ok {
			return Task{}, fmt.Errorf("MCP task is missing %s", field)
		}
	}
	for _, timestamp := range []string{task.CreatedAt, task.LastUpdatedAt} {
		if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil {
			return Task{}, errors.New("MCP task contains an invalid timestamp")
		}
	}
	if string(task.TTLMS) != "null" {
		var ttl int64
		if err := json.Unmarshal(task.TTLMS, &ttl); err != nil || ttl < 0 {
			return Task{}, errors.New("MCP task ttlMs must be a nonnegative integer or null")
		}
	}
	if task.PollInterval != nil && (*task.PollInterval < 0 || *task.PollInterval > int64(maxTaskPollInterval/time.Millisecond)) {
		return Task{}, errors.New("MCP task pollIntervalMs is outside the supported range")
	}
	if len(task.StatusMessage) > 16<<10 {
		return Task{}, errors.New("MCP task statusMessage exceeds 16 KiB")
	}
	switch task.Status {
	case "working", "cancelled":
	case "input_required":
		if !metadataOnly && (len(task.InputRequests) == 0 || len(task.InputRequests) > maxInteractiveInputRequests) {
			return Task{}, errors.New("MCP task contains an invalid input request set")
		}
	case "completed":
		if !metadataOnly && !isJSONObject(task.Result) {
			return Task{}, errors.New("completed MCP task is missing its object result")
		}
	case "failed":
		if !metadataOnly && !isJSONObject(task.Error) {
			return Task{}, errors.New("failed MCP task is missing its object error")
		}
	default:
		return Task{}, errors.New("MCP task contains an unknown status")
	}
	return task, nil
}

func isJSONObject(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var value map[string]json.RawMessage
	return json.Unmarshal(raw, &value) == nil && value != nil
}

func validateTaskID(taskID string) error {
	if taskID == "" || len(taskID) > maxTaskIDBytes || strings.ContainsAny(taskID, "\r\n\x00") {
		return errors.New("MCP task ID is invalid")
	}
	return nil
}

func (c *Client) waitForTask(ctx context.Context, tool string, task Task, options CallOptions) (result Task, waitErr error) {
	return c.waitForTaskWithReconcileInterval(ctx, tool, task, options, taskReconcileInterval)
}

func (c *Client) waitForTaskWithReconcileInterval(ctx context.Context, tool string, task Task, options CallOptions, reconcileInterval time.Duration) (result Task, waitErr error) {
	if options.OnTask != nil {
		options.OnTask(task)
	}
	responded := make(map[string]TaskInputRequest)
	inputUpdates := 0
	lastStatus, lastUpdated := task.Status, task.LastUpdatedAt
	observe := func(updated Task) {
		task = updated
		if task.Status != lastStatus || task.LastUpdatedAt != lastUpdated {
			if options.OnTask != nil {
				options.OnTask(task)
			}
			lastStatus, lastUpdated = task.Status, task.LastUpdatedAt
		}
	}
	subscriptionsSupported := true
	reconnectDelay := taskStreamReconnectBase
	streamFailures := 0
	defer func() {
		if options.CancelOnTimeout && (ctx.Err() != nil || errors.Is(waitErr, context.DeadlineExceeded)) && task.Status != "completed" && task.Status != "failed" && task.Status != "cancelled" {
			c.bestEffortCancelTask(ctx, task.TaskID)
		}
	}()
	for {
		switch task.Status {
		case "completed":
			var result struct {
				IsError bool               `json:"isError"`
				Content *[]json.RawMessage `json:"content"`
			}
			if err := json.Unmarshal(task.Result, &result); err != nil || result.Content == nil {
				return task, errors.New("completed MCP task contains an invalid tool result")
			}
			if result.IsError {
				return task, errors.New("MCP task completed with a tool error")
			}
			return task, nil
		case "failed":
			return task, errors.New("MCP task failed with a JSON-RPC error")
		case "cancelled":
			return task, errors.New("MCP task was cancelled")
		case "input_required":
			if options.Responder == nil {
				return task, fmt.Errorf("MCP task requires user input; rerun with --interactive or provide input responses")
			}
			requests, err := parseTaskInputRequests(tool, task.InputRequests)
			if err != nil {
				return task, err
			}
			responses := make(map[string]InputResponse)
			for _, request := range requests {
				prior, exists := responded[request.ID]
				if exists {
					if !reflect.DeepEqual(prior, task.InputRequests[request.ID]) {
						return task, errors.New("MCP task reused an input request ID for different content")
					}
					continue // The update may be eventually consistent; keep polling.
				}
				response, err := options.Responder(ctx, request)
				if err != nil {
					return task, err
				}
				if err := validateInputResponse(response); err != nil {
					return task, err
				}
				responses[request.ID] = response
			}
			if len(responses) > 0 {
				if inputUpdates >= maxInteractiveInputRounds {
					return task, fmt.Errorf("MCP task exceeded the %d input update limit", maxInteractiveInputRounds)
				}
				updated := task
				if uncertain, updateErr := c.updateTask(ctx, task.TaskID, responses); updateErr != nil {
					if !uncertain {
						return task, updateErr
					}
					updated, err = c.reconcileTaskInputUpdate(ctx, task.TaskID, responses)
					if err != nil {
						return updated, err
					}
				}
				for id := range responses {
					responded[id] = task.InputRequests[id]
				}
				inputUpdates++
				observe(updated)
				continue
			}
		}
		if subscriptionsSupported {
			interval := reconcileInterval
			if task.PollInterval != nil && time.Duration(*task.PollInterval)*time.Millisecond > interval {
				interval = time.Duration(*task.PollInterval) * time.Millisecond
			}
			streamCtx, cancel := context.WithTimeout(ctx, interval)
			updated, err := c.listenTaskUpdate(streamCtx, task)
			expired := errors.Is(streamCtx.Err(), context.DeadlineExceeded)
			cancel()
			// A healthy but silent stream may miss a notification. Reconcile
			// against durable state without treating this timeout as task cancellation.
			if err != nil && expired && ctx.Err() == nil {
				updated, _, err = c.GetTask(ctx, task.TaskID)
			}
			switch {
			case err == nil:
				reconnectDelay = taskStreamReconnectBase
				streamFailures = 0
				observe(updated)
				continue
			case errors.Is(err, ErrTaskSubscriptionsUnsupported):
				subscriptionsSupported = false
			case errors.Is(err, ErrTaskSubscriptionClosed), errors.Is(err, ErrTaskSubscriptionInterrupted):
				streamFailures++
				if streamFailures >= maxTaskStreamReconnects {
					subscriptionsSupported = false
					continue
				}
				if err := waitForTaskPoll(ctx, reconnectDelay); err != nil {
					return task, fmt.Errorf("reconnect MCP task subscription: %w", err)
				}
				reconnectDelay *= 2
				if reconnectDelay > taskStreamReconnectMax {
					reconnectDelay = taskStreamReconnectMax
				}
				continue
			default:
				return task, err
			}
		}
		if err := waitForTaskPoll(ctx, task.PollDelay()); err != nil {
			return task, fmt.Errorf("wait for MCP task ended: %w", err)
		}
		updated, _, err := c.GetTask(ctx, task.TaskID)
		if err != nil {
			return task, err
		}
		observe(updated)
	}
}

func (c *Client) bestEffortCancelTask(ctx context.Context, taskID string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	_, _ = c.CancelTask(ctx, taskID)
}

func waitForTaskPoll(ctx context.Context, wait time.Duration) error {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func parseTaskInputRequests(tool string, inputRequests map[string]TaskInputRequest) ([]InputRequest, error) {
	if len(inputRequests) == 0 || len(inputRequests) > maxInteractiveInputRequests {
		return nil, errors.New("MCP task contains an invalid input request set")
	}
	ids := make([]string, 0, len(inputRequests))
	for id := range inputRequests {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	requests := make([]InputRequest, 0, len(ids))
	for _, id := range ids {
		form := inputRequests[id]
		if id == "" || len(id) > maxTaskIDBytes || form.Method != "elicitation/create" || form.Params.Mode != "form" || form.Params.RequestedSchema["type"] != "object" {
			return nil, errors.New("MCP task contains an unsupported input request")
		}
		properties, ok := form.Params.RequestedSchema["properties"].(map[string]any)
		if !ok || len(properties) > 100 {
			return nil, errors.New("MCP task contains an invalid input form schema")
		}
		schemaBytes, err := json.Marshal(form.Params.RequestedSchema)
		if err != nil || len(schemaBytes) > 64<<10 || len(form.Params.Message) > 16<<10 {
			return nil, errors.New("MCP task input request exceeds its size limit")
		}
		requests = append(requests, InputRequest{Tool: tool, ID: id, Message: form.Params.Message, Schema: form.Params.RequestedSchema})
	}
	return requests, nil
}

func validateInputResponse(response InputResponse) error {
	switch response.Action {
	case "accept":
		if response.Content == nil {
			return errors.New("accepted MCP input must include an object response")
		}
	case "decline", "cancel":
		if response.Content != nil {
			return errors.New("declined MCP input must not include content")
		}
	default:
		return errors.New("MCP input responder returned an invalid action")
	}
	return nil
}
