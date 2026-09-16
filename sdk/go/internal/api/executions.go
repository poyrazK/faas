package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"
)

// ExecutionRuntime is the closed set of runtimes available to a disposable
// execution. The guest has no persistent customer disk; files are staged into
// an ephemeral scratch filesystem for the lifetime of the run only.
type ExecutionRuntime string

const (
	ExecutionRuntimeNode22    ExecutionRuntime = "node22"
	ExecutionRuntimeNode24    ExecutionRuntime = "node24"
	ExecutionRuntimePython312 ExecutionRuntime = "python312"
	ExecutionRuntimePython313 ExecutionRuntime = "python313"
)

// ExecutionNetworkMode is the network policy for a disposable execution.
type ExecutionNetworkMode string

const ExecutionNetworkNone ExecutionNetworkMode = "none"

// ExecutionNetworkPolicy requests the v1 loopback-only network policy.
type ExecutionNetworkPolicy struct {
	Mode ExecutionNetworkMode `json:"mode"`
}

// ExecutionLimitRequest contains optional caller-selected resource limits.
// Zero values select the account plan defaults.
type ExecutionLimitRequest struct {
	TimeoutMS       int `json:"timeout_ms,omitempty"`
	MemoryMB        int `json:"memory_mb,omitempty"`
	CPUMillicores   int `json:"cpu_millicores,omitempty"`
	EphemeralDiskMB int `json:"ephemeral_disk_mb,omitempty"`
	MaxOutputBytes  int `json:"max_output_bytes,omitempty"`
}

// ExecutionFile is a regular file in an ephemeral execution bundle. Content
// is base64 encoded by encoding/json and is never returned in a receipt.
type ExecutionFile struct {
	Path    string `json:"path"`
	Content []byte `json:"content"`
}

// CreateExecutionRequest is the caller-authored one-shot execution contract.
// Set either Source or Entrypoint + Files; source and input are never echoed
// by the execution read APIs.
type CreateExecutionRequest struct {
	Runtime    ExecutionRuntime        `json:"runtime"`
	Source     string                  `json:"source,omitempty"`
	Entrypoint string                  `json:"entrypoint,omitempty"`
	Files      []ExecutionFile         `json:"files,omitempty"`
	Input      json.RawMessage         `json:"input,omitempty"`
	Limits     *ExecutionLimitRequest  `json:"limits,omitempty"`
	Network    *ExecutionNetworkPolicy `json:"network,omitempty"`
}

// ResolvedExecutionLimits are the immutable limits admitted for one run.
type ResolvedExecutionLimits struct {
	TimeoutMS       int `json:"timeout_ms"`
	MemoryMB        int `json:"memory_mb"`
	CPUMillicores   int `json:"cpu_millicores"`
	EphemeralDiskMB int `json:"ephemeral_disk_mb"`
	MaxOutputBytes  int `json:"max_output_bytes"`
	PIDsMax         int `json:"pids_max"`
}

// ExecutionUsage contains bounded host-measured usage for a terminal run.
type ExecutionUsage struct {
	WallTimeMS   int64 `json:"wall_time_ms"`
	CPUTimeMS    int64 `json:"cpu_time_ms"`
	PeakMemoryMB int   `json:"peak_memory_mb"`
}

// ExecutionFailure is safe caller-facing terminal failure detail.
type ExecutionFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ExecutionStatus is the durable state machine exposed by the API.
type ExecutionStatus string

const (
	ExecutionStatusQueued      ExecutionStatus = "queued"
	ExecutionStatusRestoring   ExecutionStatus = "restoring"
	ExecutionStatusRunning     ExecutionStatus = "running"
	ExecutionStatusSucceeded   ExecutionStatus = "succeeded"
	ExecutionStatusFailed      ExecutionStatus = "failed"
	ExecutionStatusTimedOut    ExecutionStatus = "timed_out"
	ExecutionStatusOutOfMemory ExecutionStatus = "out_of_memory"
	ExecutionStatusCancelled   ExecutionStatus = "cancelled"
)

// Terminal reports whether no further lifecycle transition is expected.
func (s ExecutionStatus) Terminal() bool {
	switch s {
	case ExecutionStatusSucceeded, ExecutionStatusFailed, ExecutionStatusTimedOut,
		ExecutionStatusOutOfMemory, ExecutionStatusCancelled:
		return true
	default:
		return false
	}
}

