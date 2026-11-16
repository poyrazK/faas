package conformance

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// testAppProtectionEnrollmentAndObjects pins ADR-20261116120000 slice 1: an
// app has no enrollment until one is set; setting it again only changes the
// state; objects need an enrollment, replace per item and list in item
// order; unknown apps, states and items are refused.
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

	gate := state.AppProtectionObject{AppID: appID, Item: state.AppProtectionAvailabilityGate, ObjectID: "availability"}
	firstSLO := uuid.NewString()
	secondSLO := uuid.NewString()
	for _, obj := range []state.AppProtectionObject{
		gate,
		{AppID: appID, Item: state.AppProtectionAvailabilitySLO, ObjectID: firstSLO, Written: json.RawMessage(`{"objective_pct":99.5}`)},
		{AppID: appID, Item: state.AppProtectionAvailabilitySLO, ObjectID: secondSLO, Written: json.RawMessage(`{"objective_pct":99.9}`)},
	} {
		if err := store.RecordAppProtectionObject(fx.Ctx, obj); err != nil {
			t.Fatalf("RecordAppProtectionObject(%s): %v", obj.Item, err)
		}
	}
	for _, bad := range []state.AppProtectionObject{
		{AppID: appID, Item: "edge_recipe", ObjectID: "x"},
		{AppID: appID, Item: state.AppProtectionHealthCheck, ObjectID: ""},
		{AppID: appID, Item: state.AppProtectionHealthCheck, ObjectID: "x", Written: json.RawMessage(`{`)},
	} {
		if err := store.RecordAppProtectionObject(fx.Ctx, bad); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("RecordAppProtectionObject(%+v) = %v; want ErrInvalidArgument", bad, err)
		}
	}

	objs, err := store.ListAppProtectionObjects(fx.Ctx, appID)
	if err != nil || len(objs) != 2 {
		t.Fatalf("ListAppProtectionObjects = %+v, %v; want 2", objs, err)
	}
	if objs[0].Item != state.AppProtectionAvailabilitySLO || objs[0].ObjectID != secondSLO {
		t.Fatalf("objs[0] = %+v; want the replaced availability SLO first", objs[0])
	}
	var written map[string]float64
	if err := json.Unmarshal(objs[0].Written, &written); err != nil || written["objective_pct"] != 99.9 {
		t.Fatalf("objs[0].Written = %s, %v", objs[0].Written, err)
	}
	if objs[1].Item != state.AppProtectionAvailabilityGate || objs[1].ObjectID != "availability" || string(objs[1].Written) != "{}" {
		t.Fatalf("objs[1] = %+v (written %s); want the gate with {}", objs[1], objs[1].Written)
	}
	if other, err := store.ListAppProtectionObjects(fx.Ctx, uuid.NewString()); err != nil || len(other) != 0 {
		t.Fatalf("ListAppProtectionObjects(other app) = %+v, %v; want none", other, err)
	}
}
