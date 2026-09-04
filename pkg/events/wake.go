// wake.go — canonical wake-timeline vocabulary (issue #517 / PR-C, AC2).
//
// One row per wake phase. Each struct implements WakeEvent so the
// Platform.Emit driver (platform.go) writes a single, typed row that
// the customer-facing GET /v1/apps/{slug}/wakes/{wake_id}/timeline
// endpoint can read back without a hand-rolled jsonb filter. The
// payload field names are the same wire names the endpoint surfaces
// in the `data` object — rename here, rename in the docs, rename in
// the SDK.
//
// Naming follows the spec §5.1 audit-event taxonomy prefixed with
// `wake.` so a single §12 panel selector (`kind_prefix=wake.`)
// captures the whole wake lifecycle. The 13 kinds cover the full
// wake envelope (queue → admit → boot → readiness → proxy → park /
// build / deploy), and the legacy bare names (`state_transition`,
// `wake_boot_error`, `park_snapshot_error`, `watchdog_timeout`,
// `app.characterized`, `cron.fired`, `reaper_scale_down`,
// `stateless.advisory`, `app.signed_image_accepted`,
// `app.signature_missing`, `app.signature_invalid`) stay
// unchanged — PR-C is additive, see ADR-064 §"Compatibility".
//
// Why struct types and not `kind string + data map[string]any`:
// the Go compiler is the cheapest schema validator. A new field
// added to a payload struct lights up call sites that need to
// update; a new key in a map literal ships silently.
package events

import "time"

