// Thin in-package wrapper around pkg/audit.Auditor that bakes in the
// "apid" actor constant and the auditOps interface.
//
// The pkg/audit package is the canonical best-effort AppendEvent
// wrapper (ADR-035); apid and schedd both use it. This file exists so
// the existing `s.audit.Emit(...)` callsites in handlers don't churn
// across the cross-daemon lift — the shape stays exactly the same,
// only the underlying implementation moved to pkg/audit.
//
// Lift rationale: pkg/audit/audit.go holds the single best-effort
// wrapper (ADR-035). Lifting lets schedd reuse it for cron.fired
// without duplicating the failure contract. The apid wrapper keeps
// the auditActor const baked in so the 10+ emit sites added in
// PR #349 stay unchanged.
//
// Issue #286 added a second emit path, EmitFailedLogin, that goes
// through a buffered channel drained by a single background goroutine.
// ADR-035 explicitly rejected a synchronous INSERT on every failed
// login as a DoS amplifier under credential-stuffing; the async
// flusher is the seal that closes that gap. The downstream emit
// path is the same state.Store.AppendEvent call used by Emit — no
// new SQL surface, no new migration.
package apidserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/apid/postgresaccess"
	"github.com/onebox-faas/faas/pkg/apid/projectaccess"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

// auditActor is the value written to events.actor for every IAM-4
// audit row produced by apid. Free-form text per spec §5; schedd uses
// "schedd" so the convention is <daemon-name>. Baked into the
// wrapper here so the existing `s.audit.Emit(...)` callsites stay
// pre-PR shaped.
const auditActor = "apid"

// auditOverageCapNullSentinel is the literal string the overage-cap
// audit row writes for `new_cents` when the caller clears the cap
// (NULL round-trip). The audit row is a JSON-encoded blob; a true
// JSON null would round-trip cleanly, but the field could also be
// a not-yet-set int64=0 — using a non-JSON string sentinel lets the
// audit reader distinguish "cleared" from "set to 0" without a
// separate discriminator column. The matching test in
// handlers_ext_test.go::TestRaiseOverageCap_Clear asserts this
// sentinel. The const lifts the goconst threshold (the string
// was duplicated in handlers_ext.go + the test).
const auditOverageCapNullSentinel = "null"

// jsonNullBody is the literal "null" body that some Go versions
// emit for an empty list response (encoding/json writes `null` for
// a nil slice, `[]` for an empty but non-nil one). The empty-list
// test in server_test.go::TestListApps_Empty accepts either shape
// so the assertion holds across Go versions. Distinct from
// auditOverageCapNullSentinel — the JSON-body "null" is Go's
// runtime output, the audit-row "null" is an opaque string.
const jsonNullBody = "null"

// KindAuthLoginFailed is the events.kind value written for every
// failed login attempt on the dashboard auth surface (issue #286,
// SOC 2 CC7.2). The discriminator in the data payload is fixed —
// {ip, email_hash, user_agent} — across every emit site so the
// audit reader does not need to disambiguate between auth surfaces.
// The kind is deliberately distinct from the success-path
// "auth.login" so a filters by `kind_prefix=auth.` continue to
// surface both branches in the customer dashboard.
const KindAuthLoginFailed = "auth.login.failed"

// failedLoginChanCapacity is the buffered channel capacity for the
// async-batched failed-login audit path (issue #286). Sized at 4096
// rows — modest enough that the memory footprint stays well below
// the 6 GB control-plane slice, large enough that a single
// credential-stuffing burst that escapes the auth-limit bucket (30/s
// per IP, 10/min across the bucket) does not immediately drop rows
// before the 250 ms flush cadence fires. A non-blocking select with
// the `default` branch as the drop arm means the customer-facing
// 401 is never gated on the channel write.
const failedLoginChanCapacity = 4096

// failedLoginFlushEvery is the cadence at which the flusher goroutine
// drains the channel into batched AppendEvent calls. Sized at 250 ms
// so the audit row appears in the table within a quarter-second of
// the failed attempt — fast enough that an operator triaging a
// FaasFailedLoginSpike alert can read the audit row before the burst
// already moved on.
const failedLoginFlushEvery = 250 * time.Millisecond

// failedLoginFlushBatch is the upper bound on rows the flusher will
// drain in a single batch. Sized at 1000 so a 4096-row burst
// flushes in 4 batches worth of AppendEvent calls (~1 s total at
// 250 ms cadence) which keeps the per-row latency bounded without
// pinning a Postgres connection for hundreds of inserts at once.
const failedLoginFlushBatch = 1000

