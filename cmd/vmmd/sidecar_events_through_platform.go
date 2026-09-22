// Production SidecarEventEmitter (issue #463 / ADR-069 /
// ADR-071 / PR-C §3,§4).
//
// The cmd-level main wires this onto the
// FrameworkReadyReceiver's WithSidecarEmitter. It ships the
// guest-init audit envelopes to:
//
//   - pkg/events.Platform (the canonical "vmmd is the
//     corroborating-observation site" emitter, same
//     precedent as wake.boot_started / wake.readiness_200).
//
//   - <daemon>_sidecar_restart_total{app, sidecar} for the
//     restart envelope (PR-C §4). The counter lives on
//     wire.OpsMetrics — passed in by the cmd main loop,
//     nil-safe via the OpsMetrics.ObserveSidecarRestart
//     receiver.
//
//   - <daemon>_sidecar_health_transition_total{app, sidecar, status}
//     for the type=0x08 lifecycle envelope.
//
//   - the deployments.audit table via state.Store when
//     the wire envelope is init_failed (PR-C §3, AC #1
//     surface — failure_class: user_error audit row).
//
// The cmd main loop builds a single Platform up front
// (alongside the gRPC server's WithEvents and the VMM's
// own WithEvents); this emitter shares it so the audit
// fan-out is one process-local object. Tests substitute a
// fake that records envelopes in-memory.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// SidecarEventsThroughPlatform bundles the production
// dependencies. All fields may be nil; the receiver
// short-circuits a nil dependency so a missing wiring
// (default-local run) keeps the dispatch path
// observable-without-acting.
type SidecarEventsThroughPlatform struct {
	Platform *events.Platform
	Metrics  *wire.OpsMetrics
	Store    sidecarAuditStore // deployments-side audit row
	Failer   deploymentFailer  // PR-B AC #1: deployment-row flip on init_failed
	Log      *slog.Logger
	Now      func() time.Time
}

// sidecarAuditStore is the narrowed subset of state.Store
// the production emitter needs for the failure_class:
// user_error audit row (issue #463 / ADR-069 / ADR-071 /
// PR-C §3, AC #1). Defined as an interface so tests
// substitute a fake without spinning up a Postgres
// connection. The state.Store interface is compatible with
// this shape (mirrors events.Platform's auditStore
// precedent in pkg/events/platform.go).
type sidecarAuditStore interface {
	AppendEvent(ctx context.Context, actor, kind string, subject *string, payload []byte) error
}

// deploymentFailer (issue #463 / ADR-069 / PR-B AC #1) is
// the narrowed subset of state.Store the production emitter
// needs to flip the deployments row to status='failed' on a
// non-zero init sidecar exit. The id is the deployments.id
// UUID resolved from the live Instance at dispatch time
// (Manager.InstanceDeploymentIDAndAppID); the code is the
// RFC 7807 stable code (api.CodeInitSidecarFailed); the
// message is a customer-facing reason string formatted from
// the wire envelope (sidecar name + exit code + duration).
//
// Defined as a one-method interface so the cmd/vmmd unit
// test substitutes a fake without spinning up a Postgres
// connection. The state.Store interface is compatible with
// this shape (it carries SetDeploymentFailed at
// pkg/state/store.go:1197 — added by ADR-021 for the
// pre-build hook; PR-B AC #1 reuses the same primitive
// for the init sidecar exit path).
type deploymentFailer interface {
	SetDeploymentFailed(ctx context.Context, id, code, message string) (state.Deployment, error)
}

// sidecarFailureClassUserError is the AC #1 shibboleth for a
// failed init sidecar (issue #463 / ADR-069 / ADR-071 /
// PR-C §3). The literal is a closed enum the deployments UI
// greps on (matches state.FailureUserError in pkg/state/types.go
// for builderd pre-build hook failures). Pinned at package
// scope so the jsonb payload, the comment cross-ref, and any
// future log filter share the same value. A typo here would
// silently break the dashboard's red-traffic-light on a
// user-side init failure; the unit test
// TestSidecarInitFailedAuditPayload_FailureClass asserts the
// wire value.
const sidecarFailureClassUserError = "user_error"

