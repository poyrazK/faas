package sched

// Tests for the placement-claim subscriber (Phase 2 / Gate A,
// migration 00091 — apps.node_id nullable). MemStore-backed, no
// Postgres required.
//
//   1. TestClaimUnplaced_SuccessClaimsApp: a fresh app with
//      node_id = NULL is stamped by Engine.ClaimUnplaced; a
//      NotifyAppChanged "claimed" event is emitted.
//   2. TestClaimUnplaced_NoFitReturnsError: a capacity-full
//      fleet makes ClaimUnplaced return a wrapped error; the row
//      stays NULL; the subscriber logs and continues (the next
//      notify will retry).
//   3. TestClaimUnplaced_AlreadyClaimedByPeerSkips: a pre-bound
//      app is a no-op (idempotent on redelivery / peer-won race).
//   4. TestClaimUnplaced_RaceLosesSilently: two parallel
//      ClaimUnplaced calls on the same appID; exactly one
//      observes non-empty nodeID after both return; the loser
//      receives ErrConflict (logged, not returned to caller).
//   5. TestPlacementClaimSubscriber_FiltersKind: only
//      kind="created" reaches Engine.ClaimUnplaced.
//   6. TestPlacementClaimSubscriber_BadPayloadLogsAndContinues:
//      malformed JSON must not block subsequent messages on the
//      same channel.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestClaimUnplaced_SuccessClaimsApp: a fresh unplaced app is
// stamped by Engine.ClaimUnplaced; the resulting apps.node_id is
// non-empty; a NotifyAppChanged "claimed" event is emitted on
// success.
func TestClaimUnplaced_SuccessClaimsApp(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanHobby, 256, 2)
	if app.NodeID != "" {
		t.Fatalf("seedApp: app.NodeID = %q, want empty (post-00091 contract)", app.NodeID)
	}
	vmm := &fakeVMM{}
	notif := &fakeNotifier{}
	e := newEngine(t, store, vmm, notif, "1.10.0")

	if err := e.ClaimUnplaced(context.Background(), app.ID); err != nil {
		t.Fatalf("ClaimUnplaced: %v", err)
	}

	got, err := store.AppByID(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("AppByID post-claim: %v", err)
	}
	if got.NodeID == "" {
		t.Errorf("post-claim apps.node_id is empty; want a compute_node id")
	}
	if notif.count(db.NotifyAppChanged) != 1 {
		t.Errorf("notif events = %d, want 1 (the 'claimed' re-emit)", notif.count(db.NotifyAppChanged))
	}
	if got, _ := store.ListUnplacedApps(context.Background()); len(got) != 0 {
		t.Errorf("ListUnplacedApps post-claim = %d rows, want 0", len(got))
	}
}

// TestClaimUnplaced_NoFitReturnsError: when the seeded app's
// RAM exceeds every active node's admission ceiling (here, the
// MemStore's auto-seeded default-local carries a 47,600 MB
// ceiling — issue #97), ChoosePlacement rejects the request.
// ClaimUnplaced must surface the wrapped error and leave
// apps.node_id NULL so the next notify / cold-start sweep can
// retry once an operator widens the budget.
func TestClaimUnplaced_NoFitReturnsError(t *testing.T) {
	store := state.NewMemStore()
	// 50 GB request > 47,600 MB ceiling → no candidate fits.
	_, app, _ := seedApp(t, store, api.PlanHobby, 50_000, 2)
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")

	err := e.ClaimUnplaced(context.Background(), app.ID)
	if err == nil {
		t.Fatalf("ClaimUnplaced on capacity-exhausted fleet returned nil; want an error")
	}
	if !strings.Contains(err.Error(), "claim unplaced") {
		t.Errorf("error = %v, want wrap with op 'claim unplaced'", err)
	}

	got, err := store.AppByID(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if got.NodeID != "" {
		t.Errorf("post-failed-claim apps.node_id = %q, want empty (so retry can fire)", got.NodeID)
	}
}

// TestClaimUnplaced_AlreadyClaimedByPeerSkips: a pre-bound app
// (e.g. another schedd won the race and stamped node_id) is a
// no-op. The function returns nil; no notify is emitted.
func TestClaimUnplaced_AlreadyClaimedByPeerSkips(t *testing.T) {
	store := state.NewMemStore()
	acct, app, _ := seedApp(t, store, api.PlanHobby, 256, 2)
	// Stamp an owner manually so the function bails at the early
	// "already claimed" guard. The exact node id is irrelevant;
	// only the conditional in ClaimUnplaced matters.
	if err := store.SetAppNodeID(context.Background(), app.ID, "peer-node-id"); err != nil {
		t.Fatalf("seed pre-bound owner: %v", err)
	}

	notif := &fakeNotifier{}
	e := newEngine(t, store, &fakeVMM{}, notif, "1.10.0")
	if err := e.ClaimUnplaced(context.Background(), app.ID); err != nil {
		t.Fatalf("ClaimUnplaced on pre-bound app: %v (want nil)", err)
	}
	if n := notif.count(db.NotifyAppChanged); n != 0 {
		t.Errorf("notif events = %d, want 0 (no claim → no re-emit)", n)
	}

	// Defensive: account shape was preserved.
	got, _ := store.AccountByID(context.Background(), acct.ID)
	if got.ID != acct.ID {
		t.Errorf("account lookup regressed: got %+v, want %+v", got, acct)
	}
}

// TestClaimUnplaced_RaceLosesSilently: two parallel ClaimUnplaced
// calls on the same appID. The MemStore's SetAppNodeID uses the
// same conditional UPDATE guard as PgStore (WHERE node_id IS
// NULL), so exactly one writer wins; the loser observes
// ErrConflict and returns nil (no caller-visible error). After
// both calls return, exactly one owner is bound and the row is
// not double-stamped.
func TestClaimUnplaced_RaceLosesSilently(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanHobby, 256, 2)
	// Two engines representing two schedd peers; each has its own
	// fakeNotifier so we can assert both observed the same final
	// state.
	vmmA := &fakeVMM{}
	notifA := &fakeNotifier{}
	vmmB := &fakeVMM{}
	notifB := &fakeNotifier{}
	engineA := newEngine(t, store, vmmA, notifA, "1.10.0")
	engineB := newEngine(t, store, vmmB, notifB, "1.10.0")

	errCh := make(chan error, 2)
	go func() { errCh <- engineA.ClaimUnplaced(context.Background(), app.ID) }()
	go func() { errCh <- engineB.ClaimUnplaced(context.Background(), app.ID) }()

	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Errorf("ClaimUnplaced[%d]: %v (want nil; loser must drop silently)", i, err)
		}
	}

	got, err := store.AppByID(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("AppByID post-race: %v", err)
	}
	if got.NodeID == "" {
		t.Errorf("post-race apps.node_id is empty; want a single winner's id")
	}
	// Total 'claimed' events: exactly one (the winner). The
	// loser returned before reaching e.notif.Notify.
	total := notifA.count(db.NotifyAppChanged) + notifB.count(db.NotifyAppChanged)
	if total != 1 {
		t.Errorf("notif events total = %d, want 1 (only winner re-emits)", total)
	}
}

