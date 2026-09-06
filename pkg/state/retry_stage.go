package state

import (
	"encoding/json"
	"fmt"
	"time"

	canarycatalog "github.com/onebox-faas/faas/pkg/api/canary"
)

// RetryStageState reports the actual executable starting point. Intermediate
// files are mutable, node-local outputs; they are not retained checkpoints for
// a new deployment ID. Rebuild prerequisites and rerun all security gates.
func RetryStageState(requested StageName) StageState {
	return StageState{
		Current:             StageSourceDownload,
		History:             []StageStateItem{},
		RetryRequestedStage: requested,
		RetryRestartReason:  "Intermediate stage checkpoints are not retained; rebuilding from the original source or image.",
	}
}

// retryDeploymentInput creates a fresh attempt from the immutable inputs on a
// failed deployment. Mutable execution state is reset: canaries restart at
// their first rung with a new soak timer, service rollouts restart their
// readiness gate, and prior wake/scan/failure state stays on the old row.
func retryDeploymentInput(src Deployment, now time.Time) (Deployment, error) {
	if src.Status != DeployFailed {
		return Deployment{}, fmt.Errorf("%w: deployment %s has status %s", ErrConflict, src.ID, src.Status)
	}

	canaryPreset := src.CanaryPreset
	if canaryPreset == "" {
		canaryPreset = "none"
	}
	trafficPercent := src.TrafficPercent
	if src.CanaryTotalSteps > 0 {
		preset, err := retryCanaryPreset(src, canaryPreset)
		if err != nil {
			return Deployment{}, err
		}
		if preset.TotalSteps() != src.CanaryTotalSteps {
			return Deployment{}, fmt.Errorf("retry deployment %s: canary preset %q has %d stages, row records %d", src.ID, canaryPreset, preset.TotalSteps(), src.CanaryTotalSteps)
		}
		first, ok := preset.StageAt(0)
		if !ok {
			return Deployment{}, fmt.Errorf("retry deployment %s: canary preset %q has no first stage", src.ID, canaryPreset)
		}
		trafficPercent = first.Percent
	}

	stepStartedAt := now.UTC()
	rolloutState := "pending"
	var rolloutStartedAt *time.Time
	if IsServiceRollout(src) {
		rolloutState = "rolling_out"
		trafficPercent = 0
		started := stepStartedAt
		rolloutStartedAt = &started
	}
	var fullRootfsOverride *bool
	if src.FullRootfsOverride != nil {
		value := *src.FullRootfsOverride
		fullRootfsOverride = &value
	}

	return Deployment{
		AppID:                 src.AppID,
		ImageDigest:           src.ImageDigest,
		Kind:                  src.Kind,
		SourcePath:            src.SourcePath,
		SourceRoot:            src.SourceRoot,
		SourceBytes:           src.SourceBytes,
		Handler:               src.Handler,
		SourceURL:             src.SourceURL,
		CommitSHA:             src.CommitSHA,
		OverrideEntrypoint:    append([]string(nil), src.OverrideEntrypoint...),
		OverrideCmd:           append([]string(nil), src.OverrideCmd...),
		OverrideEnv:           append(json.RawMessage(nil), src.OverrideEnv...),
		OverrideEnvSecrets:    append(json.RawMessage(nil), src.OverrideEnvSecrets...),
		OverridePort:          src.OverridePort,
		OverrideHealthcheck:   append(json.RawMessage(nil), src.OverrideHealthcheck...),
		OverrideLivenessProbe: append(json.RawMessage(nil), src.OverrideLivenessProbe...),
		Sidecars:              append(json.RawMessage(nil), src.Sidecars...),
		Workflows:             append(json.RawMessage(nil), src.Workflows...),
		FullRootfsAllowAuto:   src.FullRootfsAllowAuto,
		FullRootfsOverride:    fullRootfsOverride,
		MinInstances:          src.MinInstances,
		TrafficPercent:        trafficPercent,
		Scope:                 src.Scope,
		Priority:              src.Priority,
		DeployedVia:           src.DeployedVia,
		DeployedByUserID:      src.DeployedByUserID,
		DeployedFromIP:        src.DeployedFromIP,
		PusherLogin:           src.PusherLogin,
		Reason:                src.Reason,
		Tag:                   src.Tag,
		DeployedBy:            src.DeployedBy,
		PRNumber:              src.PRNumber,
		RollbackOn5xx:         src.RollbackOn5xx,
		CanaryPreset:          canaryPreset,
		CanaryStep:            0,
		CanaryTotalSteps:      src.CanaryTotalSteps,
		CanaryStepStartedAt:   &stepStartedAt,
		CanaryStages:          append(json.RawMessage(nil), src.CanaryStages...),
		RolloutState:          rolloutState,
		RolloutStartedAt:      rolloutStartedAt,
		RolloutCompletedAt:    nil,
		RolloutAbortedAt:      nil,
		RolloutAbortedReason:  "",
		Status:                DeployPending,
	}, nil
}

func retryCanaryPreset(src Deployment, name string) (canarycatalog.Preset, error) {
	if name != "custom" {
		preset, ok := canarycatalog.LookupPreset(name)
		if !ok {
			return canarycatalog.Preset{}, fmt.Errorf("retry deployment %s: unknown canary preset %q", src.ID, name)
		}
		return preset, nil
	}
	var stages []canarycatalog.CustomStage
	if err := json.Unmarshal(src.CanaryStages, &stages); err != nil {
		return canarycatalog.Preset{}, fmt.Errorf("retry deployment %s: decode custom canary stages: %w", src.ID, err)
	}
	preset, err := canarycatalog.LookupCustomPreset(stages)
	if err != nil {
		return canarycatalog.Preset{}, fmt.Errorf("retry deployment %s: validate custom canary stages: %w", src.ID, err)
	}
	return preset, nil
}
