// adr: 20261029085904 — placement around nodes that are gone or cannot boot.
package sched_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func (v *refusingVMM) callsOn(nodeID string) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.calls[nodeID]
}

// gRPC fails a call fast with this when it cannot connect: the request was
// never sent, so no VM exists and another node may take the wake.
func TestWakeThatNeverReachedVMMDIsReplaced(t *testing.T) {
	var owner string
	f := newFailoverFleet(t, 2, 8192, func(nodeID string) error {
		if nodeID == owner {
			return status.Error(codes.Unavailable, `connection error: desc = "transport: Error while dialing: dial unix /run/faas/vmmd.sock: connect: connection refused"`)
		}
		return nil
	})
	owner = f.nodes[0].ID
	res, err := f.e.Wake(f.ctx, f.app(t, api.PlanPro, 256, 1).ID, "", "", sched.TriggerCronSched)
	if err != nil {
		t.Fatalf("wake with a reachable peer: %v", err)
	}
	if res.NodeID != f.nodes[1].ID {
		t.Fatalf("wake placed on %s, want the peer %s", res.NodeID, f.nodes[1].ID)
	}
	if got := f.failoverCount(t, wire.WakeFailoverReasonVMMDUnreachable, wire.WakeFailoverOutcomeSucceeded); got != 1 {
		t.Fatalf("vmmd_unreachable/succeeded failovers = %v, want 1", got)
	}
}

// gRPC reports "transport is closing" for a call whose request may already
// have reached vmmd: the wake must not be re-placed, or a VM could be booted
// twice.
func TestWakeOnAClosingTransportIsNotReplaced(t *testing.T) {
	var owner string
	f := newFailoverFleet(t, 2, 8192, func(nodeID string) error {
		if nodeID == owner {
			return status.Error(codes.Unavailable, `connection error: desc = "transport is closing"`)
		}
		return nil
	})
	owner = f.nodes[0].ID
	if _, err := f.e.Wake(f.ctx, f.app(t, api.PlanPro, 256, 1).ID, "", "", sched.TriggerCronSched); err == nil {
		t.Fatal("a wake whose transport closed mid-call succeeded")
	}
	if n := f.vmm.callsOn(f.nodes[1].ID); n != 0 {
		t.Fatalf("the wake was re-placed on the peer (%d calls); its first VM may exist", n)
	}
}

// A node that answers its heartbeat but fails every boot used to stay the
// owner's first choice until an operator drained it.
func TestNodeBootBreakerSteersPlacementAway(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		avoided bool
	}{
		{name: "node failures", err: status.Error(codes.Internal, "jailer: mount /srv/fc/jail: no space left on device"), avoided: true},
		{name: "app failures", err: api.NewProblem(http.StatusUnprocessableEntity, api.CodeAppStartupTimeout,
			"app did not become ready in time", "startup deadline exceeded"), avoided: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var owner string
			f := newFailoverFleet(t, 2, 8192, func(nodeID string) error {
				if nodeID == owner {
					return tc.err
				}
				return nil
			})
			owner = f.nodes[0].ID
			for _, app := range []state.App{f.app(t, api.PlanPro, 256, 1), f.app(t, api.PlanPro, 256, 1), f.app(t, api.PlanPro, 256, 1)} {
				if _, err := f.e.Wake(f.ctx, app.ID, "", "", sched.TriggerCronSched); err == nil {
					t.Fatal("a wake on the failing owner node succeeded")
				}
			}
			res, err := f.e.Wake(f.ctx, f.app(t, api.PlanPro, 256, 1).ID, "", "", sched.TriggerCronSched)
			if tc.avoided {
				if err != nil || res.NodeID != f.nodes[1].ID {
					t.Fatalf("wake after three node failures from three apps = %+v, %v; want it on the peer", res, err)
				}
				return
			}
			if err == nil || f.vmm.callsOn(owner) != 4 || f.vmm.callsOn(f.nodes[1].ID) != 0 {
				t.Fatalf("app failures took the node out: err=%v owner calls=%d peer calls=%d",
					err, f.vmm.callsOn(owner), f.vmm.callsOn(f.nodes[1].ID))
			}
		})
	}
}