// Wake-timeline vocabulary (issue #517 / PR-C, ADR-064). Constants
// are the canonical kind strings written to events.kind. The
// commented payload schemas below mirror the typed struct fields
// exactly — keep them in sync when adding a field.
const (
	// WakeQueueAccepted — schedd accepted the wake into the queue
	// (regular Wake RPC or cron dispatch boundary). Payload:
	// {wake_id, app_id, request_id}. (queue_wait_ms was rejected
	// from the schema — see the QueueAccepted struct doc-comment
	// for the rationale.)
	WakeQueueAccepted = "wake.queue_accepted"
	// WakeAdmitted — schedd admitted the wake past the per-app
	// concurrency gate. Payload: {wake_id, app_id, request_id,
	// account_id, plan, admitted_at}.
	WakeAdmitted = "wake.admitted"
	// WakeBootStarted — schedd started a boot (snapshot restore
	// or cold boot). Mirrored by vmmd on CreateFromSnapshot /
	// Wake. Payload: {wake_id, app_id, instance_id, node_id,
	// method, requested_at}.
	WakeBootStarted = "wake.boot_started"
	// WakeRestoreBreakdown — vmmd's detailed snapshot-restore phases.
	// Payload: {wake_id, app_id, instance_id, chroot_ms,
	// materialize_mem_ms, materialize_vmstate_ms, resolve_images_ms,
	// stage_drives_ms, stage_snapshot_ms, helper_ms, start_jailer_ms,
	// bind_tun_ms, load_snapshot_ms, resume_hook_ms, wait_ready_ms,
	// total_ms}. Emitted after a successful restore so operators can
	// identify which part of the vmmd restore window exceeded budget.
	WakeRestoreBreakdown = "wake.restore_breakdown"
	// WakeBootCompleted — schedd post-RecordRuntime; the instance
	// is now RUNNING. Sibling of the existing `app.characterized`
	// audit row (different timings — `app.characterized` follows
	// after the first request lands, this fires on RUNNING).
	// Payload: {wake_id, app_id, instance_id, node_id, method,
	// started_at, completed_at}.
	WakeBootCompleted = "wake.boot_completed"
	// WakeBootFailed — boot path failed. Sibling of the legacy
	// `wake_boot_error` audit row. Payload: {wake_id, app_id,
	// instance_id, node_id, method, reason, failed_at}.
	WakeBootFailed = "wake.boot_failed"
	// WakeReadiness200 — vmmd's waitReady saw its first 2xx probe
	// (pkg/fcvm/vmm.go). The most load-bearing new code path: today
	// there is no success-side log on readiness_200, and the §12
	// wake-latency panel derives its p50/p95/p99 from this row.
	// Dedupe guard: the first 2xx only; defer at the
	// return-point. Payload: {wake_id, app_id, instance_id,
	// node_id, healthcheck_path, probe_count, elapsed_ms}.
	WakeReadiness200 = "wake.readiness_200"
	// WakeProxyFirstByte — gatewayd-internal received the first response
	// byte from the woken instance (httptrace.GotFirstResponseByte
	// callback). Payload: {wake_id, app_id, request_id,
	// instance_id, node_id, latency_ms}.
	WakeProxyFirstByte = "wake.proxy_first_byte"
	// WakeParkStarted — schedd transitioning the instance to
	// SNAPSHOTTING. Payload: {wake_id, app_id, instance_id,
	// node_id, started_at}.
	WakeParkStarted = "wake.park_started"
	// WakeParkCompleted — snapshot succeeded; instance is PARKED.
	// Dual of WakeParkStarted. Payload: {wake_id, app_id,
	// instance_id, node_id, started_at, completed_at, snapshot_id}.
	WakeParkCompleted = "wake.park_completed"
	// WakeStalled — watchdog path: instance hasn't transitioned
	// states within the deadline. Sibling of the legacy
	// `watchdog_timeout` audit row — both fire, joined by
	// wake_id. Payload: {wake_id, app_id, instance_id, node_id,
	// reason, at}.
	WakeStalled = "wake.stalled"
	// WakeTailFailed (issue #667 / ADR-078, PR 4) — a waitUntil
	// task was unable to drain before the runtime ceiling. The
	// reason is the closed enum: "timeout" (per-plan TailTimeoutS
	// exceeded), "handler_error" (the task threw or rejected),
	// "forced_at_park" (the snapshotAndPark 5s watchdog fired
	// while the runner was still draining). Payload: {wake_id,
	// app_id, instance_id, reason, at}.
	WakeTailFailed = "wake.tail_failed"
	// InstanceLivenessFailed — vmmd poll goroutine declared the
	// VM wedged after N consecutive liveness-probe failures
	// (issue #554 / ADR-078). The state-machine transition is
	// RUNNING → STOPPED with reason='liveness_failed'; the
	// destruction happens on schedd (the only writer to
	// instances). Payload: {instance_id, app_id, deployment_id,
	// reason}.
	//
	// The closed reason set mirrors the vmmd poll goroutine's
	// histogram outcome labels:
	//
	//   liveness_n_consecutive  — counter reached per-plan N
	//   liveness_timeout        — guest-init HTTP probe timed out
	//   liveness_conn_refused   — guest-init's listener not ready
	//   liveness_conn_err       — wire-shape or syscall error
	//   liveness_non_200        — probe returned 4xx/5xx
	InstanceLivenessFailed = "instances.liveness_failed"
	// InstanceLivenessRestarted — convenience audit row mirroring
	// the LivenessFailed one with a friendlier kind for the
	// customer-facing timeline. Emitted by the same code path as
	// InstanceLivenessFailed but with the explicit "restarted"
	// wording so the dashboard's "liveness: restart count (5m)"
	// panel can filter on instances.liveness_restarted without
	// also picking up the conn_err / timeout noise.
	InstanceLivenessRestarted = "instances.liveness_restarted"
	// InstanceWorkloadOOMFailed (Cluster C / ADR-121) — the
	// guest-init cgroup.events listener observed an oom_kill on
	// the per-VM cgroup v2 leaf and the schedd engine (the only
	// owner of the instance state machine, spec §6.2) destroyed
	// the instance after stamping CodeAppRuntimeOOM. Distinct
	// from InstanceLivenessFailed: the workload is dead, not
	// the liveness probe. The state-machine transition is
	// RUNNING → STOPPED with reason='workload_oom_failed'. Payload:
	// {instance_id, app_id, deployment_id, peak_mb, plan_mb}.
	//
	// The peak_mb / plan_mb observed values are the same numbers
	// templated into the whycopy Observed closure (pkg/whycopy).
	// The dashboard's "OOM kills by deployment (5m)" panel pairs
	// this with vmm_workload_oom_kills_total from pkg/wire.
	InstanceWorkloadOOMFailed = "instances.workload_oom_failed"
	// InstanceParkedLivenessExhausted — the per-deployment
	// sliding window in pkg/sched/liveness_window.go reached 3
	// restarts in 300s; Engine.ParkDeployment parked the
	// deployment with reason='liveness_exhausted'. Mirrors
	// instances.parked_min_instances_released (issue #557 / ADR-072)
	// for the floor-release park path. Payload: {app_id, deployment_id,
	// parked_reason}.
	InstanceParkedLivenessExhausted = "instances.parked_liveness_exhausted"
	// WakeBuildSucceeded — builderd finished a build (ADR-030).
	// Payload: {app_id, deployment_id, image_digest, duration_ms}.
	WakeBuildSucceeded = "wake.build_succeeded"
	// WakeBuildFailed — builderd build failed. Payload: {app_id,
	// deployment_id, image_digest, reason}.
	WakeBuildFailed = "wake.build_failed"
	// WakeDeployFailed — apid's deploy rollback path. Payload:
	// {app_id, deployment_id, reason}.
	WakeDeployFailed = "wake.deploy_failed"
	// WakeSidecarInitExit — guest-init's runWorkloads orchestrator
	// recorded a type=="init" sidecar's terminal exit (issue #463 /
	// ADR-069 / PR-B). Status is the closed enum: "init_ok" (exit
	// code 0) or "init_failed" (non-zero exit). A failed init fails
	// the deploy with failure_class: user_error (AC #1). Payload:
	// {wake_id, app_id, instance_id, sidecar_name, status, exit_code,
	// duration_ms}.
	WakeSidecarInitExit = "wake.sidecar_init_exit"
	// WakeSidecarRestart — guest-init's runWorkloads orchestrator
	// restarted an essential sidecar after a crash (issue #463 /
	// ADR-069 / PR-B). Non-essential sidecars do NOT emit this —
	// their crash is logged on the orchestrator's stderr and the
	// supervisor returns immediately (Max=0 policy, AC #2). Payload:
	// {wake_id, app_id, instance_id, sidecar_name, attempt,
	// previous_exit_code}.
	WakeSidecarRestart = "wake.sidecar_restart"
)

