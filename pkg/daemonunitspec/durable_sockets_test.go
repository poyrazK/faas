package daemonunitspec

import (
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/daemonunit"
)

// The durable sockets are opt-in (ADR-1580): the roles add Wants=/After= in a
// drop-in only where the socket is configured, with the address the daemon
// serves. A static Requires= would bind the unit's default address on every
// host, and the daemon refuses a socket it does not serve.
func TestDurableListenerSocketsAreOptIn(t *testing.T) {
	for name, unit := range map[string]daemonunit.Unit{
		"gatewayd-internal": UnitGatewaydInternal(),
		"schedd":            UnitSchedd(),
		"realtimed":         UnitRealtimed(),
		"profiled":          UnitProfiled(),
	} {
		for _, deps := range [][]string{unit.Requires, unit.Wants, unit.After} {
			if slices.ContainsFunc(deps, func(dep string) bool { return strings.HasSuffix(dep, ".socket") }) {
				t.Errorf("%s depends on a socket in its static unit: %v", name, deps)
			}
		}
	}
}
