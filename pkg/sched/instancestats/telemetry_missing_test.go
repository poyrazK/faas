package instancestats

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// spec: §12
// H10-138: a booting row has no VM in vmmd's stats yet, so it must not count
// as a partial stats error; a running row the stream left out still does.
func TestTelemetryMissingNodesSkipsBootingInstances(t *testing.T) {
	tests := []struct {
		name  string
		state state.State
		want  bool
	}{
		{"cold booting", state.StateColdBooting, false},
		{"waking", state.StateWaking, false},
		{"running", state.StateRunning, true},
		{"draining", state.StateDraining, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instances := []state.Instance{
				{ID: "reported", NodeID: "node-a", State: string(state.StateRunning)},
				{ID: "absent", NodeID: "node-b", State: string(tt.state)},
			}
			rows := []InstanceStat{{InstanceID: "reported"}}
			missing := telemetryMissingNodes(instances, rows)
			if _, got := missing["node-b"]; got != tt.want {
				t.Fatalf("node-b missing = %v, want %v (%v)", got, tt.want, missing)
			}
			if _, got := missing["node-a"]; got {
				t.Fatalf("node-a reported its instance but was counted missing")
			}
		})
	}
}
