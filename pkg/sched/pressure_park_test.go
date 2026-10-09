// adr: 643

package sched

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func pressureInstance(id, app string, idle time.Duration, now time.Time) InstanceInfo {
	return InstanceInfo{
		Instance: id, AppID: app, Plan: api.PlanPro, State: state.StateRunning, RAMMB: 1024,
		NodeID: "n1", Started: now.Add(-10 * time.Minute), LastRequest: now.Add(-idle),
	}
}

func TestSelectPressureParkCandidatesKeepsReapingExemptions(t *testing.T) {
	now := time.Now()
	floored := pressureInstance("floored", "floor-app", time.Hour, now)
	floored.MinInstances = 1
	busy := pressureInstance("busy", "busy-app", time.Hour, now)
	busy.InflightRequests = 1
	young := pressureInstance("young", "young-app", time.Hour, now)
	young.Started = now.Add(-10 * time.Second)
	reserved := pressureInstance("reserved", "reserved-app", time.Hour, now)
	reserved.EvictionPriority = string(api.EvictionPriorityReserved)
	service := pressureInstance("service", "service-app", time.Hour, now)
	service.Mode = string(state.InstanceModeService)
	scale := pressureInstance("scale", "scale-app", 2*time.Hour, now)
	scale.Plan = api.PlanScale
	got := SelectPressureParkCandidates(now, []InstanceInfo{
		floored, busy, young, reserved, service, scale,
		pressureInstance("recent", "recent-app", 5*time.Second, now),
		pressureInstance("idle-newer", "a", 2*time.Minute, now),
		pressureInstance("idle-older", "b", 20*time.Minute, now),
	})
	var ids []string
	for _, c := range got {
		ids = append(ids, c.Instance)
	}
	want := []string{"idle-older", "idle-newer", "scale"}
	if len(ids) != len(want) {
		t.Fatalf("candidates = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("candidates = %v, want %v (LRU, Scale plan last)", ids, want)
		}
	}
}

func TestSelectPressureParkCandidatesParksOnlyAboveTheFloor(t *testing.T) {
	now := time.Now()
	a := pressureInstance("a1", "app", time.Hour, now)
	a.MinInstances = 1
	b := pressureInstance("a2", "app", 2*time.Hour, now)
	b.MinInstances = 1
	got := SelectPressureParkCandidates(now, []InstanceInfo{a, b})
	if len(got) != 1 || got[0].Instance != "a2" {
		t.Fatalf("candidates = %+v, want only the older of two instances above a floor of 1", got)
	}
}

func TestClaimPressureParkCandidate(t *testing.T) {
	now := time.Now()
	e := &Engine{}
	e.publishPressureParkCandidates([]PressureParkCandidate{
		{Instance: "own", AppID: "waking-app", RAMMB: 2048},
		{Instance: "small", AppID: "x", RAMMB: 256},
		{Instance: "large", AppID: "y", RAMMB: 1024},
	}, now)
	c, ok := e.claimPressureParkCandidate("waking-app", 1024, now)
	if !ok || c.Instance != "large" {
		t.Fatalf("first claim = %+v %v, want the candidate that fits the request, never the waking app's own", c, ok)
	}
	c, ok = e.claimPressureParkCandidate("waking-app", 1024, now)
	if !ok || c.Instance != "small" {
		t.Fatalf("second claim = %+v %v, want the remaining candidate", c, ok)
	}
	if _, ok := e.claimPressureParkCandidate("waking-app", 1024, now); ok {
		t.Fatal("a third claim succeeded; claimed candidates must not be handed out twice")
	}
	e.publishPressureParkCandidates([]PressureParkCandidate{{Instance: "z", AppID: "z", RAMMB: 1024}}, now)
	if _, ok := e.claimPressureParkCandidate("waking-app", 1024, now.Add(pressureParkFreshness+time.Second)); ok {
		t.Fatal("a stale reaper view was used")
	}
}

func TestParkForCapacityOnlyForGatewayCapacityRefusals(t *testing.T) {
	e := &Engine{}
	e.publishPressureParkCandidates([]PressureParkCandidate{{Instance: "idle", AppID: "other", RAMMB: 1024}}, time.Now())
	for _, tc := range []struct {
		trigger string
		err     error
	}{
		{TriggerGateway, nil},
		{TriggerGateway, api.NewProblem(429, api.CodePlanLimitConcur, "Concurrency limit", "at cap")},
		{TriggerGateway, errors.New("vmm unreachable")},
		{"floor.deployment", api.ErrCapacity("placement: no active compute_node fits")},
	} {
		if e.parkForCapacity(context.Background(), "app", tc.trigger, tc.err) {
			t.Fatalf("trigger=%s err=%v parked an instance", tc.trigger, tc.err)
		}
	}
}

