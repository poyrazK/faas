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

// testProtectionHealthProbeClaims pins ADR-20261116120000 §3: the baseline
// health check leaves meterd's runnable list and is claimed by apid instead,
// once per interval across replicas; a paused check or protection off is
// not claimed; a customer check of the same app stays with meterd.
func testProtectionHealthProbeClaims(t *testing.T, fx *Fixture) {
	store, ok := fx.Store.(state.AppProtectionStore)
	checks, ok2 := fx.Store.(state.SyntheticCheckStore)
	runs, ok3 := fx.Store.(state.SyntheticRunStore)
	if !ok || !ok2 || !ok3 {
		t.Skip("store has no app protection or synthetic checks")
	}
	appID := fx.App.ID
	baseline, err := checks.CreateSyntheticCheck(fx.Ctx, state.SyntheticCheck{AccountID: fx.Account.ID, AppID: appID, Name: "health",
		Method: "GET", Path: "/", TimeoutMS: 10000, IntervalSeconds: 3600}, 5)
	if err != nil {
		t.Fatalf("CreateSyntheticCheck: %v", err)
	}
	own, err := checks.CreateSyntheticCheck(fx.Ctx, state.SyntheticCheck{AccountID: fx.Account.ID, AppID: appID, Name: "mine",
		Method: "GET", Path: "/api", TimeoutMS: 10000, IntervalSeconds: 300}, 5)
	if err != nil {
		t.Fatalf("CreateSyntheticCheck: %v", err)
	}
	runnable := func() map[string]bool {
		t.Helper()
		list, err := runs.ListRunnableSyntheticChecks(fx.Ctx)
		if err != nil {
			t.Fatalf("ListRunnableSyntheticChecks: %v", err)
		}
		out := map[string]bool{}
		for _, c := range list {
			out[c.ID] = true
		}
		return out
	}
	claim := func(label string, want bool) {
		t.Helper()
		got, err := store.ClaimProtectionHealthProbes(fx.Ctx, 100)
		if err != nil {
			t.Fatalf("%s: ClaimProtectionHealthProbes: %v", label, err)
		}
		found := false
		for _, p := range got {
			if p.AppID == appID {
				found = true
				if p.CheckID != baseline.ID || p.AccountID != fx.Account.ID {
					t.Fatalf("%s: claimed %+v; want the baseline check", label, p)
				}
			}
		}
		if found != want {
			t.Fatalf("%s: claimed = %v; want %v (%+v)", label, found, want, got)
		}
	}

	if r := runnable(); !r[baseline.ID] || !r[own.ID] {
		t.Fatalf("before enrollment, runnable = %v; want both checks", r)
	}
	claim("not enrolled", false)
	if _, err := store.SetAppProtection(fx.Ctx, appID, fx.Account.ID, state.AppProtectionOn); err != nil {
		t.Fatal(err)
	}
	claim("enrolled, no health record", false)
	if err := store.RecordAppProtectionObject(fx.Ctx, state.AppProtectionObject{AppID: appID, Item: state.AppProtectionHealthCheck, ObjectID: baseline.ID}); err != nil {
		t.Fatal(err)
	}
	if r := runnable(); r[baseline.ID] || !r[own.ID] {
		t.Fatalf("after recording, runnable = %v; want only the customer's check", r)
	}
	claim("due", true)
	claim("claimed within the interval", false)

	if _, err := store.SetAppProtection(fx.Ctx, appID, fx.Account.ID, state.AppProtectionOff); err != nil {
		t.Fatal(err)
	}
	claim("protection off", false)
	if claimed, err := store.ClaimProtectionHealthProbes(fx.Ctx, 0); err != nil || len(claimed) != 0 {
		t.Fatalf("Claim(limit 0) = %+v, %v", claimed, err)
	}
}

