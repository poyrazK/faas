// Package audit is the IAM-4 / ADR-035 seam that records security and
// operational events into the append-only `events` table. Both apid
// (auth/key/secret/cron.created/cron.updated/cron.deleted/app.*/domain.*)
// and schedd (state_transition/reaper_scale_down/cron.fired) wire up one
// Auditor via [New] and call [Auditor.Emit] from their success branches.
//
// The wrapper is intentionally thin: a best-effort call into
// [state.Store.AppendEvent] that logs failures and increments the
// audit-write-failure counter. A failed audit write NEVER rolls back
// the action that produced it (ADR-035 §"Failure semantics").
//
// Failure semantics (issue #278 widened this surface — see ADR-035
// for the policy rationale):
//   - json.Marshal failure on our own map[string]any is a programmer
//     bug, not a runtime concern — log Error and return. We don't
//     reach AppendEvent, so no duration observation is recorded.
//   - AppendEvent failure logs Warn, observes the latency under
//     result="failed", and increments audit_write_failures labelled
//     by the resolved subject id (or "anonymous" if subject is nil/
//     empty). The action has already returned 200 / committed by the
//     time this fires, so the audit row is observation, not source
//     of truth. Never roll back the action.
//   - AppendEvent success observes the latency under result="ok"
//     so the failure-path latency distribution is comparable to
//     the healthy-path latency distribution (issue #278 acceptance).
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/onebox-faas/faas/pkg/auditutil"
	"github.com/onebox-faas/faas/pkg/state"
)

// Ops is the narrow counter+histogram surface Auditor needs. The
// concrete [wire.OpsMetrics] satisfies it (both daemons wire one up).
// Defined as an interface so the helper can be unit-tested with a
// stub (audit_test.go).
//
// AccountID label cardinality is bounded by the bounded-admission
// helper upstream of the counter (pkg/wire); empty input collapses to
// "anonymous" and overflow collapses to "__other__".
//
// PR-#TBD / C5 added AuditLogWriteTotal and
// AuditLogWriteFailuresTotal so /v1/admin/obs/health can
// report 5-minute write throughput + failure rates. The
// audit emit site is the only incrementer.
type Ops interface {
	AuditWriteFailures(accountID string) prometheus.Counter
	AuditWriteFailureDuration(result string) prometheus.Observer
	AuditLogWriteTotal(endpoint, kind string) prometheus.Counter
	AuditLogWriteFailuresTotal(endpoint, kind, errorClass string) prometheus.Counter
}

// Auditor is the IAM-4 audit seam. Constructed once per daemon and
// held on the server/engine. The single Emit method is what handlers
// call.
type Auditor struct {
	actor string
	store state.Store
	log   *slog.Logger
	ops   Ops
}

// New builds an Auditor. actor is the literal value written to the
// events.actor column for every row this Auditor emits — the spec §5
// convention is <daemon-name> ("apid", "schedd"). Passing actor here
// rather than as a package-level const enforces the convention at
// the call site, so a future daemon that forgets to pass an actor
// fails to compile rather than silently writing an empty string.
//
// nil ops is allowed — Emit will skip the counter increment and the
// latency observation so unit tests can run without an OpsMetrics.
//
// Typed-nil guard: routes the ops argument through SetOps so the
// same nil-normalisation logic fires regardless of whether the
// caller constructs via New or wires later via SetOps. Without this
// routing, a `var p *wire.OpsMetrics = nil; a := audit.New(..., p, ...)`
// would leave a.ops as a typed-nil interface and the next
// .AuditWriteFailureDuration(...).Observe(...) call would panic —
// the nil-receiver guard on the concrete Counter/Observer returns
// nil and .Observe on nil dereferences. Mirrors the same routing
// pkg/eventretention.New does NOT need (it accepts Ops via a
// Params struct field and a constructor helper).
func New(store state.Store, log *slog.Logger, ops Ops, actor string) *Auditor {
	a := &Auditor{actor: actor, store: store, log: log}
	a.SetOps(ops)
	return a
}