// WakeEvent is the contract pkg/events.Platform.Emit consumes. The
// concrete payload structs below (QueueAccepted, Admitted,
// BootStarted, etc.) are the only emitters — callers MUST instantiate
// one rather than rolling their own struct. The one-method interface
// makes the row schema the compiler's problem, not the daemon's.
type WakeEvent interface {
	// Kind is the events.kind string value (e.g. "wake.boot_started").
	Kind() string
	// At is the wall-clock timestamp the row carries. schedd's
	// transitionWithKind precedent sets this off the engine clock
	// so the timeline reads forward even when the goroutine that
	// emits the row is delayed.
	At() time.Time
	// Subject is the optional accounts.id (UUID) the row is
	// attributed to. nil for system-level events (e.g. cron.fired
	// when account resolution failed earlier in the path). Matches
	// the pkg/audit.Auditor.Emit contract.
	Subject() *string
	// Payload is the typed struct marshaled to jsonb on the
	// events.data column. The Platform driver calls
	// json.Marshal; the struct fields ARE the JSON keys.
	Payload() map[string]any
}

// addrString is a tiny helper so payload structs can express a
// subject pointer without forcing every constructor to write
// `if x != "" { return &x }`. Used by the typed structs below.
func addrString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// QueueAccepted — schedd accepted the wake into the queue. Fires
// from both the regular Wake RPC (pkg/sched/engine.go) and the
// cron dispatch boundary (pkg/sched/loop.go) so the customer-facing
// timeline surfaces both paths joined by request_id.
//
// queue_wait_ms is intentionally NOT part of the payload schema:
// schedd's Wake RPC entry doesn't carry a server-side accepted_at
// stamp, and adding one to derive the queue wait is a wire-shape
// change deferred to a follow-up PR. The schedule latency is
// observable from the existing instances.started_at ↔ events.at
// join (the boot_started.RequestedAt field carries the same
// timestamp once the engine captures it — see pkg/sched/engine.go
// bootInput.startedAt). A future PR will wire an accepted_at
// through the gRPC metadata envelope and add the field here.
type QueueAccepted struct {
	EmitAt    time.Time
	WakeID    string
	AppID     string
	RequestID string
}

func (e QueueAccepted) Kind() string     { return WakeQueueAccepted }
func (e QueueAccepted) At() time.Time    { return e.EmitAt }
func (e QueueAccepted) Subject() *string { return nil }
func (e QueueAccepted) Payload() map[string]any {
	return map[string]any{
		"wake_id":    e.WakeID,
		"app_id":     e.AppID,
		"request_id": e.RequestID,
	}
}

// Admitted — schedd passed the per-app concurrency gate. The
// account_id + plan fields surface in the payload so the operator
// view can group wake latency by plan (the §12 dashboard's wake
// latency panel groups by plan to surface a Hobby-tier degradation).
type Admitted struct {
	EmitAt    time.Time
	WakeID    string
	AppID     string
	RequestID string
	AccountID string
	Plan      string
}

func (e Admitted) Kind() string     { return WakeAdmitted }
func (e Admitted) At() time.Time    { return e.EmitAt }
func (e Admitted) Subject() *string { return addrString(e.AccountID) }
func (e Admitted) Payload() map[string]any {
	return map[string]any{
		"wake_id":    e.WakeID,
		"app_id":     e.AppID,
		"request_id": e.RequestID,
		"account_id": e.AccountID,
		"plan":       e.Plan,
	}
}

