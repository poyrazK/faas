package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/daemonunitspec"
)

// ADR-075: apid binds only Unix/high loopback listeners behind Caddy. Keep its
// runtime declaration aligned with the production unit's empty capability sets.
func TestCapsDeclMatchesProductionUnit(t *testing.T) {
	if len(capsDecl.Allow) != 0 {
		t.Fatalf("capsDecl.Allow = %v, want no capabilities", capsDecl.Allow)
	}
	unit := daemonunitspec.UnitApid()
	if len(unit.CapabilityBoundingSet) != 0 {
		t.Fatalf("CapabilityBoundingSet = %v, want empty", unit.CapabilityBoundingSet)
	}
	if len(unit.AmbientCapabilities) != 1 || unit.AmbientCapabilities[0] != "" {
		t.Fatalf("AmbientCapabilities = %v, want explicit empty directive", unit.AmbientCapabilities)
	}
}