// ExecutionResponse is an account-scoped execution receipt. Source and input
// are intentionally absent. A terminal receipt is persisted only after the
// disposable VM has been destroyed.
type ExecutionResponse struct {
	ID              string                  `json:"id"`
	Status          ExecutionStatus         `json:"status"`
	Runtime         ExecutionRuntime        `json:"runtime"`
	Limits          ResolvedExecutionLimits `json:"limits"`
	Result          json.RawMessage         `json:"result,omitempty"`
	Stdout          string                  `json:"stdout,omitempty"`
	Stderr          string                  `json:"stderr,omitempty"`
	OutputTruncated bool                    `json:"output_truncated"`
	ExitCode        *int                    `json:"exit_code,omitempty"`
	Usage           *ExecutionUsage         `json:"usage,omitempty"`
	Failure         *ExecutionFailure       `json:"failure,omitempty"`
	CreatedAt       string                  `json:"created_at"`
	StartedAt       *string                 `json:"started_at,omitempty"`
	FinishedAt      *string                 `json:"finished_at,omitempty"`
}

// ExecutionListResponse is one account-scoped page of execution receipts.
type ExecutionListResponse struct {
	Executions []ExecutionResponse `json:"executions"`
	Limit      int                 `json:"limit"`
	Offset     int                 `json:"offset"`
	NextOffset int                 `json:"next_offset"`
}

// ExecutionEventType is the bounded event vocabulary emitted by the
// resumable execution stream. Unknown values are preserved for forward
// compatibility.
type ExecutionEventType string

const (
	ExecutionEventStatus   ExecutionEventType = "status"
	ExecutionEventStdout   ExecutionEventType = "stdout"
	ExecutionEventStderr   ExecutionEventType = "stderr"
	ExecutionEventTerminal ExecutionEventType = "terminal"
	ExecutionEventError    ExecutionEventType = "error"
)

// ExecutionEventData is the typed JSON payload currently emitted by apid.
// Raw retains the original payload so clients can inspect fields introduced
// by a newer server without waiting for an SDK release.
type ExecutionEventData struct {
	Status          ExecutionStatus `json:"status,omitempty"`
	Chunk           string          `json:"chunk,omitempty"`
	OutputTruncated bool            `json:"output_truncated,omitempty"`
	ExitCode        *int            `json:"exit_code,omitempty"`
	FailureCode     string          `json:"failure_code,omitempty"`
	FailureMessage  string          `json:"failure_message,omitempty"`
	Usage           *ExecutionUsage `json:"usage,omitempty"`
	Raw             json.RawMessage `json:"-"`
	Value           any             `json:"-"`
}

// ExecutionEvent is one decoded execution SSE frame. ID is the monotonic
// control-plane cursor; HasID distinguishes an absent cursor from zero.
type ExecutionEvent struct {
	Type  ExecutionEventType
	ID    int64
	HasID bool
	Data  ExecutionEventData
	Raw   Event
}

// ExecutionEventParseError means the server sent a malformed event payload
// or cursor. It is never retried because reconnecting cannot repair a bad
// frame; callers should surface it as a protocol error.
type ExecutionEventParseError struct {
	Event string
	Err   error
}

func (e *ExecutionEventParseError) Error() string {
	return fmt.Sprintf("execution event %s is invalid: %v", e.Event, e.Err)
}

func (e *ExecutionEventParseError) Unwrap() error { return e.Err }

// WatchExecutionOptions controls a resumable execution watcher.
type WatchExecutionOptions struct {
	// After resumes strictly after this event cursor.
	After int64
	// Limit is the requested replay batch size. Zero uses 100.
	Limit int
	// RetryInitial is the first delay after a stream disconnect. Zero means
	// reconnect immediately; negative values are treated as zero.
	RetryInitial time.Duration
	// RetryMax caps exponential reconnect delay. Zero uses 2 seconds.
	RetryMax time.Duration
}

