// Issue #667 / ADR-078 — runner-side tail host integration for the
// go124 runner. The customer's handler is a static Go binary that
// execs the envelope round-trip; the tail host is the runner-side
// pump that drains envelope.TailPipePath after the handler returns
// and emits one 0x04 DGRAM per task via the tail-events proxy.
//
// Where this fits in the request flow:
//
//	handle()
//	  invokeHandler(ctx, handlerPath, env)  // customer subprocess
//	  → on success:
//	      drainTailHost(r.Context(), env, &resp)  // THIS file
//	      write response envelope to wire
//	      signal.SignalReady(...)                 // framework_ready DGRAM
//
// The drain runs BEFORE the response envelope is written and BEFORE
// the framework_ready signal fires. The ordering matters because:
//   - the customer's tail_seconds window ends before the response
//     returns (the handler's `waitUntil(promise)` is observed as
//     "drained" only after the runner's WaitGroup finishes)
//   - the framework_ready signal fires on the first non-5xx response
//     — wake snapshot capture waits on this signal, and the snapshot
//     shouldn't capture a VM that's still draining tail tasks
//
// ADR-078 §"Reaper gate" was amended (issue #667 follow-up) to
// document this timing change: previously the signal fired on the
// first non-5xx response, NOW it fires after tailHost.Drain()
// returns. The 5s snapshotAndPark watchdog on schedd is the upper
// bound if the drain hangs.
//
// The runner shim is a one-liner — the load-bearing logic lives
// in internal.DrainForResponse (Dedup'd from the 5x near-identical
// shims by issue #667 review item #11).
package main

import (
	"context"

	"github.com/onebox-faas/faas/guest/runners/internal"
)

func drainTailHost(ctx context.Context, env envelope, resp *response) {
	internal.DrainForResponse(ctx, reportedRuntime, env.WaitUntilSec, env.TailPipePath, &resp.TailErrors)
}
