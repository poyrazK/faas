// adr: 421
package sched

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOwnershipRecoverySweepSurvivesErrorsWithoutNotifications(t *testing.T) {
	var calls atomic.Int32
	recovered := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reb := NewRebalancer(func(ctx context.Context, source string) error {
		if source != "" {
			t.Errorf("periodic sweep source=%q", source)
		}
		if _, bounded := ctx.Deadline(); !bounded {
			t.Error("sweep has no deadline")
		}
		switch calls.Add(1) {
		case 1:
			return errors.New("temporary store failure")
		case 3:
			close(recovered)
		}
		return nil
	}, nil)
	reb.SweepInterval = time.Millisecond
	done := make(chan error, 1)
	go func() { done <- reb.RunSweep(ctx) }()
	select {
	case <-recovered:
	case <-time.After(time.Second):
		t.Fatal("periodic recovery did not survive the store failure")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("RunSweep cancellation: %v", err)
	}
}

type stalledOwnershipStore struct {
	state.Store
	entered chan struct{}
	calls   atomic.Int32
}

func (s *stalledOwnershipStore) ListOrphanedAppsPage(ctx context.Context, _, _ int, _, _ string) ([]state.OrphanedAppCandidate, error) {
	if s.calls.Add(1) == 1 {
		close(s.entered)
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestOwnershipRecoveryCoalescesHintsAndCancelsStalledStore(t *testing.T) {
	store, _, destination, _ := rebalanceTestOwners(t)
	stalled := &stalledOwnershipStore{Store: store, entered: make(chan struct{})}
	e := newEngine(t, stalled, &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithOwnerNodeID(destination.ID)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- e.RebalanceOrphanedApps(ctx, "") }()
	select {
	case <-stalled.entered:
	case <-time.After(time.Second):
		t.Fatal("initial batch never reached store")
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := e.RebalanceOrphanedApps(ctx, ""); err != nil {
				t.Error(err)
			}
		}()
	}
	coalesced := make(chan struct{})
	go func() { wg.Wait(); close(coalesced) }()
	select {
	case <-coalesced:
	case <-time.After(time.Second):
		cancel()
		<-done
		<-coalesced
		t.Fatal("node hints queued behind a stalled batch")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("stalled store cancellation: %v", err)
	}
	if stalled.calls.Load() != 1 {
		t.Fatalf("overlapping batches=%d, want 1", stalled.calls.Load())
	}
}

func TestOwnershipRecoveryFairPagesSkipOversizedApps(t *testing.T) {
	store, ctx, destination, source := rebalanceTestOwners(t)
	apps := []state.App{
		seedAppOnNode(t, store, ctx, api.PlanHobby, 128, source.ID),
		seedAppOnNode(t, store, ctx, api.PlanHobby, 128, source.ID),
		seedAppOnNode(t, store, ctx, api.PlanHobby, 128, source.ID),
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].ID < apps[j].ID })
	large := 1024
	if _, err := store.UpdateApp(ctx, apps[0].ID, state.UpdateAppParams{RAMMB: &large}); err != nil {
		t.Fatal(err)
	}
	destination.AdmissionCeilingMB = 128 + api.PerVMOverheadMB
	if _, err := store.UpsertComputeNodeFromOperator(ctx, destination); err != nil {
		t.Fatal(err)
	}
	if err := store.SetComputeNodeActive(ctx, source.ID, false); err != nil {
		t.Fatal(err)
	}
	e := newRebalanceEngine(t, store, destination.ID, &fakeNotifier{}).WithRebalanceConfig(60, 1)
	for range 3 {
		if err := e.RebalanceOrphanedApps(ctx, ""); err != nil {
			t.Fatal(err)
		}
	}
	for i, a := range apps {
		got, err := store.AppByID(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		want := destination.ID
		if i == 0 {
			want = source.ID
		}
		if got.NodeID != want {
			t.Fatalf("app %d owner=%s want=%s", i, got.NodeID, want)
		}
	}
	// Cursor wraps and retries refused work once physical headroom returns.
	destination.AdmissionCeilingMB = 4096
	if _, err := store.UpsertComputeNodeFromOperator(ctx, destination); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := e.RebalanceOrphanedApps(ctx, ""); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := store.AppByID(ctx, apps[0].ID)
	if got.NodeID != destination.ID {
		t.Fatal("refused app not retried after capacity returned")
	}
}

