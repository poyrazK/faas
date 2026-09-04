package scaleup

import (
	"testing"
)

// TestDecide_TableDriven pins every branch of the pure decide() function.
// Each case is a (stats, want) pair; the test asserts the decision
// outcome + the ShouldAdmit / Headroom flags. The table is the
// authoritative spec for the trigger; if a future change moves a
// branch, update the table here AND the comment on decide() to match.
func TestDecide_TableDriven(t *testing.T) {
	cases := []struct {
		name        string
		stats       AppStats
		wantOutcome Outcome
		wantAdmit   bool
		wantHead    int
	}{
		{
			// Both targets unset → no_signal (the consumer filters
			// this path, but the function is defensive).
			name:        "no_targets",
			stats:       AppStats{},
			wantOutcome: OutcomeNoSignal,
		},
		{
			// RPS target set + measured > target + headroom → admit.
			name: "rps_hot_with_headroom",
			stats: AppStats{
				TargetRPS: 50, MaxConcurrency: 5, Concurrency: 2,
				PerInstanceRPS: 70, HaveRPS: true,
			},
			wantOutcome: OutcomeAdmit,
			wantAdmit:   true,
			wantHead:    3,
		},
		{
			// RPS target met but at cap → reject_at_cap, no admit.
			name: "rps_hot_at_cap",
			stats: AppStats{
				TargetRPS: 50, MaxConcurrency: 5, Concurrency: 5,
				PerInstanceRPS: 70, HaveRPS: true,
			},
			wantOutcome: OutcomeRejectAtCap,
			wantHead:    0,
		},
		{
			// RPS target set + measured == target → no_signal (strict
			// > comparison: the instance is exactly at the threshold,
			// next request can ride on it).
			name: "rps_at_target_not_over",
			stats: AppStats{
				TargetRPS: 50, MaxConcurrency: 5, Concurrency: 2,
				PerInstanceRPS: 50, HaveRPS: true,
			},
			wantOutcome: OutcomeNoSignal,
		},
		{
			// RPS target set + measured below → no_signal.
			name: "rps_cool",
			stats: AppStats{
				TargetRPS: 50, MaxConcurrency: 5, Concurrency: 2,
				PerInstanceRPS: 30, HaveRPS: true,
			},
			wantOutcome: OutcomeNoSignal,
		},
		{
			// Cold path: RPS target set + zero instances → no_signal
			// (no per-instance signal yet). The first instant wake
			// that lands an instance will be picked up by the next
			// tick.
			name: "rps_cold_path",
			stats: AppStats{
				TargetRPS: 50, MaxConcurrency: 5, Concurrency: 0,
				PerInstanceRPS: 0, HaveRPS: false,
			},
			wantOutcome: OutcomeNoSignal,
		},
		{
			// CPU target set + measured > target + headroom → admit.
			name: "cpu_hot_with_headroom",
			stats: AppStats{
				TargetCPU: 70, MaxConcurrency: 5, Concurrency: 2,
				PerInstanceCPU: 80, HaveCPU: true,
			},
			wantOutcome: OutcomeAdmit,
			wantAdmit:   true,
			wantHead:    3,
		},
		{
			// CPU target set + measured > target + NIL reader (no
			// CPU sample) → no_signal (RPS path is the only one
			// that can fire). Confirms the nil-safety contract.
			name: "cpu_target_no_reader",
			stats: AppStats{
				TargetCPU: 70, MaxConcurrency: 5, Concurrency: 2,
				PerInstanceCPU: 0, HaveCPU: false,
			},
			wantOutcome: OutcomeNoSignal,
		},
		{
			// Both targets set + RPS wins → admit. CPU is below
			// threshold so its branch is irrelevant on this tick.
			name: "both_targets_rps_wins",
			stats: AppStats{
				TargetRPS: 50, TargetCPU: 70, MaxConcurrency: 5, Concurrency: 2,
				PerInstanceRPS: 60, HaveRPS: true,
				PerInstanceCPU: 50, HaveCPU: true,
			},
			wantOutcome: OutcomeAdmit,
			wantAdmit:   true,
			wantHead:    3,
		},
		{
			// Both targets set + CPU wins → admit. RPS is below
			// threshold so its branch is irrelevant.
			name: "both_targets_cpu_wins",
			stats: AppStats{
				TargetRPS: 50, TargetCPU: 70, MaxConcurrency: 5, Concurrency: 2,
				PerInstanceRPS: 30, HaveRPS: true,
				PerInstanceCPU: 80, HaveCPU: true,
			},
			wantOutcome: OutcomeAdmit,
			wantAdmit:   true,
			wantHead:    3,
		},
		{
			// Both targets set + both below → no_signal.
			name: "both_targets_neither_hot",
			stats: AppStats{
				TargetRPS: 50, TargetCPU: 70, MaxConcurrency: 5, Concurrency: 2,
				PerInstanceRPS: 30, HaveRPS: true,
				PerInstanceCPU: 50, HaveCPU: true,
			},
			wantOutcome: OutcomeNoSignal,
		},
		{
			// Headroom = 1 (last slot) → admit (the trigger
			// admits when headroom > 0, not when headroom > 1).
			name: "last_slot_headroom",
			stats: AppStats{
				TargetRPS: 50, MaxConcurrency: 5, Concurrency: 4,
				PerInstanceRPS: 70, HaveRPS: true,
			},
			wantOutcome: OutcomeAdmit,
			wantAdmit:   true,
			wantHead:    1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dec := decide(tc.stats)
			if dec.Outcome != tc.wantOutcome {
				t.Errorf("outcome = %q, want %q", dec.Outcome, tc.wantOutcome)
			}
			if dec.ShouldAdmit != tc.wantAdmit {
				t.Errorf("ShouldAdmit = %v, want %v", dec.ShouldAdmit, tc.wantAdmit)
			}
			if dec.Headroom != tc.wantHead {
				t.Errorf("Headroom = %d, want %d", dec.Headroom, tc.wantHead)
			}
		})
	}
}

func TestDecide_DesiredCapacityUsesTotalRPSAndCap(t *testing.T) {
	got := decide(AppStats{
		TargetRPS:      50,
		MaxConcurrency: 8,
		Concurrency:    2,
		PerInstanceRPS: 175,
		HaveRPS:        true,
	})
	// 2 instances × 175 RPS / 50 target = 7 desired instances.
	if got.Desired != 7 || got.Admissions != 5 {
		t.Fatalf("desired=%d admissions=%d, want desired=7 admissions=5", got.Desired, got.Admissions)
	}

	got = decide(AppStats{
		TargetRPS:      50,
		MaxConcurrency: 4,
		Concurrency:    2,
		PerInstanceRPS: 175,
		HaveRPS:        true,
	})
	if got.Desired != 4 || got.Admissions != 2 {
		t.Fatalf("capped desired=%d admissions=%d, want desired=4 admissions=2", got.Desired, got.Admissions)
	}
}
