// machine_mode_test.go — pins the M-2 mode helpers in pkg/state/machine.go
// (issue #1186 / ADR-137 §Decision 1 + ADR-138 §Decision 4).
//
// These are the single source of truth for the meter-skip and
// RAM-admission predicates. The values are referenced by
// pkg/meter/sampler.go (commit 9 wires the inline mirror check to
// IsMeteredSkippableMode) and pkg/sched/admission.go (commit 6).

package state

import "testing"

// TestIsMeteredSkippableMode pins the meter-skip predicate.
// Pre-M-2: only mode='mirror' is skipped (ADR-125). Post-M-2
// (commit 4 + commit 9): the helper is the single source of truth;
// the new modes (worker / service / job) are NOT skipped because
// they bill at the standard mb_seconds rate (spec §4.7 unchanged).
func TestIsMeteredSkippableMode(t *testing.T) {
	tests := []struct {
		mode string
		want bool
	}{
		{"normal", false},
		{"mirror", true},
		{"worker", false},
		{"service", false},
		{"job", false},
		{"", false},      // empty defaults to non-skip
		{"bogus", false}, // unknown mode is not skipped (fail-open at the meter layer; CHECK is the load-bearing defence)
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			if got := IsMeteredSkippableMode(tt.mode); got != tt.want {
				t.Errorf("IsMeteredSkippableMode(%q)=%v, want %v", tt.mode, got, tt.want)
			}
		})
	}
}

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
			if got := CountsForRAMByMode(tt.mode); got != tt.want {
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
	want := map[InstanceMode]string{
		InstanceModeNormal:  "normal",
		InstanceModeMirror:  "mirror",
		InstanceModeWorker:  "worker",
		InstanceModeService: "service",
		InstanceModeJob:     "job",
	}
	got := map[InstanceMode]string{
		InstanceModeNormal:  string(InstanceModeNormal),
		InstanceModeMirror:  string(InstanceModeMirror),
		InstanceModeWorker:  string(InstanceModeWorker),
		InstanceModeService: string(InstanceModeService),
		InstanceModeJob:     string(InstanceModeJob),
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("InstanceMode %q: string value %q, want %q", k, got[k], v)
		}
	}
}
