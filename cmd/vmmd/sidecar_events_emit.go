// Sidecar-event emission seam (issue #463 / ADR-069 /
// ADR-071 / PR-C).
//
// The FrameworkReadyReceiver is shared across all guests and
// needs to dispatch three non-framework-ready event classes
// (sidecar_init_exit, sidecar_restart, sidecar_health) to a sink the cmd main
// loop owns — pkg/events.Platform with a real state.Store
// AppendEvent, plus the vmmd_sidecar_restart_total counter
// (PR-C §4). The receiver itself cannot own those because
// (a) state.Store is constructed in cmd main, not in this
// file, and (b) the dispatch needs the app_id resolved from
// the live instance map (Manager-owned).
//
// SidecarEventEmitter is the minimal interface the receiver
// requires. Production wires EmitterThroughPlatform (see
// below) which wraps the canonical pkg/events.Platform;
// tests pass a fake that records calls in-memory. The
// receiver's emit envelope call uses the per-instance app_id
// resolved from Manager.live[instance].AppID — same source
// MarkInstanceFrameworkReady reads — so the audit and the
// counter see the same identity.
//
// No build tag: the wire types and interface are
// platform-neutral (no vsock / unix syscall), so the same
// source compiles on linux + darwin + Windows. The
// dispatcher that consumes them, however, is in
// cmd/vmmd/framework_ready_recv.go (linux-only).

package main

import "context"

// sidecarInitExitWire is the JSON-parsed payload of a
// type=0x02 DGRAM (issue #463 / ADR-069 / ADR-071 / PR-C §3).
// Field tags MUST match guest/init/sidecar_events_proxy_linux.go
// ::sidecarInitExitEnvelope — the host and guest are wire-
// adjacent peers. A rename here requires a parallel rename in
// the guest proxy.
type sidecarInitExitWire struct {
	Sidecar    string `json:"sidecar"`
	Status     string `json:"status"` // "init_ok" | "init_failed"
	ExitCode   int    `json:"exit_code"`
	DurationMs int64  `json:"duration_ms"`
}

// sidecarRestartWire is the JSON-parsed payload of a
// type=0x03 DGRAM (PR-C §4). Attempt is the 1-indexed
// supervisor restart number (1 = first restart).
type sidecarRestartWire struct {
	Sidecar string `json:"sidecar"`
	Attempt int    `json:"attempt"`
}

// sidecarHealthWire is the JSON-parsed payload of a type=0x08 lifecycle
// transition. Status is one of starting, healthy, unhealthy, restarting, or
// failed; reason is bounded diagnostic context from guest-init.
type sidecarHealthWire struct {
	Sidecar string `json:"sidecar"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
}

// SidecarEventEmitter is the dispatch sink. The receiver
// calls EmitSidecarInitExit for type=0x02 datagrams,
// EmitSidecarRestart for type=0x03, and EmitSidecarHealth for
// type=0x08. Production wires
// EmitterThroughPlatform so a real pkg/events.Platform is
// the audit destination; tests wire an in-memory fake so the
// dispatch path is unit-testable without spinning up a real
// state.Store or a Prometheus registry.
//
// EmitSidecarInitExit carries deploymentID (issue #463 /
// ADR-069 / PR-B AC #1) so the production emitter can flip
// the deployments row to status='failed' on init_failed
// without crossing a process boundary (vmmd owns the
// per-instance live map, so the deployment_id is resolvable
// at the dispatch site in O(1) — no pg_notify bridge). Empty
// deployment_id is tolerated: a legacy wake without a
// deployment_id on the wire leaves the deploy row untouched.
type SidecarEventEmitter interface {
	EmitSidecarInitExit(ctx context.Context, instanceID, appID, deploymentID, wakeID string, wire sidecarInitExitWire)
	EmitSidecarRestart(ctx context.Context, instanceID, appID, wakeID string, wire sidecarRestartWire)
	EmitSidecarHealth(ctx context.Context, instanceID, appID, wakeID string, wire sidecarHealthWire)
}

// noopSidecarEventEmitter is the zero-value default used
// when the cmd main loop hasn't wired a real emitter yet
// (e.g. during local dev without a state.Store). Both
// methods are no-ops; the receiver never blocks on a
// missing emitter because the audit is best-effort
// (pkg/events.Platform's contract).
type noopSidecarEventEmitter struct{}

func (noopSidecarEventEmitter) EmitSidecarInitExit(context.Context, string, string, string, string, sidecarInitExitWire) {
}
func (noopSidecarEventEmitter) EmitSidecarRestart(context.Context, string, string, string, sidecarRestartWire) {
}
func (noopSidecarEventEmitter) EmitSidecarHealth(context.Context, string, string, string, sidecarHealthWire) {
}

// sidecarStatusInitOK and sidecarStatusInitFailed are the
// closed-enum wire values for the sidecarInitExitWire.Status
// field (issue #463 / ADR-069 / PR-B AC #1). They live here
// (no build tag) so the platform-neutral compiler — which
// the vmmd unit test runs on darwin — can resolve them. The
// linux-tagged framework_ready_recv.go dispatcher also
// consumes them. The Status field comment on
// sidecarInitExitWire enumerates the values; keep them in
// sync if a new value is added.
const (
	sidecarStatusInitOK     = "init_ok"
	sidecarStatusInitFailed = "init_failed"
	sidecarHealthStarting   = "starting"
	sidecarHealthHealthy    = "healthy"
	sidecarHealthUnhealthy  = "unhealthy"
	sidecarHealthRestarting = "restarting"
	sidecarHealthFailed     = "failed"
)
