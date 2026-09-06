// Package sched — wake-boot trigger enum (ADR-123).
//
// This file is the single source of truth for the closed `trigger`
// enum that stamps every `wake.boot_started` and `wake.boot_completed`
// event row with the *reason* the instance started. The seven callers
// of `Engine.admitAndDispatch` (gateway, floor per-app, floor
// per-deployment, scaleup, targets, cron schedule tick, cron fire-now,
// legacy meterd) each pass one of the constants below. The dashboard,
// CLI, and `apid` wake-timeline endpoint all render the value as-is.
package sched

const (
	// TriggerGateway — a request-driven cold-boot from
	// cmd/gatewayd-internal Edge. This is the highest-priority wake:
	// the customer is waiting on the request and the latency budget
	// from §6.3 applies.
	TriggerGateway = "gateway"

	// TriggerFloor — pkg/sched/floor per-app tick (legacy pre-#555).
	// Stamped on the cold-start drift repair path.
	TriggerFloor = "floor"

	// TriggerFloorDep — pkg/sched/floor per-deployment sweep
	// (issue #555 / ADR-064 §"Decision"). One of two floor paths.
	TriggerFloorDep = "floor.deployment"

	// TriggerScaleup — pkg/sched/scaleup target-driven (CPU / RPS
	// axis). Distinct from TriggerTargets below — see
	// pkg/sched/scaleup/trigger.go:130 (the `concurrent_requests`
	// axis lives in pkg/sched/targets).
	TriggerScaleup = "scaleup"

	// TriggerTargets — pkg/sched/targets concurrent-requests axis.
	// Distinct from TriggerScaleup so an operator can disambiguate
	// a CPU-driven wake from a concurrent-requests-driven wake.
	TriggerTargets = "targets"

	// TriggerCronSched — pkg/sched/loop 60s tick path. Translated
	// from the internal CronDispatchTrigger="schedule" value at
	// pkg/sched/loop.go:2246 — the cron dispatch trigger is an
	// internal type, this is the external wire enum.
	TriggerCronSched = "cron.schedule"

	// TriggerCronManual — POST /v1/crons/{id}/run (ADR-090). Operator
	// initiated. Translated from CronDispatchTrigger="manual" at the
	// same call site.
	TriggerCronManual = "cron.manual"

	// TriggerMeterd — legacy Engine.Wake path. No current caller
	// dials it; stamped defensively for backwards compatibility
	// with the engine.go:1050 comment ("legacy fast path used by
	// meterd's per-minute sampler + cron firings").
	TriggerMeterd = "meterd"

	// TriggerMirror — best-effort asynchronous preview/mirror admission.
	TriggerMirror = "mirror"

	// TriggerServiceReplica — replacement admission for a service deployment
	// whose live count fell below its desired replica count.
	TriggerServiceReplica = "service.replica"

	// TriggerAppRestart — explicit customer-requested app restart. The
	// restart parks the current instance(s), captures a fresh snapshot, and
	// wakes one replacement instance.
	TriggerAppRestart = "app.restart"
)
