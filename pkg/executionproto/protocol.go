// Package executionproto defines the authenticated host/guest wire contract
// for disposable one-shot executions.
//
// The connection is a Firecracker vsock stream. The host must establish the
// stream only after a restore/cold-boot path has completed the resume hook.
// One request frame is followed by bounded stdout/stderr frames and exactly
// one terminal result frame. A connection is never reused for another
// execution.
package executionproto

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

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	// Version is incremented for incompatible changes to the request/result
	// envelope. The frame header itself stays compatible with the existing
	// Firecracker vsock framing used by vmmd.
	Version uint16 = 1

	// VsockPort is the guest listener reserved for one-shot executions. It is
	// intentionally distinct from resume (1024), characterization/job exit
	// (1026), framework-ready (1027), liveness/job control (1028), and health
	// check (1029).
	VsockPort uint32 = 1030

	FrameRequest uint32 = 1
	FrameStdout  uint32 = 2
	FrameStderr  uint32 = 3
	FrameResult  uint32 = 4
	FrameError   uint32 = 5

	frameHeaderBytes = 8 // 4-byte BE type + 4-byte BE body length
	// MaxFrameBytes bounds a single allocation on both sides. Stream output is
	// chunked by OutputWriter, while a result/request can be larger than one
	// ordinary log line but never larger than the platform's hard output cap.
	MaxFrameBytes = api.ExecutionOutputHardMaxBytes + 64*1024
	// MaxRequestBytes is deliberately above the independent source, bundle, and
	// input plaintext caps plus JSON/base64 escaping overhead. The admission
	// layer remains authoritative for plan-specific limits.
	MaxRequestBytes = 4*api.ExecutionPlaintextFieldMaxBytes + 64*1024
	// MaxExecutionIDBytes keeps malformed callers from turning an identifier
	// into an unbounded allocation or log field.
	MaxExecutionIDBytes = 128
	// MaxFailureMessageBytes prevents a language/runtime error from becoming a
	// second unbounded output channel. The host maps it to a safe public detail.
	MaxFailureMessageBytes = 512
	// streamChunkBytes keeps writes responsive to cancellation and makes the
	// shared output budget enforceable before any single write allocates a huge
	// frame.
	streamChunkBytes = 64 * 1024
)

var (
	ErrAlreadyUsed          = errors.New("execution protocol: session already used")
	ErrInvalidRequest       = errors.New("execution protocol: invalid request")
	ErrInvalidResult        = errors.New("execution protocol: invalid result")
	ErrFrameTooLarge        = errors.New("execution protocol: frame too large")
	ErrOutputLimitExceeded  = errors.New("execution protocol: output limit exceeded")
	ErrUnexpectedFrame      = errors.New("execution protocol: unexpected frame")
	ErrGuestExecutionFailed = errors.New("execution protocol: guest execution failed")
)

// Request is the only payload sent across the host/guest boundary. Source and
// Input are plaintext here by design: callers persist them encrypted, and the
// vmmd adapter decrypts them in host memory immediately before this request is
// sent to the already-restored disposable guest.
type Request struct {
	Version     uint16                   `json:"version"`
	ExecutionID string                   `json:"execution_id"`
	Runtime     api.ExecutionRuntime     `json:"runtime"`
	Source      string                   `json:"source,omitempty"`
	Entrypoint  string                   `json:"entrypoint,omitempty"`
	Files       []api.ExecutionFile      `json:"files,omitempty"`
	Input       json.RawMessage          `json:"input"`
	TimeoutMS   int                      `json:"timeout_ms"`
	MaxOutput   int                      `json:"max_output_bytes"`
	NetworkMode api.ExecutionNetworkMode `json:"network_mode"`
}

// Result is the terminal guest report. Stdout and Stderr are carried in
// separate stream frames and are populated by Client.Execute after the final
// result frame arrives.
type Result struct {
	Status          api.ExecutionStatus `json:"status"`
	Result          json.RawMessage     `json:"result,omitempty"`
	OutputTruncated bool                `json:"output_truncated"`
	ExitCode        *int                `json:"exit_code,omitempty"`
	FailureCode     string              `json:"failure_code,omitempty"`
	FailureMessage  string              `json:"failure_message,omitempty"`
	Usage           api.ExecutionUsage  `json:"usage,omitempty"`
	Stdout          []byte              `json:"-"`
	Stderr          []byte              `json:"-"`
}

