package deploydiff

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestRenderJSON_IncludesSafeReleasePreflight(t *testing.T) {
	d := Diff{
		Slug: "demo",
		SafeRelease: &SafeReleasePreview{
			Preset: "balanced",
			Rollout: SafeReleaseRollout{
				Step: 1, TotalSteps: 4, TrafficPercent: 1,
				Stages: []SafeReleaseStage{{TrafficPercent: 1, Duration: "2m0s"}},
			},
			HealthGate:     SafeReleaseHealthGate{Status: SafeReleaseHealthReady, Configured: 1},
			RollbackTarget: "dep-previous",
		},
	}
	var buf bytes.Buffer
	if err := RenderJSON(&buf, d); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Diff struct {
			SafeRelease SafeReleasePreview `json:"safe_release"`
		} `json:"diff"`
	}
	if err := json.Unmarshal(buf.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	got := envelope.Diff.SafeRelease
	if got.Rollout.Step != 1 || got.Rollout.TrafficPercent != 1 || got.HealthGate.Status != SafeReleaseHealthReady || got.RollbackTarget != "dep-previous" {
		t.Fatalf("safe_release = %+v, want rollout/gate/rollback details", got)
	}
}
