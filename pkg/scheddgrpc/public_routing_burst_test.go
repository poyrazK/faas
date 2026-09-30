// adr: 375
package scheddgrpc_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/sched"
)

type publicRoutingBurstEngine struct {
	fakeEngine
	mu     sync.Mutex
	inputs []struct {
		deployment, scope string
		continuation      bool
	}
}

func (e *publicRoutingBurstEngine) AdmitInstance(ctx context.Context, app, deployment, scope, trigger string) (sched.WakeResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.inputs = append(e.inputs, struct {
		deployment, scope string
		continuation      bool
	}{deployment, scope, sched.IsBurstContinuation(ctx)})
	return sched.WakeResult{InstanceID: fmt.Sprintf("burst-%d", len(e.inputs)), NodeID: "node", DeploymentID: deployment, WakeID: "wake"}, nil
}

func TestPublicRoutingDeploymentBurstRPCPreservesCohortAndContinuation(t *testing.T) {
	engine := &publicRoutingBurstEngine{}
	client := newClient(t, engine)
	var reports atomic.Int32
	if err := client.AdmitDeploymentInstancesWithIdentity(t.Context(), "app", "selected", "production", sched.TriggerGateway, 4,
		func(instance, node, deployment, wake string, method int32, capacity bool, port int, identity api.PlatformIdentity, err error) {
			if err != nil || capacity || deployment != "selected" {
				t.Errorf("burst result changed cohort: %s %v %v", deployment, capacity, err)
			}
			reports.Add(1)
		}); err != nil {
		t.Fatal(err)
	}
	engine.mu.Lock()
	if len(engine.inputs) != 4 || reports.Load() != 4 || engine.inputs[0].continuation {
		t.Fatalf("burst inputs=%+v reports=%d", engine.inputs, reports.Load())
	}
	for index, input := range engine.inputs {
		if input.deployment != "selected" || input.scope != "production" || index > 0 && !input.continuation {
			t.Fatalf("burst member changed routing: %+v", input)
		}
	}
	engine.mu.Unlock()
	backend := gateway.NewPGBackend(nil, client, nil)
	admitted, err := backend.AdmitDeploymentBurst(t.Context(), "app", "next", "production", sched.TriggerGateway, 4, 2)
	if err != nil || admitted != 2 || !backend.PickForDeployment("app", "next").OK || backend.PickForDeployment("app", "selected").OK {
		t.Fatalf("burst cache provenance: %d %v", admitted, err)
	}
}
