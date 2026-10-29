// adr: 570
package gateway

import (
	"context"
	"fmt"
	"testing"
)

// Production-us rc.304 (Hunt 14 H14-119): a RUNNING notification updated the
// picker while the wake leader's reconcile read was in flight, the read
// returned ErrTargetPlacementChanged, and gatewayd-internal answered the wake
// 503 (h14-grpc, 2026-10-29 17:20:26). A collision is a newer observation, not
// a failure: reconcile takes one fresh read and never refuses the wake on it,
// and the newer picker entry survives.
func TestReconcileLiveTargetsRereadsAfterPlacementChange(t *testing.T) {
	for _, collisions := range []int{1, 2} {
		t.Run(fmt.Sprintf("%d collisions", collisions), func(t *testing.T) {
			old := placementFixture("app", "instance")
			var b *PGBackend
			reads := 0
			newest := old
			b = NewPGBackend(nil, nil, nil).WithTargetPlacementLoader(func(context.Context, []string) (map[string]TargetPlacementSnapshot, error) {
				reads++
				served := newest
				if reads <= collisions {
					// Another writer admits while this read is in flight.
					newest.WakeID = fmt.Sprintf("admitted-%d", reads)
					b.RecordTarget(old.AppID, newest)
				}
				return map[string]TargetPlacementSnapshot{old.AppID: placementSnapshot(old.AppID, served)}, nil
			})
			if err := b.ReconcileLiveTargets(t.Context(), old.AppID); err != nil {
				t.Fatalf("a placement change refused the wake: %v", err)
			}
			if reads != 2 {
				t.Fatalf("reads = %d, want one fresh read after the collision", reads)
			}
			if pick := b.Pick(old.AppID); !pick.OK || pick.Target.WakeID != newest.WakeID {
				t.Fatalf("the newer admission did not survive: %+v", pick)
			}
		})
	}
}
