package conformance

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// testAppProtectionEnrollmentAndObjects pins ADR-20261116120000: an app has
// no enrollment until one is set; setting it again only changes the state;
// item records need an enrollment; a skipped record can be replaced, a
// created one never; records list in item order; unknown apps, states,
// items and malformed records are refused.
func testAppProtectionEnrollmentAndObjects(t *testing.T, fx *Fixture) {
	store, ok := fx.Store.(state.AppProtectionStore)
	if !ok {
		t.Skip("store has no app protection")
	}
	appID := fx.App.ID

	if _, ok, err := store.GetAppProtection(fx.Ctx, appID); err != nil || ok {
		t.Fatalf("GetAppProtection before enrollment = ok %v, err %v; want none", ok, err)
	}
	if err := store.RecordAppProtectionObject(fx.Ctx, state.AppProtectionObject{
		AppID: appID, Item: state.AppProtectionAvailabilitySLO, ObjectID: uuid.NewString(),
	}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("RecordAppProtectionObject before enrollment = %v; want ErrNotFound", err)
	}

	on, err := store.SetAppProtection(fx.Ctx, appID, fx.Account.ID, state.AppProtectionOn)
	if err != nil || on.State != state.AppProtectionOn || on.AppID != appID || on.AccountID != fx.Account.ID {
		t.Fatalf("SetAppProtection(on) = %+v, %v", on, err)
	}
	off, err := store.SetAppProtection(fx.Ctx, appID, fx.Account.ID, state.AppProtectionOff)
	if err != nil || off.State != state.AppProtectionOff || !off.CreatedAt.Equal(on.CreatedAt) {
		t.Fatalf("SetAppProtection(off) = %+v, %v; want same row, state off", off, err)
	}
	got, ok, err := store.GetAppProtection(fx.Ctx, appID)
	if err != nil || !ok || got.State != state.AppProtectionOff {
		t.Fatalf("GetAppProtection = %+v, %v, %v; want off", got, ok, err)
	}
	if _, err := store.SetAppProtection(fx.Ctx, appID, fx.Account.ID, "maybe"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("SetAppProtection(maybe) = %v; want ErrInvalidArgument", err)
	}
	if _, err := store.SetAppProtection(fx.Ctx, uuid.NewString(), fx.Account.ID, state.AppProtectionOn); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("SetAppProtection(unknown app) = %v; want ErrNotFound", err)
	}

	sloID := uuid.NewString()
	for _, obj := range []state.AppProtectionObject{
		{AppID: appID, Item: state.AppProtectionAvailabilityGate, ObjectID: "gate-1"},
		{AppID: appID, Item: state.AppProtectionAvailabilitySLO, Status: state.AppProtectionObjectSkipped, Reason: "plan"},
		{AppID: appID, Item: state.AppProtectionAvailabilitySLO, ObjectID: sloID, Written: json.RawMessage(`{"objective_pct":99.5}`)},
	} {
		if err := store.RecordAppProtectionObject(fx.Ctx, obj); err != nil {
			t.Fatalf("RecordAppProtectionObject(%s %s): %v", obj.Item, obj.Status, err)
		}
	}
	for _, again := range []state.AppProtectionObject{
		{AppID: appID, Item: state.AppProtectionAvailabilitySLO, ObjectID: uuid.NewString()},
		{AppID: appID, Item: state.AppProtectionAvailabilitySLO, Status: state.AppProtectionObjectSkipped, Reason: "plan"},
	} {
		if err := store.RecordAppProtectionObject(fx.Ctx, again); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("re-record created item (%s) = %v; want ErrConflict", again.Status, err)
		}
	}
	for _, bad := range []state.AppProtectionObject{
		{AppID: appID, Item: "edge_recipe", ObjectID: "x"},
		{AppID: appID, Item: state.AppProtectionHealthCheck, ObjectID: ""},
		{AppID: appID, Item: state.AppProtectionHealthCheck, ObjectID: "x", Written: json.RawMessage(`{`)},
		{AppID: appID, Item: state.AppProtectionHealthCheck, ObjectID: "x", Reason: "plan"},
		{AppID: appID, Item: state.AppProtectionHealthCheck, Status: state.AppProtectionObjectSkipped},
		{AppID: appID, Item: state.AppProtectionHealthCheck, Status: "maybe", ObjectID: "x"},
	} {
		if err := store.RecordAppProtectionObject(fx.Ctx, bad); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("RecordAppProtectionObject(%+v) = %v; want ErrInvalidArgument", bad, err)
		}
	}

	objs, err := store.ListAppProtectionObjects(fx.Ctx, appID)
	if err != nil || len(objs) != 2 {
		t.Fatalf("ListAppProtectionObjects = %+v, %v; want 2", objs, err)
	}
	if objs[0].Item != state.AppProtectionAvailabilitySLO || objs[0].Status != state.AppProtectionObjectCreated || objs[0].ObjectID != sloID || objs[0].Reason != "" {
		t.Fatalf("objs[0] = %+v; want the created availability SLO first", objs[0])
	}
	var written map[string]float64
	if err := json.Unmarshal(objs[0].Written, &written); err != nil || written["objective_pct"] != 99.5 {
		t.Fatalf("objs[0].Written = %s, %v", objs[0].Written, err)
	}
	if objs[1].Item != state.AppProtectionAvailabilityGate || objs[1].ObjectID != "gate-1" || string(objs[1].Written) != "{}" {
		t.Fatalf("objs[1] = %+v (written %s); want the gate with {}", objs[1], objs[1].Written)
	}
	if other, err := store.ListAppProtectionObjects(fx.Ctx, uuid.NewString()); err != nil || len(other) != 0 {
		t.Fatalf("ListAppProtectionObjects(other app) = %+v, %v; want none", other, err)
	}
}

