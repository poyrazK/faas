package runnerparity

// TestRunners_TeeCustomerStderr pins the issue #254 wiring across every
// runner package. The behavioural sibling of this pin lives in each
// runner's own main_test.go (TestHandle_StderrReachesHost, driven by
// RunStderrReachesHost) — that test proves the tee works end to end,
// but only for runtimes whose interpreter is on PATH. On a box without
// `node` or `python3` those tests skip, so this static walk is the
// backstop that cannot be skipped: it reads every main.go and asserts
// the wiring by source inspection.
//
// Two halves, both load-bearing:
//
//  1. cmd.Stderr MUST tee to os.Stderr. The runner's os.Stderr is
//     inherited from guest-init (PID1), which tees its child's output
//     into the supervisor ring (guest/init/main_linux.go:276) that vmmd
//     drains into pkg/fcvm/logbuf. A bare `cmd.Stderr = &stderr` keeps
//     customer stack traces inside the microVM forever.
//
//  2. cmd.Stdout MUST NOT tee. Stdout carries the §4.9 response
//     envelope, which is json.Unmarshal'd from that exact buffer. A
//     contributor "fixing logging" by teeing stdout too would interleave
//     customer writes into the envelope and break every response.
//
// Capturing customer *stdout* is a real gap and deliberately NOT solved
// here — it needs a separate framed channel (a spec change, not a bug
// fix). See issue #254.
//
// Every runner delegates to the shared internal.Serve (ADR-999), so the walk
// checks each runner delegates and inspects internal/serve.go once. It
// mirrors TestRunners_InvokeHandlerUsesCmdRun: file-path string matching, no
// AST parse, runs in microseconds.

import (
	"strings"
	"testing"
)

func TestRunners_TeeCustomerStderr(t *testing.T) {
	names := requireRunnersDelegate(t, runnerMains(t))
	src := serveSource(t)

	// (1) stderr is teed to the host.
	if !strings.Contains(src, "cmd.Stderr = io.MultiWriter(&stderr, os.Stderr)") {
		t.Errorf("internal/serve.go: cmd.Stderr is not teed to os.Stderr (issue #254).\n" +
			"want: cmd.Stderr = io.MultiWriter(&stderr, os.Stderr)\n" +
			"Without the tee, the customer's stack traces never leave the " +
			"microVM — guest-init's ring (and therefore `faas logs`) only " +
			"ever sees platform noise.")
	}

	// (2) stdout is NOT teed — it is protocol-bearing.
	if strings.Contains(src, "cmd.Stdout = io.MultiWriter") {
		t.Errorf("internal/serve.go: cmd.Stdout is teed — this breaks the §4.9 envelope.\n" +
			"stdout carries the response envelope and is json.Unmarshal'd " +
			"from that buffer; interleaving customer writes corrupts every " +
			"response. Capturing customer stdout needs a separate framed " +
			"channel (issue #254), not a tee.")
	}
	t.Logf("walked %d runner dirs %v — all delegate to internal.Serve: stderr teed, stdout untouched", len(names), names)
}
