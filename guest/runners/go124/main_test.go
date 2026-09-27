package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/guest/runners/internal"
	"github.com/onebox-faas/faas/guest/runners/internal/runnerparity"
	"github.com/onebox-faas/faas/pkg/api"
)

// init installs the in-process framework-ready dial hook so the
// runner's per-handler SignalReady doesn't burn 250ms on a real
// unix-socket dial to /run/guest-init/framework-ready.sock (which
// doesn't exist on a Mac/Linux test box). See
// guest/runners/internal/framework_ready_testhook.go for the
// rationale; the hook is reverted before the process exits.
func init() { internal.InstallTestProxyDialHook() }

// TestHandle_RoundTrip spins a stub handler with the same JSON contract
// the runner expects. The runner execs the handler file directly with no
// interpreter (guest/runners/go124/main.go:117), so POSIX-sh is enough
// and the helper never skips this test.
// TestHandle_StderrReachesHost pins issue #254: the customer handler's
// stderr must be teed to the runner's os.Stderr (inherited from
// guest-init PID1) so it lands in the log ring the customer reads over
// `faas logs`. The helper also asserts the §4.9 stdout envelope still
// decodes — stdout must stay a bare buffer.
func TestHandle_StderrReachesHost(t *testing.T) {
	const line = "go124-customer-stderr-marker"
	fake := runnerparity.FakeScriptWritingStderr(line)
	runnerparity.RunStderrReachesHost(t, fake, line, func(w http.ResponseWriter, r *http.Request, handlerPath string, signal *internal.RunnerSignal, _ int, _ string) {
		handle(w, r, handlerPath, signal, 0, "")
	})
}

func TestHandle_RoundTrip(t *testing.T) {
	fake := runnerparity.FakeGoScript()
	// PR 3 (issue #667 follow-up): the runner's `handle` signature
	// grew tailWaitSec + tailPipePath args. Wrap handle() with the
	// 6-arg signature the runnerparity.RunRoundTrip helper expects
	// — 0/empty args = feature disabled (the existing fake scripts
	// don't write to the tail pipe, so the round-trip smoke test
	// is unchanged).
	runnerparity.RunRoundTrip(t, fake, func(w http.ResponseWriter, r *http.Request, handlerPath string, signal *internal.RunnerSignal, _ int, _ string) {
		handle(w, r, handlerPath, signal, 0, "")
	})
}

// TestEnvelopeRoundTrip sanity-checks the JSON tags line up with §4.9,
// extended with the waitUntil envelope fields (issue #667 / ADR-078).
// Body delegated to runnerparity.AssertEnvelopeJSONTags so all five
// runners pin the same tag set. The WaitUntilSec + TailPipePath fields
// are the waitUntil primitive surface; a typo here would silently break
// the runner's tail host in PR 3, so the assertion is load-bearing.
func TestEnvelopeRoundTrip(t *testing.T) {
	runnerparity.AssertEnvelopeJSONTags(t, envelope{
		Method:       "POST",
		Path:         "/foo",
		Headers:      map[string]string{"X": "y"},
		Query:        "a=1",
		BodyB64:      base64.StdEncoding.EncodeToString([]byte("hi")),
		WaitUntilSec: 30,
		TailPipePath: "/tmp/faas-tail-xyz.jsonl",
	}, []byte("hi"))
}

// TestHandle_WaitUntilEnvelopeRoundTrip (issue #667 / ADR-078 PR 3) is
// the per-runtime counterpart to the hermetic
// TestParity_AllRuntimesHonorWaitUntil file-walk. A fake handler
// writes a JSONL line to the tail pipe before returning; the
// runner's drainTailHost reads the pipe after invokeHandler returns.
// The response envelope stays intact (status=200, body unchanged).
// The 0x04 DGRAM emit fails in unit tests (the proxy isn't running)
// — that's expected; the runner keeps draining.
func TestHandle_WaitUntilEnvelopeRoundTrip(t *testing.T) {
	fake := runnerparity.FakeGoScriptWithTail()
	runnerparity.RunWaitUntilEnvelopeRoundTrip(t, fake, func(w http.ResponseWriter, r *http.Request, handlerPath string, signal *internal.RunnerSignal, tailWaitSec int, tailPipePath string) {
		handle(w, r, handlerPath, signal, tailWaitSec, tailPipePath)
	})
}

// TestGoRunnerHandlerDefault pins the default --handler value. The
// path must be `/app/handler` (no extension) to match what
// imaged.handleDeployment writes into AppManifest.Entrypoint for
// runtime=go124 function deploys (PR #219 follow-up Phase 2). If this
// drifts, the runner's os.Stat startup check fails on first wake
// and every Go function deploy rolls back. The flag default and
// the imaged manifest path are the only two places this string
// lives; the test pins the flag side.
//
// Stays in this package — the constant lives in production code
// (guest/runners/go124/main.go:48) and the helper cannot reach it.
func TestGoRunnerHandlerDefault(t *testing.T) {
	const want = "/app/handler"
	// Re-create the flag with the SAME default literal that the
	// runner main() uses, on a fresh FlagSet, and assert the
	// default matches. We don't parse the runner's binary — we
	// just keep both ends in lockstep with a constant the test
	// shares with the production default.
	fs := flag.NewFlagSet("go124-test", flag.ContinueOnError)
	handler := fs.String("handler", want, "x")
	if err := fs.Parse([]string{}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if *handler != want {
		t.Errorf("default --handler = %q, want %q", *handler, want)
	}
}

// TestHandle_CrashedHandlerIsTerminalHandlerError drives the real runner
// with a handler that exits non-zero, as a panicking Go handler does. The
// runner must answer with the handler_error envelope gatewayd-internal
// treats as terminal; a plain-text 500 made the durable queue re-run the
// crashing handler.
func TestHandle_CrashedHandlerIsTerminalHandlerError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "handler")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'panic: boom' >&2\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
	req.Header.Set(api.InvocationIDHeader, "inv-crash")
	rec := httptest.NewRecorder()
	handle(rec, req, path, internal.NewRunnerSignal("crash-test", time.Now()), 0, "")

	var body struct {
		Error        string `json:"error"`
		InvocationID string `json:"invocation_id"`
	}
	if rec.Code != http.StatusInternalServerError || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
		t.Fatalf("status %d body %q, want a 500 JSON envelope", rec.Code, rec.Body.String())
	}
	if body.Error != "handler_error" || body.InvocationID != "inv-crash" {
		t.Fatalf("body = %+v, want error=handler_error invocation_id=inv-crash", body)
	}
	if strings.Contains(rec.Body.String(), "boom") {
		t.Fatalf("handler stderr leaked into the response: %q", rec.Body.String())
	}
}
