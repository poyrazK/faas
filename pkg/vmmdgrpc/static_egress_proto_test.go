package vmmdgrpc

import (
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

func TestStaticEgressPolicyCrossesBothWakeAdapters(t *testing.T) {
	for _, tc := range []struct {
		name, pin string
		excluded  bool
	}{{"production", "203.0.113.42", false}, {"stage", "", true}, {"legacy", "", false}} {
		t.Run(tc.name, func(t *testing.T) {
			app := &vmmdpb.AppSpec{BaseKey: "/base", LayerKey: "/layer", StaticEgressIp: tc.pin, StaticEgressExcluded: tc.excluded}
			for _, restore := range []bool{false, true} {
				var wake fcvm.WakeRequest
				var err error
				if restore {
					wake, err = toWakeRequest(t.Context(), &vmmdpb.CreateFromSnapshotRequest{Instance: "i-static", App: app})
				} else {
					wake, err = toColdBootRequest(t.Context(), &vmmdpb.CreateColdBootRequest{Instance: "i-static", App: app})
				}
				if err != nil {
					t.Fatal(err)
				}
				if wake.StaticEgressIP != tc.pin || wake.StaticEgressExcluded != tc.excluded {
					t.Fatalf("restore=%t: static egress policy lost: pin=%q excluded=%t", restore, wake.StaticEgressIP, wake.StaticEgressExcluded)
				}
			}
		})
	}
}
