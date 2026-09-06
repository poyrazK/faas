package scheddgrpc_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

type capacityWakeEngine struct {
	*fakeEngine
	count int
}

func (e *capacityWakeEngine) EnsureWakeCapacity(_ context.Context, appID, trigger string, count int) (sched.CoordOutcome, error) {
	e.count = count
	return sched.CoordOutcome{Instance: &sched.CoordInstance{InstanceID: "one", NodeID: "node", DeploymentID: "dep", WakeID: "wake-one", Port: 8080}, Additional: []*sched.CoordInstance{{InstanceID: "two", NodeID: "node", DeploymentID: "dep", WakeID: "wake-two", Port: 8081}}}, nil
}

func TestClientInitialWakeCapacityRoundTrip(t *testing.T) {
	engine := &capacityWakeEngine{fakeEngine: &fakeEngine{}}
	client := newClient(t, engine)
	var ids []string
	err := client.EnsureWakeCapacity(context.Background(), "app", "gateway", 1000, func(instance, node, deployment, wake string, method int32, port int) {
		ids = append(ids, instance)
		if node != "node" || deployment != "dep" || wake == "" || port < 8080 {
			t.Errorf("incomplete target callback: %s/%s/%s/%s/%d", instance, node, deployment, wake, port)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if engine.count != api.ScaleUpMaxBurstPerTick {
		t.Fatalf("unbounded count=%d", engine.count)
	}
	if len(ids) != 2 || ids[0] != "one" || ids[1] != "two" {
		t.Fatalf("returned instances=%v", ids)
	}
}

func TestClientInitialWakeCapacityLegacyEngine(t *testing.T) {
	engine := &ensureWakeEngine{fakeEngine: &fakeEngine{}, ensureWakeFn: func(context.Context, string, string) (sched.CoordOutcome, error) {
		return sched.CoordOutcome{Instance: &sched.CoordInstance{InstanceID: "legacy", NodeID: "node"}}, nil
	}}
	client := newClient(t, engine)
	var ids []string
	err := client.EnsureWakeCapacity(context.Background(), "app", "gateway", 2, func(instance, _, _, _ string, _ int32, _ int) { ids = append(ids, instance) })
	if err != nil || len(ids) != 1 || ids[0] != "legacy" {
		t.Fatalf("legacy response=%v err=%v", ids, err)
	}
}