// ExecutionWatcher consumes a disposable execution's events and reconnects
// with the latest cursor after a transient stream EOF or transport failure.
// Calls to Next must be serialized. Close may be called from another
// goroutine and is idempotent.
type ExecutionWatcher struct {
	client *Client
	ctx    context.Context
	cancel context.CancelFunc
	id     string
	limit  int

	mu        sync.Mutex
	decoder   *Decoder
	body      io.ReadCloser
	closed    bool
	connected bool
	cursor    int64
	delay     time.Duration
	initial   time.Duration
	maxDelay  time.Duration
	terminal  bool
}

// NewExecutionWatcher builds a lazy watcher. The first HTTP request is made
// by Next, so callers can construct a watcher before entering a select loop.
func (c *Client) NewExecutionWatcher(ctx context.Context, id string, opts WatchExecutionOptions) *ExecutionWatcher {
	if ctx == nil {
		ctx = context.Background()
	}
	watchCtx, cancel := context.WithCancel(ctx)
	initial := opts.RetryInitial
	if initial < 0 {
		initial = 0
	}
	maxDelay := opts.RetryMax
	if maxDelay <= 0 {
		maxDelay = 2 * time.Second
	}
	if maxDelay < initial {
		maxDelay = initial
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	after := opts.After
	if after < 0 {
		after = 0
	}
	return &ExecutionWatcher{
		client: c, ctx: watchCtx, cancel: cancel, id: id, limit: limit,
		cursor: after, delay: initial, initial: initial, maxDelay: maxDelay,
	}
}

// WatchExecution is the concise public constructor for a lazy watcher. It
// does not perform network I/O until Next is called.
func (c *Client) WatchExecution(ctx context.Context, id string, opts WatchExecutionOptions) *ExecutionWatcher {
	return c.NewExecutionWatcher(ctx, id, opts)
}

// Cursor returns the latest event cursor observed by the watcher.
func (w *ExecutionWatcher) Cursor() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cursor
}

// Close releases the active HTTP stream. It is safe to call more than once.
func (w *ExecutionWatcher) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	dec, body, cancel := w.decoder, w.body, w.cancel
	w.decoder, w.body = nil, nil
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if dec != nil {
		_ = dec.Close()
	}
	if body != nil {
		return body.Close()
	}
	return nil
}

// Next returns the next decoded event. It returns io.EOF after the terminal
// event has been delivered. Before a successful connection, HTTP errors are
// returned immediately; after one has succeeded, transient failures reconnect
// using the last observed cursor.
func (w *ExecutionWatcher) Next() (ExecutionEvent, error) {
	for {
		if err := w.contextErr(); err != nil {
			return ExecutionEvent{}, err
		}
		w.mu.Lock()
		if w.closed {
			w.mu.Unlock()
			return ExecutionEvent{}, io.ErrClosedPipe
		}
		terminal := w.terminal
		dec := w.decoder
		w.mu.Unlock()
		if terminal {
			return ExecutionEvent{}, io.EOF
		}
		if dec == nil {
			if err := w.open(); err != nil {
				if !w.canRetry(err) {
					return ExecutionEvent{}, err
				}
				if err := w.backoff(); err != nil {
					return ExecutionEvent{}, err
				}
				continue
			}
			continue
		}

		raw, ok := <-dec.Events()
		if ok {
			event, err := decodeExecutionEvent(raw)
			if err != nil {
				_ = w.closeCurrent()
				return ExecutionEvent{}, err
			}
			w.mu.Lock()
			if event.HasID && event.ID > w.cursor {
				w.cursor = event.ID
			}
			if event.Type == ExecutionEventTerminal {
				w.terminal = true
			}
			w.mu.Unlock()
			return event, nil
		}

		streamErr := <-dec.Errors()
		_ = w.closeCurrent()
		if streamErr != nil && !isExecutionStreamRetryable(streamErr) {
			return ExecutionEvent{}, streamErr
		}
		if err := w.backoff(); err != nil {
			return ExecutionEvent{}, err
		}
	}
}

func (w *ExecutionWatcher) contextErr() error {
	if err := w.ctx.Err(); err != nil {
		_ = w.Close()
		return err
	}
	return nil
}

func (w *ExecutionWatcher) open() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return io.ErrClosedPipe
	}
	after := w.cursor
	w.mu.Unlock()

	body, err := w.client.StreamExecutionWithLimit(w.ctx, w.id, after, w.limit)
	if err != nil {
		return err
	}
	dec := NewDecoder(body)
	dec.SetCloseFn(body.Close)
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		_ = dec.Close()
		return io.ErrClosedPipe
	}
	w.body, w.decoder, w.connected = body, dec, true
	w.mu.Unlock()
	return nil
}

