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
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	oteltrace "go.opentelemetry.io/otel/trace"

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
type Ops interface {
	AuditWriteFailures(accountID string) prometheus.Counter
	AuditWriteFailureDuration(result string) prometheus.Observer
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
func New(store state.Store, log *slog.Logger, ops Ops, actor string) *Auditor {
	return &Auditor{actor: actor, store: store, log: log, ops: ops}
}

// SetOps replaces the Ops interface after construction. Used by the
// apid server's WithOpsMetrics flow: the server is built before
// OpsMetrics (which may need to be constructed after the registry
// wiring), so the auditor starts with nil ops and gets a real one
// later. Pass nil to disable the counter/histogram path.
func (a *Auditor) SetOps(ops Ops) {
	a.ops = ops
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
func (a *Auditor) Emit(ctx context.Context, kind string, accountID *string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	if sc := oteltrace.SpanContextFromContext(ctx); sc.IsValid() {
		// Don't overwrite a customer-supplied trace_id (e.g. cron-fired
		// events that synthesise a trace_id for the row). The merge
		// falls back to the active span context only when the field
		// is absent.
		if _, ok := data["trace_id"]; !ok {
			data["trace_id"] = sc.TraceID().String()
		}
		if _, ok := data["span_id"]; !ok {
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
	err = a.store.AppendEvent(ctx, a.actor, kind, subject, payload)
	dur := time.Since(start)
	if a.ops != nil {
		if err != nil {
			a.ops.AuditWriteFailureDuration("failed").Observe(dur.Seconds())
		} else {
			a.ops.AuditWriteFailureDuration("ok").Observe(dur.Seconds())
		}
	}
	if err != nil {
		a.log.Warn("audit: append event",
			"actor", a.actor, "kind", kind, "subject", subject, "err", err)
		if a.ops != nil {
			a.ops.AuditWriteFailures(subjectStr).Inc()
		}
	}
}
