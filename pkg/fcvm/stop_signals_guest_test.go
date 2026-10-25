package fcvm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Production-us rc.294: scaling h14-wk in signalled the Firecracker host
// process, so the worker never saw its stop signal and the drain audit read
// "completed" 142 ms later. Workers now take the guest control-port path that
// jobs use (Hunt 14 H14-59).
func TestStopSignalsGuestForJobsAndWorkers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		inst   Instance
		frozen bool
		want   bool
	}{
		{"job", Instance{IsJob: true}, false, true},
		{"declared worker", Instance{ExecutionMode: api.ExecutionModeWorker}, false, true},
		{"characterized worker", Instance{Characterization: api.CharacterizationReport{ObservedClass: api.ExecutionModeWorker}}, false, true},
		{"frozen worker", Instance{ExecutionMode: api.ExecutionModeWorker}, true, false},
		{"request app", Instance{}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stopSignalsGuest(&tc.inst, tc.frozen); got != tc.want {
				t.Fatalf("stopSignalsGuest = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestConsoleShowsGuestStopped pins how SignalJob recognizes a guest that
// stopped on its own after a graceful stop (Hunt 14 H14-59): guest-init's
// exit as PID 1, with its exit code, or a power-off halt.
func TestConsoleShowsGuestStopped(t *testing.T) {
	for _, tc := range []struct {
		name     string
		console  string
		stopped  bool
		exitCode int32
	}{
		{name: "running guest", console: "[    1.2] guest-init: stage pivot\nworker tick\n"},
		{name: "drained worker", console: "worker drained\n[   30.1] Kernel panic - not syncing: Attempted to kill init! exitcode=0x00000000\n[   30.1] Rebooting in 1 seconds..\n", stopped: true},
		{name: "guest-init failed", console: "[   12.0] Kernel panic - not syncing: Attempted to kill init! exitcode=0x00000100\n", stopped: true, exitCode: 1},
		{name: "job power-off", console: "[    9.9] reboot: Power off not available: System halted instead\n", stopped: true},
		{name: "panic without code", console: "[    3.0] Kernel panic - not syncing: Attempted to kill init!\n", stopped: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "console.log")
			if err := os.WriteFile(path, []byte(tc.console), 0o600); err != nil {
				t.Fatal(err)
			}
			stopped, code := consoleShowsGuestStopped(path)
			if stopped != tc.stopped || code != tc.exitCode {
				t.Fatalf("consoleShowsGuestStopped = (%v, %d), want (%v, %d)", stopped, code, tc.stopped, tc.exitCode)
			}
		})
	}
	if stopped, _ := consoleShowsGuestStopped(filepath.Join(t.TempDir(), "missing.log")); stopped {
		t.Fatal("a missing console reported a stopped guest")
	}
}