// SetOps replaces the Ops interface after construction. Used by the
// apid server's WithOpsMetrics flow: the server is built before
// OpsMetrics (which may need to be constructed after the registry
// wiring), so the auditor starts with nil ops and gets a real one
// later. Pass nil to disable the counter/histogram path.
//
// Typed-nil guard: storing a non-nil interface wrapping a
// typed-nil pointer (e.g. SetOps(srv.ops) when srv.ops is a nil
// *wire.OpsMetrics) would panic at the next metric call — the
// nil-receiver guard on the concrete Counter/Observer returns nil
// and .Inc/.Observe on nil panics. isTypedNil normalises both
// flavours to the same nil a.ops value so the per-call guard stays
// accurate. Mirrors pkg/eventretention.Cleanup.SetOps.
func (a *Auditor) SetOps(ops Ops) {
	if isTypedNilAuditOps(ops) {
		a.ops = nil
		return
	}
	a.ops = ops
}

// isTypedNilAuditOps is the audit-side twin of
// pkg/eventretention.isTypedNil. Duplicated rather than shared so
// pkg/audit stays import-clean (pkg/audit is widely imported —
// pulling in pkg/eventretention would reverse the dep direction).
func isTypedNilAuditOps(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Chan, reflect.Func, reflect.Map, reflect.Slice, reflect.Interface:
		return rv.IsNil()
	}
	return false
}

// Emit writes one events row. accountID is optional (nil allowed for
// system-level events, e.g. cron-fired by schedd). data may be nil;
// marshal into {} on the way down so the column is always valid JSON.
//
// Issue #555 PR-5: lift the active OTel span context from ctx and
// stamp trace_id / span_id onto the data JSON so the row joins the
// in-memory trace ring on the same key. The lift is best-effort: a
// missing span context (legacy single-box without OTel) leaves the
// data unchanged.
//
// ADR-091 D20 PR-B: emit sites that want to stamp the binary
// `result` field call [EmitResult] (or wrap their data with
// auditutil.WithResult before calling Emit). Emit itself does NOT
// stamp a default result — keeps the cmd/gatewayd-internal mirror
// in lock-step (it does NOT call this method) and avoids a second
// silent wire-format change. Legacy emit sites without a meaningful
// outcome stay on Emit unchanged.
func (a *Auditor) Emit(ctx context.Context, kind string, accountID *string, data map[string]any) {
	a.emit(ctx, a.actor, kind, accountID, data, "")
}

// EmitResult is the result-bearing twin of [Emit]. result is the
// literal value written to data["result"] — "success" / "error" in
// the load-bearing case, or a finer-grained form (e.g.
// "error:code=im403") for sites that want to encode context. The
// stamp goes through [auditutil.WithResult] so a caller's explicit
// value on data["result"] always wins over the supplied result.
func (a *Auditor) EmitResult(ctx context.Context, kind string, accountID *string, data map[string]any, result string) {
	a.emit(ctx, a.actor, kind, accountID, data, result)
}

// EmitAs is the per-call-actor twin of [Emit] (issue #606 /
// SAFE-RELEASES-E.1). Most emit sites don't know who the actor is
// at the package level — they stamp the daemon-level actor that
// the constructor baked in ("apid", "schedd", …) and call it a
// day. A handful of emit sites (currently: the four deployment
// event types — app.deployed, app.signed_image_accepted,
// deployment.traffic_percent_set_on_create, deploy.local_tarball,
// deploy.source_ref) DO know who the actor is — they resolved
// the actor at request entry (cmd/apid/deploy_actor.go) and want
// to stamp it onto the audit row's actor column for SOC 2 / GDPR
// attribution. EmitAs is the explicit override for that case.
//
// actor is the literal value written to events.actor — the
// canonical shape is "<via>:<id>" (e.g. "dashboard:8a2...",
// "github:poyrazK"). See cmd/apid/deploy_actor.resolvedActorString
// for the convention. Passing actor == "" falls through to the
// constructor-baked a.actor — preserves the safe-default
// behaviour for any future emit site that wants to "use the
// baked actor explicitly" rather than implicitly. Emit sites
// that want to stamp a sentinel "unknown" should use the
// literal "<via>:unknown" form rather than relying on this
// fallback.
//
// Why a separate method rather than widening Emit: keeping the
// constructor-baked behaviour for the ~85 unchanged emit sites
// is the comment at lines 65-70 ("forget to pass an actor
// fails to compile rather than silently writing an empty
// string"). EmitAs is the explicit, opt-in escape hatch — its
// existence documents that this site has decided to attribute
// the row rather than leave it as the daemon's blanket name.
func (a *Auditor) EmitAs(ctx context.Context, actor, kind string, accountID *string, data map[string]any) {
	if actor == "" {
		actor = a.actor
	}
	a.emit(ctx, actor, kind, accountID, data, "")
}

