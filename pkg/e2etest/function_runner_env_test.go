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

// TestFunctionRunnerEnvStagesExecutablePlaceholders — the contract validates
// path-exists, so every value must name a file that is really on disk.
//
// spec: §4.6
// adr: 003
//
// This replaced an override test. The harness used to read
// os.Getenv for these names so a test could substitute a real shim; that read
// made pkg/e2etest an owner in the env contract, and declaring it turned
// imaged-only Required rows into rows apid had to satisfy too, which stopped
// apid booting. Placeholders are staged unconditionally instead.
func TestFunctionRunnerEnvStagesExecutablePlaceholders(t *testing.T) {
	for _, kv := range functionRunnerEnv(t, t.TempDir()) {
		_, value, ok := strings.Cut(kv, "=")
		if !ok {
			t.Fatalf("malformed assignment %q", kv)
		}
		info, err := os.Stat(value)
		if err != nil {
			t.Errorf("%s: %v", kv, err)
			continue
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Errorf("%s is not executable (mode %v)", value, info.Mode().Perm())
		}
	}
}
