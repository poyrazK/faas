// machine_mode_test.go — pins the M-2 mode helpers in pkg/state/machine.go
// (issue #1186 / ADR-137 §Decision 1 + ADR-138 §Decision 4).
//
// These are the single source of truth for the RAM-admission
// predicate referenced by pkg/sched/admission.go (commit 6). Every
// mode bills at the meter (ADR-20261104145353).

package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

// TestCountsForRAMByMode pins the RAM-admission predicate.
// Every mode (including mirror) counts against the tenant budget
// while RUNNING; the meter sampler is a separate layer that skips
// billing for mode='mirror'. Unknown modes fail-closed (count=true)
// so an unexpected value cannot silently consume more than the
// budget allows.
func TestCountsForRAMByMode(t *testing.T) {
	tests := []struct {
		mode string
		want bool
	}{
		{"normal", true},
		{"mirror", true},
		{"worker", true},
		{"service", true},
		{"job", true},
		{"", true},      // empty defaults to counting (fail-closed at the admission layer)
		{"bogus", true}, // unknown mode also counts (the CHECK is the load-bearing defence; this is belt-and-braces)
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			if got := state.CountsForRAMByMode(tt.mode); got != tt.want {
				t.Errorf("CountsForRAMByMode(%q)=%v, want %v", tt.mode, got, tt.want)
			}
		})
	}
}

// TestInstanceModeConstants covers the closed-set enum widening
// (issue #1186 §D / ADR-137 §Decision 1). Pre-M-2 was {normal, mirror};
// M-2 commit 4 widens to {normal, mirror, worker, service, job}.
// These constants are the Go-side mirror of the SQL CHECK widened
// in migrations/00532.
func TestInstanceModeConstants(t *testing.T) {
	want := map[state.InstanceMode]string{
		state.InstanceModeNormal:  "normal",
		state.InstanceModeMirror:  "mirror",
		state.InstanceModeWorker:  "worker",
		state.InstanceModeService: "service",
		state.InstanceModeJob:     "job",
	}
	got := map[state.InstanceMode]string{
		state.InstanceModeNormal:  string(state.InstanceModeNormal),
		state.InstanceModeMirror:  string(state.InstanceModeMirror),
		state.InstanceModeWorker:  string(state.InstanceModeWorker),
		state.InstanceModeService: string(state.InstanceModeService),
		state.InstanceModeJob:     string(state.InstanceModeJob),
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("InstanceMode %q: string value %q, want %q", k, got[k], v)
		}
	}
}
