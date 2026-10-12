package runnerparity

// Issue #667 / ADR-078 (PR 3) — checkbox #7 acceptance test:
// "all 5 runners expose waitUntil consistently."
//
// The runner-side tail host is the per-request pump that drains
// envelope.TailPipePath and emits one 0x04 DGRAM per task via
// the tail-events proxy. This test pins the cross-runtime shape:
// every runner wires the host, every runner reads the same
// env vars (FAAS_TAIL_WAIT_SEC + FAAS_TAIL_PIPE_PATH), every
// runner hands them to internal.Serve, which drains the tail after the handler returns and
// before the response envelope is written.
//
// Why a file-walk rather than a per-runtime test: the cross-
// runtime parity claim is structural — the load-bearing question
// is "does every active runtime honor the same primitive" — and
// a single file-walk covers all of them in microseconds. The
// per-runtime tail round-trip tests (TestHandle_WaitUntilEnvelopeRoundTrip)
// cover the per-runtime wire shape; this test covers the
// structural parity.
//
// Pattern mirrors TestRunners_InvokeHandlerUsesCmdRun — file
// path string matching is sufficient for the structural pin,
// no Go AST parsing needed.

import (
	"strings"
	"testing"
)

// TestParity_AllRuntimesHonorWaitUntil walks every
// guest/runners/<runtime>/ main.go and asserts:
//
//  1. main.go reads the FAAS_TAIL_WAIT_SEC + FAAS_TAIL_PIPE_PATH env vars
//  2. main.go passes them to internal.Serve (WaitUntilSec, TailPipePath)
//  3. internal/serve.go drains the tail with internal.DrainForResponse, for
//     a complete response and after a streamed one (ADR-999)
//
// A future contributor who adds a new runtime (e.g. ruby) must
// mirror the same plumbing — the test will fail until they do,
// pinning the cross-runtime parity claim made in issue #667 §"Rules".
func TestParity_AllRuntimesHonorWaitUntil(t *testing.T) {
	mains := runnerMains(t)
	names := requireRunnersDelegate(t, mains)
	var missing []string
	for _, name := range names {
		main := mains[name]
		ok := true
		for _, want := range []string{"FAAS_TAIL_WAIT_SEC", "FAAS_TAIL_PIPE_PATH", "WaitUntilSec:", "TailPipePath:"} {
			if !strings.Contains(main, want) {
				t.Errorf("%s: main.go does not carry %s to internal.Serve — the tail host cannot drain this runner's pipe", name, want)
				ok = false
			}
		}
		if !ok {
			missing = append(missing, name)
		}
	}
	if got := strings.Count(serveSource(t), "DrainForResponse("); got < 2 {
		t.Errorf("internal/serve.go calls DrainForResponse %d times; want both the complete and the streamed response paths", got)
	}
	if len(missing) > 0 {
		t.Fatalf("%d/%d runners missing the waitUntil plumbing: %v", len(missing), len(names), missing)
	}
	t.Logf("walked %d runner dirs — all thread the tail settings into internal.Serve, which drains them", len(names))
}
