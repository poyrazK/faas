// adr: 195 — scheduled scaling floors.
package meter

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestSampler_BillsScheduledFloor is the test this ADR exists to make
// possible.
//
// EffectiveMinInstances' own doc comment records what happens when the
// scheduler and the billing sampler disagree about the warm floor:
//
//	a customer who configured the floor via the legacy PATCH got a warm
//	floor they were never billed for (revenue-affecting).
//
// A scheduled floor is the same hazard with a clock attached. The sampler
// must apply the floor while the window is open — and only while it is open,
// so a closed window is not billed either.
//
// Both halves are asserted against one app and one policy; only the
// sampler's injected clock differs between the two runs. That is what makes
// it a test of the schedule rather than of the floor machinery.
func TestSampler_BillsScheduledFloor(t *testing.T) {
	const ramMB = 256
	// Hobby billable = 256 + 8 = 264 MB; one instance-minute = 264 x 60.
	const perInstance = 264 * 60

	// 08:00 Istanbul (UTC+3) on weekdays, warm for 12h. 2026-08-03 is a
	// Monday, so the window runs 05:00Z to 17:00Z.
	policy := state.ScalingPolicy{
		Timezone: "Europe/Istanbul",
		Schedules: []state.ScalingSchedule{{
			Cron: "0 8 * * 1-5", DurationS: 12 * 3600, MinInstances: 1,
		}},
		// Static floor stays 0: the app parks outside the window, so
		// anything billed here came from the schedule.
	}

	cases := []struct {
		name     string
		when     time.Time
		wantRows int
		wantMB   int64
	}{
		{
			name:     "inside the window the floor is billed",
			when:     time.Date(2026, 8, 3, 11, 0, 0, 0, time.UTC), // 14:00 Istanbul
			wantRows: 1,
			wantMB:   perInstance,
		},
		{
			name:     "outside the window nothing is billed",
			when:     time.Date(2026, 8, 3, 22, 0, 0, 0, time.UTC), // 01:00 Tuesday Istanbul
			wantRows: 0,
			wantMB:   0,
		},
		{
			name:     "weekend is outside every window",
			when:     time.Date(2026, 8, 8, 11, 0, 0, 0, time.UTC), // Saturday
			wantRows: 0,
			wantMB:   0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			appID, _ := seedFloorApp(t, store, api.PlanHobby, ramMB)
			setPolicy(t, store, appID, policy)

			// No live instances: the app is parked, so every billed
			// MB-second is the floor's.
			s := NewSampler(store, nil, func() time.Time { return tc.when })
			rows, err := s.SampleAndRoll(context.Background())
			if err != nil {
				t.Fatalf("SampleAndRoll: %v", err)
			}

			var synth []RolledRow
			for _, r := range rows {
				if r.AppID == appID && r.SyntheticFloor {
					synth = append(synth, r)
				}
			}
			if len(synth) != tc.wantRows {
				t.Fatalf("synthetic floor rows = %d, want %d — the sampler and the "+
					"scheduler must agree on the floor at %s", len(synth), tc.wantRows, tc.when)
			}
			var total int64
			for _, r := range synth {
				total += r.MBSeconds
			}
			if total != tc.wantMB {
				t.Errorf("floor mb_seconds = %d, want %d", total, tc.wantMB)
			}
		})
	}
}

// TestSampler_ScheduledFloorUsesInjectedClock pins that the sampler bills
// against its OWN clock rather than the wall clock.
//
// The distinction is invisible in production (they are the same clock) and
// load-bearing everywhere else: a replayed or back-dated sample must compute
// the floor that applied at the instant it is billing for. If the sampler
// called the wall-clock helper, this test's result would depend on when the
// suite happens to run — which is exactly the bug, and also why the
// assertion below is written as "the two clocks disagree".
func TestSampler_ScheduledFloorUsesInjectedClock(t *testing.T) {
	store := state.NewMemStore()
	appID, _ := seedFloorApp(t, store, api.PlanHobby, 256)
	// A window that is open for one hour a day, on a fixed date in the
	// past. Wall-clock "now" is essentially never inside it.
	setPolicy(t, store, appID, state.ScalingPolicy{
		Timezone: "UTC",
		Schedules: []state.ScalingSchedule{{
			Cron: "0 12 3 8 *", DurationS: 3600, MinInstances: 1,
		}},
	})
	inside := time.Date(2026, 8, 3, 12, 30, 0, 0, time.UTC)

	s := NewSampler(store, nil, func() time.Time { return inside })
	rows, err := s.SampleAndRoll(context.Background())
	if err != nil {
		t.Fatalf("SampleAndRoll: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.AppID == appID && r.SyntheticFloor {
			found = true
		}
	}
	if !found {
		t.Fatal("no synthetic floor row: the sampler evaluated the schedule against the " +
			"wall clock instead of its injected clock, so a back-dated sample bills the wrong floor")
	}
}
