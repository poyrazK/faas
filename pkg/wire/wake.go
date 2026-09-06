package wire

// Wire-level constants for the wake-tier transparency header (UX spec
// §6, docs/cold-wake.md). The header name is part of the published
// customer contract — it's documented in docs/cold-wake.md and
// docs/faas_ux_spec.md, and tested in pkg/gateway/handler_test.go and
// cmd/gregale/wake_probe_test.go. Do not rename it during a branding
// sweep; the tripwire TestLintTripwire_NoLiteralWakeHeaderOutsidePkgWire
// in cmd/gregale/lint_tripwires_test.go will fail otherwise.
//
// The Gregale rename kept the `x-faas-` prefix on purpose — the wire
// header outlives branding because customers depend on it for devtools
// debugging and the SDK/parity requirements of downstream tooling.
const (
	// WakeHeader is the response header the gateway stamps on every
	// routed app response so clients can distinguish hot traffic from
	// a snapshot restore or a cold boot.
	WakeHeader = "x-faas-wake"

	// HotWakeValue marks a response served by an already-running
	// instance without a new wake admission.
	HotWakeValue = "hot"
	// RestoredWakeValue marks a response whose request admitted an
	// instance from a usable snapshot.
	RestoredWakeValue = "restored"
	// ColdWakeValue marks a response whose request admitted a fresh
	// cold-booted instance. The value is retained for CLI compatibility.
	ColdWakeValue = "cold"
)
