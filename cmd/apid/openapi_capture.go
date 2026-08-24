package main

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// openapiSnapshotForDeployment is the runtime-registered
// implementation of [state.OpenAPICaptureFn]. cmd/apid's
// main.go calls [state.RegisterOpenAPICapture] with this
// function at startup so every pkg/state.MarkDeploymentLive
// invocation in this process can project the edge-rule list
// into the canonical JSON snapshot via pkg/openapidiff.
//
// The flow:
//
//  1. pkg/state loads the deployment's app_id + scope from
//     the in-flight tx (it has its own SELECT) and passes
//     the (already sorted, already enabled-filtered) edge-
//     rule wire shape into the callback.
//
//  2. The callback ignores the db arg — pkg/openapidiff is
//     pure, it doesn't touch Postgres. The arg exists so a
//     future impl that needs extra data (e.g. the imported
//     OpenAPI doc, fetched from app_openapi_docs) can issue
//     a SELECT in the same tx.
//
//  3. The callback calls [openapidiff.GenerateFromEdgeRules]
//     with a nil base spec (the embedded OpenAPI is loaded
//     lazily by the generator's own seam — pass nil to
//     trigger that path) and the pending rules.
//
//  4. The result is canonical-JSON-marshalled + SHA-256'd
//     by [openapidiff.MarshalSnapshot]. The byte form is
//     what the migration's snapshot jsonb column stores
//     verbatim; the SHA-256 is what the
//     deployment_openapi_snapshots_sha256_shape CHECK
//     pins.
//
// Why this lives here:
//
//	pkg/state cannot statically import pkg/openapidiff
//	(cycle via pkg/openapidiff/generator_ext.go which
//	imports pkg/state). cmd/apid already imports both — it's
//	the natural seam where the inverse-of-control registration
//	happens. Tests that don't boot apid register their own
//	fixture via [state.RegisterOpenAPICapture].
func openapiSnapshotForDeployment(_ context.Context, _ sqlc.DBTX, deploymentID, appID, scope string, rules []api.CreateEdgeRuleRequest) (state.OpenAPISnapshot, error) {
	spec, err := openapidiff.GenerateFromEdgeRules(nil, nil, rules)
	if err != nil {
		return state.OpenAPISnapshot{}, fmt.Errorf("apid: project snapshot spec for %s: %w", deploymentID, err)
	}
	raw, sha, err := openapidiff.MarshalSnapshot(spec)
	if err != nil {
		return state.OpenAPISnapshot{}, fmt.Errorf("apid: marshal snapshot for %s: %w", deploymentID, err)
	}
	return state.OpenAPISnapshot{
		DeploymentID:  deploymentID,
		AppID:         appID,
		Scope:         scope,
		Snapshot:      raw,
		SHA256:        sha,
		SchemaVersion: openapidiff.SnapshotSchemaVersion,
		CapturedAt:    time.Now().UTC(),
	}, nil
}
