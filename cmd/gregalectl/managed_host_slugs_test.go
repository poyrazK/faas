package main

import (
	"slices"
	"testing"
)

// ADR-20261107180000 (R8-11): a managed host one label under the apps domain
// is reserved as an app name; wildcards, deeper names and other domains are
// not.
func TestManagedHostSlugs(t *testing.T) {
	got := managedHostSlugs([]string{
		"direct-pilot.gregale.dev", "*.gregale.dev", "Attacked.Gregale.dev.", "a.b.gregale.dev",
		"pilot.other.dev", "direct-pilot.gregale.dev",
	}, "gregale.dev")
	if want := []string{"attacked", "direct-pilot"}; !slices.Equal(got, want) {
		t.Fatalf("managedHostSlugs = %v, want %v", got, want)
	}
	if got := managedHostSlugs([]string{"pilot.gregale.dev"}, ""); got != nil {
		t.Fatalf("with no apps domain: %v", got)
	}
}
