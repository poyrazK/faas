package state

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRetryDeploymentInput_RestartsServiceReadinessRollout(t *testing.T) {
	oldStarted := time.Now().UTC().Add(-time.Hour)
	now := time.Now().UTC()
	src := Deployment{
		ID:                     "failed-service",
		AppID:                  "app-service",
		Status:                 DeployFailed,
		CanaryPreset:           "none",
		CanaryTotalSteps:       0,
		TrafficPercent:         0,
		RolloutState:           "rolling_out",
		RolloutStartedAt:       &oldStarted,
		RolloutCompletedAt:     &oldStarted,
		OverrideReadinessProbe: json.RawMessage(`{"path":"/readyz"}`),
		OverrideMainDependsOn:  json.RawMessage(`[{"name":"proxy","condition":"healthy"}]`),
		RAMMB:                  384,
		CPUMillicores:          500,
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
	if string(got.OverrideReadinessProbe) != `{"path":"/readyz"}` {
		t.Fatalf("OverrideReadinessProbe = %s, want the source readiness policy", got.OverrideReadinessProbe)
	}
	if string(got.OverrideMainDependsOn) != `[{"name":"proxy","condition":"healthy"}]` {
		t.Fatalf("OverrideMainDependsOn = %s, want the source startup dependencies", got.OverrideMainDependsOn)
	}
	if got.RAMMB != src.RAMMB || got.CPUMillicores != src.CPUMillicores {
		t.Fatalf("retry compute shape = %d MiB/%d mCPU, want %d MiB/%d mCPU", got.RAMMB, got.CPUMillicores, src.RAMMB, src.CPUMillicores)
	}
}
