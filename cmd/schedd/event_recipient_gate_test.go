// adr: 647
package main

import "testing"

func TestEventRecipientClaimsEnabledDefaultsOn(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", true},
		{"  ", true},
		{"1", true},
		{" 1 ", true},
		{"0", false},
		{" 0 ", false},
		{"false", false},
		{"true", false},
		{"yes", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			if got := eventRecipientClaimsEnabled(tc.value); got != tc.want {
				t.Errorf("eventRecipientClaimsEnabled(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}
