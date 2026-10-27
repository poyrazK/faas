package builderd

import (
	"os"
	"testing"
)

// TestMain pins the pipeline fixtures to the full-layer build path. The
// shared runner base is on by default (ADR-20261028090000), and these
// fixtures model the full-layer cache keys, artifacts and runtime claims;
// the shared path is covered by shared_runtime_select_test.go and the metal
// source-deploy tests. A test that wants it sets the variable itself.
func TestMain(m *testing.M) {
	if _, set := os.LookupEnv(SharedRuntimeEnv); !set {
		_ = os.Setenv(SharedRuntimeEnv, "0")
	}
	os.Exit(m.Run())
}
