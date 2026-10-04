// adr: 567 — cleanup must use the recorded node after app ownership changes.
package sched

import (
	"context"
	"crypto/tls"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type qualificationRouterNode struct {
	*fakeRouterVMM
	created []state.EnvironmentQualificationExecution
	retired []state.EnvironmentQualificationExecution
}

func (v *qualificationRouterNode) CreateEnvironmentQualification(_ context.Context, frame state.EnvironmentQualificationExecution, _ AppSpec) (*WakeOutcome, error) {
	v.created = append(v.created, frame)
	return &WakeOutcome{Instance: frame.InstanceID}, nil
}

func (v *qualificationRouterNode) RetireEnvironmentQualification(_ context.Context, frame state.EnvironmentQualificationExecution) (EnvironmentQualificationRetirementEvidence, error) {
	v.retired = append(v.retired, frame)
	return EnvironmentQualificationRetirementEvidence{Execution: frame}, nil
}

func TestVMMRouterEnvironmentQualificationUsesRecordedPlacement(t *testing.T) {
	original := &qualificationRouterNode{fakeRouterVMM: &fakeRouterVMM{}}
	replacement := &qualificationRouterNode{fakeRouterVMM: &fakeRouterVMM{}}
	r := NewVMMRouter([]ComputeNodeInfo{{ID: "original", TargetURL: "unix:///original"}, {ID: "replacement", TargetURL: "unix:///replacement"}},
		func(_ context.Context, target string, _ *tls.Config) (VMM, error) {
			if target == "unix:///original" {
				return original, nil
			}
			return replacement, nil
		}, nil)
	frame := state.EnvironmentQualificationExecution{NodeID: "original", InstanceID: "reserved", RequestID: "original-request", Attempt: 2, CleanupToken: "original-cleanup"}
	if _, err := r.CreateEnvironmentQualification(t.Context(), frame, AppSpec{}); err != nil {
		t.Fatal(err)
	}
	// The router now also serves the app's replacement host. It still must
	// retire the exact original execution, with no generic Destroy call.
	if _, err := r.CreateColdBoot(t.Context(), "replacement", "serving", AppSpec{}); err != nil {
		t.Fatal(err)
	}
	evidence, err := r.RetireEnvironmentQualification(t.Context(), frame)
	if err != nil || evidence.Execution != frame || len(original.created) != 1 || original.created[0] != frame ||
		len(original.retired) != 1 || original.retired[0] != frame || len(replacement.retired) != 0 || len(original.instanceCalls) != 0 {
		t.Fatal("cleanup changed placement, authority or used generic destroy", err)
	}
}

func TestVMMRouterEnvironmentQualificationOlderClientKeepsReservationUnconfirmed(t *testing.T) {
	v := &fakeRouterVMM{}
	r := NewVMMRouter([]ComputeNodeInfo{{ID: "older", TargetURL: "unix:///older"}},
		func(context.Context, string, *tls.Config) (VMM, error) { return v, nil }, nil)
	frame := state.EnvironmentQualificationExecution{NodeID: "older", InstanceID: "reserved"}
	if _, err := r.CreateEnvironmentQualification(t.Context(), frame, AppSpec{}); !errors.Is(err, state.ErrConflict) {
		t.Fatal("older client substituted generic creation", err)
	}
	evidence, err := r.RetireEnvironmentQualification(t.Context(), frame)
	if !errors.Is(err, state.ErrConflict) || evidence != (EnvironmentQualificationRetirementEvidence{}) || len(v.instanceCalls) != 0 {
		t.Fatal("older client substituted generic retirement", err)
	}
	frame.NodeID = "unknown"
	if _, err := r.RetireEnvironmentQualification(t.Context(), frame); err == nil || len(v.instanceCalls) != 0 {
		t.Fatal("missing recorded placement fell back to a serving node")
	}
}
