package e2etest

import (
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/daemonunitspec"
)

// TestFunctionRunnerEnvSatisfiesImagedContract — the harness must supply every
// FAAS_FUNCTION_RUNNER_* path imaged declares Required, and each must point at
// a file that exists.
//
// spec: §4.6
// adr: 003
//
// Without this, imaged exits 2 at boot ("missing required environment
// variables") and never handles app_changed, so every harness test that starts
// it fails with deployments stuck at status=pending and wakes returning 503.
// That went unseen in CI because only the metal-tagged cmd/e2e tests start
// imaged; the 2026-09-13 native e2e run was the first thing to execute them.
//
// The expectations are read FROM the contract rather than hardcoded, so adding
// a seventh runtime to envcontract.go fails here instead of on hardware.
func TestFunctionRunnerEnvSatisfiesImagedContract(t *testing.T) {
	var required []string
	for _, row := range daemonunitspec.EnvContract {
		if !row.Required || !strings.HasPrefix(row.Name, "FAAS_FUNCTION_RUNNER_") {
			continue
		}
		required = append(required, row.Name)
	}
	if len(required) == 0 {
		t.Fatal("no required FAAS_FUNCTION_RUNNER_* rows in the env contract; this test is not testing anything")
	}

	// Assert on the environment imaged is ACTUALLY started with, not on the
	// helper in isolation: deleting the helper's call site is the regression
	// this is defending against, and a helper-only test cannot see it.
	got := map[string]string{}
	for _, kv := range imagedEnv(t, "postgres:///faas", "/dev/null", t.TempDir(), t.TempDir()) {
		name, value, ok := strings.Cut(kv, "=")
		if !ok {
			t.Fatalf("malformed assignment %q", kv)
		}
		got[name] = value
	}

	for _, name := range required {
		value, ok := got[name]
		if !ok {
			t.Errorf("harness does not set %s; imaged will exit 2 at boot", name)
			continue
		}
		if value == "" {
			t.Errorf("%s is empty", name)
			continue
		}
		// The contract validates path-exists, so an unset-but-present name is
		// not enough — the file has to be there.
		if _, err := os.Stat(value); err != nil {
			t.Errorf("%s points at a missing path: %v", name, err)
		}
	}
}

// TestFunctionRunnerEnvHonoursExplicitOverride — a test that stages a real shim
// must win over the placeholder, since the placeholder is not executable as a
// runtime.
func TestFunctionRunnerEnvHonoursExplicitOverride(t *testing.T) {
	real := t.TempDir() + "/real-runner"
	if err := os.WriteFile(real, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_FUNCTION_RUNNER_NODE22", real)

	for _, kv := range functionRunnerEnv(t, t.TempDir()) {
		if name, value, _ := strings.Cut(kv, "="); name == "FAAS_FUNCTION_RUNNER_NODE22" {
			if value != real {
				t.Fatalf("override ignored: got %q, want %q", value, real)
			}
			return
		}
	}
	t.Fatal("FAAS_FUNCTION_RUNNER_NODE22 missing from the environment")
}
