// Package apptaskproto defines the one-shot host/guest wire contract for
// commands executed against an immutable app deployment.
//
// The VM is booted and its scoped environment is staged before this protocol
// is dialed. Sending the request is therefore the dispatch boundary: callers
// must not connect until the durable task has transitioned to running.
package apptaskproto

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	Version   uint16 = 1
	VsockPort uint32 = 1031

	FrameRequest uint32 = 1
	FrameStdout  uint32 = 2
	FrameStderr  uint32 = 3
	FrameResult  uint32 = 4
	FrameError   uint32 = 5

	MaxTaskIDBytes         = 128
	MaxCommandArgs         = 64
	MaxCommandArgBytes     = 4096
	MaxCommandBytes        = 16384
	MinTimeoutSeconds      = 1
	MaxTimeoutSeconds      = 3600
	MinOutputBytes         = 1024
	MaxOutputBytes         = 16 * 1024 * 1024
	MaxFailureMessageBytes = 512

	frameHeaderBytes = 8
	streamChunkBytes = 64 * 1024
	MaxFrameBytes    = MaxOutputBytes + 64*1024
	MaxRequestBytes  = MaxCommandBytes + 64*1024
)

var (
	ErrAlreadyUsed         = errors.New("app task protocol: session already used")
	ErrInvalidRequest      = errors.New("app task protocol: invalid request")
	ErrInvalidResult       = errors.New("app task protocol: invalid result")
	ErrFrameTooLarge       = errors.New("app task protocol: frame too large")
	ErrOutputLimitExceeded = errors.New("app task protocol: output limit exceeded")
	ErrUnexpectedFrame     = errors.New("app task protocol: unexpected frame")
	ErrGuestFailed         = errors.New("app task protocol: guest failed")
)

type Status string

const (
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusTimedOut  Status = "timed_out"
)

func (s Status) Terminal() bool {
	return s == StatusSucceeded || s == StatusFailed || s == StatusTimedOut
}

// Request contains only dispatch-time command data. Deployment artifacts,
// environment, secrets, and network policy are staged during VM preparation
// and intentionally cannot be changed through this channel.
type Request struct {
	Version        uint16   `json:"version"`
	TaskID         string   `json:"task_id"`
	Command        []string `json:"command"`
	CommandShell   bool     `json:"command_shell"`
	TimeoutSeconds int      `json:"timeout_seconds"`
	MaxOutputBytes int      `json:"max_output_bytes"`
}

func (r Request) Validate() error {
	if r.Version != Version {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidRequest, r.Version)
	}
	if strings.TrimSpace(r.TaskID) == "" || len(r.TaskID) > MaxTaskIDBytes || strings.ContainsRune(r.TaskID, '\x00') {
		return fmt.Errorf("%w: task_id is empty or too long", ErrInvalidRequest)
	}
	if len(r.Command) == 0 || len(r.Command) > MaxCommandArgs {
		return fmt.Errorf("%w: command must contain between 1 and %d arguments", ErrInvalidRequest, MaxCommandArgs)
	}
	if r.CommandShell && len(r.Command) != 1 {
		return fmt.Errorf("%w: shell commands require exactly one command string", ErrInvalidRequest)
	}
	total := 0
	for i, arg := range r.Command {
		if strings.ContainsRune(arg, '\x00') || len(arg) > MaxCommandArgBytes {
			return fmt.Errorf("%w: command argument %d is invalid", ErrInvalidRequest, i)
		}
		total += len(arg)
	}
	if strings.TrimSpace(r.Command[0]) == "" || total > MaxCommandBytes {
		return fmt.Errorf("%w: command is empty or too large", ErrInvalidRequest)
	}
	if r.TimeoutSeconds < MinTimeoutSeconds || r.TimeoutSeconds > MaxTimeoutSeconds {
		return fmt.Errorf("%w: timeout_seconds is outside the hard range", ErrInvalidRequest)
	}
	if r.MaxOutputBytes < MinOutputBytes || r.MaxOutputBytes > MaxOutputBytes {
		return fmt.Errorf("%w: max_output_bytes is outside the hard range", ErrInvalidRequest)
	}
	return nil
}

type Result struct {
	Status          Status `json:"status"`
	OutputTruncated bool   `json:"output_truncated"`
	ExitCode        *int   `json:"exit_code,omitempty"`
	FailureCode     string `json:"failure_code,omitempty"`
	FailureMessage  string `json:"failure_message,omitempty"`
	Stdout          []byte `json:"-"`
	Stderr          []byte `json:"-"`
}

func (r Result) Validate(maxOutput int) error {
	if !r.Status.Terminal() {
		return fmt.Errorf("%w: non-terminal status %q", ErrInvalidResult, r.Status)
	}
	if len(r.FailureCode) > MaxFailureMessageBytes || len(r.FailureMessage) > MaxFailureMessageBytes {
		return fmt.Errorf("%w: failure detail is too long", ErrInvalidResult)
	}
	if maxOutput < 0 || len(r.Stdout)+len(r.Stderr) > maxOutput {
		return ErrOutputLimitExceeded
	}
	return nil
}

