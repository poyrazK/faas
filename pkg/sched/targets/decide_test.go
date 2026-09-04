package targets

import (
	"testing"
	"time"
)

// TestDecide_Table is the authoritative spec for the
// concurrent_requests scale-up decision function (PR-C, issue #462).
// Table-driven so adding a branch is a one-line PR. Mirrors
// pkg/sched/scaleup/decide_test.go's structure.
func TestDecide_Table(t *testing.T) {
	baseTime := time.Unix(1_700_000_000, 0)
	tests := []struct {
		name      string
		stats     Stats
		wantOut   Outcome
		wantAdmit bool
		wantHead  int
	}{
		{
			name: "no_target",
			stats: Stats{
				AppID:               "app1",
				TargetValue:         0,
				HaveInflight:        true,
				PerInstanceInflight: 5,
			},
			wantOut:   OutcomeNoSignal,
			wantAdmit: false,
		},
		{
			name: "cooldown_with_concurrency",
			stats: Stats{
				AppID:               "app1",
				TargetValue:         1.0,
				MaxConcurrency:      5,
				Concurrency:         2,
				HaveInflight:        true,
				PerInstanceInflight: 10,
				LastScaleOutAt:      baseTime.Add(-1 * time.Second),
				ScaleOutCooldownS:   60,
				Now:                 baseTime,
			},
			wantOut:   OutcomeCooldownHeld,
			wantAdmit: false,
		},
		{
			name: "cooldown_cold_start_bypass",
			// Concurrency == 0 with a freshly-stamped
			// LastScaleOutAt must NOT hit cooldown — the
			// load-bearing discriminator for the customer's
			// "scale on demand" use case.
			stats: Stats{
				AppID:               "app1",
				TargetValue:         1.0,
				MaxConcurrency:      5,
				Concurrency:         0,
				HaveInflight:        true,
				PerInstanceInflight: 10,
				LastScaleOutAt:      baseTime,
				ScaleOutCooldownS:   60,
				Now:                 baseTime,
			},
			wantOut:   OutcomeAdmit,
			wantAdmit: true,
			wantHead:  5,
		},
		{
			name: "inflight_hot_with_headroom",
			stats: Stats{
				AppID:               "app1",
				TargetValue:         1.0,
				MaxConcurrency:      5,
				Concurrency:         2,
				HaveInflight:        true,
				PerInstanceInflight: 5,
				LastScaleOutAt:      time.Time{}, // zero → no cooldown consult
				ScaleOutCooldownS:   60,
				Now:                 baseTime,
			},
			wantOut:   OutcomeAdmit,
			wantAdmit: true,
			wantHead:  3,
		},
		{
			name: "inflight_at_target_not_over",
			// Strict > matters: target=1, measured=1 → not hot.
			stats: Stats{
				AppID:               "app1",
				TargetValue:         1.0,
				MaxConcurrency:      5,
				Concurrency:         2,
				HaveInflight:        true,
				PerInstanceInflight: 1,
				LastScaleOutAt:      time.Time{},
				ScaleOutCooldownS:   60,
				Now:                 baseTime,
			},
			wantOut:   OutcomeNoSignal,
			wantAdmit: false,
		},
		{
			name: "inflight_cool",
			stats: Stats{
				AppID:               "app1",
				TargetValue:         5.0,
				MaxConcurrency:      5,
				Concurrency:         2,
				HaveInflight:        true,
				PerInstanceInflight: 1,
				LastScaleOutAt:      time.Time{},
				ScaleOutCooldownS:   60,
				Now:                 baseTime,
			},
			wantOut:   OutcomeNoSignal,
			wantAdmit: false,
		},
		{
			name: "inflight_no_reader",
			// No signal → NoSignal even when target is met.
			stats: Stats{
				AppID:               "app1",
				TargetValue:         1.0,
				MaxConcurrency:      5,
				Concurrency:         2,
				HaveInflight:        false,
				PerInstanceInflight: 0,
				LastScaleOutAt:      time.Time{},
				ScaleOutCooldownS:   60,
				Now:                 baseTime,
			},
			wantOut:   OutcomeNoSignal,
			wantAdmit: false,
		},
		{
			name: "inflight_hot_at_cap",
			// Hot but no headroom → RejectAtCap.
			stats: Stats{
				AppID:               "app1",
				TargetValue:         1.0,
				MaxConcurrency:      5,
				Concurrency:         5,
				HaveInflight:        true,
				PerInstanceInflight: 10,
				LastScaleOutAt:      time.Time{},
				ScaleOutCooldownS:   60,
				Now:                 baseTime,
			},
			wantOut:   OutcomeRejectAtCap,
			wantAdmit: false,
			wantHead:  0,
		},
		{
			name: "last_slot_headroom",
			// Concurrency = MaxConcurrency - 1 → headroom=1.
			stats: Stats{
				AppID:               "app1",
				TargetValue:         1.0,
				MaxConcurrency:      5,
				Concurrency:         4,
				HaveInflight:        true,
				PerInstanceInflight: 10,
				LastScaleOutAt:      time.Time{},
				ScaleOutCooldownS:   60,
				Now:                 baseTime,
			},
			wantOut:   OutcomeAdmit,
			wantAdmit: true,
			wantHead:  1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := decide(tc.stats)
			if got.Outcome != tc.wantOut {
				t.Errorf("Outcome = %v, want %v", got.Outcome, tc.wantOut)
			}
			if got.ShouldAdmit != tc.wantAdmit {
				t.Errorf("ShouldAdmit = %v, want %v", got.ShouldAdmit, tc.wantAdmit)
			}
			if got.Headroom != tc.wantHead {
				t.Errorf("Headroom = %d, want %d", got.Headroom, tc.wantHead)
			}
		})
	}
}

func TestDecide_DesiredCapacityUsesInflightAndCap(t *testing.T) {
	got := decide(Stats{
		TargetValue:         10,
		MaxConcurrency:      8,
		Concurrency:         2,
		PerInstanceInflight: 35,
		HaveInflight:        true,
	})
	// 2 instances × 35 inflight / 10 target = 7 desired instances.
	if got.Desired != 7 || got.Admissions != 5 {
		t.Fatalf("desired=%d admissions=%d, want desired=7 admissions=5", got.Desired, got.Admissions)
	}

	got = decide(Stats{
		TargetValue:         10,
		MaxConcurrency:      4,
		Concurrency:         2,
		PerInstanceInflight: 35,
		HaveInflight:        true,
	})
	if got.Desired != 4 || got.Admissions != 2 {
		t.Fatalf("capped desired=%d admissions=%d, want desired=4 admissions=2", got.Desired, got.Admissions)
	}
}