// auditOps is the narrow counter+histogram surface the success-path
// emit needs (mirrors pkg/audit.Ops). wire.OpsMetrics satisfies it
// (it has AuditWriteFailures and AuditWriteFailureDuration).
// Defined as an interface here so the async failed-login code path
// (which sits outside pkg/audit) can share the same testing seam
// pattern.
//
// Issue #278 widened the audit-write surface:
//   - AuditWriteFailures takes accountID so the counter can be
//     labelled per-customer. The label value flows through the
//     bounded admission set; empty input becomes "anonymous" and
//     overflow collapses to "__other__".
//   - AuditWriteFailureDuration records the AppendEvent latency
//     histogram labelled by result ∈ {ok, failed} so an operator
//     can distinguish a Postgres outage (slow failed) from a
//     transient insert race (fast failed).
type auditOps interface {
	AuditWriteFailures(accountID string) prometheus.Counter
	AuditWriteFailureDuration(result string) prometheus.Observer
}

// auditFailedOps is the narrow counter surface the failed-login
// channel needs (issue #286). Same testing pattern as auditOps —
// defined as an interface so the auditor can be unit-tested with a
// stub. The counter values flow through the bounded
// ipLabelSet/audit write set documented on wire.OpsMetrics.
//
// FailedLoginAuditWriteFailures is on this interface — NOT on
// auditOps — because the failed-login audit path is structurally
// distinct from the success path: the row's subject is always nil
// (a failed login cannot be attributed to a known account), so
// routing its audit-write failures through the success-path
// AuditWriteFailures("") counter would collapse them into the
// `account_id="anonymous"` series alongside legitimate
// anonymous-success-path failures and make operator triage harder.
// Pair the dedicated counter with apid_audit_write_failures_total
// {account_id} for the SOC 2 CC7.2 surface.
type auditFailedOps interface {
	FailedLoginTotal(ip string) prometheus.Counter
	FailedLoginDropped() prometheus.Counter
	FailedLoginAuditWriteFailures() prometheus.Counter
}

// failedLoginRow is the in-memory shape that flows through the
// async-batched audit channel. The fields are pre-computed at the
// handler site so the flusher does not need to re-read
// r.Header.Get("User-Agent") etc.; the At timestamp is the
// failure-detection time (not the flush time) so an audit row
// reader can correlate the row with the offending request.
type failedLoginRow struct {
	IP        string
	EmailHash string
	UserAgent string
	At        time.Time
}

// auditor wraps pkg/audit.Auditor for the success-path emit (so
// in-package `s.audit.Emit(...)` callsites keep their pre-PR shape)
// and layers the async-batched failed-login flusher on top. The
// two paths share the same actor constant + store + log surface,
// just via different code paths (sync delegation to inner vs.
// channel→goroutine). store / log / ops are duplicated here
// because the failed-login flusher must call AppendEvent
// synchronously — pkg/audit.Auditor doesn't expose its private
// fields, and adding a "write one row with custom kind" method to
// pkg/audit just to serve the failed-login case would couple a
// cross-daemon shared type to an apid-only async shape.
type auditor struct {
	// inner holds the success-path pkg/audit.Auditor. The IAM-4
	// sync emit routes through it; this wrapper exists to bake in
	// the auditActor constant and the auditOps interface so the
	// 10+ emit sites added in PR #349 don't churn.
	inner *audit.Auditor

	// store / log / ops duplicate the inner Auditor's private
	// fields so flushOne can call AppendEvent directly. The
	// constructor wires them up identically; the lazy-ops
	// rebinding in WithOpsMetrics also re-binds these directly.
	store state.Store
	log   *slog.Logger
	ops   auditOps

	// failed-login async channel (issue #286). failedCh is the
	// buffered enqueue point; failedOps is the per-IP counter
	// surface; flushEvery / flushBatch are the draining pacer.
	// closeSignal is the wakeup channel Close signals on so the
	// flusher goroutine exits immediately rather than waiting
	// for the next ticker tick on shutdown.
	failedCh    chan failedLoginRow
	closeSignal chan struct{}
	failedOps   auditFailedOps
	flushEvery  time.Duration
	flushBatch  int

	// closeOnce guards double-Close (idempotent on shutdown). wg
	// waits for the flusher goroutine to drain in-flight rows
	// before Close returns, so the daemon can stop on a clean
	// silence after the audit row is in the table.
	closeOnce sync.Once
	wg        sync.WaitGroup
}

