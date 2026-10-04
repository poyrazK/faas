// adr: 521 — capture never re-resolves an attempt from the current app owner.
package sched

import (
	"context"
	"crypto/tls"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type qualificationSnapshotRouterNode struct {
	*qualificationRouterNode
	captured []state.EnvironmentQualificationExecution
}

func (v *qualificationSnapshotRouterNode) CaptureEnvironmentQualification(_ context.Context, frame state.EnvironmentQualificationExecution) (EnvironmentQualificationSnapshotEvidence, error) {
	v.captured = append(v.captured, frame)
	return EnvironmentQualificationSnapshotEvidence{Execution: frame}, nil
}

func TestVMMRouterEnvironmentQualificationSnapshotUsesOriginalPlacement(t *testing.T) {
	original := &qualificationSnapshotRouterNode{qualificationRouterNode: &qualificationRouterNode{fakeRouterVMM: &fakeRouterVMM{}}}
	replacement := &qualificationSnapshotRouterNode{qualificationRouterNode: &qualificationRouterNode{fakeRouterVMM: &fakeRouterVMM{}}}
	older := &fakeRouterVMM{}
	r := NewVMMRouter([]ComputeNodeInfo{{ID: "original", TargetURL: "unix:///original"}, {ID: "replacement", TargetURL: "unix:///replacement"}, {ID: "older", TargetURL: "unix:///older"}},
		func(_ context.Context, target string, _ *tls.Config) (VMM, error) {
			switch target {
			case "unix:///original":
				return original, nil
			case "unix:///replacement":
				return replacement, nil
			default:
				return older, nil
			}
		}, nil)
	frame := state.EnvironmentQualificationExecution{NodeID: "original", InstanceID: "reserved", CleanupToken: "private", Attempt: 3}
	if _, err := r.CreateColdBoot(t.Context(), "replacement", "serving", AppSpec{}); err != nil {
		t.Fatal(err)
	}
	evidence, err := r.CaptureEnvironmentQualification(t.Context(), frame)
	if err != nil || evidence.Execution != frame || len(original.captured) != 1 || original.captured[0] != frame || len(replacement.captured) != 0 || len(original.instanceCalls) != 0 {
		t.Fatal("capture substituted placement or generic lookup", err)
	}
	frame.NodeID = "older"
	if evidence, err := r.CaptureEnvironmentQualification(t.Context(), frame); !errors.Is(err, state.ErrConflict) || evidence != (EnvironmentQualificationSnapshotEvidence{}) || len(older.instanceCalls) != 0 {
		t.Fatal("older node fell back to generic capture", err)
	}
	frame.NodeID = "unknown"
	if _, err := r.CaptureEnvironmentQualification(t.Context(), frame); err == nil || len(replacement.captured) != 0 {
		t.Fatal("unknown placement borrowed another node")
	}
}