// BootStarted — schedd started a boot. method is the scheddpb
// wake method (WAKE_RESTORE / WAKE_COLD_BOOT) as a string so the
// payload is self-describing without dragging the protobuf package
// into the wire shape.
//
// ADR-123 adds three fields so the wake timeline answers "why did
// this wake happen, what was queueing, what was the per-app
// concurrency?" without joining the legacy cron.fired / floor.wake
// audit rows.
//
//   - Trigger is the closed enum (see pkg/sched/triggers.go) for the
//     caller that drove the wake — "gateway" for a request-driven
//     cold-boot, "floor" for the per-app floor tick, etc. Empty on
//     the Phase-1 fast-path return where an existing RUNNING
//     instance was reused (the trigger field on the *original* boot
//     row stays; this row's Payload() omits the "trigger" key
//     because the row itself is not the row the customer timeline
//     surfaces).
//
//   - QueuedCount is the wake queue depth at admit time. Schedd
//     reads it off e.ledger.Concurrency(app.ID) just before the
//     admit gate runs (the same reading the gate consults). The
//     gateway-initiated path (Trigger == "gateway") also uses this
//     schedd-side value, NOT WakeGate.InflightWaiters — see ADR-123
//     §"Decision 2" for the rationale (the gateway-side count
//     reflects "currently-waiting request count" not
//     "siblings-admitted").
//
//   - ConcurrencyAtAdmit is the same ledger.Concurrency reading, so
//     a downstream reader does not need to know which trigger path
//     was used to source the value. Always populated (0 is the cold
//     start case).
//
//   - AtCapacity (PR-A) is true when this wake was admitted at the
//     plan's per-app MaxConcurrency ceiling — i.e. the pre-admit
//     ledger reading was maxConc-1 and the post-admit reading became
//     maxConc. Closes the user's "2/2 at concurrency limit" reference
//     line. NOTE: this is emitted ONLY on the admit path; rejections
//     do not produce a BootStarted row. BootCompleted intentionally
//     does NOT carry AtCapacity — it's an admit-time concept, and the
//     boot_completed row reflects the post-RecordRuntime state where
//     the cap concept is stale.
type BootStarted struct {
	EmitAt             time.Time
	WakeID             string
	AppID              string
	InstanceID         string
	NodeID             string
	Method             string
	RequestedAt        time.Time
	Trigger            string // ADR-123 — pkg/sched/triggers.go closed enum
	QueuedCount        int    // ADR-123 — ledger.Concurrency at admit
	ConcurrencyAtAdmit int    // ADR-123 — same reading; 0 is cold start
	AtCapacity         bool   // PR-A — true when post-admit ledger == plan MaxConcurrency
}

// RestoreBreakdown — vmmd's detailed snapshot-restore timings. The
// measurements cover the complete JailerVMM.Restore call, from chroot
// creation through the first successful readiness probe. Values are integer
// milliseconds and are deliberately emitted as one typed event so the
// wake-timeline endpoint can expose the breakdown without a node-log lookup.
type RestoreBreakdown struct {
	EmitAt               time.Time
	WakeID               string
	AppID                string
	InstanceID           string
	ChrootMs             int64
	MaterializeMemMs     int64
	MaterializeVMStateMs int64
	ResolveImagesMs      int64
	StageDrivesMs        int64
	StageSnapshotMs      int64
	HelperMs             int64
	StartJailerMs        int64
	BindTunMs            int64
	LoadSnapshotMs       int64
	ResumeHookMs         int64
	WaitReadyMs          int64
	TotalMs              int64
}

func (e RestoreBreakdown) Kind() string     { return WakeRestoreBreakdown }
func (e RestoreBreakdown) At() time.Time    { return e.EmitAt }
func (e RestoreBreakdown) Subject() *string { return nil }
func (e RestoreBreakdown) Payload() map[string]any {
	return map[string]any{
		"wake_id":                e.WakeID,
		"app_id":                 e.AppID,
		"instance_id":            e.InstanceID,
		"chroot_ms":              e.ChrootMs,
		"materialize_mem_ms":     e.MaterializeMemMs,
		"materialize_vmstate_ms": e.MaterializeVMStateMs,
		"resolve_images_ms":      e.ResolveImagesMs,
		"stage_drives_ms":        e.StageDrivesMs,
		"stage_snapshot_ms":      e.StageSnapshotMs,
		"helper_ms":              e.HelperMs,
		"start_jailer_ms":        e.StartJailerMs,
		"bind_tun_ms":            e.BindTunMs,
		"load_snapshot_ms":       e.LoadSnapshotMs,
		"resume_hook_ms":         e.ResumeHookMs,
		"wait_ready_ms":          e.WaitReadyMs,
		"total_ms":               e.TotalMs,
	}
}

func (e BootStarted) Kind() string     { return WakeBootStarted }
func (e BootStarted) At() time.Time    { return e.EmitAt }
func (e BootStarted) Subject() *string { return nil }
func (e BootStarted) Payload() map[string]any {
	p := map[string]any{
		"wake_id":              e.WakeID,
		"app_id":               e.AppID,
		"instance_id":          e.InstanceID,
		"node_id":              e.NodeID,
		"method":               e.Method,
		"requested_at":         e.RequestedAt.UTC(),
		"queued_count":         e.QueuedCount,
		"concurrency_at_admit": e.ConcurrencyAtAdmit,
		"at_capacity":          e.AtCapacity,
	}
	// ADR-123: trigger is absent on the Phase-1 fast-path return
	// (engine.go:1119) where an existing RUNNING instance was reused
	// — the original boot row's events.data carries the value, this
	// row is omitted from the customer-facing timeline anyway.
	if e.Trigger != "" {
		p["trigger"] = e.Trigger
	}
	return p
}

