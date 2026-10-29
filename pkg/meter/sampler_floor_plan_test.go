package meter

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A stored floor outlives a downgrade. schedd then holds none on a plan
// without floors and no more than the plan allows on the others, so billing
// the stored floor charged for capacity never provided; on Free it ran the
// quota out and suspended the account.
func TestSampler_FloorBillsOnlyWhatThePlanHolds(t *testing.T) {
	at := func() time.Time { return time.Date(2026, 10, 8, 8, 5, 0, 0, time.UTC) }
	for _, tc := range []struct {
		name  string
		plan  api.Plan
		floor int
		want  int
	}{
		{name: "free holds none", plan: api.PlanFree, floor: 2, want: 0},
		{name: "hobby caps at its max", plan: api.PlanHobby, floor: 2, want: api.MustLimitsFor(api.PlanHobby).MaxMinInstances},
		{name: "pro caps at its max", plan: api.PlanPro, floor: 10, want: api.MustLimitsFor(api.PlanPro).MaxMinInstances},
		{name: "scale within its max", plan: api.PlanScale, floor: 2, want: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			appID, _ := seedFloorApp(t, store, api.PlanScale, 512)
			setPolicy(t, store, appID, state.ScalingPolicy{MinInstances: tc.floor})
			app, err := store.AppByID(ctx, appID)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateAccountPlan(ctx, app.AccountID, tc.plan); err != nil {
				t.Fatalf("downgrade: %v", err)
			}
			rows, err := NewSampler(store, nil, at).SampleAndRoll(ctx)
			if err != nil {
				t.Fatalf("SampleAndRoll: %v", err)
			}
			if got := len(syntheticFloorRows(rows, appID)); got != tc.want {
				t.Fatalf("%d synthetic floor rows, want %d", got, tc.want)
			}
		})
	}
}
