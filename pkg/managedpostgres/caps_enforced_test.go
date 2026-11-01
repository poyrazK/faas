// adr: 20261031091607 — the production release counts the caps as enforced only
// when both the compute and the storage cap are on.
package managedpostgres

import "testing"

func TestCapsEnforcedNeedsBothCapsOn(t *testing.T) {
	for _, tc := range []struct {
		compute, storage string
		want             bool
	}{
		{"on", "on", true},
		{" ON ", "on", true},
		{"on", "", false},
		{"", "on", false},
		{"on", "shadow", false},
		{"shadow", "on", false},
		{"on", "bogus", false},
		{"off", "off", false},
	} {
		env := map[string]string{CapEnforcementEnv: tc.compute, StorageCapEnforcementEnv: tc.storage}
		if got := CapsEnforced(func(k string) string { return env[k] }); got != tc.want {
			t.Errorf("compute %q storage %q = %v, want %v", tc.compute, tc.storage, got, tc.want)
		}
	}
	if CapsEnforced(nil) {
		t.Error("no environment is not enforced")
	}
}