// RequestFromResolvedExecution is the narrow mapping used by vmmd adapters.
// The scheduler should pass the remaining host deadline as TimeoutMS when the
// request is built; this helper uses the admitted limit as the initial value
// and copies input so the persisted payload can be released independently.
func RequestFromResolvedExecution(id string, req api.ResolvedExecutionRequest) Request {
	input := append(json.RawMessage(nil), req.Input...)
	networkMode := req.Network.Mode
	if networkMode == "" {
		networkMode = api.ExecutionNetworkNone
	}
	return Request{
		Version:     Version,
		ExecutionID: id,
		Runtime:     req.Runtime,
		Source:      req.Source,
		Entrypoint:  req.Entrypoint,
		Files:       cloneExecutionFiles(req.Files),
		Input:       input,
		TimeoutMS:   req.Limits.TimeoutMS,
		MaxOutput:   req.Limits.MaxOutputBytes,
		NetworkMode: networkMode,
	}
}

// ErrorFrame is reserved for transport/protocol failures. A language error
// is a normal Result with status=failed; this frame never carries arbitrary
// guest output or host paths.
type ErrorFrame struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Validate enforces the same closed runtime/network and hard byte boundaries
// as admission. Plan-specific limits are checked before this package is
// called; these checks defend the guest boundary if a future caller bypasses
// apid or a stale scheduler sends malformed state.
func (r Request) Validate() error {
	if r.Version != Version {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidRequest, r.Version)
	}
	if strings.TrimSpace(r.ExecutionID) == "" || len(r.ExecutionID) > MaxExecutionIDBytes {
		return fmt.Errorf("%w: execution_id is empty or too long", ErrInvalidRequest)
	}
	if !r.Runtime.Valid() {
		return fmt.Errorf("%w: unsupported runtime %q", ErrInvalidRequest, r.Runtime)
	}
	if strings.TrimSpace(r.Source) != "" {
		if strings.ContainsRune(r.Source, '\x00') || len(r.Source) > api.ExecutionPlaintextFieldMaxBytes || len(r.Files) != 0 || r.Entrypoint != "" {
			return fmt.Errorf("%w: source is invalid or mixed with a bundle", ErrInvalidRequest)
		}
	} else if err := api.ValidateExecutionBundle(r.Entrypoint, r.Files, api.ExecutionPlaintextFieldMaxBytes); err != nil {
		return fmt.Errorf("%w: bundle is invalid: %w", ErrInvalidRequest, err)
	}
	if len(r.Input) == 0 {
		return fmt.Errorf("%w: input is empty", ErrInvalidRequest)
	}
	if len(r.Input) > api.ExecutionPlaintextFieldMaxBytes || !json.Valid(r.Input) {
		return fmt.Errorf("%w: input is invalid or too large", ErrInvalidRequest)
	}
	if r.TimeoutMS < api.ExecutionTimeoutMinMS || r.TimeoutMS > api.ExecutionTimeoutHardMaxMS {
		return fmt.Errorf("%w: timeout_ms is outside the hard range", ErrInvalidRequest)
	}
	if r.MaxOutput < api.ExecutionOutputMinBytes || r.MaxOutput > api.ExecutionOutputHardMaxBytes {
		return fmt.Errorf("%w: max_output_bytes is outside the hard range", ErrInvalidRequest)
	}
	if r.NetworkMode != api.ExecutionNetworkNone {
		return fmt.Errorf("%w: network mode %q is not supported", ErrInvalidRequest, r.NetworkMode)
	}
	return nil
}

func cloneExecutionFiles(files []api.ExecutionFile) []api.ExecutionFile {
	cloned := make([]api.ExecutionFile, len(files))
	for i, file := range files {
		cloned[i] = api.ExecutionFile{Path: file.Path, Content: append([]byte(nil), file.Content...)}
	}
	return cloned
}

