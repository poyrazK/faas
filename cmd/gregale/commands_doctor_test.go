package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestRunDoctorChecks_LoopbackBindError pins the loopback-bind
// check's failure path. Fixture writes a server.js containing the
// canonical bad bind; the check must return status=error +
// code=app_loopback_bound + the whycopy hint containing
// "127.0.0.1" so the customer sees the root cause.
func TestRunDoctorChecks_LoopbackBindError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "server.js"), []byte("app.listen(8080, '127.0.0.1');\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	rep := runDoctorChecks(dir)
	if len(rep.Checks) != 8 {
		t.Fatalf("expected 8 checks, got %d", len(rep.Checks))
	}
	var loopback *doctorCheck
	for i := range rep.Checks {
		if rep.Checks[i].Name == "loopback-bind" {
			loopback = &rep.Checks[i]
		}
	}
	if loopback == nil {
		t.Fatalf("loopback-bind check missing")
	}
	if loopback.Status != "error" {
		t.Fatalf("expected error, got %q", loopback.Status)
	}
	if loopback.Code != api.CodeAppLoopbackBound {
		t.Fatalf("expected code %q, got %q", api.CodeAppLoopbackBound, loopback.Code)
	}
	if !strings.Contains(loopback.Hint, "127.0.0.1") {
		t.Fatalf("hint must mention 127.0.0.1, got %q", loopback.Hint)
	}
	if len(loopback.Sources) == 0 {
		t.Fatalf("expected sources to list server.js")
	}
	if !strings.Contains(loopback.Sources[0], "server.js") {
		t.Fatalf("expected sources[0] to mention server.js, got %q", loopback.Sources[0])
	}
}

// TestRunDoctorChecks_CleanRepo pins the clean local path: source
// checks are ok, while checks that require deployed telemetry are
// explicitly skipped. The shape matters: JSON output must be
// deterministic without claiming an unperformed check passed.
func TestRunDoctorChecks_CleanRepo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "server.js"), []byte("app.listen(process.env.PORT);\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	rep := runDoctorChecks(dir)
	skipped := map[string]bool{
		"port-bind": true, "runtime-oom": true, "dep-install": true, "startup-timeout": true,
	}
	for _, c := range rep.Checks {
		want := "ok"
		if skipped[c.Name] {
			want = "skipped"
			if c.Reason == "" {
				t.Errorf("check %s is skipped without a reason", c.Name)
			}
		}
		if c.Status != want {
			t.Errorf("check %s expected %s, got %q (code=%s, hint=%s)",
				c.Name, want, c.Status, c.Code, c.Hint)
		}
	}
}

// TestRunDoctorChecks_EnvVarMissing pins the env-required check.
// Fixture writes source that references $DATABASE_URL but no
// .gregale/env.json declares it. The check must flag it as
// status=error + code=env_var_missing.
func TestRunDoctorChecks_EnvVarMissing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.py"), []byte(`import os\nprint(os.environ["DATABASE_URL"])\n`), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	rep := runDoctorChecks(dir)
	var envCheck *doctorCheck
	for i := range rep.Checks {
		if rep.Checks[i].Name == "env-required" {
			envCheck = &rep.Checks[i]
		}
	}
	if envCheck == nil {
		t.Fatalf("env-required check missing")
	}
	if envCheck.Status != "error" {
		t.Fatalf("expected error, got %q", envCheck.Status)
	}
	if envCheck.Code != api.CodeEnvVarMissing {
		t.Fatalf("expected code %q, got %q", api.CodeEnvVarMissing, envCheck.Code)
	}
	if len(envCheck.Sources) == 0 || envCheck.Sources[0] != "DATABASE_URL" {
		t.Fatalf("expected Sources[0]=DATABASE_URL, got %v", envCheck.Sources)
	}
}

// TestRunDoctorChecks_StatelessOnlyDir pins the stateless-only
// check's persistence-signal detection. Fixture has a top-level
// data/ directory → check fires with code=stateless_only_violation.
func TestRunDoctorChecks_StatelessOnlyDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rep := runDoctorChecks(dir)
	var statCheck *doctorCheck
	for i := range rep.Checks {
		if rep.Checks[i].Name == "stateless-only" {
			statCheck = &rep.Checks[i]
		}
	}
	if statCheck == nil {
		t.Fatalf("stateless-only check missing")
	}
	if statCheck.Status != "error" {
		t.Fatalf("expected error, got %q", statCheck.Status)
	}
	if statCheck.Code != api.CodeStatelessOnlyViolation {
		t.Fatalf("expected code %q, got %q", api.CodeStatelessOnlyViolation, statCheck.Code)
	}
}

