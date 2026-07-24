package meter

// sampler_test exercises the Move 1 wiring in SampleAndRoll: the
// requests=N field is set from CountInstanceInvocationsInMinute (the
// meter's join key), and the AppendUsage idempotency on
// (instance, minute) keeps restart-driven redelivery from inflating
// the per-minute count.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedMinuteUsage seeds an account, app, instance, and a rolling minute.
// Returns the IDs the test needs to assert on.
func seedMinuteUsage(t *testing.T) (state.Store, string /*appID*/, string /*instID*/, time.Time /*minute*/) {
	t.Helper()
	store := state.NewMemStore()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "u@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "u", RAMMB: 256, Type: state.AppTypeApp,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Status: state.DeployLive, Kind: state.DeploymentKindImage,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	ins, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 256, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	return store, app.ID, ins.ID, time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
}

// TestSampleAndRoll_RequestsEqualsInvocationCount pins the Move 1
// wiring: three dispatching rows for (instance, minute) →
// usage_minutes.requests = 3. The meter's CountInstanceInvocationsInMinute
// reads state='dispatching' + due_at within the minute; without the
// instance_id stamp (drain's StampInstanceInvocation) the count would
// be 0.
func TestSampleAndRoll_RequestsEqualsInvocationCount(t *testing.T) {
	store, appID, instID, minute := seedMinuteUsage(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		inv := state.Invocation{
			AppID: appID, AccountID: "ignored", Source: state.InvocationQueue,
			Method: "POST", Path: "/x", DueAt: minute.Add(time.Duration(i) * time.Second),
		}
		enq, err := store.EnqueueInvocation(ctx, inv)
		if err != nil {
			t.Fatalf("EnqueueInvocation %d: %v", i, err)
		}
		// Walk the row through pending → dispatching with the
		// instance stamped. Mirrors the drain's claim + stamp path.
		if _, err := store.ClaimInvocation(ctx, enq.ID, "", 30); err != nil {
			t.Fatalf("ClaimInvocation %d: %v", i, err)
		}
		if err := store.StampInstanceInvocation(ctx, enq.ID, instID); err != nil {
			t.Fatalf("StampInstanceInvocation %d: %v", i, err)
		}
	}

	s := NewSampler(store, func() time.Time { return minute })
	rows, err := s.SampleAndRoll(ctx)
	if err != nil {
		t.Fatalf("SampleAndRoll: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rolled rows = %d, want 1", len(rows))
	}
	// Read the per-minute usage row. MemStore's AppendUsage wrote
	// MBSeconds + Requests — read back via the public API.
	month := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	usage, err := store.UsageByMonth(ctx /* accountID */, rowAccountID(t, store, appID), month)
	if err != nil {
		t.Fatalf("UsageByMonth: %v", err)
	}
	// The first row with non-zero Requests wins; we want the one
	// for our appID. MemStore's UsageByMonth aggregates per (app, month)
	// — sum Requests across all instances for the app.
	var totalReq int64
	for _, u := range usage {
		if u.AppID == appID {
			totalReq = u.Requests
		}
	}
	if totalReq != 3 {
		t.Errorf("usage requests = %d, want 3 (one per dispatching row)", totalReq)
	}
}

// TestSampleAndRoll_AppendUsageIdempotent pins the no-double-count
// path: a second SampleAndRoll within the same minute must NOT inflate
// requests. AppendUsage's (instance, minute) idempotency is the
// production guarantee; this is the meter's side of the contract.
func TestSampleAndRoll_AppendUsageIdempotent(t *testing.T) {
	store, appID, instID, minute := seedMinuteUsage(t)
	ctx := context.Background()
	// One dispatching row.
	enq, err := store.EnqueueInvocation(ctx, state.Invocation{
		AppID: appID, Source: state.InvocationQueue, DueAt: minute,
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation: %v", err)
	}
	if _, err := store.ClaimInvocation(ctx, enq.ID, "", 30); err != nil {
		t.Fatalf("ClaimInvocation: %v", err)
	}
	if err := store.StampInstanceInvocation(ctx, enq.ID, instID); err != nil {
		t.Fatalf("StampInstanceInvocation: %v", err)
	}

	s := NewSampler(store, func() time.Time { return minute })
	acct := rowAccountID(t, store, appID)
	month := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	if _, err := s.SampleAndRoll(ctx); err != nil {
		t.Fatalf("first sample: %v", err)
	}
	first, _ := store.UsageByMonth(ctx, acct, month)
	var firstReq int64
	for _, u := range first {
		if u.AppID == appID {
			firstReq = u.Requests
		}
	}

	// Second sample within the same minute. AppendUsage's
	// (instance, minute) idempotency must keep requests at 1.
	if _, err := s.SampleAndRoll(ctx); err != nil {
		t.Fatalf("second sample: %v", err)
	}
	second, _ := store.UsageByMonth(ctx, acct, month)
	var secondReq int64
	for _, u := range second {
		if u.AppID == appID {
			secondReq = u.Requests
		}
	}
	if secondReq != firstReq {
		t.Errorf("requests after second sample = %d, want %d (idempotent on (instance, minute))", secondReq, firstReq)
	}
}

// TestSampleAndRoll_ZeroInvocationsZeroRequests is the cold-wake
// path: an instance with no invocations rolled in the minute has
// requests=0. Matches the meter's pre-Move-1 behaviour for just-parked
// instances.
func TestSampleAndRoll_ZeroInvocationsZeroRequests(t *testing.T) {
	store, appID, _, minute := seedMinuteUsage(t)
	ctx := context.Background()

	s := NewSampler(store, func() time.Time { return minute })
	if _, err := s.SampleAndRoll(ctx); err != nil {
		t.Fatalf("SampleAndRoll: %v", err)
	}
	acct := rowAccountID(t, store, appID)
	month := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	usage, _ := store.UsageByMonth(ctx, acct, month)
	for _, u := range usage {
		if u.AppID == appID && u.Requests != 0 {
			t.Errorf("cold-wake instance requests = %d, want 0", u.Requests)
		}
	}
}

// rowAccountID is a tiny helper to round-trip the account id from
// appID. The test surface for MemStore does not expose a
// list-apps-by-id path that returns account_id, so we read the app
// and pull AccountID off it.
func rowAccountID(t *testing.T, store state.Store, appID string) string {
	t.Helper()
	app, err := store.AppByID(context.Background(), appID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	return app.AccountID
}

// silence unused imports if a future refactor drops one of the
// helpers above.
var _ = json.Valid
