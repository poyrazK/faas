package meter

// adr: 060

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// floorLedgerStore adds the floor residency ledger to ledgerStore: the app
// was last resident at lastResident (zero: never).
type floorLedgerStore struct {
	*ledgerStore
	lastResident time.Time
}

func (s *floorLedgerStore) AppResidentSince(_ context.Context, _ string, since, before time.Time) (bool, error) {
	return !s.lastResident.IsZero() && s.lastResident.After(since) && s.lastResident.Before(before), nil
}

// A min-instances floor the platform has not held for longer than
// api.MinInstancesFloorUnheldGrace (node outage, hostless boot failures) is
// not billed; one held a moment ago still fills its gap (account usage audit
// 2026-11-21: voting-redis billed its whole floor through a ~24 h outage).
func TestSampler_FloorUnheldByPlatformIsNotBilled(t *testing.T) {
	for _, tc := range []struct {
		name         string
		lastResident time.Duration // before the rolled minute; 0 = never
		young        bool          // the deployment is created in the rolled minute
		wantFloor    bool
	}{
		{name: "never held since an old deployment", wantFloor: false},
		{name: "held an hour ago", lastResident: time.Hour, wantFloor: false},
		{name: "held two minutes ago", lastResident: 2 * time.Minute, wantFloor: true},
		{name: "brand-new deployment bills from t=0", young: true, wantFloor: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			// The deployment is created at the real clock; unless young, the
			// rolled minute is a day later, so it is older than the grace.
			base := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Minute)
			if tc.young {
				base = time.Now().UTC().Truncate(time.Minute)
			}
			store := &floorLedgerStore{ledgerStore: newLedgerStore()}
			if tc.lastResident > 0 {
				store.lastResident = base.Add(-tc.lastResident)
			}
			acct, err := store.CreateAccount(ctx, "floor-unheld@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "floor-unheld", RAMMB: 256, Type: state.AppTypeApp, MinInstances: 1})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployLive, Kind: state.DeploymentKindImage}); err != nil {
				t.Fatal(err)
			}
			recordComplete(t, store, base.Add(-api.MeterCatchUpWindow), base)
			now := base.Add(time.Minute + api.MeterRollSettle)
			sampler := NewSampler(store, nil, func() time.Time { return now })
			if _, err := tick(t, store.ledgerStore, sampler, now); err != nil {
				t.Fatalf("tick: %v", err)
			}
			got, _ := store.appended(base)
			want := int64(0)
			if tc.wantFloor {
				want = int64(api.BillableRAMMB(256)) * 60
			}
			if got != want {
				t.Fatalf("minute billed %d MB-s, want %d", got, want)
			}
		})
	}
}
