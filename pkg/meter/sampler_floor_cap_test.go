// adr: 20261103131742
package meter

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// floorSidecarStore gives deployments sidecar RAM.
type floorSidecarStore struct {
	MemStore *state.MemStore
	sidecars map[string][]int
}

func (s *floorSidecarStore) DeploymentSidecarRAMs(_ context.Context, deploymentID string) ([]int, error) {
	return s.sidecars[deploymentID], nil
}

// A floor instance runs the deployment's sidecars, so its synthetic row is
// priced like a live instance of the deployment: plan RAM + 8 MB plus each
// sidecar's RAM.
func TestSampler_MinInstancesFloorPricesSidecars(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	appID, depID := seedFloorApp(t, mem, api.PlanScale, 1024)
	setPolicy(t, mem, appID, state.ScalingPolicy{MinInstances: 1})
	fake := &floorSidecarStore{MemStore: mem, sidecars: map[string][]int{depID: {128, 64}}}
	minute := time.Date(2026, 10, 8, 8, 5, 0, 0, time.UTC)
	rows, err := NewSampler(statetest.NewMem(fake), nil, func() time.Time { return minute }).SampleAndRoll(ctx)
	if err != nil {
		t.Fatalf("SampleAndRoll: %v", err)
	}
	floor := syntheticFloorRows(rows, appID)
	want := api.BillableRAMMBWithSidecars(1024, []int{128, 64})
	if want <= api.BillableRAMMB(1024) {
		t.Fatalf("billable RAM with sidecars %d is not above %d without", want, api.BillableRAMMB(1024))
	}
	if len(floor) != 1 || floor[0].AdmissionMB != want || floor[0].MBSeconds != int64(want)*60 {
		t.Fatalf("floor rows = %+v, want one row at %d MB", floor, want)
	}
}

// At the spend cap schedd parks the subject's floors, so the sampler records
// no synthetic floor usage for capacity schedd does not hold: a cap of 0 is
// reached at once, and meterd's hold counts while it is for the current cap.
func TestSampler_MinInstancesFloorStopsAtSpendCap(t *testing.T) {
	minute := time.Date(2026, 10, 8, 8, 5, 0, 0, time.UTC)
	month := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name      string
		cap       *int64
		hold      *state.SpendCapHold
		wantFloor bool
	}{
		{name: "no cap", wantFloor: true},
		{name: "cap not reached", cap: ptrInt64(10_000), wantFloor: true},
		{name: "cap of zero", cap: ptrInt64(0)},
		{name: "meterd hold for the cap", cap: ptrInt64(500), hold: &state.SpendCapHold{Month: month, CapCents: 500, ObservedCents: 600}},
		{name: "hold for an older cap", cap: ptrInt64(10_000), hold: &state.SpendCapHold{Month: month, CapCents: 500, ObservedCents: 600}, wantFloor: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			store.SetClockForTest(func() time.Time { return minute })
			appID, _ := seedFloorApp(t, store, api.PlanScale, 1024)
			setPolicy(t, store, appID, state.ScalingPolicy{MinInstances: 2})
			app, err := store.AppByID(ctx, appID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.cap != nil {
				store.SetOverageCapCentsForTest(app.AccountID, *tc.cap)
			}
			if tc.hold != nil {
				hold := *tc.hold
				hold.AccountID = app.AccountID
				if err := store.PutSpendCapHold(ctx, hold); err != nil {
					t.Fatal(err)
				}
			}
			rows, err := NewSampler(store, nil, func() time.Time { return minute }).SampleAndRoll(ctx)
			if err != nil {
				t.Fatalf("SampleAndRoll: %v", err)
			}
			want := 0
			if tc.wantFloor {
				want = 2
			}
			if got := len(syntheticFloorRows(rows, appID)); got != want {
				t.Fatalf("%d synthetic floor rows, want %d", got, want)
			}
		})
	}
}

func ptrInt64(v int64) *int64 { return &v }
