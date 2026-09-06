// wake_test.go — fill pkg/wire coverage of the wake-tier wire
// header constants in wake.go. The constants themselves are at 0%
// on the baseline because every production reference to them goes
// through the gatewayd-internal / schedd path (covered indirectly
// at the integration level) — no unit test currently imports the
// literal to assert it stays stable.
//
// These two constants are load-bearing:
//   - WakeHeader is the response header the gateway stamps on every
//     routed app response so clients can distinguish hot, restored,
//     and cold traffic. The header name is part of the
//     published customer contract (docs/cold-wake.md,
//     docs/faas_ux_spec.md) and tested in pkg/gateway/handler_test.go
//     and cmd/gregale/wake_probe_test.go. Renaming it would break
//     the tripwire TestLintTripwire_NoLiteralWakeHeaderOutsidePkgWire
//     in cmd/gregale/lint_tripwires_test.go.
//   - the three values form the closed wire vocabulary. ColdWakeValue
//     remains byte-for-byte compatible with the existing CLI probe.

package wire_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/wire"
)

// TestWakeHeader_Stable pins the literal wire name. The header is
// part of the published customer contract; renaming it is a breaking
// change for every CLI consumer and dashboard probe.
func TestWakeHeader_Stable(t *testing.T) {
	if got := wire.WakeHeader; got != "x-faas-wake" {
		t.Errorf("WakeHeader = %q, want x-faas-wake", got)
	}
}

// TestColdWakeValue_Stable pins the cold-marker literal. The
// probeWakeState matcher in cmd/gregale/wake_probe.go compares
// against this value byte-for-byte; any drift here silently
// downgrades "cold" responses to "warm" in the CLI status line.
func TestColdWakeValue_Stable(t *testing.T) {
	if got := wire.ColdWakeValue; got != "cold" {
		t.Errorf("ColdWakeValue = %q, want cold", got)
	}
}

func TestWakeTierValues_Stable(t *testing.T) {
	if got := wire.HotWakeValue; got != "hot" {
		t.Errorf("HotWakeValue = %q, want hot", got)
	}
	if got := wire.RestoredWakeValue; got != "restored" {
		t.Errorf("RestoredWakeValue = %q, want restored", got)
	}
}