// TestScanSource_SkipsVendoredDirs pins the noise-reduction
// contract. Files under vendor/, node_modules/, .git/ must not
// be scanned — preflight scans the customer's tree, and
// vendored deps are large (3-30 MB) and full of false positives.
func TestScanSource_SkipsVendoredDirs(t *testing.T) {
	dir := t.TempDir()
	vendor := filepath.Join(dir, "vendor")
	if err := os.Mkdir(vendor, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Write a vendor file that would trip the regex; the test
	// asserts scanSource skips it.
	if err := os.WriteFile(filepath.Join(vendor, "lib.js"),
		[]byte("app.listen(80, '127.0.0.1');\n"), 0o644); err != nil {
		t.Fatalf("write vendor: %v", err)
	}
	out := scanSource(dir, loopbackBindRegex, 5)
	if len(out) != 0 {
		t.Fatalf("expected 0 sources (vendor must be skipped), got %v", out)
	}
}

// TestRenderDoctorHuman_HasAllChecks pins the human-renderer
// shape. The customer-facing line count must be stable so
// script consumers grep on a fixed line index per check.
func TestRenderDoctorHuman_HasAllChecks(t *testing.T) {
	dir := t.TempDir()
	rep := runDoctorChecks(dir)
	var sb strings.Builder
	renderDoctorHuman(&sb, rep)
	out := sb.String()
	for _, name := range []string{"port-bind", "loopback-bind", "arch", "env-required", "stateless-only", "runtime-oom", "dep-install", "startup-timeout"} {
		if !strings.Contains(out, name) {
			t.Errorf("render missing check %q in:\n%s", name, out)
		}
	}
	if !strings.Contains(out, "No local findings") || !strings.Contains(out, "were skipped") {
		t.Errorf("render missing skipped-check summary in:\n%s", out)
	}
}

// TestDoctorReport_HasErrorsAndHasWarnings pins the two helpers
// the deploy path relies on. The semantics are subtle: HasErrors
// is a hard-fail signal for --doctor-strict, but HasWarnings must
// only signal "render the report + continue" (not exit 1). A
// fixture with one error + one warn must trip both helpers; an
// all-ok fixture must trip neither.
func TestDoctorReport_HasErrorsAndHasWarnings(t *testing.T) {
	t.Run("all ok trips neither", func(t *testing.T) {
		dir := t.TempDir()
		rep := runDoctorChecks(dir)
		if rep.HasErrors() {
			t.Errorf("clean repo: HasErrors=true, want false")
		}
		if rep.HasWarnings() {
			t.Errorf("clean repo: HasWarnings=true, want false")
		}
	})
	t.Run("stateless fixture trips HasErrors only", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "data"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		rep := runDoctorChecks(dir)
		if !rep.HasErrors() {
			t.Errorf("stateless fixture: HasErrors=false, want true")
		}
		// The current ruleset emits "error" for stateless-only; no
		// rule emits "warn" today. The helpers must remain stable
		// even if a future check promotes a finding to warn, so we
		// only assert HasErrors here (HasWarnings is exercised by
		// the dedicated loopback-bind error fixture below for the
		// "trip BOTH helpers" path; the rule today emits error for
		// loopback-bind too, so we don't get a clean warn-only case
		// without faking — keep the test honest).
	})
	t.Run("manual fixture trips both helpers", func(t *testing.T) {
		// Synthetic report to verify the helper semantics in
		// isolation, independent of the check implementations. This
		// pins the contract for any future check that promotes
		// a finding to warn (e.g. a dep-install soft-warning).
		rep := doctorReport{
			Path: "/tmp",
			Checks: []doctorCheck{
				{Name: "fake-ok", Status: "ok"},
				{Name: "fake-warn", Status: "warn"},
				{Name: "fake-error", Status: "error"},
			},
		}
		if !rep.HasErrors() {
			t.Errorf("HasErrors=false, want true")
		}
		if !rep.HasWarnings() {
			t.Errorf("HasWarnings=false, want true")
		}
	})
	t.Run("warn-only trips HasWarnings, not HasErrors", func(t *testing.T) {
		rep := doctorReport{
			Checks: []doctorCheck{
				{Name: "fake-ok", Status: "ok"},
				{Name: "fake-warn", Status: "warn"},
			},
		}
		if rep.HasErrors() {
			t.Errorf("warn-only: HasErrors=true, want false")
		}
		if !rep.HasWarnings() {
			t.Errorf("warn-only: HasWarnings=false, want true")
		}
	})
}
