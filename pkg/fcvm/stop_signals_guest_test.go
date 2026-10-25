package fcvm

import (
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