// TestPlacementClaimSubscriber_FiltersKind: only kind="created"
// messages drive an Engine.ClaimUnplaced call. Other kinds
// (updated/deleted/parked/woken/renamed/claimed) are dropped
// without touching the store.
func TestPlacementClaimSubscriber_FiltersKind(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanHobby, 256, 2)
	notif := &fakeNotifier{}
	e := newEngine(t, store, &fakeVMM{}, notif, "1.10.0")

	feed := newFakeNotify(8)
	sub := NewPlacementClaimSubscriber(e, silenceLog())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sub.Run(ctx, feed.Channel()) }()

	// All non-created kinds must be no-ops.
	for _, kind := range []string{"updated", "deleted", "parked", "woken", "renamed", "claimed"} {
		feed.Send(db.Notification{
			Channel: db.NotifyAppChanged,
			Payload: `{"kind":"` + kind + `","app_id":"` + app.ID + `"}`,
		})
	}
	// Give the loop a moment to drain the buffered sends.
	if err := waitFor(func() bool { return notif.count(db.NotifyAppChanged) == 0 }, time.Second); err != nil {
		t.Fatalf("non-created kinds emitted a 'claimed' notify")
	}

	// Now the created kind lands.
	feed.Send(db.Notification{
		Channel: db.NotifyAppChanged,
		Payload: `{"kind":"created","app_id":"` + app.ID + `","slug":"` + app.Slug + `"}`,
	})
	if err := waitFor(func() bool { return notif.count(db.NotifyAppChanged) >= 1 }, 2*time.Second); err != nil {
		t.Fatalf("kind=created did not produce a 'claimed' re-emit (events=%d)", notif.count(db.NotifyAppChanged))
	}

	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}
}

// TestPlacementClaimSubscriber_BadPayloadLogsAndContinues: a
// malformed JSON payload must log a warning and let the loop
// continue — the next valid message still drives a claim.
func TestPlacementClaimSubscriber_BadPayloadLogsAndContinues(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanHobby, 256, 2)
	notif := &fakeNotifier{}
	e := newEngine(t, store, &fakeVMM{}, notif, "1.10.0")

	feed := newFakeNotify(4)
	sub := NewPlacementClaimSubscriber(e, silenceLog())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sub.Run(ctx, feed.Channel()) }()

	// Malformed: not JSON.
	feed.Send(db.Notification{Channel: db.NotifyAppChanged, Payload: "not json {"})
	// Wrong channel: defensive drop.
	feed.Send(db.Notification{Channel: db.NotifyComputeNodesChanged, Payload: `{"kind":"created","app_id":"x"}`})
	// Empty app_id: defensive drop (still valid JSON).
	feed.Send(db.Notification{Channel: db.NotifyAppChanged, Payload: `{"kind":"created","app_id":""}`})

	// Valid message after the bad ones.
	feed.Send(db.Notification{
		Channel: db.NotifyAppChanged,
		Payload: `{"kind":"created","app_id":"` + app.ID + `","slug":"` + app.Slug + `"}`,
	})

	if err := waitFor(func() bool { return notif.count(db.NotifyAppChanged) >= 1 }, 2*time.Second); err != nil {
		t.Fatalf("valid message after bad ones did not produce a 'claimed' re-emit (events=%d)",
			notif.count(db.NotifyAppChanged))
	}

	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}
}

// TestPlacementClaimSubscriber_RunCtxCancelClosesLoop: cancelling
// ctx must cause Run to return ctx.Err() within a reasonable
// window (the loop must not block forever on the channel).
func TestPlacementClaimSubscriber_RunCtxCancelClosesLoop(t *testing.T) {
	store := state.NewMemStore()
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	feed := newFakeNotify(1)
	sub := NewPlacementClaimSubscriber(e, silenceLog())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sub.Run(ctx, feed.Channel()) }()

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Run did not return within 2s of cancel")
	}
}