// EmitAsResult is the result-bearing twin of [EmitAs]. Same
// per-call-actor semantics; result is forwarded to the shared
// emit body just like EmitResult's. Mirroring the Emit /
// EmitResult split keeps the trace-lift + marshal + metric-
// observation code paths in lock-step across the four entry
// points (Emit / EmitResult / EmitAs / EmitAsResult).
func (a *Auditor) EmitAsResult(ctx context.Context, actor, kind string, accountID *string, data map[string]any, result string) {
	if actor == "" {
		actor = a.actor
	}
	a.emit(ctx, actor, kind, accountID, data, result)
}

// emit is the shared body. result == "" is the legacy Emit path;
// result != "" is the EmitResult path. Single source of truth for
// the trace lift, marshal, store call, and metric observation —
// keeping the entry points in lock-step. actor is the literal
// value to stamp onto events.actor; callers pre-resolve it (Emit /
// EmitResult pass a.actor; EmitAs / EmitAsResult pass the
// per-call override).
func (a *Auditor) emit(ctx context.Context, actor, kind string, accountID *string, data map[string]any, result string) {
	data = auditutil.WithResult(data, result)
	if data == nil {
		data = map[string]any{}
	}
	if sc := oteltrace.SpanContextFromContext(ctx); sc.TraceID().IsValid() {
		// Don't overwrite a customer-supplied trace_id (e.g. cron-fired
		// events that synthesise a trace_id for the row). The merge
		// falls back to the active span context only when the field
		// is absent.
		//
		// PR-#TBD / fix-cluster A — gate the trace_id lift on
		// TraceID().IsValid() alone, not the full SpanContext.IsValid().
		// The synthetic SpanContexts that pkg/middleware.WithSpanContext
		// and pkg/scheddgrpc.withTraceIDSpan stamp on ctx carry a
		// valid TraceID + zero SpanID (we don't synthesise a SpanID —
		// that would require a real tracer to be load-bearing); the
		// OTel SDK treats a zero SpanID as invalid, so the full
		// SpanContext.IsValid() gate would skip the lift entirely on
		// both the HTTP and gRPC operator-action paths. Splitting the
		// gate keeps the trace_id lift load-bearing while the span_id
		// lift (which DOES need a valid SpanID to be meaningful) stays
		// a no-op for synthetic contexts.
		if _, ok := data["trace_id"]; !ok {
			data["trace_id"] = sc.TraceID().String()
		}
		if _, ok := data["span_id"]; !ok && sc.SpanID().IsValid() {
			data["span_id"] = sc.SpanID().String()
		}
	}
	payload, err := json.Marshal(data)
	if err != nil {
		a.log.Error("audit: marshal", "kind", kind, "err", err)
		return
	}
	// Normalize the subject into a string we can label the metric
	// with. nil and empty collapse to the empty string; the bounded-
	// admission helper upstream of the counter maps that to
	// "anonymous". This keeps the labelled-counter helper on a single
	// non-nil string argument so Ops stays a clean two-method
	// interface.
	subjectStr := ""
	if accountID != nil {
		subjectStr = *accountID
	}
	var subject *string
	if subjectStr != "" {
		subject = accountID
	}
	start := time.Now()
	// PR-#TBD / C4 — lift the trace_id off the jsonb payload and
	// persist it as a column on the events table (migrations/00486).
	// The OTel-context lift at audit.go:221-232 and the explicit
	// data["trace_id"] set by C3's schedd subscriber both write
	// into the same map key, so this extraction captures whichever
	// source supplied the value. A nil-typed entry (key present,
	// value nil) is treated as nil.
	var traceID *string
	if v, ok := data["trace_id"].(string); ok && v != "" {
		traceID = &v
	}
	err = a.store.AppendEventWithTrace(ctx, actor, kind, subject, payload, traceID)
	dur := time.Since(start)
	// PR-#TBD / C5 — increment the success/failure counters
	// behind /v1/admin/obs/health. The kind label is mapped
	// onto the closed set (auditKindClosedSet in pkg/wire);
	// unknown kinds collapse to "other" so a typo in audit
	// emit sites cannot blow up Prometheus cardinality. The
	// endpoint label is taken from a.actor (the per-daemon
	// string wired at New — apid, schedd, meterd,
	// gatewayd-internal), pre-validated against
	// auditEndpointClosedSet.
	endpoint := a.actor
	metricKind := auditKindMetricLabel(kind)
	if a.ops != nil {
		if err != nil {
			a.ops.AuditWriteFailureDuration("failed").Observe(dur.Seconds())
		} else {
			a.ops.AuditWriteFailureDuration("ok").Observe(dur.Seconds())
		}
	}
	if err != nil {
		a.log.Warn("audit: append event",
			"actor", actor, "kind", kind, "subject", subject, "err", err)
		if a.ops != nil {
			a.ops.AuditWriteFailures(subjectStr).Inc()
			a.ops.AuditLogWriteFailuresTotal(endpoint, metricKind, errorClassFromErr(err)).Inc()
		}
	} else if a.ops != nil {
		a.ops.AuditLogWriteTotal(endpoint, metricKind).Inc()
	}
}