// Validate validates the terminal result and the combined output budget.
func (r Result) Validate(maxOutput int) error {
	if !r.Status.Terminal() {
		return fmt.Errorf("%w: non-terminal status %q", ErrInvalidResult, r.Status)
	}
	if len(r.Result) == 0 {
		return fmt.Errorf("%w: result is empty", ErrInvalidResult)
	}
	if !json.Valid(r.Result) {
		return fmt.Errorf("%w: result is not valid JSON", ErrInvalidResult)
	}
	if r.FailureCode != "" && len(r.FailureCode) > MaxFailureMessageBytes {
		return fmt.Errorf("%w: failure code too long", ErrInvalidResult)
	}
	if len(r.FailureMessage) > MaxFailureMessageBytes {
		return fmt.Errorf("%w: failure message too long", ErrInvalidResult)
	}
	if maxOutput < 0 || len(r.Result)+len(r.Stdout)+len(r.Stderr) > maxOutput {
		return fmt.Errorf("%w: combined output exceeds limit", ErrOutputLimitExceeded)
	}
	return nil
}

// Client speaks one request/response exchange over a connected Firecracker
// vsock stream. It is intentionally single-use: a restored VM must never be
// reused for a second caller.
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

// Execute sends exactly one request and drains frames until one terminal
// result arrives. Cancellation closes the stream so a stuck guest cannot keep
// the host goroutine or VM alive beyond the caller's deadline.
func (c *Client) Execute(ctx context.Context, req Request) (Result, error) {
	var zero Result
	if c == nil || c.conn == nil {
		return zero, fmt.Errorf("%w: nil client", ErrInvalidRequest)
	}
	if err := req.Validate(); err != nil {
		return zero, err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return zero, fmt.Errorf("%w: marshal request: %w", ErrInvalidRequest, err)
	}
	if len(body) > MaxRequestBytes {
		return zero, fmt.Errorf("%w: request body is %d bytes", ErrFrameTooLarge, len(body))
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
		case FrameStdout:
			if len(out.Stdout)+len(out.Stderr)+len(frameBody) > req.MaxOutput {
				return zero, ErrOutputLimitExceeded
			}
			out.Stdout = append(out.Stdout, frameBody...)
		case FrameStderr:
			if len(out.Stdout)+len(out.Stderr)+len(frameBody) > req.MaxOutput {
				return zero, ErrOutputLimitExceeded
			}
			out.Stderr = append(out.Stderr, frameBody...)
		case FrameResult:
			if err := json.Unmarshal(frameBody, &out); err != nil {
				return zero, fmt.Errorf("%w: decode result: %w", ErrInvalidResult, err)
			}
			if err := out.Validate(req.MaxOutput); err != nil {
				return zero, err
			}
			return out, nil
		case FrameError:
			var guestErr ErrorFrame
			if err := json.Unmarshal(frameBody, &guestErr); err != nil {
				return zero, fmt.Errorf("%w: decode error frame: %w", ErrUnexpectedFrame, err)
			}
			if guestErr.Code == "" || len(guestErr.Code) > MaxFailureMessageBytes || len(guestErr.Message) > MaxFailureMessageBytes {
				return zero, fmt.Errorf("%w: malformed error frame", ErrUnexpectedFrame)
			}
			return zero, fmt.Errorf("%w: %s", ErrGuestExecutionFailed, guestErr.Code)
		default:
			return zero, fmt.Errorf("%w: frame type %d", ErrUnexpectedFrame, frameType)
		}
	}
}

// OutputWriter streams one channel while enforcing the request's combined
// output budget. It is safe for a handler to use stdout and stderr
// concurrently; frame writes remain ordered by the connection mutex.
type OutputWriter struct {
	conn      net.Conn
	max       int
	mu        *sync.Mutex
	used      *int
	usedMu    *sync.Mutex
	ctx       context.Context
	frameType uint32
}

func (w *OutputWriter) Write(p []byte) (int, error) {
	if w == nil || w.conn == nil || w.mu == nil || w.used == nil || w.usedMu == nil {
		return 0, errors.New("execution protocol: nil output writer")
	}
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	written := 0
	for len(p) > 0 {
		n := len(p)
		if n > streamChunkBytes {
			n = streamChunkBytes
		}
		w.usedMu.Lock()
		if *w.used+n > w.max {
			w.usedMu.Unlock()
			return written, ErrOutputLimitExceeded
		}
		*w.used += n
		w.usedMu.Unlock()

		w.mu.Lock()
		err := writeFrame(w.ctx, w.conn, w.frameType, p[:n])
		w.mu.Unlock()
		if err != nil {
			w.usedMu.Lock()
			*w.used -= n
			w.usedMu.Unlock()
			return written, err
		}
		written += n
		p = p[n:]
	}
	return written, nil
}

