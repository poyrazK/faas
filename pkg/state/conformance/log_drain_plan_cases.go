package conformance

import (
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// deliveredAppLogDrains is the set of drain IDs both delivery reads return:
// ListEnabledAppLogDrains and, where the store has one, the gateway's
// consumer snapshot. It fails when the two disagree.
func deliveredAppLogDrains(t *testing.T, fx *Fixture) map[string]bool {
	t.Helper()
	listed, err := fx.Store.ListEnabledAppLogDrains(fx.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, d := range listed {
		out[d.ID] = true
	}
	inventory, ok := fx.Store.(state.ApplicationStandardLogInventoryStore)
	if !ok {
		return out
	}
	snapshot, err := inventory.LoadApplicationStandardLogConsumerSnapshot(fx.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	fromSnapshot := map[string]bool{}
	for _, d := range snapshot.Drains {
		fromSnapshot[d.ID] = true
	}
	if len(fromSnapshot) != len(out) {
		t.Fatalf("consumer snapshot delivers %d drains, ListEnabledAppLogDrains %d", len(fromSnapshot), len(out))
	}
	for id := range out {
		if !fromSnapshot[id] {
			t.Fatalf("drain %s is in ListEnabledAppLogDrains but not in the consumer snapshot", id)
		}
	}
	return out
}

// testLogDrainsDeliverOnlyWhatThePlanCovers pins logs audit 2026-10-31 D3:
// drain allowances are checked only when a drain is created, so after a
// downgrade delivery itself keeps an owner to its plan. It runs the oldest
// enabled drains up to the per-app and account-wide allowances, none on Free,
// and every one again after an upgrade; a disabled drain spends no slot.
func testLogDrainsDeliverOnlyWhatThePlanCovers(t *testing.T, fx *Fixture) {
	t.Helper()
	hobby := api.MustLimitsFor(api.PlanHobby)
	if hobby.LogDrainPerApp != 3 || hobby.LogDrainPerAccount != 10 {
		t.Fatalf("this case is written for Hobby's 3 per app and 10 per account, got %d and %d", hobby.LogDrainPerApp, hobby.LogDrainPerAccount)
	}
	apps := []state.App{fx.App}
	for range 3 {
		app, err := fx.Store.CreateApp(fx.Ctx, state.App{AccountID: fx.Account.ID, Slug: "drain-plan-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128})
		if err != nil {
			t.Fatal(err)
		}
		apps = append(apps, app)
	}
	create := func(app state.App, enabled bool) string {
		t.Helper()
		d, err := fx.Store.CreateAppLogDrain(fx.Ctx, state.AppLogDrain{AppID: app.ID, AccountID: fx.Account.ID, Kind: state.AppLogDrainKindHTTPJSON,
			TargetURL: "https://logs.example/" + uuid.NewString(), Enabled: enabled})
		if err != nil {
			t.Fatal(err)
		}
		return d.ID
	}
	// 4 enabled + 1 disabled on the first app, then 3, 3 and 2 on the others:
	// 12 enabled drains, created oldest first in this order.
	var first, rest []string
	for range 4 {
		first = append(first, create(apps[0], true))
	}
	disabled := create(apps[0], false)
	for i, n := range []int{3, 3, 2} {
		for range n {
			rest = append(rest, create(apps[i+1], true))
		}
	}
	expect := func(step string, delivered, pausedFirst, pausedLast []string) {
		t.Helper()
		got := deliveredAppLogDrains(t, fx)
		all := append(append(slices.Clone(first), rest...), disabled)
		for _, id := range all {
			if got[id] != slices.Contains(delivered, id) {
				t.Fatalf("%s: drain %s delivered = %v, want %v", step, id, got[id], !got[id])
			}
		}
		for app, want := range map[string][]string{apps[0].ID: pausedFirst, apps[3].ID: pausedLast} {
			paused, err := fx.Store.ListAppLogDrainsPausedByPlan(fx.Ctx, app)
			if err != nil {
				t.Fatal(err)
			}
			slices.Sort(paused)
			want = slices.Sorted(slices.Values(want))
			if !slices.Equal(paused, want) {
				t.Fatalf("%s: paused drains of app %s = %v, want %v", step, app, paused, want)
			}
		}
	}

	if err := fx.Store.UpdateAccountPlan(fx.Ctx, fx.Account.ID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	// The first app's 4th drain is past 3 per app; the last app's 2nd drain
	// is the 11th the account would run, past 10 per account.
	expect("hobby", append(slices.Clone(first[:3]), rest[:7]...), first[3:], rest[7:])

	// Disabling a covered drain frees its slot for the next one.
	off := false
	if _, err := fx.Store.UpdateAppLogDrain(fx.Ctx, first[0], state.UpdateAppLogDrainParams{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	expect("hobby, one disabled", append(slices.Clone(first[1:]), rest[:7]...), nil, rest[7:])

	if err := fx.Store.UpdateAccountPlan(fx.Ctx, fx.Account.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	expect("free", nil, first[1:], rest[6:])

	if err := fx.Store.UpdateAccountPlan(fx.Ctx, fx.Account.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	expect("pro", append(slices.Clone(first[1:]), rest...), nil, nil)
}
