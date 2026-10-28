// spec: §6.2 invariant 2 · adr: 193, 20261027172037, 20261027220445 — owner
// schedds admitting to shared nodes at the same moment never book a node past
// its RAM ceiling, vCPU budget or CPU budget, and the durable reservation
// never leaves a node short of full.
package sched_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// ownerFleet is three owner schedds on one PostgreSQL, each owning one node
// and the apps on it. Each node is bound by a different resource, so every
// part of the durable reservation is under contention.
type ownerFleet struct {
	store   *state.PgStore
	nodes   []state.ComputeNode
	engines []*sched.Engine
	apps    [][]string // apps[i] are owner i's apps

	mu   sync.Mutex
	live [][]string // live[i] are the instances owner i admitted and has not parked
}

const (
	ownerFleetAppRAMMB = 248
	ownerFleetAppsEach = 6
	ownerFleetAppConc  = 3
)

func newOwnerFleet(t *testing.T) *ownerFleet {
	t.Helper()
	ctx := t.Context()
	store := state.NewPgStore(pgtest.OpenMigrated(t))
	existing, err := store.ListComputeNodes(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range existing {
		if err := store.MarkComputeNodeInactive(ctx, n.ID); err != nil {
			t.Fatal(err)
		}
	}
	perInstanceMB := ownerFleetAppRAMMB + api.PerVMOverheadMB
	vcpus := api.MustLimitsFor(api.PlanPro).VCPU
	f := &ownerFleet{store: store}
	for i, shape := range []struct{ ceilingMB, vcpuBudget, vpcpus int }{
		{ceilingMB: 4 * perInstanceMB, vcpuBudget: 160, vpcpus: 4},        // RAM: 4 instances
		{ceilingMB: 64 * perInstanceMB, vcpuBudget: 3 * vcpus, vpcpus: 4}, // vCPU: 3 instances
		{ceilingMB: 64 * perInstanceMB, vcpuBudget: 160, vpcpus: 1},       // CPU: 8 instances
	} {
		node, err := store.CreateComputeNode(ctx, state.ComputeNode{
			Name: fmt.Sprintf("owner-%d-%s", i, uuid.NewString()[:8]), Lifecycle: state.NodeLifecycleActive,
			TargetURL: fmt.Sprintf("tcp://10.0.5.%d:7000", i+2), VPCPUs: shape.vpcpus, VCPUBudget: shape.vcpuBudget,
			MemMB: 2 * shape.ceilingMB, AdmissionCeilingMB: shape.ceilingMB, MaxConcurrency: 64, Active: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.HeartbeatComputeNode(ctx, node.ID); err != nil {
			t.Fatal(err)
		}
		f.nodes = append(f.nodes, node)
		f.engines = append(f.engines, sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0").WithOwnerNodeID(node.ID))
		var owned []string
		for range ownerFleetAppsEach {
			owned = append(owned, f.app(t, node.ID))
		}
		f.apps = append(f.apps, owned)
		f.live = append(f.live, nil)
	}
	return f
}

func (f *ownerFleet) app(t *testing.T, nodeID string) string {
	t.Helper()
	ctx := t.Context()
	acct, err := f.store.CreateAccount(ctx, "owners-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := f.store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "owners-" + uuid.NewString()[:8], Type: state.AppTypeFunction,
		RAMMB: ownerFleetAppRAMMB, MaxConcurrency: ownerFleetAppConc, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := f.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:" + uuid.NewString(), Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetAppNodeID(ctx, app.ID, nodeID); err != nil {
		t.Fatal(err)
	}
	return app.ID
}

// nodeUsage is what placement and the durable reservation sum per node.
type nodeUsage struct{ ramMB, vcpu, cpuMillicores int64 }

func (f *ownerFleet) usage(ctx context.Context) (map[string]nodeUsage, error) {
	ids := make([]string, 0, len(f.nodes))
	for _, n := range f.nodes {
		ids = append(ids, n.ID)
	}
	ram, err := f.store.ComputeNodeUsedMBByNode(ctx, ids)
	if err != nil {
		return nil, err
	}
	vcpu, err := f.store.ComputeNodeUsedVCPUByNode(ctx, ids)
	if err != nil {
		return nil, err
	}
	cpu, err := f.store.ComputeNodeUsedCPUMillicoresByNode(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]nodeUsage, len(ids))
	for _, id := range ids {
		out[id] = nodeUsage{ramMB: ram[id], vcpu: vcpu[id], cpuMillicores: cpu[id]}
	}
	return out, nil
}

func cpuBudget(n state.ComputeNode) int64 { return int64(n.VPCPUs) * 1000 * api.CPUOvercommit }

// violations lists every node booked past a budget.
func (f *ownerFleet) violations(used map[string]nodeUsage) []string {
	var out []string
	for _, n := range f.nodes {
		u := used[n.ID]
		if u.ramMB > int64(n.AdmissionCeilingMB) {
			out = append(out, fmt.Sprintf("%s: %d MB booked past its %d MB ceiling", n.Name, u.ramMB, n.AdmissionCeilingMB))
		}
		if u.vcpu > int64(n.VCPUBudget) {
			out = append(out, fmt.Sprintf("%s: %d vCPU booked past its %d vCPU budget", n.Name, u.vcpu, n.VCPUBudget))
		}
		if u.cpuMillicores > cpuBudget(n) {
			out = append(out, fmt.Sprintf("%s: %d millicores booked past its %d millicore budget", n.Name, u.cpuMillicores, cpuBudget(n)))
		}
	}
	return out
}

// takeLive removes and returns one of owner's live instances, if any.
func (f *ownerFleet) takeLive(owner int, rng *rand.Rand) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	live := f.live[owner]
	if len(live) == 0 {
		return "", false
	}
	k := rng.IntN(len(live))
	id := live[k]
	f.live[owner] = append(live[:k], live[k+1:]...)
	return id, true
}

func (f *ownerFleet) addLive(owner int, id string) {
	f.mu.Lock()
	f.live[owner] = append(f.live[owner], id)
	f.mu.Unlock()
}

type ownerFleetRun struct{ admitted, refused, parked atomic.Int64 }

// run drives every owner's workers at once until each has seen stopAfter
// capacity refusals in a row or the deadline passes. park is the share of
// operations that park one of the owner's instances instead of admitting.
func (f *ownerFleet) run(t *testing.T, seed uint64, deadline time.Time, park float64, stopAfter int) *ownerFleetRun {
	t.Helper()
	const workersEach = 4
	var wg sync.WaitGroup
	out := &ownerFleetRun{}
	var stream uint64
	for owner, e := range f.engines {
		for range workersEach {
			stream++
			rng := rand.New(rand.NewPCG(seed, stream))
			wg.Add(1)
			go func() {
				defer wg.Done()
				for streak := 0; streak < stopAfter && time.Now().Before(deadline); {
					if rng.Float64() < park {
						if id, ok := f.takeLive(owner, rng); ok {
							if err := e.Park(t.Context(), id); err != nil {
								t.Errorf("owner %d park %s: %v", owner, id, err)
								return
							}
							out.parked.Add(1)
						}
						continue
					}
					app := f.apps[owner][rng.IntN(len(f.apps[owner]))]
					res, err := e.AdmitInstance(t.Context(), app, "", "", sched.TriggerCronSched)
					var problem *api.Problem
					switch {
					case err == nil && res.AtCapacity:
					case err == nil:
						streak = 0
						out.admitted.Add(1)
						f.addLive(owner, res.InstanceID)
					case errors.As(err, &problem) && problem.Code == api.CodeCapacity:
						streak++
						out.refused.Add(1)
					default:
						t.Errorf("owner %d admit %s: %v", owner, app, err)
						return
					}
				}
			}()
		}
	}
	wg.Wait()
	return out
}

func TestPropertyOwnersAdmittingTogetherKeepNodeBudgets(t *testing.T) {
	f := newOwnerFleet(t)
	seed := uint64(time.Now().UnixNano())
	t.Logf("seed %d", seed)

	// A checker samples every node while the owners race.
	stop := make(chan struct{})
	checked := make(chan []string, 1)
	go func() {
		var found []string
		defer func() { checked <- found }()
		for {
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Millisecond):
			}
			used, err := f.usage(t.Context())
			if err != nil {
				found = append(found, err.Error())
				return
			}
			if len(found) < 100 {
				found = append(found, f.violations(used)...)
			}
		}
	}()

	// Fill: every owner admits until no node takes another instance.
	fill := f.run(t, seed, time.Now().Add(30*time.Second), 0, 6)
	used, err := f.usage(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	perInstanceMB := int64(ownerFleetAppRAMMB + api.PerVMOverheadMB)
	vcpus := int64(api.MustLimitsFor(api.PlanPro).VCPU)
	for _, n := range f.nodes {
		u := used[n.ID]
		full := u.ramMB+perInstanceMB > int64(n.AdmissionCeilingMB) || u.vcpu+vcpus > int64(n.VCPUBudget) ||
			u.cpuMillicores+api.DefaultAppCPUMillicores > cpuBudget(n)
		if !full {
			t.Errorf("%s has room for another instance after every owner was refused: %+v", n.Name, u)
		}
	}
	t.Logf("fill: %d admitted, %d capacity refusals, usage %+v", fill.admitted.Load(), fill.refused.Load(), used)

	// Churn: owners park and admit at once, so the room each park frees is
	// raced for by every owner.
	churn := f.run(t, seed+1, time.Now().Add(3*time.Second), 0.4, 1<<30)
	t.Logf("churn: %d parked, %d admitted, %d capacity refusals", churn.parked.Load(), churn.admitted.Load(), churn.refused.Load())
	if churn.parked.Load() == 0 || churn.admitted.Load() == 0 {
		t.Errorf("churn parked %d and admitted %d instances, want both: freed room was never raced for", churn.parked.Load(), churn.admitted.Load())
	}

	close(stop)
	found := <-checked
	if used, err = f.usage(t.Context()); err != nil {
		t.Fatal(err)
	}
	found = append(found, f.violations(used)...)
	if len(found) > 0 {
		t.Fatalf("%d samples saw a node booked past a budget (seed %d), first: %s", len(found), seed, found[0])
	}
}