// Handler executes one request. It must not retain source/input after return.
// A handler returning a Result with an empty result is normalized to JSON null
// by Serve; other validation failures become a bounded ErrorFrame.
type Handler func(context.Context, Request, *OutputWriter, *OutputWriter) (Result, error)

// Serve handles one request on conn and then returns. The caller owns conn
// closure. The guest side should terminate its init process after Serve
// returns, ensuring the VM cannot become a reusable worker.
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
	if err := json.Unmarshal(body, &req); err != nil {
		return writeError(ctx, conn, "invalid_request", "request is not valid JSON")
	}
	if err := req.Validate(); err != nil {
		return writeError(ctx, conn, "invalid_request", "request failed validation")
	}

	var streamMu sync.Mutex
	var usedMu sync.Mutex
	used := 0
	outCtx, cancel := context.WithTimeout(ctx, time.Duration(req.TimeoutMS)*time.Millisecond)
	defer cancel()
	stdout := &OutputWriter{conn: conn, max: req.MaxOutput, mu: &streamMu, used: &used, usedMu: &usedMu, ctx: outCtx, frameType: FrameStdout}
	stderr := &OutputWriter{conn: conn, max: req.MaxOutput, mu: &streamMu, used: &used, usedMu: &usedMu, ctx: outCtx, frameType: FrameStderr}
	result, handlerErr := handler(outCtx, req, stdout, stderr)
	if handlerErr != nil {
		result = Result{Status: statusForHandlerError(outCtx, handlerErr), Result: json.RawMessage("null"), FailureCode: failureCodeForHandlerError(outCtx, handlerErr)}
	}
	if len(result.Result) == 0 {
		result.Result = json.RawMessage("null")
	}
	result.Stdout = nil
	result.Stderr = nil
	usedMu.Lock()
	streamBytes := used
	usedMu.Unlock()
	if err := result.Validate(req.MaxOutput - streamBytes); err != nil {
		return writeError(ctx, conn, "invalid_result", "result exceeded the output budget")
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

func statusForHandlerError(ctx context.Context, err error) api.ExecutionStatus {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return api.ExecutionStatusTimedOut
	}
	return api.ExecutionStatusFailed
}

func failureCodeForHandlerError(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, ErrOutputLimitExceeded) {
		return "output_limit"
	}
	return "guest_error"
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
	var hdr [frameHeaderBytes]byte
	binary.BigEndian.PutUint32(hdr[0:4], frameType)
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(body)))
	if err := connDeadline(ctx, conn); err != nil {
		return err
	}
	if err := writeAll(conn, hdr[:]); err != nil {
		return contextError(ctx, err)
	}
	if len(body) != 0 {
		if err := writeAll(conn, body); err != nil {
			return contextError(ctx, err)
		}
	}
	return nil
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

func readFrame(ctx context.Context, conn net.Conn) (uint32, []byte, error) {
	var zero uint32
	var hdr [frameHeaderBytes]byte
	if err := connDeadline(ctx, conn); err != nil {
		return zero, nil, err
	}
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return zero, nil, contextError(ctx, err)
	}
	frameType := binary.BigEndian.Uint32(hdr[0:4])
	bodyLen := binary.BigEndian.Uint32(hdr[4:8])
	if bodyLen > MaxFrameBytes {
		return zero, nil, fmt.Errorf("%w: %d bytes", ErrFrameTooLarge, bodyLen)
	}
	body := make([]byte, int(bodyLen))
	if _, err := io.ReadFull(conn, body); err != nil {
		return zero, nil, contextError(ctx, err)
	}
	return frameType, body, nil
}

func connDeadline(ctx context.Context, conn net.Conn) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return err
		}
		return nil
	}
	return conn.SetDeadline(time.Time{})
}

func contextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	// A connection deadline and a context deadline can fire in adjacent
	// scheduler ticks. net.Pipe and Unix sockets commonly report the former as
	// a timeout just before ctx.Err observes the latter; preserve the protocol
	// contract by mapping that race back to context.DeadlineExceeded.
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
	return func() {
		close(done)
	}
}
