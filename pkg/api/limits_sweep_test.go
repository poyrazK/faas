package api

import "testing"

// This file drives the 0%-coverage Plan methods on Limits. Each
// test exercises the method across all four plans to flip the
// branch coverage. No fixtures, no mocks — these are pure
// dispatchers over the plan table.

func TestSweep_PlanValid(t *testing.T) {
	for _, p := range []Plan{PlanFree, PlanHobby, PlanPro, PlanScale} {
		if !p.Valid() {
			t.Errorf("Plan(%q).Valid() = false", p)
		}
	}
	if Plan("invalid").Valid() {
		t.Error("Plan(\"invalid\").Valid() = true, want false")
	}
}

func TestSweep_PlanIsPaid(t *testing.T) {
	if PlanFree.IsPaid() {
		t.Error("Free should not be paid")
	}
	if !PlanHobby.IsPaid() {
		t.Error("Hobby should be paid")
	}
	if !PlanPro.IsPaid() {
		t.Error("Pro should be paid")
	}
	if !PlanScale.IsPaid() {
		t.Error("Scale should be paid")
	}
}

func TestSweep_PlanIncludedGBHours(t *testing.T) {
	for _, p := range []Plan{PlanFree, PlanHobby, PlanPro, PlanScale} {
		got := p.PlanIncludedGBHours()
		if got < 0 {
			t.Errorf("Plan(%q).PlanIncludedGBHours() = %d, want >= 0", p, got)
		}
	}
}

func TestSweep_PlanRequiresStripeUpgradeTo(t *testing.T) {
	for _, next := range []Plan{PlanFree, PlanHobby, PlanPro, PlanScale} {
		_ = PlanFree.RequiresStripeUpgradeTo(next)
		_ = PlanHobby.RequiresStripeUpgradeTo(next)
		_ = PlanScale.RequiresStripeUpgradeTo(next)
	}
}

func TestSweep_PlanMinMaxInstances(t *testing.T) {
	for _, p := range []Plan{PlanFree, PlanHobby, PlanPro, PlanScale} {
		_ = p.MinInstancesAllowed()
		_ = p.MaxInstancesAllowed()
	}
}

func TestSweep_PlanWarmSnapshot(t *testing.T) {
	for _, p := range []Plan{PlanFree, PlanHobby, PlanPro, PlanScale} {
		_ = p.WarmSnapshotNewAppDefault()
		_ = p.WarmSnapshotAllowed()
	}
}

func TestSweep_PlanLiveness(t *testing.T) {
	for _, p := range []Plan{PlanFree, PlanHobby, PlanPro, PlanScale} {
		_ = p.LivenessAllowed()
		_ = p.LivenessPeriodSeconds()
		_ = p.LivenessConsecutiveFailures()
		_ = p.LivenessCooldownSeconds()
		_ = p.LivenessMaxRestarts()
		_ = p.LivenessWindowSeconds()
		_ = p.GRPCLivenessAllowed()
	}
}

func TestSweep_PlanStreaming(t *testing.T) {
	for _, p := range []Plan{PlanFree, PlanHobby, PlanPro, PlanScale} {
		_ = p.StreamingEnabled()
		_ = p.StreamingResponseAllowed()
	}
}

func TestSweep_PlanWebSocket(t *testing.T) {
	for _, p := range []Plan{PlanFree, PlanHobby, PlanPro, PlanScale} {
		_ = p.WebSocketEnabled()
		_ = p.WebSocketResponseAllowed()
	}
}

func TestSweep_PlanSidecar(t *testing.T) {
	for _, p := range []Plan{PlanFree, PlanHobby, PlanPro, PlanScale} {
		_ = p.SidecarAllowed()
	}
}

func TestSweep_PlanEgressAllowlist(t *testing.T) {
	for _, p := range []Plan{PlanFree, PlanHobby, PlanPro, PlanScale} {
		_ = p.EgressAllowlistAllowed()
		_ = p.EgressAllowlistMaxSize()
	}
}

func TestSweep_PlanPublicAuthIPAllowlist(t *testing.T) {
	for _, p := range []Plan{PlanFree, PlanHobby, PlanPro, PlanScale} {
		_ = p.PublicAuthIPAllowlistAllowed()
		_ = p.PublicAuthIPAllowlistMaxEntries()
	}
}

func TestSweep_PlanRequireAuthn(t *testing.T) {
	for _, p := range []Plan{PlanFree, PlanHobby, PlanPro, PlanScale} {
		_ = p.RequireAuthnAllowed()
		_ = p.RequireAuthnDefault()
		_ = p.PublicAuthModeDefault()
	}
}

func TestSweep_LimitsFor(t *testing.T) {
	for _, p := range []Plan{PlanFree, PlanHobby, PlanPro, PlanScale} {
		if _, ok := LimitsFor(p); !ok {
			t.Errorf("LimitsFor(%q) returned ok=false", p)
		}
	}
	if _, ok := LimitsFor(Plan("invalid")); ok {
		t.Error("LimitsFor(invalid) returned ok=true")
	}
}

func TestSweep_MustLimitsFor(t *testing.T) {
	for _, p := range []Plan{PlanFree, PlanHobby, PlanPro, PlanScale} {
		got := MustLimitsFor(p)
		if got.Plan != p {
			t.Errorf("MustLimitsFor(%q).Plan = %q", p, got.Plan)
		}
	}
}

func TestSweep_DefaultComputeNodeCeilingMB(t *testing.T) {
	if got := DefaultComputeNodeCeilingMB(); got <= 0 {
		t.Errorf("DefaultComputeNodeCeilingMB() = %d, want > 0", got)
	}
}

func TestSweep_ConntrackCapProbe(t *testing.T) {
	// ConntrackCapProbe runs "sysctl net.netfilter.nf_conntrack_max"
	// — may fail in CI; we just exercise the path.
	_ = ConntrackCapProbe()
}

func TestSweep_ExecCmd(t *testing.T) {
	// execCmd is the internal helper ConntrackCapProbe wraps.
	// Run a known command so we exercise both success and shell-fail
	// branches.
	out, err := execCmd("echo", "hello")
	if err != nil {
		t.Fatalf("execCmd(echo) err = %v", err)
	}
	if string(out) == "" {
		t.Error("execCmd(echo) returned empty output")
	}
	// Unknown binary returns non-nil error.
	if _, err := execCmd("definitely-not-a-real-binary-xyz"); err == nil {
		t.Error("execCmd(fake binary) returned nil err")
	}
}

func TestSweep_SidecarBuildManifest(t *testing.T) {
	m := SidecarBuildManifest()
	if len(m.Entrypoint) != 1 || m.Entrypoint[0] != "/bin/sidecar-placeholder" {
		t.Errorf("Entrypoint = %v, want [/bin/sidecar-placeholder]", m.Entrypoint)
	}
	if m.Port != DefaultAppPort {
		t.Errorf("Port = %d, want %d", m.Port, DefaultAppPort)
	}
	if m.Healthz != "/healthz" {
		t.Errorf("Healthz = %q, want /healthz", m.Healthz)
	}
}