func (w *ExecutionWatcher) closeCurrent() error {
	w.mu.Lock()
	dec, body := w.decoder, w.body
	w.decoder, w.body = nil, nil
	w.mu.Unlock()
	if dec != nil {
		_ = dec.Close()
	}
	if body != nil {
		return body.Close()
	}
	return nil
}

func (w *ExecutionWatcher) canRetry(err error) bool {
	w.mu.Lock()
	connected := w.connected
	closed := w.closed
	w.mu.Unlock()
	return connected && !closed && isExecutionStreamRetryable(err)
}

func (w *ExecutionWatcher) backoff() error {
	delay := w.delay
	if delay > 0 {
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-w.ctx.Done():
			timer.Stop()
			return w.ctx.Err()
		}
	}
	if w.delay == 0 {
		w.delay = w.initial
	} else {
		w.delay *= 2
		if w.delay > w.maxDelay {
			w.delay = w.maxDelay
		}
	}
	return nil
}

func isExecutionStreamRetryable(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		status := apiErr.Problem.Status
		return status == 408 || status == 429 || status >= 500
	}
	// Decoder errors are read-side transport failures. They are safe to
	// replay from the last acknowledged cursor.
	return true
}

func decodeExecutionEvent(raw Event) (ExecutionEvent, error) {
	event := ExecutionEvent{Type: ExecutionEventType(raw.Event), Raw: raw}
	if event.Type == "" {
		event.Type = ExecutionEventType("message")
	}
	if raw.ID != "" {
		id, err := strconv.ParseInt(raw.ID, 10, 64)
		if err != nil || id < 0 {
			return ExecutionEvent{}, &ExecutionEventParseError{Event: raw.Event, Err: fmt.Errorf("invalid event id %q", raw.ID)}
		}
		event.ID, event.HasID = id, true
	}
	data := ExecutionEventData{Raw: json.RawMessage(raw.Data)}
	if raw.Data != "" {
		var value any
		if err := json.Unmarshal([]byte(raw.Data), &value); err != nil {
			return ExecutionEvent{}, &ExecutionEventParseError{Event: raw.Event, Err: err}
		}
		data.Value = value
		// Known execution events use object payloads. Preserve scalar/array
		// payloads for forward-compatible event types instead of rejecting
		// them; typed fields are populated only when the payload is an object.
		if _, object := value.(map[string]any); object {
			if err := json.Unmarshal([]byte(raw.Data), &data); err != nil {
				return ExecutionEvent{}, &ExecutionEventParseError{Event: raw.Event, Err: err}
			}
			data.Raw = json.RawMessage(raw.Data)
			data.Value = value
		}
	}
	event.Data = data
	return event, nil
}

// RunOptions controls Client.Run's event callback and watcher policy.
type RunOptions struct {
	Watch   WatchExecutionOptions
	OnEvent func(ExecutionEvent) error
}

// Run submits one ephemeral execution, delivers its events, and returns the
// final receipt after the terminal event. The callback is invoked in event
// order and may return an error to stop local consumption; it does not cancel
// the already-admitted remote execution.
func (c *Client) Run(ctx context.Context, req CreateExecutionRequest, opts RunOptions) (ExecutionResponse, error) {
	receipt, err := c.CreateExecution(ctx, req)
	if err != nil {
		return ExecutionResponse{}, err
	}
	watcher := c.NewExecutionWatcher(ctx, receipt.ID, opts.Watch)
	defer watcher.Close()
	for {
		event, nextErr := watcher.Next()
		if nextErr != nil {
			if errors.Is(nextErr, io.EOF) {
				break
			}
			return receipt, fmt.Errorf("watch execution %s: %w", receipt.ID, nextErr)
		}
		if opts.OnEvent != nil {
			if callbackErr := opts.OnEvent(event); callbackErr != nil {
				return receipt, callbackErr
			}
		}
		if event.Type == ExecutionEventTerminal {
			break
		}
	}
	return c.GetExecution(ctx, receipt.ID)
}
