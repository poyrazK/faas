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

func stateOpenAPISnapshotForDeployment(_ context.Context, _ sqlc.DBTX, deploymentID, appID, scope string, rules []api.CreateEdgeRuleRequest, importedDoc []byte) (state.OpenAPISnapshot, error) {
	snap, _, err := snapshotFromDocument(deploymentID, appID, scope, importedDoc, rules)
	if err != nil {
		return state.OpenAPISnapshot{}, fmt.Errorf("openapidiff: project snapshot for deployment %s: %w", deploymentID, err)
	}
	snap.CapturedAt = time.Now().UTC()
	return snap, nil
}

func snapshotFromDocument(deploymentID, appID, scope string, importedDoc []byte, rules []api.CreateEdgeRuleRequest) (state.OpenAPISnapshot, string, error) {
	if len(importedDoc) > 0 {
		spec, err := LoadBytes(importedDoc)
		if err != nil {
			return state.OpenAPISnapshot{}, "", fmt.Errorf("parse imported OpenAPI document: %w", err)
		}
		raw, sha, err := MarshalSnapshot(spec)
		if err != nil {
			return state.OpenAPISnapshot{}, "", fmt.Errorf("marshal imported OpenAPI document: %w", err)
		}
		return state.OpenAPISnapshot{
			DeploymentID:  deploymentID,
			AppID:         appID,
			Scope:         scope,
			Snapshot:      raw,
			SHA256:        sha,
			SchemaVersion: SnapshotSchemaVersion,
		}, SnapshotSourceManualImport, nil
	}

	spec, err := GenerateFromEdgeRules(nil, nil, rules)
	if err != nil {
		return state.OpenAPISnapshot{}, "", fmt.Errorf("project edge-rule OpenAPI surface: %w", err)
	}
	raw, sha, err := MarshalSnapshot(spec)
	if err != nil {
		return state.OpenAPISnapshot{}, "", fmt.Errorf("marshal edge-rule OpenAPI surface: %w", err)
	}
	return state.OpenAPISnapshot{
		DeploymentID:  deploymentID,
		AppID:         appID,
		Scope:         scope,
		Snapshot:      raw,
		SHA256:        sha,
		SchemaVersion: SnapshotSchemaVersion,
	}, SnapshotSourceEdgeRules, nil
}
