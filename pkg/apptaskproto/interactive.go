package apptaskproto

// Interactive sessions (ADR-958). After the version-2 request frame the host
// may send any number of stdin, resize, and stdin-close frames while the guest
// streams output. The session ends with exactly one result or error frame.
// Output is never budgeted or retained: it belongs to the attached client.

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"sync"
	"time"
)

const (
	InteractiveVersion uint16 = 2

	FrameStdin      uint32 = 6
	FrameResize     uint32 = 7
	FrameStdinClose uint32 = 8

	MaxStdinChunkBytes = 64 * 1024
	MaxTerminalRows    = 1000
	MaxTerminalCols    = 1000

	resizeFrameBytes = 4
	inputQueueDepth  = 32
)

// ErrHostDisconnected is the cancellation cause a guest handler observes when
// the attached client went away before the process exited.
var ErrHostDisconnected = errors.New("app task protocol: host disconnected")

// GuestError carries the guest's error-frame code.
type GuestError struct {
	Code string
}

func (e *GuestError) Error() string { return "app task protocol: guest failed: " + e.Code }

func (e *GuestError) Unwrap() error { return ErrGuestFailed }

func (r Request) validateInteractive() error {
	if !r.Interactive {
		return fmt.Errorf("%w: version %d requires an interactive request", ErrInvalidRequest, InteractiveVersion)
	}
	if r.MaxOutputBytes != 0 {
		return fmt.Errorf("%w: interactive sessions do not retain output", ErrInvalidRequest)
	}
	if !r.TTY {
		if r.Rows != 0 || r.Cols != 0 {
			return fmt.Errorf("%w: terminal size requires a tty", ErrInvalidRequest)
		}
		return nil
	}
	if !validTerminalSize(r.Rows, r.Cols) {
		return fmt.Errorf("%w: terminal size is outside the supported range", ErrInvalidRequest)
	}
	return nil
}

func validTerminalSize(rows, cols uint16) bool {
	return rows >= 1 && rows <= MaxTerminalRows && cols >= 1 && cols <= MaxTerminalCols
}

// InputKind identifies one host-to-guest interactive event.
type InputKind uint8

const (
	InputStdin InputKind = iota + 1
	InputResize
	InputStdinClose
)

// InputEvent is one stdin chunk, terminal resize, or stdin close.
type InputEvent struct {
	Kind InputKind
	Data []byte
	Rows uint16
	Cols uint16
}

func (e InputEvent) validate() error {
	switch e.Kind {
	case InputStdin:
		if len(e.Data) == 0 || len(e.Data) > MaxStdinChunkBytes {
			return fmt.Errorf("%w: stdin chunk size", ErrInvalidRequest)
		}
	case InputResize:
		if !validTerminalSize(e.Rows, e.Cols) {
			return fmt.Errorf("%w: terminal size is outside the supported range", ErrInvalidRequest)
		}
	case InputStdinClose:
	default:
		return fmt.Errorf("%w: unknown input kind %d", ErrInvalidRequest, e.Kind)
	}
	return nil
}