// BootCompleted — schedd post-RecordRuntime. Distinct from the
// legacy `app.characterized` audit row, which fires when the first
// request lands; this row fires when the instance enters RUNNING,
// so the wake timeline is correct even on apps that never receive
// a request.
//
// ADR-123 — same trigger / queued_count / concurrency_at_admit
// fields as BootStarted (carried via bootInput which is immutable
// across the unlocked Phase 3 window, so both rows carry the same
// snapshot). The customer's "wake timeline" surfaces these three
// fields identically on both rows.
type BootCompleted struct {
	EmitAt             time.Time
	WakeID             string
	AppID              string
	InstanceID         string
	NodeID             string
	Method             string
	StartedAt          time.Time
	CompletedAt        time.Time
	Trigger            string // ADR-123 — pkg/sched/triggers.go closed enum
	QueuedCount        int    // ADR-123 — ledger.Concurrency at admit
	ConcurrencyAtAdmit int    // ADR-123 — same reading; 0 is cold start
}

func (e BootCompleted) Kind() string     { return WakeBootCompleted }
func (e BootCompleted) At() time.Time    { return e.EmitAt }
func (e BootCompleted) Subject() *string { return nil }
func (e BootCompleted) Payload() map[string]any {
	p := map[string]any{
		"wake_id":              e.WakeID,
		"app_id":               e.AppID,
		"instance_id":          e.InstanceID,
		"node_id":              e.NodeID,
		"method":               e.Method,
		"started_at":           e.StartedAt.UTC(),
		"completed_at":         e.CompletedAt.UTC(),
		"queued_count":         e.QueuedCount,
		"concurrency_at_admit": e.ConcurrencyAtAdmit,
	}
	if e.Trigger != "" {
		p["trigger"] = e.Trigger
	}
	return p
}

// BootFailed — boot path failed. The reason string is the same
// value schedd's transitionWithKind passes to the legacy
// `wake_boot_error` audit row so the legacy + typed rows are
// joinable on (wake_id, reason) for the operator's debug view.
type BootFailed struct {
	EmitAt     time.Time
	WakeID     string
	AppID      string
	InstanceID string
	NodeID     string
	Method     string
	Reason     string
	FailedAt   time.Time
}

func (e BootFailed) Kind() string     { return WakeBootFailed }
func (e BootFailed) At() time.Time    { return e.EmitAt }
func (e BootFailed) Subject() *string { return nil }
func (e BootFailed) Payload() map[string]any {
	return map[string]any{
		"wake_id":     e.WakeID,
		"app_id":      e.AppID,
		"instance_id": e.InstanceID,
		"node_id":     e.NodeID,
		"method":      e.Method,
		"reason":      e.Reason,
		"failed_at":   e.FailedAt.UTC(),
	}
}

// Readiness200 — vmmd's waitReady saw its first 2xx probe. The
// ProbeCount / ElapsedMs fields let the operator view a wake
// timeline and see "the readiness probe took 4 attempts and 320ms"
// without joining against the vmmd log.
type Readiness200 struct {
	EmitAt          time.Time
	WakeID          string
	AppID           string
	InstanceID      string
	NodeID          string
	HealthcheckPath string
	ProbeCount      int
	ElapsedMs       int64
}

func (e Readiness200) Kind() string     { return WakeReadiness200 }
func (e Readiness200) At() time.Time    { return e.EmitAt }
func (e Readiness200) Subject() *string { return nil }
func (e Readiness200) Payload() map[string]any {
	return map[string]any{
		"wake_id":          e.WakeID,
		"app_id":           e.AppID,
		"instance_id":      e.InstanceID,
		"node_id":          e.NodeID,
		"healthcheck_path": e.HealthcheckPath,
		"probe_count":      e.ProbeCount,
		"elapsed_ms":       e.ElapsedMs,
	}
}

// ProxyFirstByte — gatewayd-internal received the first response byte from
// the woken instance. LatencyMs is the wall-clock from queue
// acceptance to first byte so the customer timeline shows the
// end-to-end latency, not just the proxy hop.
type ProxyFirstByte struct {
	EmitAt     time.Time
	WakeID     string
	AppID      string
	RequestID  string
	InstanceID string
	NodeID     string
	LatencyMs  int64
}

func (e ProxyFirstByte) Kind() string     { return WakeProxyFirstByte }
func (e ProxyFirstByte) At() time.Time    { return e.EmitAt }
func (e ProxyFirstByte) Subject() *string { return nil }
func (e ProxyFirstByte) Payload() map[string]any {
	return map[string]any{
		"wake_id":     e.WakeID,
		"app_id":      e.AppID,
		"request_id":  e.RequestID,
		"instance_id": e.InstanceID,
		"node_id":     e.NodeID,
		"latency_ms":  e.LatencyMs,
	}
}