// A node the heartbeat took out of rotation still had RUNNING rows until
// recovery parked them; the fast path handed them out meanwhile.
func TestWakeFastPathSkipsAnInstanceOnANodeOutOfRotation(t *testing.T) {
	f := newFailoverFleet(t, 2, 8192, func(string) error { return nil })
	registry := sched.NewNodeRegistry(f.nodes)
	f.e.WithNodeRegistry(registry)
	owner := f.nodes[0]
	app := f.app(t, api.PlanPro, 256, 2)
	dep, err := f.store.LiveDeployment(f.ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	stranded, err := f.store.CreateInstanceWithMode(f.ctx, app.ID, dep.ID, string(state.StateRunning), 256, owner.ID, uuid.NewString(), string(state.InstanceModeNormal))
	if err != nil {
		t.Fatal(err)
	}
	if res, err := f.e.Wake(f.ctx, app.ID, "", "", sched.TriggerCronSched); err != nil || res.InstanceID != stranded.ID {
		t.Fatalf("wake with the owner node healthy = %+v, %v; want its running instance", res, err)
	}

	if err := f.store.MarkComputeNodeInactive(f.ctx, owner.ID); err != nil {
		t.Fatal(err)
	}
	registry.Remove(owner.ID)
	res, err := f.e.Wake(f.ctx, app.ID, "", "", sched.TriggerCronSched)
	if err != nil {
		t.Fatalf("wake with the owner node out of rotation: %v", err)
	}
	if res.InstanceID == stranded.ID || res.NodeID != f.nodes[1].ID {
		t.Fatalf("wake = %+v, want a new instance on the peer, not %s on the dead node", res, stranded.ID)
	}
}

// pingableVMM answers Ping per node, as a running or stopped vmmd would.
type pingableVMM struct {
	*refusingVMM
	down string
}

func (v *pingableVMM) Ping(ctx context.Context, nodeID string) (*sched.PingOutcome, error) {
	if nodeID == v.down {
		return nil, status.Error(codes.Unavailable, `connection error: desc = "transport: Error while dialing: dial unix /run/faas/vmmd.sock: connect: connection refused"`)
	}
	return v.refusingVMM.Ping(ctx, nodeID)
}

// vmmd streams capacity every second to the scheduler on its node. A silent
// stream whose vmmd does not answer now takes the node out of placement in
// seconds, not after the heartbeat's three failed probes; a vmmd that answers
// is only reconnecting and stays in; resumed reports bring the node back.
func TestSilentCapacityStreamMovesPlacementAway(t *testing.T) {
	ctx := context.Background()
	f := newFailoverFleet(t, 2, 8192, func(string) error { return nil })
	owner := f.nodes[0]
	vmm := &pingableVMM{refusingVMM: f.vmm}
	e := sched.XNewEngine(t, f.store, vmm, &sched.FakeNotifier{}, "1.10.0").WithOwnerNodeID(owner.ID)
	report := func() {
		t.Helper()
		if err := e.CapacitySink()(sched.CapacityReport{NodeID: owner.ID, SampledAt: time.Now(), RAMHeadroomMB: 8192}); err != nil {
			t.Fatal(err)
		}
	}
	readiness := func() state.NodeReadiness {
		t.Helper()
		n, err := f.store.ComputeNodeByID(ctx, owner.ID)
		if err != nil {
			t.Fatal(err)
		}
		return n.EffectiveReadiness()
	}
	lost := time.Duration(api.CapacityStreamLostSeconds+1) * time.Second

	report()
	start := time.Now()
	e.ObserveCapacityStreams(ctx, start)
	if got := readiness(); got != state.NodeReadinessReady {
		t.Fatalf("readiness with a live stream = %s, want ready", got)
	}
	e.ObserveCapacityStreams(ctx, start.Add(lost))
	if got := readiness(); got != state.NodeReadinessReady {
		t.Fatalf("readiness while vmmd answers its ping = %s, want ready: the stream is only reconnecting", got)
	}
	vmm.down = owner.ID
	e.ObserveCapacityStreams(ctx, start.Add(2*lost))
	if got := readiness(); got != state.NodeReadinessUnavailable {
		t.Fatalf("readiness after the stream went silent and vmmd stopped answering = %s, want unavailable", got)
	}
	res, err := e.Wake(ctx, f.app(t, api.PlanPro, 256, 1).ID, "", "", sched.TriggerCronSched)
	if err != nil || res.NodeID != f.nodes[1].ID {
		t.Fatalf("wake with the owner's vmmd silent = %+v, %v; want it on the peer", res, err)
	}

	vmm.down = ""
	report()
	e.ObserveCapacityStreams(ctx, time.Now())
	if got := readiness(); got != state.NodeReadinessReady {
		t.Fatalf("readiness after reports resumed = %s, want ready", got)
	}
}
