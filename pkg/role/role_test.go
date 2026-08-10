// Package role tests — Gate-B per-daemon refusal gate.
//
// The test matrix covers every (allow-list × observed role) cell
// for the daemons with distinct allow-lists (apid, schedd, vmmd,
// gatewayd-internal). The remaining daemons (meterd, githubd,
// builderd, imaged, gatewayd-public) share an allow-list with one
// of these and would only duplicate coverage.
//
// The error-shape test pins the runbook sentence so an operator
// reading the systemd journal can map it to a host_vars fix.
package role

import (
	"errors"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		raw     string
		want    Role
		wantErr bool
	}{
		{raw: "", want: RoleSingleBox},
		{raw: "single-box", want: RoleSingleBox},
		{raw: "control-plane", want: RoleControlPlane},
		{raw: "compute-only", want: RoleComputeOnly},
		{raw: "SINGLE-BOX", wantErr: true},   // case-sensitive on purpose
		{raw: "controlplane", wantErr: true}, // missing hyphen
		{raw: "multi-box", wantErr: true},    // not a real role
		{raw: "garbage", wantErr: true},
		{raw: "single-box ", wantErr: true}, // trailing whitespace
	}
	for _, c := range cases {
		got, err := Parse(c.raw)
		if (err != nil) != c.wantErr {
			t.Errorf("Parse(%q) err=%v wantErr=%v", c.raw, err, c.wantErr)
			continue
		}
		if !c.wantErr && got != c.want {
			t.Errorf("Parse(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestStrictParse(t *testing.T) {
	if _, err := StrictParse(""); err == nil {
		t.Error("StrictParse(\"\") = nil; want error (empty is required input)")
	}
	r, err := StrictParse("control-plane")
	if err != nil || r != RoleControlPlane {
		t.Errorf("StrictParse(\"control-plane\") = (%q, %v); want (control-plane, nil)", r, err)
	}
}

func TestFromConfig_DefaultsToSingleBox(t *testing.T) {
	// Both empty: TOML default + env unset → RoleSingleBox.
	t.Setenv("FAAS_ROLE_TEST", "")
	if got := FromConfig("", "FAAS_ROLE_TEST"); got != RoleSingleBox {
		t.Errorf("FromConfig(\"\", env unset) = %q; want single-box", got)
	}
}

func TestFromConfig_UnknownTOMLPassesThrough(t *testing.T) {
	// Unknown TOML value is NOT silently swallowed — the daemon's
	// Require call at boot will surface the typed error so the
	// operator sees the actual bad value. We deliberately do NOT
	// parse-error here because the call site is inside LoadConfig
	// which already has the existing typed-error surface.
	t.Setenv("FAAS_ROLE_TEST", "")
	if got := FromConfig("garbage", "FAAS_ROLE_TEST"); got != Role("garbage") {
		t.Errorf("FromConfig(garbage, env unset) = %q; want passthrough, "+
			"(Require surfaces the typed error at boot)", got)
	}
}

func TestFromConfig_EnvWinsOverTOML(t *testing.T) {
	// Env wins when non-empty, even when TOML is set to a different
	// value. This is the per-deploy override path.
	t.Setenv("FAAS_ROLE_TEST", "compute-only")
	if got := FromConfig("control-plane", "FAAS_ROLE_TEST"); got != RoleComputeOnly {
		t.Errorf("FromConfig(control-plane, env=compute-only) = %q; want compute-only "+
			"(env must override TOML — per-deploy override path)", got)
	}
}

func TestFromConfig_EnvEmptyFallsBackToTOML(t *testing.T) {
	// Explicitly empty env: fall back to TOML. The env supervisor
	// may export FAAS_*_ROLE="" to force a TOML-driven boot.
	t.Setenv("FAAS_ROLE_TEST", "")
	if got := FromConfig("control-plane", "FAAS_ROLE_TEST"); got != RoleControlPlane {
		t.Errorf("FromConfig(control-plane, env=\"\") = %q; want control-plane "+
			"(empty env must fall back to TOML)", got)
	}
}

func TestFromConfig_UnknownEnvIsReturnedAsIs(t *testing.T) {
	// Unknown env value is NOT swallowed — the daemon's Require
	// call will surface it via the typed error so the operator
	// sees the bad value in the boot log. Silently defaulting
	// would mask the misconfiguration.
	t.Setenv("FAAS_ROLE_TEST", "not-a-role")
	if got := FromConfig("", "FAAS_ROLE_TEST"); got != Role("not-a-role") {
		t.Errorf("FromConfig(\"\", env=not-a-role) = %q; want %q (passthrough, "+
			"Require surfaces the typed error)", got, "not-a-role")
	}
}

// TestRequire covers every (allow-list × observed role) cell for
// the four representative daemons. The remaining daemons share an
// allow-list with one of these and would only duplicate coverage.
func TestRequire(t *testing.T) {
	// Control-plane daemons: apid, schedd, gatewayd-public,
	// githubd, meterd. Allow = single-box, control-plane.
	controlPlaneAL := []Role{RoleSingleBox, RoleControlPlane}
	// Compute-only daemons: vmmd, gatewayd-internal, builderd,
	// imaged. Allow = single-box, compute-only.
	computeOnlyAL := []Role{RoleSingleBox, RoleComputeOnly}

	type cell struct {
		daemon   string
		allow    []Role
		observed Role
		wantErr  bool
	}
	cases := []cell{
		// apid under control-plane allow
		{"apid", controlPlaneAL, RoleSingleBox, false},
		{"apid", controlPlaneAL, RoleControlPlane, false},
		{"apid", controlPlaneAL, RoleComputeOnly, true},
		// schedd under control-plane allow
		{"schedd", controlPlaneAL, RoleSingleBox, false},
		{"schedd", controlPlaneAL, RoleControlPlane, false},
		{"schedd", controlPlaneAL, RoleComputeOnly, true},
		// vmmd under compute-only allow
		{"vmmd", computeOnlyAL, RoleSingleBox, false},
		{"vmmd", computeOnlyAL, RoleComputeOnly, false},
		{"vmmd", computeOnlyAL, RoleControlPlane, true},
		// gatewayd-internal under compute-only allow
		{"gatewayd-internal", computeOnlyAL, RoleSingleBox, false},
		{"gatewayd-internal", computeOnlyAL, RoleComputeOnly, false},
		{"gatewayd-internal", computeOnlyAL, RoleControlPlane, true},
	}
	for _, c := range cases {
		err := Require(c.daemon, c.observed, c.allow...)
		if (err != nil) != c.wantErr {
			t.Errorf("Require(%q, %q, allow=%v) err=%v, wantErr=%v",
				c.daemon, c.observed, c.allow, err, c.wantErr)
		}
	}
}

// TestRequire_RefusesUnknownRole pins that an unknown role string
// (e.g. an operator typo in host_vars) is handled the same way as
// a known-but-disallowed role. The daemon refuses to start; the
// systemd journal captures the boot diagnostic.
func TestRequire_RefusesUnknownRole(t *testing.T) {
	err := Require("apid", Role("not-a-role"), RoleSingleBox, RoleControlPlane)
	if err == nil {
		t.Fatal("Require: unknown role accepted; want refusal")
	}
	// An unknown role is NOT a known disallowed role, so the
	// refusal should still mention the allowed list (the operator
	// needs to see what IS allowed to fix the typo).
	var refused *ErrRefused
	if !errors.As(err, &refused) {
		t.Fatalf("Require: error type = %T; want *ErrRefused", err)
	}
	if refused.Daemon != "apid" {
		t.Errorf("ErrRefused.Daemon = %q; want apid", refused.Daemon)
	}
	if refused.Observed != Role("not-a-role") {
		t.Errorf("ErrRefused.Observed = %q; want not-a-role", refused.Observed)
	}
	if len(refused.Allowed) != 2 {
		t.Errorf("ErrRefused.Allowed = %v; want 2 entries", refused.Allowed)
	}
}

// TestRequire_RefusalShape pins the runbook sentence so an
// operator reading the systemd journal can map the error to a
// host_vars fix without grepping the codebase.
func TestRequire_RefusalShape(t *testing.T) {
	err := Require("apid", RoleComputeOnly, RoleSingleBox, RoleControlPlane)
	if err == nil {
		t.Fatal("Require: RoleComputeOnly accepted; want refusal")
	}
	msg := err.Error()
	// The runbook sentence shape is "<daemon>: refusing to start
	// as role <observed> (allowed: <list>)". The exact ordering
	// matters because runbook search hits jump on the substring.
	for _, want := range []string{
		"apid: refusing to start as role",
		"compute-only",
		"single-box",
		"control-plane",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("Refusal message %q missing substring %q", msg, want)
		}
	}
}

// TestRequire_EmptyAllowAlwaysRefuses pins the edge case of a
// malformed call site (no roles allowed). The daemon refuses to
// start. This is caught at compile time in a production build but
// the test pins the runtime behaviour for a future caller that
// builds an allow-list from a config-driven map.
func TestRequire_EmptyAllowAlwaysRefuses(t *testing.T) {
	err := Require("apid", RoleSingleBox)
	if err == nil {
		t.Fatal("Require with empty allow accepted RoleSingleBox; want refusal")
	}
	var refused *ErrRefused
	if !errors.As(err, &refused) {
		t.Fatalf("Require: error type = %T; want *ErrRefused", err)
	}
	if len(refused.Allowed) != 0 {
		t.Errorf("ErrRefused.Allowed = %v; want empty", refused.Allowed)
	}
}

// TestAllRolesIsSorted pins the canonical ordering used in runbook
// tables and operator-facing error messages. Changing the order
// requires a runbook update.
func TestAllRolesIsSorted(t *testing.T) {
	want := []Role{RoleSingleBox, RoleControlPlane, RoleComputeOnly}
	if len(AllRoles) != len(want) {
		t.Fatalf("AllRoles length = %d; want %d", len(AllRoles), len(want))
	}
	for i, r := range want {
		if AllRoles[i] != r {
			t.Errorf("AllRoles[%d] = %q; want %q", i, AllRoles[i], r)
		}
	}
}
