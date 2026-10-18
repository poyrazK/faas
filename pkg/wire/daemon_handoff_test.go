package wire_test

// adr: 2026101801

import (
	"errors"
	"os"
	"os/exec"
	"testing"

	"github.com/onebox-faas/faas/pkg/wire"
)

const handoffProbeHelperEnv = "WIRE_HANDOFF_PROBE_HELPER"

func TestHandoffProbeHelperProcess(t *testing.T) {
	mode := os.Getenv(handoffProbeHelperEnv)
	if mode == "" {
		t.Skip("helper process")
	}
	// The test binary has parsed its own flags; Daemon parses the probe's.
	os.Args = []string{os.Args[0], "-handoff-capable"}
	switch mode {
	case "handoff":
		wire.HandoffDaemon("probe", nil)
	case "plain":
		wire.Daemon("probe", nil)
	}
}

// deployctl reloads a daemon only when its binary answers -handoff-capable
// with 0; a daemon without hand-off answers 3, and a release that predates
// the flag fails to parse it (2).
func TestHandoffCapableProbe(t *testing.T) {
	for mode, want := range map[string]int{"handoff": 0, "plain": 3} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestHandoffProbeHelperProcess$")
		cmd.Env = append(os.Environ(), handoffProbeHelperEnv+"="+mode)
		err := cmd.Run()
		got := 0
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			got = exit.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s daemon: -handoff-capable exit %d, want %d", mode, got, want)
		}
	}
}
