package daemonunitspec

import "testing"

// TestUnitVmmd_GOGC pins vmmd's measured GC setting (2026-11-21 profile:
// GC and allocation were 5-8% of its request-path CPU at Go's default).
// GOMEMLIMIT stays unset on purpose: see TestUnitVmmd_NoMemoryHigh for why
// vmmd's cgroup cap is not a bound on its heap.
func TestUnitVmmd_GOGC(t *testing.T) {
	var gogc string
	for _, kv := range UnitVmmd().Environment {
		switch kv.Key {
		case "GOGC":
			gogc = kv.Value
		case "GOMEMLIMIT":
			t.Errorf("vmmd: GOMEMLIMIT = %q, want unset (the cgroup is mostly jail tmpfs, not heap)", kv.Value)
		}
	}
	if gogc != "200" {
		t.Errorf("vmmd: GOGC = %q, want \"200\"", gogc)
	}
}