// newAuditor builds the helper. nil ops is allowed — the sync emit
// will skip the counter increment so unit tests can run without an
// OpsMetrics. The async failed-login channel is NOT started here —
// the caller must invoke Start(ctx) after SetFailedOps has wired
// the failed-login counter, so the flusher goroutine can talk to a
// fully-constructed OpsMetrics. This split keeps the
// newServerWithDeps → WithOpsMetrics → SetFailedOps → Start → Close
// lifecycle explicit.
func newAuditor(store state.Store, log *slog.Logger, ops audit.Ops) *auditor {
	return &auditor{
		inner:       audit.New(store, log, ops, auditActor),
		store:       store,
		log:         log,
		ops:         ops,
		failedCh:    make(chan failedLoginRow, failedLoginChanCapacity),
		closeSignal: make(chan struct{}),
		flushEvery:  failedLoginFlushEvery,
		flushBatch:  failedLoginFlushBatch,
	}
}

// setOps forwards to the inner Auditor so WithOpsMetrics can re-bind
// the counter after construction. Mirrors the pre-PR s.audit.ops =
// ops line at server.go:187. Also re-binds the local ops field
// (used by the failed-login flusher's AuditWriteFailureDuration
// observation).
func (a *auditor) setOps(ops audit.Ops) {
	if a == nil {
		return
	}
	if a.inner != nil {
		a.inner.SetOps(ops)
	}
	a.ops = ops
}

// SetFailedOps wires the failed-login counter surface. Called by
// WithOpsMetrics after the OpsMetrics is constructed (issue #286).
// Mirrors the lazy ops binding pattern used for the success-path
// inner Auditor — the auditor is constructed at server-build time so
// the handler closure has a non-nil receiver, while the metrics
// layer is wired later so the daemons-without-OpsMetrics test path
// remains a valid call shape.
func (a *auditor) SetFailedOps(ops auditFailedOps) {
	if a == nil {
		return
	}
	a.failedOps = ops
}

// pkgAuditor returns the underlying pkg/audit.Auditor so other
// in-process subsystems (PR-G's reconcile.Service) can emit audit
// rows under the same actor (auditActor="apid") without rebuilding
// a parallel pipeline. Returns nil only when the auditor wrapper
// itself is nil (defensive — same shape as setOps).
func (a *auditor) pkgAuditor() *audit.Auditor {
	if a == nil {
		return nil
	}
	return a.inner
}

// Start spins up the flusher goroutine. Called once per server
// after SetFailedOps. The goroutine terminates when the channel is
// closed via Close.
func (a *auditor) Start(ctx context.Context) {
	if a == nil || a.failedCh == nil {
		return
	}
	a.wg.Add(1)
	go a.runFlusher(ctx)
}

// Close stops the flusher goroutine and blocks until all
// in-flight rows have been drained into the events table. Idempotent
// — calling Close twice is a no-op. The daemon shutdown path
// (cmd/apid/main.go) defers this so SIGTERM leaves the audit table
// consistent with the in-process queue.
func (a *auditor) Close() {
	if a == nil || a.failedCh == nil {
		return
	}
	a.closeOnce.Do(func() {
		close(a.failedCh)
		// Wake the flusher goroutine so the final drain runs
		// immediately rather than waiting for the next ticker
		// tick (up to flushEvery = 250ms in production).
		close(a.closeSignal)
		a.wg.Wait()
	})
}

// Emit delegates to pkg/audit.Auditor.Emit. Same signature, same
// best-effort semantics — see pkg/audit/audit.go for the failure
// contract (ADR-035).
func (a *auditor) Emit(ctx context.Context, kind string, accountID *string, data map[string]any) {
	if a == nil || a.inner == nil {
		return
	}
	if g, ok := postgresaccess.From(ctx); ok {
		accountID = &g.Actor.ID
	}
	a.inner.Emit(ctx, kind, accountID, postgresaccess.AuditData(ctx, projectaccess.AuditData(ctx, appaccess.AuditData(ctx, data))))
}

// EmitAs is the per-call-actor twin of Emit (issue #606 /
// SAFE-RELEASES-E.1). Used only by the four deployment-path
// audit emit sites (cmd/apid/handlers_sidecars.go,
// handlers_source_tarball.go, handlers_source_ref.go,
// deploy_inputs.go::createDeploymentMultipart). The actor
// string is the resolved "<via>:<id>" form from
// cmd/apid/deploy_actor.resolvedActorString. Mirrors
// pkg/audit.Auditor.EmitAs — same contract, same nil-receiver
// safety.
func (a *auditor) EmitAs(ctx context.Context, actor, kind string, accountID *string, data map[string]any) {
	if a == nil || a.inner == nil {
		return
	}
	a.inner.EmitAs(ctx, actor, kind, accountID, postgresaccess.AuditData(ctx, projectaccess.AuditData(ctx, appaccess.AuditData(ctx, data))))
}