// sidecarAuditEventActor is the actor stamped on every
// sidecar audit row. cmd/vmmd is the canonical emitter.
const sidecarAuditEventActor = "vmmd"

// EmitSidecarInitExit (issue #463 / ADR-069 / ADR-071 /
// PR-C §3): translate the type=0x02 wire envelope into a
// pkg/events.SidecarInitExit event. On init_failed also
// write the failure_class: user_error audit row to the
// deployments-side audit table — AC #1's surface for
// observability.
//
// Both writes are best-effort: pkg/events.Platform already
// logs + drops a failing AppendEvent, so we don't double-
// log here. The struct fields are derived from the wire
// envelope + the (instance, appID) passed in by the
// dispatcher (cmd/vmmd::dispatchSidecarInitExit).
//
// deploymentID (PR-B AC #1) is the deployments.id UUID the
// instance was woken for. On init_failed + non-empty
// deploymentID + non-nil Failer, the deployments row is
// flipped to status='failed' with error_code =
// api.CodeInitSidecarFailed. Empty deploymentID (legacy
// pre-PR-B wake that didn't carry the id on the wire) is
// tolerated — the audit row still lands so the dispatch is
// observable, but the deploy row stays untouched.
func (e *SidecarEventsThroughPlatform) EmitSidecarInitExit(
	ctx context.Context, instanceID, appID, deploymentID, wakeID string, wireEnv sidecarInitExitWire,
) {
	if e == nil {
		return
	}
	now := e.now()
	if e.Platform != nil {
		e.Platform.Emit(ctx, events.SidecarInitExit{
			EmitAt: now, WakeID: wakeID, AppID: appID,
			InstanceID:  instanceID,
			SidecarName: wireEnv.Sidecar,
			Status:      wireEnv.Status,
			ExitCode:    wireEnv.ExitCode,
			DurationMs:  wireEnv.DurationMs,
		})
	}
	// AC #1 surface: on init_failed, the deployment-side
	// audit table MUST see failure_class: user_error so
	// the deployments UI flags the deploy row. We don't
	// gate on a built pkg/events audit writer (the
	// canonical one is pkg/events.Platform's emit, which
	// also writes a row keyed on kind =
	// wake.sidecar_init_exit) — we surface a second
	// helper event so the dashboard distinguishes
	// init_ok (traffic light green) from init_failed
	// (red + the failure_class shibboleth).
	if wireEnv.Status == sidecarStatusInitFailed && e.Store != nil {
		payload := buildSidecarInitFailedPayload(instanceID, appID, wakeID, wireEnv)
		if err := e.Store.AppendEvent(ctx,
			sidecarAuditEventActor,
			"sidecar.init_failed",
			&instanceID,
			payload,
		); err != nil && e.Log != nil {
			e.Log.Warn("sidecar.init_failed audit write failed",
				"instance", instanceID, "sidecar", wireEnv.Sidecar, "err", err)
		}
	}
	// PR-B AC #1 (deploy-row flip): on init_failed with a
	// non-empty deployment_id, call SetDeploymentFailed so
	// the customer-visible deploy row reflects the failure
	// within the same hot-loop dispatch (no pg_notify bridge,
	// no apid round-trip). Best-effort: a SetDeploymentFailed
	// failure is logged + dropped — the audit row above is
	// the secondary observability surface. Empty deploymentID
	// (legacy wake) is skipped — the audit row is the only
	// observable signal in that case.
	if wireEnv.Status == sidecarStatusInitFailed && deploymentID != "" && e.Failer != nil {
		msg := fmt.Sprintf("init sidecar %q exited %d after %dms",
			wireEnv.Sidecar, wireEnv.ExitCode, wireEnv.DurationMs)
		if _, err := e.Failer.SetDeploymentFailed(ctx, deploymentID, api.CodeInitSidecarFailed, msg); err != nil && e.Log != nil {
			e.Log.Warn("sidecar.init_failed deployment-row flip failed",
				"instance", instanceID, "deployment_id", deploymentID,
				"sidecar", wireEnv.Sidecar, "err", err)
		}
	}
}

