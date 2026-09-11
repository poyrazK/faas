// adr: 171
package executionproto

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func testRequest() Request {
	return Request{
		Version:     Version,
		ExecutionID: "exec-123",
		Runtime:     api.ExecutionRuntimeNode22,
		Source:      "export default async function main(input) { return {answer: input.n + 1}; }",
		Input:       json.RawMessage(`{"n": 41}`),
		TimeoutMS:   2_000,
		MaxOutput:   4 << 10,
		NetworkMode: api.ExecutionNetworkNone,
	}
}

func TestClientServeRoundTripStreamsBoundedResult(t *testing.T) {
	host, guest := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	serveDone := make(chan error, 1)
	go func() {
		serveDone <- Serve(ctx, guest, func(_ context.Context, req Request, stdout, stderr *OutputWriter) (Result, error) {
			if req.ExecutionID != "exec-123" {
				return Result{}, errors.New("unexpected execution id")
			}
			if _, err := stdout.Write([]byte("hello\n")); err != nil {
				return Result{}, err
			}
			if _, err := stderr.Write([]byte("warning\n")); err != nil {
				return Result{}, err
			}
			return Result{Status: api.ExecutionStatusSucceeded, Result: json.RawMessage(`{"answer":42}`)}, nil
		})
	}()

	client, err := NewClient(host)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.Execute(ctx, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Result) != `{"answer":42}` {
		t.Fatalf("result = %s", got.Result)
	}
	if string(got.Stdout) != "hello\n" || string(got.Stderr) != "warning\n" {
		t.Fatalf("streams = %q/%q", got.Stdout, got.Stderr)
	}
	if err := <-serveDone; err != nil {
		t.Fatal(err)
	}
	_ = host.Close()
}

func TestClientIsSingleUse(t *testing.T) {
	host, guest := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		_ = Serve(ctx, guest, func(context.Context, Request, *OutputWriter, *OutputWriter) (Result, error) {
			return Result{Status: api.ExecutionStatusSucceeded, Result: json.RawMessage("null")}, nil
		})
	}()
	client, err := NewClient(host)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Execute(ctx, testRequest()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Execute(ctx, testRequest()); !errors.Is(err, ErrAlreadyUsed) {
		t.Fatalf("second execute error = %v, want %v", err, ErrAlreadyUsed)
	}
	_ = host.Close()
}

func TestServeRejectsInvalidRequestBeforeHandler(t *testing.T) {
	host, guest := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- Serve(ctx, guest, func(context.Context, Request, *OutputWriter, *OutputWriter) (Result, error) {
			return Result{Status: api.ExecutionStatusSucceeded, Result: json.RawMessage("null")}, nil
		})
	}()
	if err := writeFrame(ctx, host, FrameRequest, []byte(`{"version":1,"execution_id":"x","runtime":"bogus"}`)); err != nil {
		t.Fatal(err)
	}
	typ, body, err := readFrame(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	if typ != FrameError {
		t.Fatalf("frame type = %d, want error", typ)
	}
	var ef ErrorFrame
	if err := json.Unmarshal(body, &ef); err != nil {
		t.Fatal(err)
	}
	if ef.Code != "invalid_request" {
		t.Fatalf("error code = %q", ef.Code)
	}
	if err := <-serveDone; err != nil {
		t.Fatal(err)
	}
	_ = host.Close()
}

func TestClientRejectsCombinedOutputOverBudget(t *testing.T) {
	host, guest := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		_, _, _ = readFrame(ctx, guest)
		_ = writeFrame(ctx, guest, FrameStdout, make([]byte, 1024))
		_ = writeFrame(ctx, guest, FrameResult, []byte(`{"status":"succeeded","result":"x"}`))
	}()
	req := testRequest()
	req.MaxOutput = 1024
	client, err := NewClient(host)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Execute(ctx, req); !errors.Is(err, ErrOutputLimitExceeded) {
		t.Fatalf("error = %v, want %v", err, ErrOutputLimitExceeded)
	}
	_ = host.Close()
}

func TestReadFrameRejectsOversizedBodyWithoutAllocation(t *testing.T) {
	host, guest := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		var hdr [frameHeaderBytes]byte
		binary.BigEndian.PutUint32(hdr[0:4], FrameRequest)
		binary.BigEndian.PutUint32(hdr[4:8], uint32(MaxFrameBytes+1))
		_, err := guest.Write(hdr[:])
		done <- err
	}()
	_, _, err := readFrame(ctx, host)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("error = %v, want %v", err, ErrFrameTooLarge)
	}
	_ = host.Close()
	_ = <-done
}

func TestServeMapsHandlerDeadlineToTimedOutResult(t *testing.T) {
	host, guest := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		_ = Serve(ctx, guest, func(ctx context.Context, _ Request, _, _ *OutputWriter) (Result, error) {
			<-ctx.Done()
			return Result{}, ctx.Err()
		})
	}()
	req := testRequest()
	req.TimeoutMS = api.ExecutionTimeoutMinMS
	client, err := NewClient(host)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.Execute(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != api.ExecutionStatusTimedOut || got.FailureCode != "timeout" {
		t.Fatalf("result = %+v", got)
	}
	_ = host.Close()
}
