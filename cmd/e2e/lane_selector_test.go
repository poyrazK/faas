package e2e_test

// The runner must accept a LANE in FAAS_E2E_PHASE, not only a phase.
//
// The first smoke dispatch (run 35155683638) ran no tests: run-native-e2e.sh
// validated FAAS_E2E_PHASE against NATIVE_E2E_PHASES alone and died with
// "unknown phase smoke" before its lane branch was reached. The lane's shell
// contracts tested the library, not the runner's gate. This pins the gate
// itself, in ordinary CI.

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestRunnerValidatesSelectorsThroughTheLibrary(t *testing.T) {
	runner, err := os.ReadFile(filepath.Join("..", "..", "scripts", "ci", "run-native-e2e.sh"))
	if err != nil {
		t.Fatalf("read run-native-e2e.sh: %v", err)
	}
	lib, err := os.ReadFile(filepath.Join("..", "..", "scripts", "ci", "native-e2e-phases.sh"))
	if err != nil {
		t.Fatalf("read native-e2e-phases.sh: %v", err)
	}

	if !regexp.MustCompile(`native_e2e_is_selector\s+"\$\{phase\}"`).Match(runner) {
		t.Error("run-native-e2e.sh does not validate FAAS_E2E_PHASE with native_e2e_is_selector; " +
			"a lane such as smoke is rejected as an unknown phase and the run executes nothing")
	}
	// The old gate — phases only — must not come back beside the new one.
	if regexp.MustCompile(`NATIVE_E2E_PHASES\[@\]\}"\s*\|\s*grep -qx "\$\{phase\}"`).Match(runner) {
		t.Error("run-native-e2e.sh still gates FAAS_E2E_PHASE on NATIVE_E2E_PHASES alone")
	}
	for _, fn := range []string{"native_e2e_is_selector", "native_e2e_is_lane", "native_e2e_lane_regex"} {
		if !regexp.MustCompile(`(?m)^` + fn + `\(\)\s*\{`).Match(lib) {
			t.Errorf("native-e2e-phases.sh does not define %s", fn)
		}
	}
}
