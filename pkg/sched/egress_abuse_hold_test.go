package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 361 — a second fan-out recycle on one account within the hold window
// places the account abuse hold: the account's running instances are
// destroyed without a snapshot and every later wake is refused with the
// hold problem.
func TestEgressFanoutRepeatHoldsAccount(t *testing.T) {
	store := state.NewMemStore()
	acct, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	now := time.Unix(1_700_000_000, 0)
	e.now = func() time.Time { return now }
	ctx := context.Background()

	first, err := e.Wake(ctx, app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if err := e.RecycleForEgressAbuse(ctx, first.InstanceID, EgressAbuseFanout, 1500, 1200); err != nil {
		t.Fatalf("first recycle: %v", err)
	}
	if got, _ := store.AccountByID(ctx, acct.ID); got.AbuseHeld() {
		t.Fatal("one recycle must not hold the account")
	}

	now = now.Add(30 * time.Minute)
	second, err := e.Wake(ctx, app.ID, "", "", "")
	if err != nil {
		t.Fatalf("second Wake: %v", err)
	}
	// A sibling instance is still running when the hold lands.
	admitted, err := e.AdmitInstances(ctx, app.ID, "", TriggerPrewarm, 1)
	if err != nil || len(admitted) != 1 {
		t.Fatalf("AdmitInstances = %v, %v; want one sibling", admitted, err)
	}
	sibling := admitted[0]
	if sibling.InstanceID == second.InstanceID {
		t.Fatalf("sibling wake reused %s; the test needs two running instances", second.InstanceID)
	}
	snapshotsBefore := vmm.snapshots
	if err := e.RecycleForEgressAbuse(ctx, second.InstanceID, EgressAbuseFanout, 1500, 1200); err != nil {
		t.Fatalf("second recycle: %v", err)
	}
	held, _ := store.AccountByID(ctx, acct.ID)
	if !held.AbuseHeld() || held.AbuseHoldReason != state.AccountAbuseHoldEgressFanout {
		t.Fatalf("account after repeat = held %v reason %q, want egress_fanout hold", held.AbuseHeld(), held.AbuseHoldReason)
	}
	if ins, _ := store.InstanceByID(ctx, sibling.InstanceID); ins.State != string(state.StateStopped) {
		t.Fatalf("sibling state = %s, want stopped by the hold drain", ins.State)
	}
	if vmm.snapshots != snapshotsBefore {
		t.Fatalf("the hold drain snapshotted %d guests; held guests must be destroyed", vmm.snapshots-snapshotsBefore)
	}

	_, err = e.Wake(ctx, app.ID, "", "", "")
	var problem *api.Problem
	if !errors.As(err, &problem) || problem.Code != api.CodeAccountAbuseHold || !errors.Is(err, ErrPermanentWake) {
		t.Fatalf("wake on a held account = %v, want a permanent %s problem", err, api.CodeAccountAbuseHold)
	}
}

// Recycles further apart than the window never add up to a hold.
func TestEgressFanoutRecyclesOutsideWindowDoNotHold(t *testing.T) {
	e := &Engine{}
	start := time.Unix(1_700_000_000, 0)
	window := time.Duration(api.EgressFanoutHoldWindowSeconds) * time.Second
	if e.noteEgressAbuseRecycle("acct", start) {
		t.Fatal("first recycle reached the threshold")
	}
	if e.noteEgressAbuseRecycle("acct", start.Add(window+time.Second)) {
		t.Fatal("recycles a window apart reached the threshold")
	}
	if !e.noteEgressAbuseRecycle("acct", start.Add(window+2*time.Second)) {
		t.Fatal("two recycles a second apart did not reach the threshold")
	}
	if e.noteEgressAbuseRecycle("other", start) {
		t.Fatal("recycles leaked across accounts")
	}
}

// adr: 361 — flood recycles count toward the same escalation as fan-out; the
// hold records the signal of the recycle that tipped it over.
func TestEgressFloodAfterFanoutHoldsWithFloodReason(t *testing.T) {
	store := state.NewMemStore()
	acct, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	ctx := context.Background()
	first, err := e.Wake(ctx, app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if err := e.RecycleForEgressAbuse(ctx, first.InstanceID, EgressAbuseFanout, 1300, 1200); err != nil {
		t.Fatalf("fan-out recycle: %v", err)
	}
	second, err := e.Wake(ctx, app.ID, "", "", "")
	if err != nil {
		t.Fatalf("second Wake: %v", err)
	}
	if err := e.RecycleForEgressAbuse(ctx, second.InstanceID, EgressAbuseFlood, 900, 600); err != nil {
		t.Fatalf("flood recycle: %v", err)
	}
	held, _ := store.AccountByID(ctx, acct.ID)
	if !held.AbuseHeld() || held.AbuseHoldReason != state.AccountAbuseHoldEgressFlood {
		t.Fatalf("hold = %v %q, want egress_flood", held.AbuseHeld(), held.AbuseHoldReason)
	}
}
