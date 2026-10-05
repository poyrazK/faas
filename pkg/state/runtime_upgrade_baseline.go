package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// RuntimeUpgradeBaseline retains identity and revision fingerprints, never
// plaintext values or sealed envelopes. It is preparation evidence, not an
// activation receipt or a historical secret value that can be restored.
type RuntimeUpgradeBaseline struct {
	DeploymentID, ServingDeploymentID, ServingRootfsKey, ServingRuntimeReleaseID, TargetReleaseID string
	ConfigurationFingerprint, SecretFingerprint, InputFingerprint, InputSecretFingerprint         string
	CapturedAt                                                                                    time.Time
}

// RuntimeUpgradeBaselineStore belongs to the future apid executor. Capture
// follows target pinning, before queue admission, on a zero-traffic candidate.
// Validation is a point-in-time check; cutover must repeat it in its own write
// transaction together with target qualification and fresh readiness gates.
type RuntimeUpgradeBaselineStore interface {
	CaptureDeploymentRuntimeUpgradeBaseline(context.Context, string, string) (RuntimeUpgradeBaseline, error)
	DeploymentRuntimeUpgradeBaseline(context.Context, string) (RuntimeUpgradeBaseline, error)
	ValidateDeploymentRuntimeUpgradeBaseline(context.Context, string) error
}

// CheckDeploymentRuntimeUpgradeBaseline preserves historical builds without a
// captured baseline. Database errors cannot downgrade a captured preparation.
func CheckDeploymentRuntimeUpgradeBaseline(ctx context.Context, store any, deploymentID string) error {
	baselines, ok := store.(RuntimeUpgradeBaselineStore)
	if !ok {
		return nil
	}
	err := baselines.ValidateDeploymentRuntimeUpgradeBaseline(ctx, deploymentID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("validate runtime upgrade baseline: %w", err)
	}
	return nil
}

func newRuntimeUpgradeBaseline(app App, candidate, serving Deployment, target, current RuntimeRelease, candidateValues, servingValues RuntimeAppValuesSnapshot) (RuntimeUpgradeBaseline, error) {
	if app.Status != AppActive || validateRuntimeUpgradeTarget(app, candidate, target) != nil || current.Validate() != nil ||
		candidate.ID == serving.ID || serving.AppID != app.ID || serving.Status != DeployLive || serving.DeletedAt != nil ||
		serving.TrafficPercent != 100 || serving.RootfsKey == "" || current.Runtime != target.Runtime || current.Architecture != target.Architecture ||
		candidate.SourceSHA256 != serving.SourceSHA256 || candidate.SourceBytes != serving.SourceBytes ||
		candidate.SourceRoot != serving.SourceRoot || candidate.Handler != serving.Handler ||
		candidateValues.Scope != servingValues.Scope || candidateValues.EnvironmentID != servingValues.EnvironmentID ||
		candidateValues.AppID != app.ID || servingValues.AppID != app.ID {
		return RuntimeUpgradeBaseline{}, ErrConflict
	}
	// Named sidecars need their own retained artifact and readiness contract.
	for _, dep := range []Deployment{candidate, serving} {
		var sidecars []json.RawMessage
		if json.Unmarshal(normalizeProjectCloneArtifact(projectCloneArtifactFromDeployment(dep)).Sidecars, &sidecars) != nil || len(sidecars) != 0 {
			return RuntimeUpgradeBaseline{}, ErrConflict
		}
	}
	servingFence, err := NewRuntimeAppConfigFence(servingValues)
	if err != nil {
		return RuntimeUpgradeBaseline{}, err
	}
	inputFence, err := runtimeUpgradeInputFence(candidate, serving.ID, candidateValues)
	if err != nil {
		return RuntimeUpgradeBaseline{}, err
	}
	return RuntimeUpgradeBaseline{DeploymentID: candidate.ID, ServingDeploymentID: serving.ID,
		ServingRootfsKey: serving.RootfsKey, ServingRuntimeReleaseID: current.ID, TargetReleaseID: target.ID,
		ConfigurationFingerprint: servingFence.Fingerprint, SecretFingerprint: servingFence.SecretFence.Fingerprint,
		InputFingerprint: inputFence.Fingerprint, InputSecretFingerprint: inputFence.SecretFence.Fingerprint}, nil
}

func runtimeUpgradeInputFence(dep Deployment, servingID string, snapshot RuntimeAppValuesSnapshot) (RuntimeAppConfigFence, error) {
	intent := normalizeProjectCloneArtifact(projectCloneArtifactFromDeployment(dep))
	// Build output and attempt identity change legitimately during assembly and
	// retries. Source inputs and all customer overrides remain in the fence.
	intent.ID, intent.ImageDigest, intent.RootfsPath, intent.RootfsKey, intent.RootfsBytes = "", "", "", "", 0
	intent.InferredProfile = nil // derived image evidence, not customer intent
	intent.SecretReloadSignal, intent.SecretReloadSignalKnown = "", false
	snapshot.SecretGrants.ReloadSignal = "" // discovered from the rebuilt image
	var err error
	snapshot.Configuration.ArtifactHash, err = runtimeConfigurationHash(struct {
		Intent                    projectCloneArtifact
		MinInstances, CanarySteps int
		CanaryPreset              string
		CanaryStages              json.RawMessage
	}{intent, dep.MinInstances, dep.CanaryTotalSteps, dep.CanaryPreset, dep.CanaryStages})
	if err != nil {
		return RuntimeAppConfigFence{}, err
	}
	// Retries retain the same reviewed input hash while getting a new row ID.
	snapshot.DeploymentID = servingID
	return NewRuntimeAppConfigFence(snapshot)
}

func sameRuntimeUpgradeBaseline(a, b RuntimeUpgradeBaseline) bool {
	a.CapturedAt, b.CapturedAt = time.Time{}, time.Time{}
	a.DeploymentID, b.DeploymentID = "", ""
	return a == b
}

func runtimeUpgradeServingStable(deployments []Deployment, candidate, serving Deployment) bool {
	for _, other := range deployments {
		if other.AppID != candidate.AppID || normalizedDeploymentScope(other.Scope) != normalizedDeploymentScope(candidate.Scope) || other.Status != DeployLive {
			continue
		}
		if other.CanaryTotalSteps > 0 && (other.RolloutState == "pending" || other.RolloutState == "rolling_out") ||
			(other.ID != serving.ID && other.TrafficPercent > 0) {
			return false
		}
	}
	return normalizedDeploymentScope(candidate.Scope) == normalizedDeploymentScope(serving.Scope)
}
