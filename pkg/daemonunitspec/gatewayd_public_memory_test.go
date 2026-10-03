package daemonunitspec

import "testing"

func TestGatewaydPublicGoMemoryHeadroom(t *testing.T) {
	u := UnitGatewaydPublic()
	if u.MemoryMax != "512M" {
		t.Fatalf("public gateway cgroup cap = %s", u.MemoryMax)
	}
	for _, kv := range u.Environment {
		if kv.Key == "GOMEMLIMIT" && kv.Value == "384MiB" {
			return
		}
	}
	t.Fatal("public gateway must leave headroom below its cgroup cap")
}
