//go:build linux || darwin

package fcvm

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestQualificationArtifactRetirementBindsCaptureRestoreAndSmoke(t *testing.T) {
	_, capture, _ := nativeQualificationFixture(t)
	restored := capture
	restored.InstanceID, restored.WakeID, restored.CleanupToken = uuid.NewString(), uuid.NewString(), uuid.NewString()
	restored.CaptureInstanceID = capture.InstanceID
	smoke := state.EnvironmentQualificationSmokeReceipt{RequestID: capture.RequestID, Attempt: capture.Attempt, GraphID: capture.GraphID,
		CaptureInstanceID: capture.InstanceID, InstanceID: restored.InstanceID, Resource: capture.Resource, PolicyID: "http-healthz-v1",
		PolicySHA256: strings.Repeat("a", 64), ResultSHA256: strings.Repeat("b", 64), RecordedAt: time.Now().UTC()}
	if err := validateQualificationArtifactRetirement(capture, restored, smoke, capture.NodeID); err != nil {
		t.Fatal("valid original/restore/smoke tuple was rejected", err)
	}
	for name, mutate := range map[string]func(*state.EnvironmentQualificationExecution, *state.EnvironmentQualificationExecution, *state.EnvironmentQualificationSmokeReceipt){
		"capture_instance": func(c, r *state.EnvironmentQualificationExecution, _ *state.EnvironmentQualificationSmokeReceipt) {
			r.CaptureInstanceID = uuid.NewString()
		},
		"restore_attempt": func(_ *state.EnvironmentQualificationExecution, r *state.EnvironmentQualificationExecution, _ *state.EnvironmentQualificationSmokeReceipt) {
			r.Attempt++
		},
		"smoke_target": func(_, r *state.EnvironmentQualificationExecution, s *state.EnvironmentQualificationSmokeReceipt) {
			s.InstanceID = uuid.NewString()
		},
		"smoke_digest": func(_, _ *state.EnvironmentQualificationExecution, s *state.EnvironmentQualificationSmokeReceipt) {
			s.ResultSHA256 = "not-a-digest"
		},
	} {
		t.Run(name, func(t *testing.T) {
			c, r, s := capture, restored, smoke
			mutate(&c, &r, &s)
			if err := validateQualificationArtifactRetirement(c, r, s, capture.NodeID); err == nil {
				t.Fatal("mismatched owner evidence authorized artifact retirement")
			}
		})
	}
}