// production-us hunt #5 (H5-32): a full node refused a gateway wake while an
// idle instance of another app held the slot. The refused wake now parks
// that instance (snapshot, so its next wake restores) and places.
func TestGatewayWakeParksAnIdleInstanceWhenTheNodeIsFull(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 128, 1)
	vacct, err := store.CreateAccount(ctx, "victim@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	victim, err := store.CreateApp(ctx, state.App{AccountID: vacct.ID, Slug: "victim", RAMMB: 128, MaxConcurrency: 1, IdleTimeoutS: 600})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: victim.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive}); err != nil {
		t.Fatal(err)
	}
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	idle, err := e.Wake(ctx, victim.ID, "", "", "")
	if err != nil {
		t.Fatalf("wake victim: %v", err)
	}
	idleSince := time.Now().Add(-10 * time.Minute)
	if _, err := store.TouchInstancesLastSeen(ctx, []state.InstanceTouch{{InstanceID: idle.InstanceID, LastRequest: idleSince}}); err != nil {
		t.Fatal(err)
	}
	// Fill the node's vCPU budget around the idle instance.
	for i := 0; e.Ledger().Admit(Request{
		Instance: "fill-" + strconv.Itoa(i), AppID: "fill-app-" + strconv.Itoa(i),
		Plan: api.PlanFree, RAMMB: 8, VCPU: 1, MaxConcurrency: 1, NodeID: e.defaultLocalNodeID,
	}) == nil; i++ {
	}
	e.publishPressureParkCandidates([]PressureParkCandidate{{
		Instance: idle.InstanceID, AppID: victim.ID, NodeID: e.defaultLocalNodeID, Plan: api.PlanPro, RAMMB: 128, LastActivity: idleSince,
	}}, time.Now())

	if _, err := e.Wake(ctx, app.ID, "", "", "floor.deployment"); !isFleetCapacityRefusal(err) {
		t.Fatalf("background wake err = %v, want the capacity refusal (only gateway wakes park others)", err)
	}
	res, err := e.Wake(ctx, app.ID, "", "", TriggerGateway)
	if err != nil {
		t.Fatalf("gateway wake: %v, want it placed after parking the idle instance", err)
	}
	if res.InstanceID == "" || vmm.snapshots != 1 {
		t.Fatalf("result=%+v snapshots=%d, want a placed instance and one park snapshot", res, vmm.snapshots)
	}
	row, err := store.InstanceByID(ctx, idle.InstanceID)
	if err != nil || row.State != string(state.StateParked) {
		t.Fatalf("idle instance state = %q (%v), want parked", row.State, err)
	}
}

// production-us hunt #6 (H5-59): a new rollout moved the previous deployment
// of a traffic split to 0%, but its warm instance kept a slot. With the
// serving instance it filled max_concurrency plus the rollout grant, so every
// smoke wake of the candidate was refused and the deploy failed. The refused
// admission now parks the idle zero-traffic instance and admits.
func TestAdmissionAtCapParksAnIdleZeroTrafficSibling(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, serving := seedApp(t, store, api.PlanScale, 256, 1)
	staged, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:def", Status: state.DeployLive,
		TrafficPercent: 0, TrafficPercentExplicit: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if staged, err = store.DeploymentByID(ctx, staged.ID); err != nil || staged.TrafficPercent != 0 {
		t.Fatalf("staged deployment traffic = %d (%v), want 0", staged.TrafficPercent, err)
	}
	candidate, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:123", Status: state.DeploySnapshotting,
		TrafficPercent: 0, TrafficPercentExplicit: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	servingIns, err := e.AdmitInstance(ctx, app.ID, serving.ID, "", TriggerGateway)
	if err != nil || servingIns.InstanceID == "" {
		t.Fatalf("admit serving: %+v %v", servingIns, err)
	}
	stagedIns, err := e.AdmitInstance(ctx, app.ID, staged.ID, "", TriggerGateway)
	if err != nil || stagedIns.InstanceID == "" {
		t.Fatalf("admit staged (rollout grant): %+v %v", stagedIns, err)
	}
	// Both instances are idle; the clock moves past the park guards.
	e.now = func() time.Time { return time.Now().Add(10 * time.Minute) }

	res, err := e.AdmitInstance(ctx, app.ID, candidate.ID, "", TriggerDeploymentSmoke)
	if err != nil || res.AtCapacity || res.InstanceID == "" {
		t.Fatalf("candidate admission = %+v, %v; want admitted after parking the zero-traffic instance", res, err)
	}
	if row, err := store.InstanceByID(ctx, stagedIns.InstanceID); err != nil || row.State != string(state.StateParked) {
		t.Fatalf("zero-traffic instance state = %q (%v), want parked", row.State, err)
	}
	if row, err := store.InstanceByID(ctx, servingIns.InstanceID); err != nil || row.State != string(state.StateRunning) {
		t.Fatalf("serving instance state = %q (%v), want running", row.State, err)
	}
}

// The serving deployment, the requested deployment and busy instances keep
// their slots: a refusal with nothing idle at 0% stays a refusal.
func TestParkZeroTrafficSiblingKeepsServingAndBusyInstances(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, serving := seedApp(t, store, api.PlanScale, 256, 2)
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	for range 2 {
		if res, err := e.AdmitInstance(ctx, app.ID, serving.ID, "", TriggerGateway); err != nil || res.InstanceID == "" {
			t.Fatalf("admit serving: %+v %v", res, err)
		}
	}
	e.now = func() time.Time { return time.Now().Add(10 * time.Minute) }
	if e.parkZeroTrafficSibling(ctx, app.ID, "") {
		t.Fatal("parked an instance of the serving deployment")
	}
	if vmm.snapshots != 0 {
		t.Fatalf("snapshots = %d, want none", vmm.snapshots)
	}
}