// Interact runs one interactive session. Events read from input are forwarded
// until the guest returns its result; a closed input channel stops forwarding
// without ending the session. Cancelling ctx closes the connection, which the
// guest treats as a hang-up.
func (c *Client) Interact(ctx context.Context, req Request, input <-chan InputEvent, receive OutputReceiver) (Result, error) {
	var zero Result
	if c == nil || c.conn == nil {
		return zero, fmt.Errorf("%w: nil client", ErrInvalidRequest)
	}
	if !req.Interactive {
		return zero, fmt.Errorf("%w: interactive request required", ErrInvalidRequest)
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

	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := closeOnCancel(sessionCtx, c.conn)
	defer stop()
	var writeMu sync.Mutex
	if err := writeFrame(sessionCtx, c.conn, FrameRequest, body); err != nil {
		return zero, err
	}
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		forwardInput(sessionCtx, c.conn, &writeMu, input)
	}()
	defer func() {
		cancel()
		<-writerDone
	}()

	for {
		frameType, frameBody, err := readStreamFrame(sessionCtx, c.conn)
		if err != nil {
			return zero, err
		}
		switch frameType {
		case FrameStdout, FrameStderr:
			if receive == nil {
				continue
			}
			stream := "stdout"
			if frameType == FrameStderr {
				stream = "stderr"
			}
			if err := receive(sessionCtx, stream, frameBody); err != nil {
				return zero, err
			}
		case FrameResult:
			var out Result
			if err := json.Unmarshal(frameBody, &out); err != nil {
				return zero, fmt.Errorf("%w: decode result: %w", ErrInvalidResult, err)
			}
			out.Stdout, out.Stderr = nil, nil
			if err := out.Validate(math.MaxInt); err != nil {
				return zero, err
			}
			return out, nil
		case FrameError:
			var guestErr ErrorFrame
			if err := json.Unmarshal(frameBody, &guestErr); err != nil || guestErr.Code == "" ||
				len(guestErr.Code) > MaxFailureMessageBytes || len(guestErr.Message) > MaxFailureMessageBytes {
				return zero, fmt.Errorf("%w: malformed error frame", ErrUnexpectedFrame)
			}
			return zero, &GuestError{Code: guestErr.Code}
		default:
			return zero, fmt.Errorf("%w: frame type %d", ErrUnexpectedFrame, frameType)
		}
	}
}

