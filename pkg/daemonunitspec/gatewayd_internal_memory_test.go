package daemonunitspec

import "testing"

// TestGatewaydInternalGoRuntimeTuning pins the measured GC setting and the
// headroom that keeps it safe under the cgroup cap.
func TestGatewaydInternalGoRuntimeTuning(t *testing.T) {
	u := UnitGatewaydInternal()
	if u.MemoryMax != "512M" {
		t.Fatalf("internal gateway cgroup cap = %s; re-derive GOMEMLIMIT before changing it", u.MemoryMax)
	}
	want := map[string]string{"GOGC": "200", "GOMEMLIMIT": "384MiB"}
	for _, kv := range u.Environment {
		if v, ok := want[kv.Key]; ok {
			if kv.Value != v {
				t.Errorf("%s = %q, want %q", kv.Key, kv.Value, v)
			}
			delete(want, kv.Key)
		}
	}
	for k := range want {
		t.Errorf("internal gateway unit is missing %s", k)
	}
}
