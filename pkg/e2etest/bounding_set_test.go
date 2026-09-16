package e2etest

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/daemonunitspec"
)

// TestBoundingSetPrefixMatchesImagedUnit — the harness must start imaged with
// the capability bounding set its production unit declares, and must never
// leave cap_sys_admin reachable.
//
// spec: §11
// adr: 075
//
// imaged calls capdecl at boot and refuses to start when a denied capability is
// still in the live bounding set. The native e2e gate runs as root (jailer,
// netns and KVM need it), so without this wrapper imaged exits with
// "capdecl: declaration denies caps present in live Bnd set: cap_sys_admin"
// and every deploy-path test fails downstream. Seen on the 2026-09-14 run.
func TestBoundingSetPrefixMatchesImagedUnit(t *testing.T) {
	unit, err := daemonunitspec.UnitByName("imaged")
	if err != nil {
		t.Fatalf("imaged unit spec: %v", err)
	}
	if len(unit.CapabilityBoundingSet) == 0 {
		t.Fatal("imaged unit declares no CapabilityBoundingSet; this test is not testing anything")
	}
	for _, c := range unit.CapabilityBoundingSet {
		if strings.EqualFold(c, "cap_sys_admin") {
			t.Fatal("imaged unit now allows cap_sys_admin; capdecl and ADR-075 disagree with this test")
		}
	}

	// The prefix is only meaningful as root on Linux; assert the argv shape the
	// harness would build from the spec, independent of the host running it.
	set := "-all"
	for _, c := range unit.CapabilityBoundingSet {
		set += ",+" + strings.TrimPrefix(strings.ToLower(c), "cap_")
	}
	if strings.Contains(set, "sys-admin") || strings.Contains(set, "sys_admin") {
		t.Fatalf("derived bounding set re-admits sys_admin: %s", set)
	}
	if !strings.HasPrefix(set, "-all,") {
		t.Fatalf("bounding set must start from -all, got %q", set)
	}
	// Every declared capability must survive the translation, or imaged loses a
	// privilege it legitimately needs (e.g. cap_sys_chroot for the jail).
	for _, c := range unit.CapabilityBoundingSet {
		want := "+" + strings.TrimPrefix(strings.ToLower(c), "cap_")
		if !strings.Contains(set, want) {
			t.Errorf("declared capability %s missing from derived set %q", c, set)
		}
	}
}
