package apptaskproto

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func testRequest() Request {
	return Request{Version: Version, TaskID: "task-1", Command: []string{"bin/console", "cleanup"}, TimeoutSeconds: 2, MaxOutputBytes: 1024}
}

func TestClientServeRoundTrip(t *testing.T) {
	host, guest := net.Pipe()
	defer host.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, guest, func(_ context.Context, req Request, stdout, stderr *OutputWriter) (Result, error) {
			if req.TaskID != "task-1" {
				return Result{}, errors.New("unexpected task")
			}
			_, _ = stdout.Write([]byte("hello\n"))
			_, _ = stderr.Write([]byte("warning\n"))
			exit := 0
			return Result{Status: StatusSucceeded, ExitCode: &exit}, nil
		})
	}()
	client, err := NewClient(host)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Execute(ctx, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSucceeded || string(result.Stdout) != "hello\n" || string(result.Stderr) != "warning\n" {
		t.Fatalf("result = %+v", result)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestOutputLimitTruncatesWithoutFailingCommand(t *testing.T) {
	host, guest := net.Pipe()
	defer host.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, guest, func(_ context.Context, _ Request, stdout, _ *OutputWriter) (Result, error) {
			payload := make([]byte, 2048)
			if n, err := stdout.Write(payload); err != nil || n != len(payload) {
				return Result{}, errors.New("writer changed command semantics")
			}
			exit := 0
			return Result{Status: StatusSucceeded, ExitCode: &exit}, nil
		})
	}()
	client, _ := NewClient(host)
	result, err := client.Execute(ctx, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !result.OutputTruncated || len(result.Stdout) != 1024 {
		t.Fatalf("result truncated=%v stdout=%d", result.OutputTruncated, len(result.Stdout))
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRequestValidation(t *testing.T) {
	for _, mutate := range []func(*Request){
		func(r *Request) { r.Command = nil },
		func(r *Request) { r.Command = []string{"echo", "x"}; r.CommandShell = true },
		func(r *Request) { r.TimeoutSeconds = 0 },
		func(r *Request) { r.MaxOutputBytes = MaxOutputBytes + 1 },
		func(r *Request) { r.Command = []string{"bad\x00command"} },
	} {
		req := testRequest()
		mutate(&req)
		if err := req.Validate(); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("Validate() error = %v", err)
		}
	}
}

func TestClientIsSingleUse(t *testing.T) {
	host, guest := net.Pipe()
	defer host.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		_ = Serve(ctx, guest, func(context.Context, Request, *OutputWriter, *OutputWriter) (Result, error) {
			return Result{Status: StatusSucceeded}, nil
		})
	}()
	client, _ := NewClient(host)
	if _, err := client.Execute(ctx, testRequest()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Execute(ctx, testRequest()); !errors.Is(err, ErrAlreadyUsed) {
		t.Fatalf("second execute = %v", err)
	}
}
