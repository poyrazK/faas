package daemonunitspec

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/daemonunit"
)

// Daemons other services dial over a unix socket restart behind a systemd
// socket that keeps the listener open (docs/adr/1580-zero-downtime-daemon-restarts.md).
func TestDurableListenerSocketsAreRequiredAndOrdered(t *testing.T) {
	tests := []struct {
		name   string
		unit   daemonunit.Unit
		socket string
	}{
		{"gatewayd-internal", UnitGatewaydInternal(), "faas-gatewayd-internal.socket"},
		{"schedd", UnitSchedd(), "faas-schedd.socket"},
		{"realtimed", UnitRealtimed(), "faas-realtimed.socket"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !slices.Contains(tt.unit.Requires, tt.socket) {
				t.Errorf("Requires = %v, want %s", tt.unit.Requires, tt.socket)
			}
			if !slices.Contains(tt.unit.After, tt.socket) {
				t.Errorf("After = %v, want %s", tt.unit.After, tt.socket)
			}
		})
	}
}

// profiled reuses realtimed's unit shape but binds its own socket.
func TestProfiledDoesNotRequireRealtimedSocket(t *testing.T) {
	u := UnitProfiled()
	if slices.Contains(u.Requires, "faas-realtimed.socket") || slices.Contains(u.After, "faas-realtimed.socket") {
		t.Fatalf("profiled depends on realtimed's socket: After=%v Requires=%v", u.After, u.Requires)
	}
}
