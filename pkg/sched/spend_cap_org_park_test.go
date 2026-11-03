// adr: 20261030093512 — a company app parks and drops its warm pool at its
// organization's spend cap, not at the cap of the member who created it.
package sched_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// orgSpendCapFixture is a Pro member's company app in a Pro organization that
// owns its resources, and a checker that reports the cap reached for one
// subject.
type orgSpendCapFixture struct {
	store  *state.MemStore
	member state.Account
	org    state.Org
	app    state.App
	dep    state.Deployment
	capped atomic.Value
}

func newOrgSpendCapFixture(ctx context.Context, t *testing.T) *orgSpendCapFixture {
	t.Helper()
	f := &orgSpendCapFixture{store: state.NewMemStore()}
	f.capped.Store("")
	member, err := f.store.CreateAccount(ctx, "cap-park-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	// MemStore creates a team organization owning its resources: its own
	// billing subject.
	org, err := f.store.CreateOrg(ctx, state.Org{Slug: "capped-" + uuid.NewString()[:8], Name: "Capped", Plan: api.PlanFree, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateAccountPlan(ctx, org.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	app, err := f.store.CreateApp(ctx, state.App{AccountID: member.ID, OrgID: org.ID, Slug: "company-app", RAMMB: 128, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := f.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	f.member, f.org, f.app, f.dep = member, org, app, dep
	return f
}

func (f *orgSpendCapFixture) engine(t *testing.T, vmm *sched.FakeVMM) *sched.Engine {
	t.Helper()
	return sched.XNewEngine(t, f.store, vmm, &sched.FakeNotifier{}, "1.10.0").WithOverageChecker(sched.NewMockChecker(
		func(_ context.Context, subject string) (sched.OverageStatus, int64, int64, error) {
			if subject == f.capped.Load() {
				return sched.OverageReached, 1_500, 1_000, nil
			}
			return sched.OverageOK, 0, 0, nil
		}))
}

// The reaper parks a running company app when its organization reaches its
// cap; the creator's own cap leaves it running.
func TestParkAppFollowsTheOrganizationSpendCap(t *testing.T) {
	ctx := context.Background()
	f := newOrgSpendCapFixture(ctx, t)
	e := f.engine(t, &sched.FakeVMM{})
	woke, err := e.Wake(ctx, f.app.ID, "", "", sched.TriggerGateway)
	if err != nil || woke.InstanceID == "" {
		t.Fatalf("Wake = (%+v, %v), want a running instance", woke, err)
	}
	f.capped.Store(f.member.ID)
	if acted, err := e.ParkApp(ctx, f.app.ID); err != nil || acted != 0 {
		t.Fatalf("ParkApp at the creator's own cap = (%d, %v), want the company app left running", acted, err)
	}
	f.capped.Store(f.org.ID)
	if acted, err := e.ParkApp(ctx, f.app.ID); err != nil || acted != 1 {
		t.Fatalf("ParkApp at the organization's cap = (%d, %v), want the running instance parked", acted, err)
	}
}

// warmInstance publishes one WARM instance of the fixture's app, as a
// paused warm-pool VM.
func (f *orgSpendCapFixture) warmInstance(ctx context.Context, t *testing.T) state.Instance {
	t.Helper()
	node, err := f.store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	ins, err := f.store.CreateInstanceWithMode(ctx, f.app.ID, f.dep.ID, string(state.StateWaking), f.app.RAMMB, node.ID, uuid.NewString(), string(state.InstanceModeNormal))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.store.RuntimeAppValuesForDeployment(ctx, f.app.AccountID, f.app.ID, f.dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := state.NewRuntimeAppConfigFence(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	warm, err := f.store.PublishOwnedInstanceRuntime(ctx, state.RuntimeInstancePublication{
		AccountID: f.app.AccountID, AppID: f.app.ID, InstanceID: ins.ID, NodeID: ins.NodeID, WakeID: ins.WakeID,
		ExpectedState: string(state.StateWaking), TargetState: string(state.StateWarm), Fence: fence.SecretFence, ConfigFence: fence,
		Netns: "fc-" + ins.ID, HostIP: "10.100.0.2", GuestUID: 20001,
	})
	if err != nil {
		t.Fatal(err)
	}
	return warm
}

// A paused warm VM is billed, so a company app keeps its warm pool while only
// its creator is at a cap and gives it up while its organization is.
func TestWarmPoolFollowsTheOrganizationSpendCap(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		capped   func(*orgSpendCapFixture) string
		wantWarm bool
	}{
		{"creator at a cap", func(f *orgSpendCapFixture) string { return f.member.ID }, true},
		{"organization at its cap", func(f *orgSpendCapFixture) string { return f.org.ID }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newOrgSpendCapFixture(ctx, t)
			size := 1
			if _, err := f.store.UpdateApp(ctx, f.app.ID, state.UpdateAppParams{WarmPoolSize: &size, SetWarmPoolSize: true}); err != nil {
				t.Fatal(err)
			}
			warm := f.warmInstance(ctx, t)
			f.capped.Store(tc.capped(f))
			if err := f.engine(t, &sched.FakeVMM{}).ReconcileWarmPool(ctx, f.app.ID); err != nil {
				t.Fatalf("ReconcileWarmPool: %v", err)
			}
			after, err := f.store.InstanceByID(ctx, warm.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got := after.State == string(state.StateWarm); got != tc.wantWarm {
				t.Fatalf("warm instance state = %s, want it kept warm: %v", after.State, tc.wantWarm)
			}
		})
	}
}
