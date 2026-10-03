// adr: 459 — environment intent and runtime ownership contracts.
package fcvm

import (
	"io"
	"os"
	"time"

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
	if marker := os.Getenv("GREGALE_NATIVE_HELPER_INPUT_MARKER"); marker != "" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil || os.WriteFile(marker, data, 0o600) != nil {
			os.Exit(4)
		}
	}
	if output := os.Getenv("GREGALE_NATIVE_HELPER_OUTPUT"); output != "" {
		_, _ = os.Stdout.WriteString(output)
	}
	if err := os.WriteFile(os.Getenv("GREGALE_NATIVE_LAUNCH_MARKER"), []byte("authorized"), 0o600); err != nil {
		os.Exit(3)
	}
	if os.Getenv("GREGALE_NATIVE_HOLD_HELPER_FIXTURE") == "1" {
		for {
			time.Sleep(time.Hour)
		}
	}
	os.Exit(0)
}
