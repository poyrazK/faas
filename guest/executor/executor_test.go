package executor

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
)

func TestNodeExecutionReturnsJSONResult(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	e := New()
	result, err := e.Handle(context.Background(), executionproto.Request{
		Version: executionproto.Version, ExecutionID: "node-test", Runtime: api.ExecutionRuntimeNode22,
		Source: `export default async (input, context) => ({value: input.value + 1, runtime: context.runtime})`,
		Input:  json.RawMessage(`{"value":41}`), TimeoutMS: 3000, MaxOutput: 4096,
		NetworkMode: api.ExecutionNetworkNone,
	}, nil, nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if result.Status != api.ExecutionStatusSucceeded || string(result.Result) != `{"value":42,"runtime":"node22"}` {
		t.Fatalf("result = %+v", result)
	}
}

func TestPythonExecutionSupportsAsyncMain(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	e := New()
	result, err := e.Handle(context.Background(), executionproto.Request{
		Version: executionproto.Version, ExecutionID: "python-test", Runtime: api.ExecutionRuntimePython312,
		Source: "async def main(input, context):\n    return {'value': input['value'] + 1, 'runtime': context['runtime']}\n",
		Input:  json.RawMessage(`{"value":41}`), TimeoutMS: 3000, MaxOutput: 4096,
		NetworkMode: api.ExecutionNetworkNone,
	}, nil, nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if result.Status != api.ExecutionStatusSucceeded || string(result.Result) != `{"value":42,"runtime":"python312"}` {
		t.Fatalf("result = %+v", result)
	}
}

func TestNodeExecutionStagesEphemeralBundle(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	result, err := New().Handle(context.Background(), executionproto.Request{
		Version: executionproto.Version, ExecutionID: "node-bundle-test", Runtime: api.ExecutionRuntimeNode22,
		Entrypoint: "src/main.mjs", Files: []api.ExecutionFile{
			{Path: "src/main.mjs", Content: []byte("import { answer } from './lib.mjs'; export default async input => ({value: answer + input.value})")},
			{Path: "src/lib.mjs", Content: []byte("export const answer = 41")},
		},
		Input: json.RawMessage(`{"value":1}`), TimeoutMS: 3000, MaxOutput: 4096,
		NetworkMode: api.ExecutionNetworkNone,
	}, nil, nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if result.Status != api.ExecutionStatusSucceeded || string(result.Result) != `{"value":42}` {
		t.Fatalf("result = %+v", result)
	}
}

func TestPythonExecutionStagesEphemeralBundle(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	result, err := New().Handle(context.Background(), executionproto.Request{
		Version: executionproto.Version, ExecutionID: "python-bundle-test", Runtime: api.ExecutionRuntimePython312,
		Entrypoint: "pkg/main.py", Files: []api.ExecutionFile{
			{Path: "pkg/main.py", Content: []byte("from helper import answer\ndef main(input, context):\n    return {'value': answer + input['value']}\n")},
			{Path: "pkg/helper.py", Content: []byte("answer = 41\n")},
		},
		Input: json.RawMessage(`{"value":1}`), TimeoutMS: 3000, MaxOutput: 4096,
		NetworkMode: api.ExecutionNetworkNone,
	}, nil, nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if result.Status != api.ExecutionStatusSucceeded || string(result.Result) != `{"value":42}` {
		t.Fatalf("result = %+v", result)
	}
}

func TestExecutionTimeoutDoesNotReturnSuccess(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	e := New()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := e.Handle(ctx, executionproto.Request{
		Version: executionproto.Version, ExecutionID: "timeout-test", Runtime: api.ExecutionRuntimeNode22,
		Source: `export default async () => { await new Promise(resolve => setTimeout(resolve, 5000)); return 1 }`,
		Input:  json.RawMessage("null"), TimeoutMS: 5000, MaxOutput: 4096,
		NetworkMode: api.ExecutionNetworkNone,
	}, nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
}

func TestHandleEnforcesRequestTimeoutWithoutParentDeadline(t *testing.T) {
	e := New()
	// Avoid depending on a host-installed language runtime: the production
	// resolver is replaced with a deterministic long-running helper, while the
	// real process-group cancellation path remains exercised.
	e.resolve = func(executionproto.Request) (string, []string, string, error) {
		return "/bin/sh", []string{"-c", "sleep 5"}, nodeSourceName, nil
	}
	started := time.Now()
	_, err := e.Handle(context.Background(), executionproto.Request{
		Version: executionproto.Version, ExecutionID: "request-timeout-test", Runtime: api.ExecutionRuntimeNode22,
		Source: "export default () => 1", Input: json.RawMessage("null"), TimeoutMS: 100, MaxOutput: 4096,
		NetworkMode: api.ExecutionNetworkNone,
	}, nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("request timeout took %s, want bounded cancellation", elapsed)
	}
}

func TestNodeConsoleOutputUsesProtocolStream(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	server, client := net.Pipe()
	defer client.Close()
	serveDone := make(chan error, 1)
	go func() { serveDone <- executionproto.Serve(context.Background(), server, New().Handle) }()
	protoClient, err := executionproto.NewClient(client)
	if err != nil {
		t.Fatal(err)
	}
	result, err := protoClient.Execute(context.Background(), executionproto.Request{
		Version: executionproto.Version, ExecutionID: "stdout-test", Runtime: api.ExecutionRuntimeNode22,
		Source: `export default async (input) => { console.log("hello"); return input }`,
		Input:  json.RawMessage(`{"ok":true}`), TimeoutMS: 3000, MaxOutput: 4096,
		NetworkMode: api.ExecutionNetworkNone,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if string(result.Stdout) != "hello\n" || string(result.Result) != `{"ok":true}` {
		t.Fatalf("result = %+v", result)
	}
	if err := <-serveDone; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestNodeOutputLimitReturnsBoundedFailure(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	server, client := net.Pipe()
	defer client.Close()
	serveDone := make(chan error, 1)
	go func() { serveDone <- executionproto.Serve(context.Background(), server, New().Handle) }()
	protoClient, err := executionproto.NewClient(client)
	if err != nil {
		t.Fatal(err)
	}
	result, err := protoClient.Execute(context.Background(), executionproto.Request{
		Version: executionproto.Version, ExecutionID: "output-limit-test", Runtime: api.ExecutionRuntimeNode22,
		Source: `export default async () => { console.log("x".repeat(4096)); return true }`,
		// Output-limit admission must win over the guest timeout, but starting
		// Node under the race-enabled package suite can exceed three seconds on
		// a busy CI runner. Keep enough headroom for process startup while still
		// exercising the bounded-output cancellation path.
		Input: json.RawMessage("null"), TimeoutMS: 10_000, MaxOutput: 1024,
		NetworkMode: api.ExecutionNetworkNone,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != api.ExecutionStatusFailed || !result.OutputTruncated || result.FailureCode != "output_limit" {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Stdout)+len(result.Stderr)+len(result.Result) > 1024 {
		t.Fatalf("result exceeded output cap: %d", len(result.Stdout)+len(result.Stderr)+len(result.Result))
	}
	if err := <-serveDone; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestExecutionRejectsUnsupportedRuntime(t *testing.T) {
	_, err := New().Handle(context.Background(), executionproto.Request{
		Version: executionproto.Version, ExecutionID: "bad-runtime", Runtime: "ruby",
		Source: "main", Input: json.RawMessage("null"), TimeoutMS: 1000, MaxOutput: 1024,
		NetworkMode: api.ExecutionNetworkNone,
	}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid request") {
		t.Fatalf("error = %v, want invalid request", err)
	}
}

func TestGuestEnvContainsNoInheritedValues(t *testing.T) {
	env := guestEnv(api.ExecutionRuntimeNode24)
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "SECRET") || strings.Contains(joined, "AWS_") {
		t.Fatalf("guest env contains inherited secret-like value: %q", joined)
	}
	if !strings.Contains(joined, "FAAS_RUNTIME=node24") {
		t.Fatalf("runtime missing from env: %q", joined)
	}
}
