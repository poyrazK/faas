package main

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/daemonunitspec"
)

func TestCapabilityDeclarationMatchesSystemdBoundingSet(t *testing.T) {
	declared := make(map[string]struct{}, len(capsDecl.Allow))
	for _, capability := range capsDecl.Allow {
		declared[strings.ToLower(capability)] = struct{}{}
	}

	unitCaps := daemonunitspec.UnitVmmd().CapabilityBoundingSet
	if len(unitCaps) != len(declared) {
		t.Fatalf("CapabilityBoundingSet has %d entries; declaration has %d", len(unitCaps), len(declared))
	}
	for _, capability := range unitCaps {
		if _, ok := declared[strings.ToLower(capability)]; !ok {
			t.Errorf("CapabilityBoundingSet contains undeclared %q", capability)
		}
	}
}
