// adr: 375
package gateway_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
)

func TestTargetPublicationEnsureWarmReadsConfiguration(t *testing.T) {
	for _, kind := range []string{"missing source", "ready", "disabled probe", "store failure", "wrong owner"} {
		t.Run(kind, func(t *testing.T) {
			scheduler := &ensureWakeOnlyScheduler{}
			reads := 0
			failure := errors.New("readiness store unavailable")
			b := gateway.NewPGBackend(nil, scheduler, nil).WithTargetReadinessLoader(func(ctx context.Context, targets []gateway.Target) (map[string]gateway.TargetReadinessSnapshot, error) {
				reads++
				if len(targets) != 1 || targets[0].AppID != "app-1" || targets[0].InstanceID != "instance-ensured" || targets[0].WakeID != "wake-ensured" || targets[0].NodeID != "compute-1" {
					t.Fatalf("warm publication lost lifetime: %+v", targets)
				}
				if _, bounded := ctx.Deadline(); !bounded {
					t.Fatal("warm readiness read is unbounded")
				}
				target := targets[0]
				snapshot := gateway.TargetReadinessSnapshot{AppID: target.AppID, InstanceID: target.InstanceID, DeploymentID: target.DeploymentID, NodeID: target.NodeID, WakeID: target.WakeID,
					RequiredSources: []string{"primary_app"}, States: map[string]gateway.ReadinessState{}}
				switch kind {
				case "ready":
					snapshot.States["primary_app"] = gateway.ReadinessState{Ready: true, UpdatedAt: time.Now(), EventID: 1}
				case "disabled probe":
					snapshot.RequiredSources = nil
				case "store failure":
					return nil, failure
				case "wrong owner":
					snapshot.AppID = "foreign"
				}
				return map[string]gateway.TargetReadinessSnapshot{target.InstanceID: snapshot}, nil
			})
			wake, method, atCapacity, err := b.EnsureWarm(t.Context(), "app-1", "", "gateway")
			wantError := kind == "store failure" || kind == "wrong owner"
			ready := kind == "ready" || kind == "disabled probe"
			if (err != nil) != wantError || (kind == "store failure" && !errors.Is(err, failure)) || wake != "wake-ensured" || method != gateway.WakeMethodSnapshotRestore || atCapacity ||
				reads != 1 || scheduler.ensureCalls != 1 || scheduler.admitCalls != 0 || b.CapacityCount("app-1") != 1 || b.Pick("app-1").OK != ready {
				t.Fatalf("warm readiness kind=%s wake=%s method=%v atCapacity=%t reads=%d calls=%d/%d capacity=%d pick=%+v err=%v", kind, wake, method, atCapacity, reads, scheduler.ensureCalls, scheduler.admitCalls, b.CapacityCount("app-1"), b.Pick("app-1"), err)
			}
		})
	}
}