// ParkStarted — schedd transitioning the instance to
// SNAPSHOTTING. The dual WakeParkCompleted row carries the snapshot_id.
//
// DeploymentID (issue #555 PR-6) is the deployment_id the parked
// instance belongs to. The DeploymentCounterWatcher
// (pkg/sched/deployment_counter_watcher.go) reads the deployment_id
// off the in-process event payload and resets the per-deployment
// sampling window when the last live instance parks.
type ParkStarted struct {
	EmitAt       time.Time
	WakeID       string
	AppID        string
	DeploymentID string
	InstanceID   string
	NodeID       string
	StartedAt    time.Time
}

func (e ParkStarted) Kind() string     { return WakeParkStarted }
func (e ParkStarted) At() time.Time    { return e.EmitAt }
func (e ParkStarted) Subject() *string { return nil }
func (e ParkStarted) Payload() map[string]any {
	p := map[string]any{
		"wake_id":     e.WakeID,
		"app_id":      e.AppID,
		"instance_id": e.InstanceID,
		"node_id":     e.NodeID,
		"started_at":  e.StartedAt.UTC(),
	}
	if e.DeploymentID != "" {
		p["deployment_id"] = e.DeploymentID
	}
	return p
}

// ParkCompleted — snapshot succeeded; instance is PARKED.
//
// DeploymentID (issue #555 PR-6) is the deployment_id the parked
// instance belongs to. Watched by the DeploymentCounterWatcher for
// the per-deployment 100% sampling window reset (issue #555
// acceptance #5).
type ParkCompleted struct {
	EmitAt       time.Time
	WakeID       string
	AppID        string
	DeploymentID string
	InstanceID   string
	NodeID       string
	StartedAt    time.Time
	CompletedAt  time.Time
	SnapshotID   string
}

func (e ParkCompleted) Kind() string     { return WakeParkCompleted }
func (e ParkCompleted) At() time.Time    { return e.EmitAt }
func (e ParkCompleted) Subject() *string { return nil }
func (e ParkCompleted) Payload() map[string]any {
	p := map[string]any{
		"wake_id":      e.WakeID,
		"app_id":       e.AppID,
		"instance_id":  e.InstanceID,
		"node_id":      e.NodeID,
		"started_at":   e.StartedAt.UTC(),
		"completed_at": e.CompletedAt.UTC(),
		"snapshot_id":  e.SnapshotID,
	}
	if e.DeploymentID != "" {
		p["deployment_id"] = e.DeploymentID
	}
	return p
}

// Stalled — watchdog path. The existing `watchdog_timeout` audit
// row stays unchanged for GDPR-export compatibility; this row
// carries the structured payload so the customer-facing timeline
// surfaces the same event with a typed shape.
type Stalled struct {
	EmitAt     time.Time
	WakeID     string
	AppID      string
	InstanceID string
	NodeID     string
	Reason     string
}

func (e Stalled) Kind() string     { return WakeStalled }
func (e Stalled) At() time.Time    { return e.EmitAt }
func (e Stalled) Subject() *string { return nil }
func (e Stalled) Payload() map[string]any {
	return map[string]any{
		"wake_id":     e.WakeID,
		"app_id":      e.AppID,
		"instance_id": e.InstanceID,
		"node_id":     e.NodeID,
		"reason":      e.Reason,
	}
}

// TailFailed (issue #667 / ADR-078) — a waitUntil task reached a
// terminal that was NOT a clean completion. Reason is the closed
// enum: "timeout" (per-plan TailTimeoutS exceeded), "handler_error"
// (the task threw or rejected), "forced_at_park" (the
// snapshotAndPark 5s watchdog fired while the runner was still
// draining). One row per unfinished tail on the watchdog path
// so an operator can count exactly how many tasks were lost.
// WakeID is empty on the watchdog path (the snapshotAndPark
// ins.WakeID is the most recent boot's wake; for the watch-
// dog row we keep the schema identical to a runner-emitted
// TailFailed so the timeline endpoint renders both with the
// same code path).
type TailFailed struct {
	EmitAt     time.Time
	AppID      string
	InstanceID string
	Reason     string
}

func (e TailFailed) Kind() string     { return WakeTailFailed }
func (e TailFailed) At() time.Time    { return e.EmitAt }
func (e TailFailed) Subject() *string { return nil }
func (e TailFailed) Payload() map[string]any {
	return map[string]any{
		"app_id":      e.AppID,
		"instance_id": e.InstanceID,
		"reason":      e.Reason,
	}
}

// LivenessFailed — vmmd poll goroutine declared the VM wedged
// (issue #554 / ADR-078). The state machine transition is
// RUNNING → STOPPED with reason='liveness_failed' and the
// Engine.DestroyForLivenessFailure path is the emitter. The
// payload is the closed (instance_id, app_id, deployment_id)
// tuple + the reason classifier so the dashboard's
// "liveness: failure cause (5m)" panel can group by
// {timeout, conn_refused, conn_err, non_200, unauthorized,
// n_consecutive}.
type LivenessFailed struct {
	EmitAt       time.Time
	InstanceID   string
	AppID        string
	DeploymentID string
	Reason       string
}