type ErrorFrame struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type OutputReceiver func(context.Context, string, []byte) error

type Client struct {
	conn net.Conn
	mu   sync.Mutex
	used bool
}

func NewClient(conn net.Conn) (*Client, error) {
	if conn == nil {
		return nil, fmt.Errorf("%w: nil connection", ErrInvalidRequest)
	}
	return &Client{conn: conn}, nil
}

func (c *Client) Execute(ctx context.Context, req Request) (Result, error) {
	return c.ExecuteWithOutput(ctx, req, nil)
}

func (c *Client) ExecuteWithOutput(ctx context.Context, req Request, receive OutputReceiver) (Result, error) {
	var zero Result
	if c == nil || c.conn == nil {
		return zero, fmt.Errorf("%w: nil client", ErrInvalidRequest)
	}
	if err := req.Validate(); err != nil {
		return zero, err
	}
	body, err := json.Marshal(req)
	if err != nil || len(body) > MaxRequestBytes {
		return zero, fmt.Errorf("%w: request could not be encoded", ErrInvalidRequest)
	}
	c.mu.Lock()
	if c.used {
		c.mu.Unlock()
		return zero, ErrAlreadyUsed
	}
	c.used = true
	c.mu.Unlock()

	stop := closeOnCancel(ctx, c.conn)
	defer stop()
	if err := writeFrame(ctx, c.conn, FrameRequest, body); err != nil {
		return zero, err
	}
	var out Result
	for {
		frameType, frameBody, err := readFrame(ctx, c.conn)
		if err != nil {
			return zero, err
		}
		switch frameType {
		case FrameStdout, FrameStderr:
			if len(out.Stdout)+len(out.Stderr)+len(frameBody) > req.MaxOutputBytes {
				return zero, ErrOutputLimitExceeded
			}
			stream := "stdout"
			if frameType == FrameStdout {
				out.Stdout = append(out.Stdout, frameBody...)
			} else {
				stream = "stderr"
				out.Stderr = append(out.Stderr, frameBody...)
			}
			if receive != nil {
				if err := receive(ctx, stream, frameBody); err != nil {
					return zero, err
				}
			}
		case FrameResult:
			if err := json.Unmarshal(frameBody, &out); err != nil {
				return zero, fmt.Errorf("%w: decode result: %w", ErrInvalidResult, err)
			}
			if err := out.Validate(req.MaxOutputBytes); err != nil {
				return zero, err
			}
			return out, nil
		case FrameError:
			var guestErr ErrorFrame
			if err := json.Unmarshal(frameBody, &guestErr); err != nil || guestErr.Code == "" ||
				len(guestErr.Code) > MaxFailureMessageBytes || len(guestErr.Message) > MaxFailureMessageBytes {
				return zero, fmt.Errorf("%w: malformed error frame", ErrUnexpectedFrame)
			}
			return zero, fmt.Errorf("%w: %s", ErrGuestFailed, guestErr.Code)
		default:
			return zero, fmt.Errorf("%w: frame type %d", ErrUnexpectedFrame, frameType)
		}
	}
}

// OutputWriter preserves the command's normal write semantics when the
// persisted output budget is exhausted: the remaining bytes are discarded,
// the terminal result is marked truncated, and Write still reports success.
type OutputWriter struct {
	conn      net.Conn
	max       int
	streamMu  *sync.Mutex
	budgetMu  *sync.Mutex
	used      *int
	truncated *bool
	ctx       context.Context
	frameType uint32
}

func (w *OutputWriter) Write(p []byte) (int, error) {
	if w == nil || w.conn == nil || w.streamMu == nil || w.budgetMu == nil || w.used == nil || w.truncated == nil {
		return 0, errors.New("app task protocol: nil output writer")
	}
	original := len(p)
	for len(p) > 0 {
		if err := w.ctx.Err(); err != nil {
			return original - len(p), err
		}
		n := min(len(p), streamChunkBytes)
		w.budgetMu.Lock()
		remaining := w.max - *w.used
		if remaining <= 0 {
			*w.truncated = true
			w.budgetMu.Unlock()
			return original, nil
		}
		if n > remaining {
			n = remaining
			*w.truncated = true
		}
		*w.used += n
		w.budgetMu.Unlock()

		w.streamMu.Lock()
		err := writeFrame(w.ctx, w.conn, w.frameType, p[:n])
		w.streamMu.Unlock()
		if err != nil {
			w.budgetMu.Lock()
			*w.used -= n
			w.budgetMu.Unlock()
			return original - len(p), err
		}
		p = p[n:]
		if n == remaining && len(p) > 0 {
			w.budgetMu.Lock()
			*w.truncated = true
			w.budgetMu.Unlock()
			return original, nil
		}
	}
	return original, nil
}