// testPlatformProbeWakeLedger pins ADR-20261116120000 §3: a probe wake is
// recorded once; recent wakes report each instance's latest probe end;
// unbilled seconds are each instance's overlap of [woke_at, done+tail] with
// the window, overlapping wakes counted once; old wakes are deleted.
func testPlatformProbeWakeLedger(t *testing.T, fx *Fixture) {
	store, ok := fx.Store.(state.PlatformProbeWakeStore)
	if !ok {
		t.Skip("store has no platform probe wakes")
	}
	base := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	wake := func(instance string, woke, done time.Duration) state.PlatformProbeWake {
		return state.PlatformProbeWake{InstanceID: instance, AppID: fx.App.ID, WokeAt: base.Add(woke), ProbeDoneAt: base.Add(done)}
	}
	for _, w := range []state.PlatformProbeWake{
		wake("i-1", 10*time.Second, 14*time.Second),
		wake("i-1", 10*time.Second, 14*time.Second), // repeat ignored
		wake("i-1", 20*time.Second, 22*time.Second), // overlaps the first's tail
		wake("i-2", 50*time.Second, 55*time.Second), // runs into the next minute
	} {
		if err := store.RecordPlatformProbeWake(fx.Ctx, w); err != nil {
			t.Fatalf("RecordPlatformProbeWake(%+v): %v", w, err)
		}
	}
	for _, bad := range []state.PlatformProbeWake{
		{AppID: fx.App.ID, WokeAt: base, ProbeDoneAt: base},
		wake("i-3", 10*time.Second, 5*time.Second),
	} {
		if err := store.RecordPlatformProbeWake(fx.Ctx, bad); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("RecordPlatformProbeWake(%+v) = %v; want ErrInvalidArgument", bad, err)
		}
	}
	if err := store.RecordPlatformProbeWake(fx.Ctx, state.PlatformProbeWake{InstanceID: "i-4", AppID: uuid.NewString(),
		WokeAt: base, ProbeDoneAt: base}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("RecordPlatformProbeWake(unknown app) = %v; want ErrNotFound", err)
	}

	recent, err := store.RecentPlatformProbeWakes(fx.Ctx, base)
	if err != nil || !recent["i-1"].Equal(base.Add(22*time.Second)) || !recent["i-2"].Equal(base.Add(55*time.Second)) {
		t.Fatalf("RecentPlatformProbeWakes = %v, %v", recent, err)
	}
	if later, _ := store.RecentPlatformProbeWakes(fx.Ctx, base.Add(30*time.Second)); len(later) != 1 {
		t.Fatalf("RecentPlatformProbeWakes(after i-1) = %v; want only i-2", later)
	}

	tail := 10 * time.Second
	first, err := store.PlatformProbeUnbilledSeconds(fx.Ctx, base, base.Add(time.Minute), tail)
	// i-1: [10s, 24s] ∪ [20s, 32s] = [10s, 32s] → 22 s; i-2: [50s, 60s) → 10 s.
	if err != nil || first["i-1"] != 22 || first["i-2"] != 10 {
		t.Fatalf("unbilled seconds, first minute = %v, %v; want i-1 22, i-2 10", first, err)
	}
	second, err := store.PlatformProbeUnbilledSeconds(fx.Ctx, base.Add(time.Minute), base.Add(2*time.Minute), tail)
	// i-2's window ends at 65 s → 5 s of the second minute.
	if err != nil || second["i-2"] != 5 || second["i-1"] != 0 {
		t.Fatalf("unbilled seconds, second minute = %v, %v; want i-2 5", second, err)
	}

	n, err := store.DeletePlatformProbeWakesBefore(fx.Ctx, base.Add(30*time.Second), 100)
	if err != nil || n != 2 {
		t.Fatalf("DeletePlatformProbeWakesBefore = %d, %v; want the two i-1 wakes", n, err)
	}
	if left, _ := store.RecentPlatformProbeWakes(fx.Ctx, base); len(left) != 1 || left["i-2"].IsZero() {
		t.Fatalf("after delete = %v; want only i-2", left)
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