func forwardInput(ctx context.Context, conn net.Conn, writeMu *sync.Mutex, input <-chan InputEvent) {
	if input == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-input:
			if !ok {
				return
			}
			frameType, body, err := encodeInput(event)
			if err != nil {
				continue
			}
			writeMu.Lock()
			err = writeStreamFrame(ctx, conn, frameType, body)
			writeMu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

func encodeInput(event InputEvent) (uint32, []byte, error) {
	if err := event.validate(); err != nil {
		return 0, nil, err
	}
	switch event.Kind {
	case InputStdin:
		return FrameStdin, event.Data, nil
	case InputResize:
		body := make([]byte, resizeFrameBytes)
		binary.BigEndian.PutUint16(body[:2], event.Rows)
		binary.BigEndian.PutUint16(body[2:], event.Cols)
		return FrameResize, body, nil
	default:
		return FrameStdinClose, nil, nil
	}
}

func decodeInput(frameType uint32, body []byte) (InputEvent, error) {
	var event InputEvent
	switch frameType {
	case FrameStdin:
		event = InputEvent{Kind: InputStdin, Data: body}
	case FrameResize:
		if len(body) != resizeFrameBytes {
			return InputEvent{}, fmt.Errorf("%w: resize frame size", ErrInvalidRequest)
		}
		event = InputEvent{Kind: InputResize, Rows: binary.BigEndian.Uint16(body[:2]), Cols: binary.BigEndian.Uint16(body[2:])}
	case FrameStdinClose:
		if len(body) != 0 {
			return InputEvent{}, fmt.Errorf("%w: stdin close frame carries data", ErrInvalidRequest)
		}
		event = InputEvent{Kind: InputStdinClose}
	default:
		return InputEvent{}, fmt.Errorf("%w: frame type %d", ErrUnexpectedFrame, frameType)
	}
	return event, event.validate()
}

// InteractiveSession is the guest-side view of one interactive request.
// Input is closed when the host stops sending; Stdout and Stderr stream to
// the host without a budget.
type InteractiveSession struct {
	Request Request
	Input   <-chan InputEvent
	Stdout  *StreamWriter
	Stderr  *StreamWriter
}

type InteractiveHandler func(context.Context, *InteractiveSession) (Result, error)

// StreamWriter writes unbudgeted output frames.
type StreamWriter struct {
	ctx       context.Context
	conn      net.Conn
	mu        *sync.Mutex
	frameType uint32
}

func (w *StreamWriter) Write(p []byte) (int, error) {
	if w == nil || w.conn == nil || w.mu == nil {
		return 0, errors.New("app task protocol: nil stream writer")
	}
	written := 0
	for written < len(p) {
		n := min(len(p)-written, streamChunkBytes)
		w.mu.Lock()
		err := writeStreamFrame(w.ctx, w.conn, w.frameType, p[written:written+n])
		w.mu.Unlock()
		if err != nil {
			return written, err
		}
		written += n
	}
	return written, nil
}

func serveInteractive(ctx context.Context, conn net.Conn, req Request, handler InteractiveHandler) error {
	runCtx, cancel := context.WithTimeoutCause(ctx, time.Duration(req.TimeoutSeconds)*time.Second, context.DeadlineExceeded)
	defer cancel()
	sessionCtx, hangUp := context.WithCancelCause(runCtx)
	defer hangUp(nil)

	var writeMu sync.Mutex
	input := make(chan InputEvent, inputQueueDepth)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer close(input)
		for {
			frameType, body, err := readStreamFrame(sessionCtx, conn)
			if err != nil {
				hangUp(ErrHostDisconnected)
				return
			}
			event, err := decodeInput(frameType, body)
			if err != nil {
				hangUp(ErrHostDisconnected)
				return
			}
			select {
			case input <- event:
			case <-sessionCtx.Done():
				return
			}
		}
	}()

	session := &InteractiveSession{
		Request: req,
		Input:   input,
		Stdout:  &StreamWriter{ctx: sessionCtx, conn: conn, mu: &writeMu, frameType: FrameStdout},
		Stderr:  &StreamWriter{ctx: sessionCtx, conn: conn, mu: &writeMu, frameType: FrameStderr},
	}
	result, handlerErr := handler(sessionCtx, session)
	if handlerErr != nil {
		result = Result{Status: StatusFailed, FailureCode: "guest_error", FailureMessage: "command execution failed"}
		switch {
		case errors.Is(context.Cause(sessionCtx), ErrHostDisconnected):
			result.FailureCode = "client_disconnected"
			result.FailureMessage = "the attached client disconnected"
		case errors.Is(runCtx.Err(), context.DeadlineExceeded):
			result.Status = StatusTimedOut
			result.FailureCode = "timeout"
			result.FailureMessage = "session reached its time limit"
		}
	}
	result.Stdout, result.Stderr = nil, nil
	result.OutputTruncated = false
	if err := result.Validate(math.MaxInt); err != nil {
		writeMu.Lock()
		err = writeError(ctx, conn, "invalid_result", "result failed validation")
		writeMu.Unlock()
		return err
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > MaxFrameBytes {
		writeMu.Lock()
		err = writeError(ctx, conn, "invalid_result", "result could not be encoded")
		writeMu.Unlock()
		return err
	}
	writeMu.Lock()
	err = writeStreamFrame(ctx, conn, FrameResult, encoded)
	writeMu.Unlock()
	hangUp(nil)
	_ = conn.SetReadDeadline(time.Now())
	<-readerDone
	return err
}

// readStreamFrame and writeStreamFrame are the full-duplex variants of
// readFrame and writeFrame: they bound each direction separately, so a reader
// waiting for input never moves the writer's deadline and vice versa.
func readStreamFrame(ctx context.Context, conn net.Conn) (uint32, []byte, error) {
	if err := ctx.Err(); err != nil {
		return 0, nil, err
	}
	deadline, _ := ctx.Deadline()
	if err := conn.SetReadDeadline(deadline); err != nil {
		return 0, nil, err
	}
	var header [frameHeaderBytes]byte
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

func writeStreamFrame(ctx context.Context, conn net.Conn, frameType uint32, body []byte) error {
	if len(body) > MaxFrameBytes {
		return ErrFrameTooLarge
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline, _ := ctx.Deadline()
	if err := conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	var header [frameHeaderBytes]byte
	binary.BigEndian.PutUint32(header[:4], frameType)
	binary.BigEndian.PutUint32(header[4:], uint32(len(body)))
	if err := writeAll(conn, header[:]); err != nil {
		return contextError(ctx, err)
	}
	if err := writeAll(conn, body); err != nil {
		return contextError(ctx, err)
	}
	return nil
}