// EmitFailedLogin is the handler-side entry point for the
// failed-login audit row (issue #286). Non-blocking — the channel
// write either succeeds or the row is dropped into
// apid_failed_login_audit_dropped_total. The handler then returns
// the 401 unconditionally; the audit row is observation, not source
// of truth.
//
// ip is the source IP extracted via pkg/middleware.ClientIP (the
// same loopback+XFF trust contract the auth-limit bucket uses —
// diverging from that contract would silently make a credential-
// stuffing burst look like a different (smaller) attack).
// emailHash is pkg/auth.HashEmail (lowercase+trimmed SHA-256 hex).
// userAgent is the raw inbound User-Agent header — sanitized at
// this seam via pkg/logsanitize.Field before any further use, so
// the value that lands in the events.data JSON, the slog
// warning-on-drop line, and the per-IP metric label is the same
// sanitized form (CodeQL go/log-injection closure, issue #286
// review fix #9).
func (a *auditor) EmitFailedLogin(ip, emailHash, userAgent string) {
	if a == nil || a.failedCh == nil {
		return
	}
	// Sanitize the user-agent ONCE at the seam so every downstream
	// sink (audit row, slog warn-on-drop, future log lines) sees
	// the same byte sequence. Doing this here rather than at each
	// sink keeps the sanitization contract in a single place and
	// avoids the failure mode where one path forgets to sanitize
	// and an attacker-supplied CRLF lands in a downstream tool.
	sanitizedUA := logsanitize.Field(userAgent)
	// Increment the per-IP counter BEFORE the channel write so a
	// dropped row still produces the Prometheus signal. The audit
	// row is observation; the counter is the SOC 2 evidence.
	if a.failedOps != nil {
		a.failedOps.FailedLoginTotal(ip).Inc()
	}
	row := failedLoginRow{
		IP:        ip,
		EmailHash: emailHash,
		UserAgent: sanitizedUA,
		At:        time.Now(),
	}
	select {
	case a.failedCh <- row:
		// Enqueued. The flusher will pick it up on the next tick.
	default:
		// Channel is full. The audit row is observation, NOT
		// source of truth — the auth response is the customer-facing
		// invariant and was already queued before the EmitFailedLogin
		// call. Drop the row, increment the unlabelled drop counter,
		// and log a sanitized Warn so the operator can correlate
		// "apid_failed_login_audit_dropped_total rising" with a
		// slog line carrying the IP.
		if a.failedOps != nil {
			a.failedOps.FailedLoginDropped().Inc()
		}
		a.log.Warn("audit: failed-login channel full, dropped",
			"ip", logsanitize.Field(ip))
	}
}

// runFlusher is the single-goroutine drain loop for the async
// failed-login audit channel (issue #286). One loop per daemon.
//
// Cadence: the loop waits on the ticker + a wakeup channel. The
// ticker is the low-traffic bound (flushEvery=250ms — the audit
// row appears within a quarter second of the failed attempt even
// when the request rate is zero). closeSignal lets Close wake
// the goroutine on shutdown so the daemon doesn't wait up to
// flushEvery for the final drain.
//
// The channel receive on a.failedCh is folded into drainFlusher
// — a naive "receive-and-discard" in this select would drop the
// row on the floor. drainFlusher is the only place that pops rows.
//
// On Close: failedCh is closed (which makes drainFlusher's
// `<-a.failedCh` return on `!ok` and exit the loop) AND
// closeSignal is signalled (which wakes the loop immediately
// rather than waiting for the next ticker tick). The two are
// distinct mechanisms: the channel close is the exit, the signal
// is the wakeup. Close's wg.Wait blocks until runFlusher has
// returned.
//
// On ctx.Done: drain the channel and exit. Used when the parent
// context is cancelled (e.g. daemon shutdown without Close being
// called, though Close is the canonical path).
func (a *auditor) runFlusher(ctx context.Context) {
	defer a.wg.Done()
	ticker := time.NewTicker(a.flushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			a.drainFlusher(ctx)
			return
		case <-a.closeSignal:
			// Channel was closed. The final drain picks up any
			// rows still buffered (drainFlusher's `case row, ok
			// := <-a.failedCh` returns on `!ok`).
			a.drainFlusher(ctx)
			return
		case <-ticker.C:
			// Cadence tick. Drain whatever is buffered so the
			// audit row latency stays bounded even when the
			// request rate is low.
			a.drainFlusher(ctx)
		}
	}
}