func (e LivenessFailed) Kind() string     { return InstanceLivenessFailed }
func (e LivenessFailed) At() time.Time    { return e.EmitAt }
func (e LivenessFailed) Subject() *string { return nil }
func (e LivenessFailed) Payload() map[string]any {
	return map[string]any{
		"instance_id":   e.InstanceID,
		"app_id":        e.AppID,
		"deployment_id": e.DeploymentID,
		"reason":        e.Reason,
	}
}

// LivenessRestarted — convenience audit row emitted by the
// DestroyForLivenessFailure path on the same transition. The
// "restarted" wording is friendlier for the customer-facing
// timeline; the dash panels can filter on
// `kind_prefix=instances.liveness_restarted` to surface the
// explicit "this VM was killed because the liveness probe failed"
// notice without also picking up the wire-shape noise from
// LivenessFailed.
type LivenessRestarted struct {
	EmitAt       time.Time
	InstanceID   string
	AppID        string
	DeploymentID string
	Reason       string
}

func (e LivenessRestarted) Kind() string     { return InstanceLivenessRestarted }
func (e LivenessRestarted) At() time.Time    { return e.EmitAt }
func (e LivenessRestarted) Subject() *string { return nil }
func (e LivenessRestarted) Payload() map[string]any {
	return map[string]any{
		"instance_id":   e.InstanceID,
		"app_id":        e.AppID,
		"deployment_id": e.DeploymentID,
		"reason":        e.Reason,
	}
}

// WorkloadOOMFailed (Cluster C / ADR-121) — the guest-init's
// cgroup.events listener observed an oom_kill on the per-VM cgroup
// v2 leaf and the schedd engine destroyed the instance after
// stamping CodeAppRuntimeOOM. The state-machine transition is
// RUNNING → STOPPED with reason='workload_oom_failed'.
// Mirrors LivenessFailed's shape but carries the observed
// (peak_mb, plan_mb) payload so the dashboard's
// "workload OOM: peak vs plan (5m)" panel can plot the actual
// overshoot against the customer's plan cap.
type WorkloadOOMFailed struct {
	EmitAt       time.Time
	InstanceID   string
	AppID        string
	DeploymentID string
	PeakMB       int
	PlanMB       int
}

func (e WorkloadOOMFailed) Kind() string     { return InstanceWorkloadOOMFailed }
func (e WorkloadOOMFailed) At() time.Time    { return e.EmitAt }
func (e WorkloadOOMFailed) Subject() *string { return nil }
func (e WorkloadOOMFailed) Payload() map[string]any {
	return map[string]any{
		"instance_id":   e.InstanceID,
		"app_id":        e.AppID,
		"deployment_id": e.DeploymentID,
		"peak_mb":       e.PeakMB,
		"plan_mb":       e.PlanMB,
	}
}

// ParkedLivenessExhausted — the per-deployment sliding window
// in pkg/sched/liveness_window.go reached 3 restarts in 300s; the
// Engine.ParkDeployment path parked the deployment with
// reason='liveness_exhausted'. Mirrors the
// MinInstancesReleased shape but emits under the
// instances.parked_liveness_exhausted kind so the
// `?kind_prefix=instances.parked_*` filter on
// GET /v1/audit-events surfaces it.
type ParkedLivenessExhausted struct {
	EmitAt       time.Time
	AppID        string
	DeploymentID string
	ParkedReason string
}

func (e ParkedLivenessExhausted) Kind() string     { return InstanceParkedLivenessExhausted }
func (e ParkedLivenessExhausted) At() time.Time    { return e.EmitAt }
func (e ParkedLivenessExhausted) Subject() *string { return nil }
func (e ParkedLivenessExhausted) Payload() map[string]any {
	return map[string]any{
		"app_id":        e.AppID,
		"deployment_id": e.DeploymentID,
		"parked_reason": e.ParkedReason,
	}
}

// BuildSucceeded — builderd finished a build. ImageDigest is the
// OCI digest of the resulting image so the timeline can join
// against the deployment row.
type BuildSucceeded struct {
	EmitAt       time.Time
	AppID        string
	DeploymentID string
	ImageDigest  string
	DurationMs   int64
}

func (e BuildSucceeded) Kind() string     { return WakeBuildSucceeded }
func (e BuildSucceeded) At() time.Time    { return e.EmitAt }
func (e BuildSucceeded) Subject() *string { return nil }
func (e BuildSucceeded) Payload() map[string]any {
	return map[string]any{
		"app_id":        e.AppID,
		"deployment_id": e.DeploymentID,
		"image_digest":  e.ImageDigest,
		"duration_ms":   e.DurationMs,
	}
}

