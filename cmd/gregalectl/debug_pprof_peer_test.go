package main

import (
	"errors"
	"testing"
)

// Abstract sockets have no permissions: gregalectl must not hand root a
// profile from whoever bound @faas-debug/<daemon> first.
func TestDebugPprofServerAllowed(t *testing.T) {
	old := debugPprofLookupUID
	t.Cleanup(func() { debugPprofLookupUID = old })
	debugPprofLookupUID = func(name string) (uint32, error) {
		if name == "faas-apid" {
			return 1500, nil // an account created without --system
		}
		return 0, errors.New("unknown user")
	}
	for _, tc := range []struct {
		uid    uint32
		socket string
		want   bool
	}{
		{0, "faas-debug/apid", true},
		{995, "faas-debug/schedd", true}, // a system account
		{1500, "faas-debug/apid", true},  // the daemon's own account
		{1500, "faas-debug/apid.4242", true},
		{1500, "faas-debug/schedd", false},  // another daemon's account
		{1000, "faas-debug/apid", false},    // a person
		{20001, "faas-debug/schedd", false}, // a tenant jail uid
	} {
		if got := debugPprofServerAllowed(tc.uid, tc.socket); got != tc.want {
			t.Errorf("debugPprofServerAllowed(%d, %q) = %v, want %v", tc.uid, tc.socket, got, tc.want)
		}
	}
}
