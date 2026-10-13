package main

import (
	"encoding/base64"
	"flag"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/guest/runners/internal"
	"github.com/onebox-faas/faas/guest/runners/internal/runnerparity"
)

// init installs the in-process framework-ready dial hook so the
// runner's per-handler SignalReady doesn't burn 250ms on a real
// unix-socket dial to /run/guest-init/framework-ready.sock (which
// doesn't exist on a Mac/Linux test box). See
// guest/runners/internal/framework_ready_testhook.go for the
// rationale; the hook is reverted before the process exits.
func init() { internal.InstallTestProxyDialHook() }

// TestHandle_RoundTrip spins a stub handler with the same JSON contract
// the runner expects. The runner's spawn-the-binary path needs an
// actual `node` on PATH; the helper skips when missing.
// TestHandle_StderrReachesHost pins issue #254: the customer handler's
// stderr must be teed to the runner's os.Stderr (inherited from
// guest-init PID1) so it lands in the log ring the customer reads over
// `faas logs`. The helper also asserts the §4.9 stdout envelope still
// decodes — stdout must stay a bare buffer.
func TestHandle_StderrReachesHost(t *testing.T) {
	const line = "node22-customer-stderr-marker"
	fake := runnerparity.FakeNodeScriptWritingStderr(line)
	runnerparity.RunStderrReachesHost(t, fake, line, func(w http.ResponseWriter, r *http.Request, handlerPath string, signal *internal.RunnerSignal, _ int, _ string) {
		handle(w, r, handlerPath, signal, 0, "")
	})
}

func TestHandle_RoundTrip(t *testing.T) {
	fake := runnerparity.FakeNodeScript()
	// PR 3 (issue #667 follow-up): wrap handle() with 0/empty
	// tail-primitive args (feature disabled in the round-trip
	// smoke test). The new TestHandle_WaitUntilEnvelopeRoundTrip
	// exercises the tail host path.
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
// runner core (internal.Serve) drains the pipe after the handler returns.
func TestHandle_WaitUntilEnvelopeRoundTrip(t *testing.T) {
	fake := runnerparity.FakeNodeScriptWithTail()
	runnerparity.RunWaitUntilEnvelopeRoundTrip(t, fake, func(w http.ResponseWriter, r *http.Request, handlerPath string, signal *internal.RunnerSignal, tailWaitSec int, tailPipePath string) {
		handle(w, r, handlerPath, signal, tailWaitSec, tailPipePath)
	})
}

// TestNodeRunnerHandlerDefault pins the default --handler value. The
// path must be `/app/node22.js` to match what imaged.handleDeployment
// writes into AppManifest.Entrypoint for runtime=node22 function
// deploys. (A previous revision of this runner had `/app/handler.js`
// here, which silently mismatched the imaged manifest and only
// surfaced as a first-wake rollback — the path drift was load-bearing
// enough to ship the fix in the go124 PR. If this test breaks, every
// node22 function deploy rolls back on first wake.)
//
// Stays in this package — the constant lives in production code
// (guest/runners/node22/main.go:54) and the helper cannot reach it.
func TestNodeRunnerHandlerDefault(t *testing.T) {
	const want = "/app/node22.js"
	fs := flag.NewFlagSet("node22-test", flag.ContinueOnError)
	handler := fs.String("handler", want, "x")
	if err := fs.Parse([]string{}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if *handler != want {
		t.Errorf("default --handler = %q, want %q", *handler, want)
	}
}