// auditKindMetricLabel maps an audit kind (free-text, e.g.
// "operator.action.force_park" or
// "operator.action.force_park.outcome") onto the closed metric
// label set declared in pkg/wire.NewOpsMetrics
// (auditKindClosedSet). Unknown kinds collapse to "other" so
// cardinality stays bounded regardless of caller typos.
//
// Mapping table:
//
//	"operator.action.force_park"             → "force_park"
//	"operator.action.force_cold_boot"        → "force_cold_boot"
//	"operator.action.force_restart"          → "force_restart"
//	"operator.action.force_park.outcome"     → "force_park.outcome"
//	"operator.action.force_cold_boot.outcome" → "force_cold_boot.outcome"
//	"operator.action.force_restart.outcome"  → "force_restart.outcome"
//	"operator.action.node_drain"             → "node_drain"
//	"operator.action.node_force_drain"       → "node_force_drain"
//	"operator.action.node_activate"          → "node_activate"
//	"operator.action.node_drain.outcome"     → "node_drain.outcome"
//	"operator.action.node_force_drain.outcome" → "node_force_drain.outcome"
//	"operator.action.node_activate.outcome"  → "node_activate.outcome"
//
// Plus the apid request-side aliases (the apid handler emits use
// the instance-oriented names "park_instance" and
// "restart_instance"; schedd's outcome-side emits use the
// verb-oriented names "force_<verb>.outcome". Both sides MUST
// land on the same metric label so a single PromQL query can
// join them — aliasing the instance-oriented forms onto the
// verb-oriented labels keeps the closed set tight without
// renaming the audit emit sites):
//
//	"operator.action.park_instance"          → "force_park"
//	"operator.action.park_instance.outcome"  → "force_park.outcome"
//	"operator.action.restart_instance"       → "force_restart"
//	"operator.action.restart_instance.outcome" → "force_restart.outcome"
//
// Plus the ADR-124 deployment queue-control kinds (cmd/apid/handlers_queue_controls.go) —
// the verb IS the metric label, so these map to themselves:
//
//	"deployment.cancelled"                   → "deployment.cancelled"
//	"deployment.reordered"                   → "deployment.reordered"
//	"deployment.cleared"                     → "deployment.cleared"
//	"deployment.clear_obsolete"              → "deployment.clear_obsolete"
//
//	anything else                           → "other"
//
// Called from pkg/audit.Auditor.emit (PR-#TBD / C5). PR-#TBD's
// /v1/admin/obs/health reads the resulting counters via PromQL.
func auditKindMetricLabel(kind string) string {
	const (
		requestSuffix    = ".outcome"
		verbPark         = "force_park"
		verbColdBoot     = "force_cold_boot"
		verbRestart      = "force_restart"
		verbNodeDrain    = "node_drain"
		verbNodeForce    = "node_force_drain"
		verbNodeActivate = "node_activate"
		instancePark     = "park_instance"
		instanceRestart  = "restart_instance"
		operatorPrefix   = "operator.action."
	)
	switch kind {
	case operatorPrefix + verbPark:
		return verbPark
	case operatorPrefix + verbColdBoot:
		return verbColdBoot
	case operatorPrefix + verbRestart:
		return verbRestart
	case operatorPrefix + verbPark + requestSuffix:
		return verbPark + requestSuffix
	case operatorPrefix + verbColdBoot + requestSuffix:
		return verbColdBoot + requestSuffix
	case operatorPrefix + verbRestart + requestSuffix:
		return verbRestart + requestSuffix
	case operatorPrefix + verbNodeDrain:
		return verbNodeDrain
	case operatorPrefix + verbNodeForce:
		return verbNodeForce
	case operatorPrefix + verbNodeActivate:
		return verbNodeActivate
	case operatorPrefix + verbNodeDrain + requestSuffix:
		return verbNodeDrain + requestSuffix
	case operatorPrefix + verbNodeForce + requestSuffix:
		return verbNodeForce + requestSuffix
	case operatorPrefix + verbNodeActivate + requestSuffix:
		return verbNodeActivate + requestSuffix
	// apid request-side aliases (instance-oriented). The
	// schedd-side outcome audit emits these via the verb-
	// oriented forms above; aliasing keeps both surfaces on
	// the same metric label without renaming the audit emit
	// sites (which would break the join with intent.Kind).
	case operatorPrefix + instancePark:
		return verbPark
	case operatorPrefix + instancePark + requestSuffix:
		return verbPark + requestSuffix
	case operatorPrefix + instanceRestart:
		return verbRestart
	case operatorPrefix + instanceRestart + requestSuffix:
		return verbRestart + requestSuffix
	// ADR-124 deployment queue-control audit kinds
	// (pkg/reconcile/audit.go). The verb IS the metric label — no
	// aliasing needed because the apid handlers are the sole
	// emit site and the kinds were chosen to match the label.
	case "deployment.cancelled":
		return "deployment.cancelled"
	case "deployment.reordered":
		return "deployment.reordered"
	case "deployment.cleared":
		return "deployment.cleared"
	case "deployment.clear_obsolete":
		return "deployment.clear_obsolete"
	default:
		return "other"
	}
}