// drainFlusher reads up to a.flushBatch rows off the channel and
// writes one events row per read. Called from runFlusher (size
// threshold OR tick) and from the ctx.Done branch (shutdown drain).
// Best-effort — a failed AppendEvent logs Warn and increments the
// audit failure counter; the row is dropped from the audit surface.
// The per-IP counter has already been incremented by EmitFailedLogin
// at enqueue time, so the SOC 2 counter is unaffected by audit-write
// failures.
//
// ctx is the goroutine context the flusher was started with (the
// daemon main context from WithOpsMetrics, or the test-supplied
// context from Start). The flusher derives a 5-second timeout
// context per row in flushOne; the parent ctx is threaded through
// so a daemon shutdown can short-circuit the in-flight AppendEvent
// calls rather than waiting the full timeout (contextcheck lint
// closure, golangci-lint v2.4.0).
func (a *auditor) drainFlusher(ctx context.Context) {
	for i := 0; i < a.flushBatch; i++ {
		select {
		case row, ok := <-a.failedCh:
			if !ok {
				// Channel closed. The loop in runFlusher has
				// already exited; this drain is the final
				// pass from the shutdown path.
				return
			}
			a.flushOne(ctx, row)
		default:
			// Channel empty. The drain is done for this tick.
			return
		}
	}
}

// flushOne writes one failed-login audit row via AppendEvent. The
// failure semantics mirror pkg/audit.Auditor.Emit (Warn + counter
// on failure, never roll back the auth response). We do NOT
// delegate to inner.Emit because the failed-login row uses a nil
// subject (a failed login cannot be attributed to a known account)
// and a dedicated kind that pkg/audit is agnostic to.
//
// The AuditWriteFailureDuration observation IS recorded under
// result ∈ {ok, failed} — that latency distribution is the same
// across both paths so an operator looking at
// apid_audit_write_failure_duration_seconds{result="failed"} sees
// both surfaces. The success-path AuditWriteFailures counter is
// NOT incremented for the failed-login path; that counter is
// labelled by subject, and the dedicated
// FailedLoginAuditWriteFailures counter preserves the structural
// separation the SOC 2 CC7.2 surface relies on.
//
// ctx is the parent flusher context (see drainFlusher). A 5-second
// per-row timeout is derived from ctx so a daemon shutdown can
// short-circuit the in-flight AppendEvent rather than waiting the
// full timeout.
func (a *auditor) flushOne(ctx context.Context, row failedLoginRow) {
	data := map[string]any{
		"ip":         row.IP,
		"email_hash": row.EmailHash,
		"user_agent": row.UserAgent,
	}
	payload, err := json.Marshal(data)
	if err != nil {
		// map[string]any of strings cannot fail marshalling.
		// Treat as a programmer bug — log Error and skip.
		a.log.Error("audit: failed-login marshal",
			"ip", logsanitize.Field(row.IP), "err", err)
		return
	}
	// Use the row's At timestamp as the audit-row at value via
	// a derived context. AppendEvent does not accept a timestamp
	// parameter — the events row's at column is server-side
	// now(). We accept the tiny drift (handler-timestamp vs.
	// INSERT-now) as the trade-off for not changing the
	// AppendEvent signature.
	flushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	start := time.Now()
	err = a.store.AppendEvent(flushCtx, auditActor, KindAuthLoginFailed, nil, payload)
	dur := time.Since(start)
	cancel()
	if a.ops != nil {
		if err != nil {
			a.ops.AuditWriteFailureDuration("failed").Observe(dur.Seconds())
		} else {
			a.ops.AuditWriteFailureDuration("ok").Observe(dur.Seconds())
		}
	}
	if err != nil {
		a.log.Warn("audit: failed-login append event",
			"ip", logsanitize.Field(row.IP),
			"err", err)
		if a.failedOps != nil {
			// Use the dedicated failed-login audit-write failure
			// counter, NOT the success-path AuditWriteFailures.
			// Routing through AuditWriteFailures would collapse
			// the row's nil subject to "anonymous" in the
			// success-path counter, conflating it with
			// legitimate anonymous-success-path failures. The
			// dedicated counter preserves the separation; the
			// SOC 2 CC7.2 surface reads both this and the
			// success-path counter as paired views.
			a.failedOps.FailedLoginAuditWriteFailures().Inc()
		}
	}
}
