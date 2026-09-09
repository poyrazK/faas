package openapidiff

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RegisterStateCapture wires the OpenAPI projector into the state package.
// apid calls this once during startup, after constructing its store. Keeping
// the registration at this package boundary avoids an import cycle: state
// owns the transition and persistence, while openapidiff owns projection and
// canonical serialization.
func RegisterStateCapture() {
	state.RegisterOpenAPICapture(stateOpenAPISnapshotForDeployment)
}

func stateOpenAPISnapshotForDeployment(_ context.Context, _ sqlc.DBTX, deploymentID, appID, scope string, rules []api.CreateEdgeRuleRequest) (state.OpenAPISnapshot, error) {
	spec, err := GenerateFromEdgeRules(nil, nil, rules)
	if err != nil {
		return state.OpenAPISnapshot{}, fmt.Errorf("openapidiff: project snapshot for deployment %s: %w", deploymentID, err)
	}
	raw, sha, err := MarshalSnapshot(spec)
	if err != nil {
		return state.OpenAPISnapshot{}, fmt.Errorf("openapidiff: marshal snapshot for deployment %s: %w", deploymentID, err)
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
