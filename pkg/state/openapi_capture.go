package state

import (
	"context"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// OpenAPICaptureFn projects a deployment's current edge-rule
// set into the canonical JSON snapshot bytes + SHA-256 digest.
//
// The Store calls this from inside its in-flight status='live'
// tx so the snapshot UPSERT commits atomically with the status
// flip. The contract:
//
//   - ctx: the caller's ctx (cancellation / tracing propagate).
//   - db:  sqlc.DBTX — the in-flight tx (or pool, for callers
//     that don't have one). The callback may issue SELECTs to
//     load any extra data it needs; the Store guarantees the
//     status UPDATE and the eventual snapshot UPSERT happen on
//     the same db so the row is consistent.
//   - deploymentID / appID / scope: the deployment row's
//     identifiers. The Store already knows these from its own
//     read in the same tx; the callback does NOT need to
//     re-read.
//   - rules: the app's current enabled edge-rule list, already
//     projected to []api.CreateEdgeRuleRequest (the wire
//     shape) and sorted (Priority asc, CreatedAt desc) so the
//     output is deterministic for the same input.
//
// Returning a zero-value OpenAPISnapshot with no error is the
// "skip capture" signal — used by callers that haven't
// registered a real impl (e.g. the schedd unit tests).
//
// Why a runtime-registered callback instead of a direct
// pkg/openapidiff import:
//
//	pkg/openapidiff (via loader.go on origin/main + via
//	generator_ext.go on origin/main) transitively imports
//	pkg/state. If pkg/state statically imported pkg/openapidiff,
//	the cycle would prevent any package that imports both from
//	compiling. By exposing the capture as a registered
//	function, pkg/state only knows the SHAPE of the result
//	(OpenAPISnapshot), not the library that produces it. The
//	cmd/apid daemon, which already imports both, wires the
//	real impl at startup via [RegisterOpenAPICapture].
//
// The default impl is a sentinel that records "unregistered" in
// the caller's logs and returns a zero snapshot — every
// production path that calls into this MUST be wired.
type OpenAPICaptureFn func(ctx context.Context, db sqlc.DBTX, deploymentID, appID, scope string, rules []api.CreateEdgeRuleRequest) (OpenAPISnapshot, error)

// openAPICaptureRegistry guards the package-level registered
// capture so a late init (e.g. cmd/apid's server.go before the
// listener binds) does not race with a concurrent
// MarkDeploymentLive.
var (
	openAPICaptureRegistry sync.RWMutex
	openAPICapture         OpenAPICaptureFn = noopOpenAPICapture
)

// RegisterOpenAPICapture sets the package-level capture
// function. Pass nil to disable capture (the Store still
// commits the status UPDATE — the gate's "no baseline" branch
// handles the missing-snapshot case for that deployment until
// the operator wires the real impl). Production callers
// (cmd/apid) MUST register a non-nil impl at startup.
//
// Safe to call exactly once at process init; later calls
// overwrite the prior impl. The mutex protects against the
// race where MarkDeploymentLive is invoked before the daemon
// finishes wiring.
func RegisterOpenAPICapture(fn OpenAPICaptureFn) {
	openAPICaptureRegistry.Lock()
	defer openAPICaptureRegistry.Unlock()
	if fn == nil {
		openAPICapture = noopOpenAPICapture
		return
	}
	openAPICapture = fn
}

// getOpenAPICapture returns the currently registered capture.
// Used by pgstore.markDeploymentLiveTx and
// memstore.MarkDeploymentLive — both are read-only paths that
// acquire the RWMutex for reading.
func getOpenAPICapture() OpenAPICaptureFn {
	openAPICaptureRegistry.RLock()
	defer openAPICaptureRegistry.RUnlock()
	return openAPICapture
}

// noopOpenAPICapture is the zero-value impl used when no real
// impl has been registered. Returns a zero snapshot — the
// UPSERT step then writes a zero-bytes snapshot row, which
// would fail the migration's `len(snapshot) == 0` validation
// in upsertDeploymentOpenAPISnapshotDBTX. So in practice the
// pkg/state callers short-circuit when the snapshot is the
// zero value (see pgstore.markDeploymentLiveTx /
// memstore.MarkDeploymentLive).
func noopOpenAPICapture(_ context.Context, _ sqlc.DBTX, _, _, _ string, _ []api.CreateEdgeRuleRequest) (OpenAPISnapshot, error) {
	return OpenAPISnapshot{}, nil
}
