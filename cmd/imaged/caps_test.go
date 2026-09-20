// Unit tests for cmd/imaged/caps.go. Pins the declaration
// shape so a future PR that drops cap_sys_admin from Deny (or
// grows Allow beyond the extraction pair) trips these tests instead of silently
// regressing DEPLOY-1's "vmmd is the only root mount owner"
// invariant.
package main

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/capdecl"
)

// TestCapsDecl_AllowsOnlyExtractionCapabilities pins the narrow pair imaged
// needs to preserve OCI ownership and keep traversing restrictive directories
// after ownership changes. Mount authority remains denied and owned by vmmd.
func TestCapsDecl_AllowsOnlyExtractionCapabilities(t *testing.T) {
	want := []string{"cap_chown", "cap_dac_override"}
	if !slices.Equal(capsDecl.Allow, want) {
		t.Errorf("capsDecl.Allow = %v, want %v", capsDecl.Allow, want)
	}
}

// TestCapsDecl_DeniesCapSysAdmin: review finding M1. The
// Deny list MUST contain cap_sys_admin — the tripwire for
// the "vmmd is the only root component that mounts
// filesystems" invariant (CLAUDE.md / spec §11). The
// matching edit in deploy/systemd/faas-imaged.service shrinks
// CapabilityBoundingSet= to exclude cap_sys_admin so the
// runtimecheck passes on a healthy boot.
func TestCapsDecl_DeniesCapSysAdmin(t *testing.T) {
	if !slices.Contains(capsDecl.Deny, "cap_sys_admin") {
		t.Errorf("capsDecl.Deny missing cap_sys_admin (M1 tripwire); got %v", capsDecl.Deny)
	}
}

// TestCapsDecl_NoAllowDenyOverlap: capdecl.Declaration.Validate
// enforces this at runtime too, but pinning it here makes
// the failure mode obvious if a future PR adds cap_sys_admin
// to both Allow (the regression) and Deny (the tripwire).
func TestCapsDecl_NoAllowDenyOverlap(t *testing.T) {
	for _, c := range capsDecl.Allow {
		if slices.Contains(capsDecl.Deny, c) {
			t.Errorf("cap %q appears in both Allow and Deny", c)
		}
	}
}

// TestCapsDecl_ValidatesCleanly: round-trip the declaration
// through capdecl.Validate (the same call pkg/capdecl/
// runtimecheck.Check makes at boot). Catches malformed
// declarations (empty cap names, duplicates) before the
// daemon reaches the runtimecheck gate.
func TestCapsDecl_ValidatesCleanly(t *testing.T) {
	var decl capdecl.Declaration
	decl.Allow = append(decl.Allow, capsDecl.Allow...)
	decl.Deny = append(decl.Deny, capsDecl.Deny...)
	if err := decl.Validate(); err != nil {
		t.Errorf("capsDecl.Validate() = %v, want nil", err)
	}
}