// testAppProtectionReconcileClaims pins the reconciler schedule: only
// enrolled ('on') apps that are due are claimed; a claim leases the app
// until it is finished or the lease runs out; finishing schedules the next
// reconcile; re-enrolling makes it due at once.
func testAppProtectionReconcileClaims(t *testing.T, fx *Fixture) {
	store, ok := fx.Store.(state.AppProtectionStore)
	if !ok {
		t.Skip("store has no app protection")
	}
	appID := fx.App.ID
	claimMine := func(label string, want bool) {
		t.Helper()
		claimed, err := store.ClaimAppProtectionReconciles(fx.Ctx, 100, time.Minute)
		if err != nil {
			t.Fatalf("%s: ClaimAppProtectionReconciles: %v", label, err)
		}
		got := false
		for _, p := range claimed {
			if p.AppID == appID {
				got = true
				if p.State != state.AppProtectionOn || p.AccountID != fx.Account.ID {
					t.Fatalf("%s: claimed %+v", label, p)
				}
			}
		}
		if got != want {
			t.Fatalf("%s: claimed app = %v; want %v (%+v)", label, got, want, claimed)
		}
	}

	claimMine("never enrolled", false)
	if _, err := store.SetAppProtection(fx.Ctx, appID, fx.Account.ID, state.AppProtectionOff); err != nil {
		t.Fatal(err)
	}
	claimMine("off", false)
	if _, err := store.SetAppProtection(fx.Ctx, appID, fx.Account.ID, state.AppProtectionOn); err != nil {
		t.Fatal(err)
	}
	claimMine("on and due", true)
	claimMine("leased", false)
	if err := store.FinishAppProtectionReconcile(fx.Ctx, appID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	claimMine("finished, next in an hour", false)
	got, _, err := store.GetAppProtection(fx.Ctx, appID)
	if err != nil || got.NextReconcileAt.Before(time.Now().Add(50*time.Minute)) {
		t.Fatalf("NextReconcileAt = %v, %v; want about an hour out", got.NextReconcileAt, err)
	}
	if err := store.FinishAppProtectionReconcile(fx.Ctx, appID, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	claimMine("finished, due again", true)
	if _, err := store.SetAppProtection(fx.Ctx, appID, fx.Account.ID, state.AppProtectionOn); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishAppProtectionReconcile(fx.Ctx, uuid.NewString(), time.Now()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("FinishAppProtectionReconcile(unknown) = %v; want ErrNotFound", err)
	}
	if claimed, err := store.ClaimAppProtectionReconciles(fx.Ctx, 0, time.Minute); err != nil || len(claimed) != 0 {
		t.Fatalf("Claim(limit 0) = %+v, %v", claimed, err)
	}
}