type Handler func(context.Context, Request, *OutputWriter, *OutputWriter) (Result, error)

func Serve(ctx context.Context, conn net.Conn, handler Handler) error {
	if conn == nil || handler == nil {
		return fmt.Errorf("%w: nil connection or handler", ErrInvalidRequest)
	}
	stop := closeOnCancel(ctx, conn)
	defer stop()
	frameType, body, err := readFrame(ctx, conn)
	if err != nil {
		return err
	}
	if frameType != FrameRequest {
		return writeError(ctx, conn, "unexpected_frame", "request frame required")
	}
	if len(body) > MaxRequestBytes {
		return writeError(ctx, conn, "invalid_request", "request body is too large")
	}
	var req Request
	if err := json.Unmarshal(body, &req); err != nil || req.Validate() != nil {
		return writeError(ctx, conn, "invalid_request", "request failed validation")
	}

	var streamMu, budgetMu sync.Mutex
	used := 0
	truncated := false
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(req.TimeoutSeconds)*time.Second)
	defer cancel()
	stdout := &OutputWriter{conn: conn, max: req.MaxOutputBytes, streamMu: &streamMu, budgetMu: &budgetMu, used: &used, truncated: &truncated, ctx: runCtx, frameType: FrameStdout}
	stderr := &OutputWriter{conn: conn, max: req.MaxOutputBytes, streamMu: &streamMu, budgetMu: &budgetMu, used: &used, truncated: &truncated, ctx: runCtx, frameType: FrameStderr}
	result, handlerErr := handler(runCtx, req, stdout, stderr)
	if handlerErr != nil {
		result = Result{Status: StatusFailed, FailureCode: "guest_error", FailureMessage: "command execution failed"}
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) || errors.Is(handlerErr, context.DeadlineExceeded) {
			result.Status = StatusTimedOut
			result.FailureCode = "timeout"
			result.FailureMessage = "command timed out"
		}
	}
	budgetMu.Lock()
	result.OutputTruncated = result.OutputTruncated || truncated
	streamBytes := used
	budgetMu.Unlock()
	result.Stdout, result.Stderr = nil, nil
	if err := result.Validate(req.MaxOutputBytes - streamBytes); err != nil {
		return writeError(ctx, conn, "invalid_result", "result failed validation")
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > MaxFrameBytes {
		return writeError(ctx, conn, "invalid_result", "result could not be encoded")
	}
	streamMu.Lock()
	err = writeFrame(ctx, conn, FrameResult, encoded)
	streamMu.Unlock()
	return err
}

func writeError(ctx context.Context, conn net.Conn, code, message string) error {
	if len(code) > MaxFailureMessageBytes || len(message) > MaxFailureMessageBytes {
		return ErrInvalidResult
	}
	body, err := json.Marshal(ErrorFrame{Code: code, Message: message})
	if err != nil {
		return err
	}
	return writeFrame(ctx, conn, FrameError, body)
}

func writeFrame(ctx context.Context, conn net.Conn, frameType uint32, body []byte) error {
	if len(body) > MaxFrameBytes {
		return ErrFrameTooLarge
	}
	var header [frameHeaderBytes]byte
	binary.BigEndian.PutUint32(header[:4], frameType)
	binary.BigEndian.PutUint32(header[4:], uint32(len(body)))
	if err := connDeadline(ctx, conn); err != nil {
		return err
	}
	if err := writeAll(conn, header[:]); err != nil {
		return contextError(ctx, err)
	}
	if err := writeAll(conn, body); err != nil {
		return contextError(ctx, err)
	}
	return nil
}

func readFrame(ctx context.Context, conn net.Conn) (uint32, []byte, error) {
	var header [frameHeaderBytes]byte
	if err := connDeadline(ctx, conn); err != nil {
		return 0, nil, err
	}
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return 0, nil, contextError(ctx, err)
	}
	frameType := binary.BigEndian.Uint32(header[:4])
	bodyLen := binary.BigEndian.Uint32(header[4:])
	if bodyLen > MaxFrameBytes {
		return 0, nil, fmt.Errorf("%w: %d bytes", ErrFrameTooLarge, bodyLen)
	}
	body := make([]byte, int(bodyLen))
	if _, err := io.ReadFull(conn, body); err != nil {
		return 0, nil, contextError(ctx, err)
	}
	return frameType, body, nil
}

func writeAll(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(p) {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}

func connDeadline(ctx context.Context, conn net.Conn) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok {
		return conn.SetDeadline(deadline)
	}
	return conn.SetDeadline(time.Time{})
}

func contextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return context.DeadlineExceeded
		}
	}
	return err
}

func closeOnCancel(ctx context.Context, conn net.Conn) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	return func() { close(done) }
}