// errorClassFromErr classifies a pgx / Postgres error onto the
// closed auditLogWriteFailuresTotal error_class label set. The
// mapping is intentionally conservative: only SQLSTATEs the
// events / operator_intents tables actually emit at audit
// write-time get a labelled bucket; everything else collapses
// to "other" so an obscure transient doesn't fill a dashboard
// panel with one-off series.
//
// SQLSTATE 23514 (check_violation) is the events.trace_id
// regex at 00486 — labelled so a regression in the C4 trace_id
// middleware surfaces as a check_violation bucket spike. SQLSTATE
// 23505 (unique_violation) is the events.id / events.??? PK
// races — labelled so the operator can tell the difference.
//
// pgx wraps pgconn.PgError values via errors.As; the function
// is pgx-aware but does NOT depend on pgx directly to keep this
// helper unit-testable from the audit_test package.
func errorClassFromErr(err error) string {
	if err == nil {
		return "other"
	}
	// Probe the wrapped chain via the standard library interface
	// (any *pgconn.PgError exposes Code() and SQLState() in the
	// pgx v5 stack). When err is not a *pgconn.PgError, fall
	// through to "other".
	type sqlStater interface{ SQLState() string }
	var s sqlStater
	// errors.As is the cheapest reliable probe across the pgx
	// stack; we accept the import cost once here rather than
	// threading pgconn through the audit package's test surface.
	if errors.As(err, &s) {
		switch s.SQLState() {
		case "23514":
			return "sqlstate_23514"
		case "23505":
			return "sqlstate_23505"
		case "57014", "57P01", "57P02", "57P03":
			// statement_timeout / admin_shutdown / crash_shutdown /
			// cannot_connect_now. Collapsed to "timeout" since the
			// operator's response is the same: investigate the
			// pool / database. The fine-grained labels would be
			// nice-to-have but the bucket is closed.
			return "timeout"
		}
	}
	// ctx.DeadlineExceeded surfaces here too — match on string
	// rather than importing context in two places.
	if err.Error() == context.DeadlineExceeded.Error() {
		return "timeout"
	}
	return "other"
}
