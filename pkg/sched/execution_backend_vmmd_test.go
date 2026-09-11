// adr: 171
package sched

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
)

func TestVmmdExecutionBackendDecryptsOnlyAfterRestoreAndMapsResult(t *testing.T) {
	var restored ExecutionRestoreRequest
	transport := &recordingExecutionTransport{result: executionproto.Result{
		Status: api.ExecutionStatusSucceeded,
		Result: json.RawMessage(`{"ok":true}`),
		Stdout: []byte("hello\n"),
	}}
	backend := NewVmmdExecutionBackend(func(_ context.Context, request ExecutionRestoreRequest) (VmmdExecutionTransport, error) {
		restored = request
		return transport, nil
	}, func(_ context.Context, sealed []byte, kid string) (string, json.RawMessage, error) {
		if string(sealed) != "sealed" || kid != "kid-1" {
			t.Fatalf("decoder received %q/%q", sealed, kid)
		}
		return "export default async function main() { return true; }", json.RawMessage(`{"n":1}`), nil
	})

	limits := api.ResolvedExecutionLimits{TimeoutMS: 1000, MaxOutputBytes: 4096}
	session, err := backend.Restore(context.Background(), ExecutionRestoreRequest{
		ID: "exec-1", AccountID: "acct-1", Runtime: api.ExecutionRuntimeNode22,
		NetworkMode: api.ExecutionNetworkNone, Limits: limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := ExecutionPayload{Sealed: []byte("sealed"), KID: "kid-1"}
	outcome, err := session.Execute(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	if restored.ID != "exec-1" || restored.AccountID != "acct-1" {
		t.Fatalf("restore request = %+v", restored)
	}
	if transport.request.ExecutionID != "exec-1" || transport.request.Runtime != api.ExecutionRuntimeNode22 {
		t.Fatalf("wire request = %+v", transport.request)
	}
	if string(outcome.Result) != `{"ok":true}` || outcome.Stdout != "hello\n" {
		t.Fatalf("outcome = %+v", outcome)
	}
	if err := session.Destroy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := session.Destroy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if transport.destroyCalls != 1 {
		t.Fatalf("destroy calls = %d, want 1", transport.destroyCalls)
	}
}

func TestVmmdExecutionSessionClampsGuestTimeoutToRemainingDeadline(t *testing.T) {
	transport := &recordingExecutionTransport{result: executionproto.Result{
		Status: api.ExecutionStatusSucceeded,
		Result: json.RawMessage("null"),
	}}
	backend := NewVmmdExecutionBackend(func(context.Context, ExecutionRestoreRequest) (VmmdExecutionTransport, error) {
		return transport, nil
	}, func(context.Context, []byte, string) (string, json.RawMessage, error) {
		return "return true", json.RawMessage("null"), nil
	})
	session, err := backend.Restore(context.Background(), ExecutionRestoreRequest{
		ID: "exec-2", Runtime: api.ExecutionRuntimeNode22,
		NetworkMode: api.ExecutionNetworkNone,
		Limits:      api.ResolvedExecutionLimits{TimeoutMS: 10_000, MaxOutputBytes: 1024},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if _, err := session.Execute(ctx, ExecutionPayload{Sealed: []byte("s")}); err != nil {
		t.Fatal(err)
	}
	if transport.request.TimeoutMS > 300 || transport.request.TimeoutMS < 100 {
		t.Fatalf("timeout_ms = %d, want remaining deadline", transport.request.TimeoutMS)
	}
}

func TestOutcomeFromProtocolResultDoesNotExposeGuestFailureText(t *testing.T) {
	outcome := outcomeFromProtocolResult(executionproto.Result{
		Status:         api.ExecutionStatusFailed,
		Result:         json.RawMessage("null"),
		FailureCode:    "stack_trace",
		FailureMessage: "/tmp/tenant-secret.js: line 4: secret=abc",
	})
	if outcome.FailureCode != "guest_error" || outcome.FailureMessage != "execution failed inside the isolated guest" {
		t.Fatalf("outcome = %+v", outcome)
	}
	if outcome.FailureMessage == "" || outcome.FailureMessage == "/tmp/tenant-secret.js: line 4: secret=abc" {
		t.Fatal("guest failure detail leaked")
	}
}

func TestVmmdExecutionBackendRequiresBothWiringFunctions(t *testing.T) {
	if _, err := (&VmmdExecutionBackend{}).Restore(context.Background(), ExecutionRestoreRequest{}); !errors.Is(err, ErrExecutionCoordinatorNotWired) {
		t.Fatalf("error = %v", err)
	}
}

type recordingExecutionTransport struct {
	request      executionproto.Request
	result       executionproto.Result
	destroyCalls int
}

func (t *recordingExecutionTransport) Execute(_ context.Context, request executionproto.Request) (executionproto.Result, error) {
	t.request = request
	return t.result, nil
}

func (t *recordingExecutionTransport) Destroy(context.Context) error {
	t.destroyCalls++
	return nil
}
