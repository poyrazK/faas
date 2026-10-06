// adr: 611
package main

import "testing"

func TestPrivateRuntimeDrainFlagRequiresRoutingAndDefaultsOff(t *testing.T) {
	for _, tc := range []struct {
		routing, drain   string
		enabled, invalid bool
	}{
		{"", "", false, false}, {"1", "", false, false}, {"", "1", false, true}, {"1", "1", true, false},
	} {
		enabled, err := privateRuntimeDrainEnabled(func(key string) string {
			if key == "FAAS_RUNTIME_UPGRADE_DRAIN_CONFIRMATION" {
				return tc.drain
			}
			return tc.routing
		})
		if enabled != tc.enabled || (err != nil) != tc.invalid {
			t.Fatal(tc, enabled, err)
		}
	}
}
