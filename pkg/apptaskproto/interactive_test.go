package apptaskproto

import (
	"bytes"
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

func interactiveRequest() Request {
	return Request{Version: InteractiveVersion, TaskID: "task-1", Command: []string{"/bin/sh"}, TimeoutSeconds: 5, Interactive: true, TTY: true, Rows: 24, Cols: 80}
}

func TestInteractiveRoundTrip(t *testing.T) {
	host, guest := net.Pipe()
	defer host.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var resized InputEvent
	served := make(chan error, 1)
	go func() {
		served <- ServeSession(ctx, guest, func(context.Context, Request, *OutputWriter, *OutputWriter) (Result, error) {
			return Result{}, errors.New("batch handler must not run")
		}, func(_ context.Context, s *InteractiveSession) (Result, error) {
			if s.Request.Rows != 24 || s.Request.Cols != 80 || !s.Request.TTY {
				return Result{}, errors.New("unexpected request")
			}
			for event := range s.Input {
				switch event.Kind {
				case InputStdin:
					if _, err := s.Stdout.Write(bytes.ToUpper(event.Data)); err != nil {
						return Result{}, err
					}
				case InputResize:
					resized = event
				case InputStdinClose:
					_, _ = s.Stderr.Write([]byte("bye"))
					exit := 3
					return Result{Status: StatusFailed, ExitCode: &exit, FailureCode: "command_failed", FailureMessage: "command exited unsuccessfully"}, nil
				}
			}
			return Result{}, errors.New("input closed early")
		})
	}()

	client, err := NewClient(host)
	if err != nil {
		t.Fatal(err)
	}
	input := make(chan InputEvent, 4)
	input <- InputEvent{Kind: InputStdin, Data: []byte("hello")}
	input <- InputEvent{Kind: InputResize, Rows: 50, Cols: 120}
	input <- InputEvent{Kind: InputStdinClose}
	var mu sync.Mutex
	var stdout, stderr bytes.Buffer
	result, err := client.Interact(ctx, interactiveRequest(), input, func(_ context.Context, stream string, chunk []byte) error {
		mu.Lock()
		defer mu.Unlock()
		if stream == "stdout" {
			stdout.Write(chunk)
		} else {
			stderr.Write(chunk)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusFailed || result.ExitCode == nil || *result.ExitCode != 3 {
		t.Fatalf("result = %+v", result)
	}
	if stdout.String() != "HELLO" || stderr.String() != "bye" {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
	if resized.Rows != 50 || resized.Cols != 120 {
		t.Fatalf("resize = %+v", resized)
	}
}

func TestInteractiveHostDisconnectHangsUpGuest(t *testing.T) {
	host, guest := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	causes := make(chan error, 1)
	go func() {
		_ = ServeSession(ctx, guest, func(context.Context, Request, *OutputWriter, *OutputWriter) (Result, error) {
			return Result{}, nil
		}, func(sessionCtx context.Context, s *InteractiveSession) (Result, error) {
			for range s.Input {
			}
			<-sessionCtx.Done()
			causes <- context.Cause(sessionCtx)
			return Result{}, sessionCtx.Err()
		})
	}()
	client, _ := NewClient(host)
	clientCtx, stopClient := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := client.Interact(clientCtx, interactiveRequest(), nil, nil)
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	stopClient()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Interact error = %v", err)
	}
	select {
	case cause := <-causes:
		if !errors.Is(cause, ErrHostDisconnected) {
			t.Fatalf("cause = %v", cause)
		}
	case <-ctx.Done():
		t.Fatal("guest handler did not observe the hang-up")
	}
}

func TestInteractiveRejectedByBatchOnlyGuest(t *testing.T) {
	host, guest := net.Pipe()
	defer host.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		_ = Serve(ctx, guest, func(context.Context, Request, *OutputWriter, *OutputWriter) (Result, error) {
			return Result{}, errors.New("batch handler must not run")
		})
	}()
	client, _ := NewClient(host)
	_, err := client.Interact(ctx, interactiveRequest(), nil, nil)
	var guestErr *GuestError
	if !errors.As(err, &guestErr) || guestErr.Code != "interactive_unsupported" || !errors.Is(err, ErrGuestFailed) {
		t.Fatalf("Interact error = %v", err)
	}
}

func TestInteractiveTimeoutReportsTimedOut(t *testing.T) {
	host, guest := net.Pipe()
	defer host.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		_ = ServeSession(ctx, guest, func(context.Context, Request, *OutputWriter, *OutputWriter) (Result, error) {
			return Result{}, nil
		}, func(sessionCtx context.Context, _ *InteractiveSession) (Result, error) {
			<-sessionCtx.Done()
			return Result{}, sessionCtx.Err()
		})
	}()
	client, _ := NewClient(host)
	req := interactiveRequest()
	req.TimeoutSeconds = 1
	result, err := client.Interact(ctx, req, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusTimedOut || result.FailureCode != "timeout" {
		t.Fatalf("result = %+v", result)
	}
}

func TestInteractiveRequestValidation(t *testing.T) {
	if err := interactiveRequest().Validate(); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	pipes := interactiveRequest()
	pipes.TTY, pipes.Rows, pipes.Cols = false, 0, 0
	if err := pipes.Validate(); err != nil {
		t.Fatalf("pipe request rejected: %v", err)
	}
	for name, mutate := range map[string]func(*Request){
		"v1 interactive":     func(r *Request) { r.Version = Version },
		"v2 batch":           func(r *Request) { r.Interactive = false; r.TTY = false; r.Rows = 0; r.Cols = 0 },
		"output budget":      func(r *Request) { r.MaxOutputBytes = 1024 },
		"zero rows":          func(r *Request) { r.Rows = 0 },
		"too many cols":      func(r *Request) { r.Cols = MaxTerminalCols + 1 },
		"size without tty":   func(r *Request) { r.TTY = false },
		"missing command":    func(r *Request) { r.Command = nil },
		"timeout above hard": func(r *Request) { r.TimeoutSeconds = MaxTimeoutSeconds + 1 },
	} {
		req := interactiveRequest()
		mutate(&req)
		if err := req.Validate(); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("%s: Validate() error = %v", name, err)
		}
	}
	batch := testRequest()
	batch.TTY = true
	if err := batch.Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("v1 request with tty accepted: %v", err)
	}
}

func TestInputEventCodec(t *testing.T) {
	for _, event := range []InputEvent{
		{Kind: InputStdin, Data: []byte("ls\n")},
		{Kind: InputResize, Rows: 1, Cols: MaxTerminalCols},
		{Kind: InputStdinClose},
	} {
		frameType, body, err := encodeInput(event)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeInput(frameType, body)
		if err != nil {
			t.Fatal(err)
		}
		if decoded.Kind != event.Kind || !bytes.Equal(decoded.Data, event.Data) || decoded.Rows != event.Rows || decoded.Cols != event.Cols {
			t.Fatalf("decoded %+v from %+v", decoded, event)
		}
	}
	for _, bad := range []InputEvent{
		{Kind: InputStdin},
		{Kind: InputStdin, Data: make([]byte, MaxStdinChunkBytes+1)},
		{Kind: InputResize, Rows: 0, Cols: 80},
		{Kind: 99},
	} {
		if _, _, err := encodeInput(bad); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("encodeInput(%+v) error = %v", bad.Kind, err)
		}
	}
	if _, err := decodeInput(FrameStdinClose, []byte("x")); err == nil {
		t.Fatal("stdin close with data accepted")
	}
	if _, err := decodeInput(FrameResize, []byte{0, 1}); err == nil {
		t.Fatal("short resize accepted")
	}
}
