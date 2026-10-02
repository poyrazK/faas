package fcvm

import (
	"os"

	"github.com/onebox-faas/faas/pkg/jailsetup"
)

// The test binary hosts the gate fixture before testing parses the production
// helper's command arguments. It never executes a real jailer or guest.
func runNativeLaunchHelperFixture() {
	if os.Getenv("GREGALE_NATIVE_LAUNCH_FIXTURE") != "1" {
		return
	}
	gate := os.NewFile(3, "fixture-launch-gate")
	if err := jailsetup.AwaitLaunchGate(gate); err != nil {
		if outcome := os.Getenv("GREGALE_NATIVE_GATE_OUTCOME"); outcome != "" {
			_ = os.WriteFile(outcome, []byte("rejected"), 0o600)
		}
		os.Exit(2)
	}
	_ = gate.Close()
	if err := os.WriteFile(os.Getenv("GREGALE_NATIVE_LAUNCH_MARKER"), []byte("authorized"), 0o600); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}