// EmitSidecarRestart (issue #463 / ADR-069 / ADR-071 /
// PR-C §4): translate the type=0x03 wire envelope into a
// pkg/events.SidecarRestart event AND increment the
// <daemon>_sidecar_restart_total{app, sidecar} counter.
// The counter is the §12 dashboard panel the operator
// queries to find crash-looping essential sidecars; the
// event is the per-cycle audit row.
func (e *SidecarEventsThroughPlatform) EmitSidecarRestart(
	ctx context.Context, instanceID, appID, wakeID string, wireEnv sidecarRestartWire,
) {
	if e == nil {
		return
	}
	if e.Platform != nil {
		e.Platform.Emit(ctx, events.SidecarRestart{
			EmitAt: nowOrDefault(e.Now),
			WakeID: wakeID, AppID: appID,
			InstanceID:  instanceID,
			SidecarName: wireEnv.Sidecar,
			Attempt:     wireEnv.Attempt,
			// PreviousExitCode is not on the wire in
			// PR-C; left at the zero value (-1) so the
			// audit row says "exit_code not surfaced"
			// rather than fabricating a number.
		})
	}
	// Counter increment is on the OpsMetrics — the same
	// struct that owns every other daemon-level counter.
	// Nil-safe via the OpsMetrics receiver.
	e.Metrics.ObserveSidecarRestart(appID, wireEnv.Sidecar)
}

// EmitSidecarHealth translates a guest-init lifecycle transition into the
// canonical event stream and transition counter. Both sinks are nil-safe so
// local vmmd runs can still receive the wire without requiring a database or
// metrics registry.
func (e *SidecarEventsThroughPlatform) EmitSidecarHealth(
	ctx context.Context, instanceID, appID, wakeID string, wireEnv sidecarHealthWire,
) {
	if e == nil {
		return
	}
	if e.Platform != nil {
		e.Platform.Emit(ctx, events.SidecarHealth{
			EmitAt: nowOrDefault(e.Now), WakeID: wakeID, AppID: appID,
			InstanceID: instanceID, SidecarName: wireEnv.Sidecar,
			Status: wireEnv.Status, Reason: wireEnv.Reason,
		})
	}
	e.Metrics.ObserveSidecarHealth(appID, wireEnv.Sidecar, wireEnv.Status)
}

// nowOrDefault returns the receiver's clock, falling back
// to time.Now so a single direct call doesn't depend on
// the Now field being injected.
func nowOrDefault(now func() time.Time) time.Time {
	if now == nil {
		return time.Now()
	}
	return now()
}

// now is the receiver's wall clock. Matches the
// OpsMetrics.Now pattern: nil = time.Now so production
// code doesn't have to wire the clock unless it has a
// reason to (see pkg/wire/metrics.go).
func (e *SidecarEventsThroughPlatform) now() time.Time {
	return nowOrDefault(e.Now)
}

// buildSidecarInitFailedPayload assembles the
// failure_class: user_error audit row body (AC #1, PR-C
// §3). Schema matches the audit table's jsonb column
// shape — a structure with failure_class set to the
// shibboleth value plus the carrier fields. Kept as a
// free function so a unit test in cmd/vmmd asserts the
// failure_class shibboleth is "user_error" exactly (and
// never drift-prone aliases).
func buildSidecarInitFailedPayload(instanceID, appID, wakeID string, env sidecarInitExitWire) []byte {
	// Marshal by hand so a future audit schema drift
	// (key rename, additional required field) is caught
	// by the cmd/vmmd unit test
	// TestSidecarInitFailedAuditPayload_FailureClass.
	type payload struct {
		FailureClass string `json:"failure_class"`
		Sidecar      string `json:"sidecar"`
		InstanceID   string `json:"instance_id"`
		AppID        string `json:"app_id"`
		WakeID       string `json:"wake_id"`
		ExitCode     int    `json:"exit_code"`
		DurationMs   int64  `json:"duration_ms"`
	}
	body := payload{
		FailureClass: sidecarFailureClassUserError, // AC #1 shibboleth — never inline-alias.
		Sidecar:      env.Sidecar,
		InstanceID:   instanceID,
		AppID:        appID,
		WakeID:       wakeID,
		ExitCode:     env.ExitCode,
		DurationMs:   env.DurationMs,
	}
	out, _ := json.Marshal(body)
	return out
}
