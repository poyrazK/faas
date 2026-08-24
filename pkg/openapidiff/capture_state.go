package openapidiff

// RegisterStateCapture wires the [state.OpenAPICaptureFn] that
// pkg/state.MarkDeploymentLive invokes from inside its in-tx
// capture path (PR-B / ADR-121). The capture projects the
// deployment's current edge-rule list onto the embedded OpenAPI
// spec via [GenerateFromEdgeRules], canonical-marshals + SHA-256s
// it via [MarshalSnapshot], and returns a [state.OpenAPISnapshot]
// ready for the pgstore UPSERT.
//
// Why this lives here and not in cmd/apid:
//
//   - pkg/openapidiff already imports pkg/state (via
//     generator_ext.go on origin/main — the spec_cache + the
//     GenerateFromApp seam). The impl is a natural extension of
//     that surface, NOT a re-entrant cycle.
//
//   - The cmd/apid binary was the natural owner for this code
//     before e2e tests that drive [state.NewPgStore] directly
//     started failing (TestRollbackSpecific_E2E et al.; see the
//     sec11_sweep_test.go::TestMain wiring). Hoisting the impl
//     into pkg/openapidiff lets BOTH cmd/apid and cmd/e2e register
//     it from a single TestMain entry point — no per-test
//     boilerplate, no drift between cmd/apid and cmd/e2e
//     copies.
//
//   - pkg/state cannot statically import pkg/openapidiff (the
//     cycle this package lives behind is intentional). The
//     callback type [state.OpenAPICaptureFn] is the contract
//     surface; the actual projection logic lives here.
//
// Safe to call exactly once at process init (the registered fn
// is process-wide). Pass-through to [state.RegisterOpenAPICapture]
// from the daemon's TestMain; the fn is a process-global guard
// keyed on the import path. Threads [state.OpenAPICaptureFn]
// (defined in pkg/state) so the cycle direction stays one-way.

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RegisterStateCapture wires the OpenAPI snapshot capture into
// the pkg/state process-global registry. Call exactly once at
// startup (cmd/apid/main.go in production, sec11_sweep_test.go's
// TestMain in e2e).
func RegisterStateCapture() {
	state.RegisterOpenAPICapture(stateOpenAPISnapshotForDeployment)
}

// stateOpenAPISnapshotForDeployment is the registered
// implementation of [state.OpenAPICaptureFn] (PR-B / ADR-121).
// It is the single source of truth for the "edge-rule list →
// canonical JSON + SHA-256 → snapshot row" projection; cmd/apid
// and cmd/e2e share this impl.
func stateOpenAPISnapshotForDeployment(_ context.Context, _ sqlc.DBTX, deploymentID, appID, scope string, rules []api.CreateEdgeRuleRequest) (state.OpenAPISnapshot, error) {
	spec, err := GenerateFromEdgeRules(nil, nil, rules)
	if err != nil {
		return state.OpenAPISnapshot{}, fmt.Errorf("openapidiff: project snapshot spec for %s: %w", deploymentID, err)
	}
	raw, sha, err := MarshalSnapshot(spec)
	if err != nil {
		return state.OpenAPISnapshot{}, fmt.Errorf("openapidiff: marshal snapshot for %s: %w", deploymentID, err)
	}
	return state.OpenAPISnapshot{
		DeploymentID:  deploymentID,
		AppID:         appID,
		Scope:         scope,
		Snapshot:      raw,
		SHA256:        sha,
		SchemaVersion: SnapshotSchemaVersion,
		CapturedAt:    time.Now().UTC(),
	}, nil
}