func TestOwnershipRecoverySourceScopedBeforeBatchLimit(t *testing.T) {
	store, ctx, destination, source := rebalanceTestOwners(t)
	other, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "other-source", TargetURL: "unix:///other.sock", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	var apps []state.App
	for range 4 {
		apps = append(apps, seedAppOnNode(t, store, ctx, api.PlanHobby, 128, source.ID))
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].ID < apps[j].ID })
	last := apps[len(apps)-1]
	if err := store.ReassignAppOwner(ctx, last.ID, source.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{source.ID, other.ID} {
		if err := store.SetComputeNodeActive(ctx, id, false); err != nil {
			t.Fatal(err)
		}
	}
	// Bypass only fixture cooldown; production still uses the fenced transfer.
	store.SetAppReassignedAtForTest(ctx, last.ID, time.Now().Add(-time.Hour))
	e := newRebalanceEngine(t, store, destination.ID, &fakeNotifier{}).WithRebalanceConfig(60, 1)
	if err := e.RebalanceOrphanedApps(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := store.AppByID(ctx, last.ID)
	if got.NodeID != destination.ID {
		t.Fatal("other source's earlier IDs consumed the scoped batch")
	}
}

func TestOwnershipRecoveryUnavailableSchedulerCannotClaimOnStartup(t *testing.T) {
	store, ctx, destination, source := rebalanceTestOwners(t)
	app := seedAppOnNode(t, store, ctx, api.PlanHobby, 128, source.ID)
	for _, id := range []string{destination.ID, source.ID} {
		if err := store.SetComputeNodeActive(ctx, id, false); err != nil {
			t.Fatal(err)
		}
	}
	e := newRebalanceEngine(t, store, destination.ID, &fakeNotifier{})
	if err := e.RebalanceOrphanedApps(ctx, ""); err != nil {
		t.Fatal(err)
	}
	got, _ := store.AppByID(ctx, app.ID)
	if got.NodeID != source.ID {
		t.Fatal("unavailable scheduler claimed an app during startup sweep")
	}
}

func TestOwnershipRecoveryHostLossBeyondBatchWithoutTraffic(t *testing.T) {
	store := state.NewMemStore()
	exerciseOwnershipRecoveryHostLoss(t, store, func() state.Store { return store })
}

// ADR-421 acceptance: 60 services lose their owner, healthy-host capacity is
// initially exhausted, the scheduler/client restarts, then capacity returns.
// Real allocation/recovery dispatch runs against a fake VMM, with no requests
// or node/app notifications delivered. Includes desired-zero preservation.
func exerciseOwnershipRecoveryHostLoss(t *testing.T, store state.Store, reopen func() state.Store) {
	t.Helper()
	ctx := context.Background()
	destination, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name: "lost-owner", TargetURL: "unix:///lost-vmmd.sock", Active: true,
		VPCPUs: 80, MemMB: 56000, MaxConcurrency: 20, AdmissionCeilingMB: api.RAMAdmissionCeilingMB, VCPUBudget: 512,
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.CreateAccount(ctx, "host-loss@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	type service struct {
		app, dep string
		desired  int
	}
	var services []service
	// Keep all original residency on the host that will be lost; production
	// service placement otherwise spreads replicas across healthy hosts.
	if err := store.SetComputeNodeActive(ctx, destination.ID, false); err != nil {
		t.Fatal(err)
	}
	origin := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithOwnerNodeID(source.ID)
	origin.serviceReconcileSubmit = func(context.Context, string) {}
	for i := range 60 {
		desired := 2
		if i == 59 {
			desired = 0
		}
		app, err := store.CreateApp(ctx, state.App{
			AccountID: account.ID, Slug: fmt.Sprintf("host-loss-%d", i), RAMMB: 128, MaxConcurrency: 2,
			IdleTimeoutS: 60, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService,
				ServiceReplicas: &state.ServiceReplicas{Min: desired, Max: 2, Desired: desired}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetAppNodeID(ctx, app.ID, source.ID); err != nil {
			t.Fatal(err)
		}
		dep, err := store.CreateDeployment(ctx, state.Deployment{
			AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:host-loss", Status: state.DeployLive,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		seedOwnershipRecoveryReplicas(t, origin, dep.ID, desired)
		rows, err := listServiceReplicas(ctx, store, app.ID, dep.ID)
		if err != nil || classifyServiceReplicas(rows).ready != desired {
			t.Fatalf("initial replicas for %d: %+v, %v", i, rows, err)
		}
		services = append(services, service{app.ID, dep.ID, desired})
	}
	if err := store.SetComputeNodeActive(ctx, source.ID, false); err != nil {
		t.Fatal(err)
	}
	// Existing host-failure primitives retire the old residency before
	// ownership recovery. This controller does not invent a VM-death verdict.
	for _, s := range services {
		rows, err := store.ListInstancesForApp(ctx, s.app)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if err := origin.RecreateInstance(ctx, row.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	destination.AdmissionCeilingMB = api.PerVMOverheadMB
	if _, err := store.UpsertComputeNodeFromOperator(ctx, destination); err != nil {
		t.Fatal(err)
	}
	notif := &fakeNotifier{}
	e := newEngine(t, store, &fakeVMM{}, notif, "1.10.0").WithOwnerNodeID(destination.ID)
	if err := e.RebalanceOrphanedApps(ctx, ""); err != nil {
		t.Fatal(err)
	}
	for _, s := range services {
		app, err := store.AppByID(ctx, s.app)
		if err != nil || app.NodeID != source.ID {
			t.Fatalf("app moved before capacity returned: %+v %v", app, err)
		}
	}
	store = reopen()
	e = newEngine(t, store, &fakeVMM{}, notif, "1.10.0").WithOwnerNodeID(destination.ID)
	loop := NewLoop(nil, e, testLog())
	t.Cleanup(loop.workPool().drain)
	destination.AdmissionCeilingMB = api.RAMAdmissionCeilingMB
	destination.VCPUBudget = 512
	if _, err := store.UpsertComputeNodeFromOperator(ctx, destination); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := e.RebalanceOrphanedApps(ctx, ""); err != nil {
			t.Fatal(err)
		}
		loop.workPool().drain()
	}
	// Saturated service submissions stay discoverable by the durable sweep.
	for range 12 {
		loop.runServiceRecovery(ctx)
		loop.workPool().drain()
	}
	for _, s := range services {
		app, err := store.AppByID(ctx, s.app)
		if err != nil || app.NodeID != destination.ID {
			t.Fatalf("orphan not recovered: %+v %v", app, err)
		}
		rows, err := listServiceReplicas(ctx, store, s.app, s.dep)
		if err != nil {
			t.Fatal(err)
		}
		status := classifyServiceReplicas(rows)
		if status.ready != s.desired || status.managed() != s.desired {
			ledger, _ := store.ServiceRecoveryByApp(ctx, s.app)
			t.Fatalf("recovered replicas for %s: %+v want=%d ledger=%+v rows=%+v", s.app, status, s.desired, ledger, rows)
		}
		for _, row := range rows {
			if row.State == string(state.StateRunning) && row.NodeID != destination.ID {
				t.Fatalf("replacement remains on lost host: %+v", row)
			}
		}
	}
	if countRebalancedNotifies(notif) != 60 {
		t.Fatalf("routing invalidations=%d, want exactly 60", countRebalancedNotifies(notif))
	}
	if origin.ledger.ResidentRAM() != 0 {
		t.Fatal("old owner admission reservation leaked")
	}
}

// The fixture starts 120 replicas in rapid succession. Concurrent lifecycle
// work may hold the input fence; retry only that typed refusal, through the
// production admission path, under a short deadline.
func seedOwnershipRecoveryReplicas(t *testing.T, e *Engine, deploymentID string, desired int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for {
		err := e.convergeServiceReplicasToTarget(ctx, deploymentID, desired, true)
		if err == nil {
			return
		}
		if !errors.Is(err, state.ErrApplicationStandardRuntimeBusy) {
			t.Fatalf("seed replicas: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("runtime input fence did not settle: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