// BuildFailed — builderd build failed. ImageDigest is the digest
// of the partially-built image (empty string if the build failed
// before the image was committed).
type BuildFailed struct {
	EmitAt       time.Time
	AppID        string
	DeploymentID string
	ImageDigest  string
	Reason       string
}

func (e BuildFailed) Kind() string     { return WakeBuildFailed }
func (e BuildFailed) At() time.Time    { return e.EmitAt }
func (e BuildFailed) Subject() *string { return nil }
func (e BuildFailed) Payload() map[string]any {
	return map[string]any{
		"app_id":        e.AppID,
		"deployment_id": e.DeploymentID,
		"image_digest":  e.ImageDigest,
		"reason":        e.Reason,
	}
}

// DeployFailed — apid's deploy rollback path. The reason string
// is the operator-facing error (e.g. "image_scan_failed",
// "cosign_signature_invalid") so the timeline can group by reason.
type DeployFailed struct {
	EmitAt       time.Time
	AppID        string
	DeploymentID string
	Reason       string
}

func (e DeployFailed) Kind() string     { return WakeDeployFailed }
func (e DeployFailed) At() time.Time    { return e.EmitAt }
func (e DeployFailed) Subject() *string { return nil }
func (e DeployFailed) Payload() map[string]any {
	return map[string]any{
		"app_id":        e.AppID,
		"deployment_id": e.DeploymentID,
		"reason":        e.Reason,
	}
}

// SidecarInitExit — guest-init's runWorkloads orchestrator
// recorded a type=="init" sidecar's terminal exit (issue #463 /
// ADR-069 / PR-B). Status is the closed enum: "init_ok" (exit
// code 0) or "init_failed" (non-zero exit). A failed init fails
// the deploy with failure_class: user_error (AC #1). DurationMs is
// the wall-clock from supervisor.Run start to terminal exit so
// operators can see init-side init latency in the wake timeline.
// AppID + InstanceID join back to the canonical schedd-emitted
// rows so the sidecar's lifecycle is visible in the same timeline
// as the main wake.
//
// WakeID is intentionally empty on this event class: the sidecar
// lifecycle is asynchronous to any single schedd-issued wake
// (init runs once per cold boot; restarts can outlive a wake).
// The schedd's wakeID is not on the AF_VSOCK DGRAM wire and
// guest-init has no other source for it. Downstream consumers
// that need wake-correlation should join on
// (app_id, instance_id, emit_at) instead. Surfacing a wakeID
// here would require either an extra envelope byte the
// orchestrator can't fill or a second-order lookup on the host
// — neither is justified by today's audit needs.
type SidecarInitExit struct {
	EmitAt      time.Time
	WakeID      string // always "" today; see struct doc
	AppID       string
	InstanceID  string
	SidecarName string
	Status      string // "init_ok" | "init_failed"
	ExitCode    int
	DurationMs  int64
}

func (e SidecarInitExit) Kind() string     { return WakeSidecarInitExit }
func (e SidecarInitExit) At() time.Time    { return e.EmitAt }
func (e SidecarInitExit) Subject() *string { return nil }
func (e SidecarInitExit) Payload() map[string]any {
	return map[string]any{
		"wake_id":      e.WakeID,
		"app_id":       e.AppID,
		"instance_id":  e.InstanceID,
		"sidecar_name": e.SidecarName,
		"status":       e.Status,
		"exit_code":    e.ExitCode,
		"duration_ms":  e.DurationMs,
	}
}

// SidecarRestart — guest-init's runWorkloads orchestrator
// restarted an essential sidecar after a crash (issue #463 /
// ADR-069 / PR-B). Non-essential sidecars do NOT emit this —
// their crash is logged on the orchestrator's stderr and the
// supervisor returns immediately (Max=0 policy, AC #2). Attempt
// is the 1-indexed restart number (1 = first restart); the main
// workload's restart budget lives on the supervisor's Max field
// (MaxRestarts). PreviousExitCode carries the run's exit code so
// operators can distinguish OOM (137) from user_error (1) from
// signal-driven exit (-1).
//
// WakeID is intentionally empty on this event class — see
// SidecarInitExit's struct doc for the same rationale
// (asynchronous to any single wake, not on the DGRAM wire).
type SidecarRestart struct {
	EmitAt           time.Time
	WakeID           string // always "" today; see struct doc
	AppID            string
	InstanceID       string
	SidecarName      string
	Attempt          int
	PreviousExitCode int
}

func (e SidecarRestart) Kind() string     { return WakeSidecarRestart }
func (e SidecarRestart) At() time.Time    { return e.EmitAt }
func (e SidecarRestart) Subject() *string { return nil }
func (e SidecarRestart) Payload() map[string]any {
	return map[string]any{
		"wake_id":            e.WakeID,
		"app_id":             e.AppID,
		"instance_id":        e.InstanceID,
		"sidecar_name":       e.SidecarName,
		"attempt":            e.Attempt,
		"previous_exit_code": e.PreviousExitCode,
	}
}
