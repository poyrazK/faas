package state

import (
	"testing"
	"time"
)

func TestRetryDeploymentInput_RestartsServiceReadinessRollout(t *testing.T) {
	oldStarted := time.Now().UTC().Add(-time.Hour)
	now := time.Now().UTC()
	src := Deployment{
		ID:                 "failed-service",
		AppID:              "app-service",
		Status:             DeployFailed,
		CanaryPreset:       "none",
		CanaryTotalSteps:   0,
		TrafficPercent:     0,
		RolloutState:       "rolling_out",
		RolloutStartedAt:   &oldStarted,
		RolloutCompletedAt: &oldStarted,
	}

	got, err := retryDeploymentInput(src, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != DeployPending || got.TrafficPercent != 0 || got.RolloutState != "rolling_out" {
		t.Fatalf("service rollout was not restarted: %+v", got)
	}
	if got.RolloutStartedAt == nil || !got.RolloutStartedAt.Equal(now) {
		t.Fatalf("RolloutStartedAt = %v, want %v", got.RolloutStartedAt, now)
	}
	if got.RolloutCompletedAt != nil || got.RolloutAbortedAt != nil || got.RolloutAbortedReason != "" {
		t.Fatalf("terminal rollout state carried into retry: %+v", got)
	}
}
