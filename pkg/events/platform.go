// platform.go — pkg/events.Platform is the typed fan-out seam every
// wake-timeline site routes through (issue #517 / PR-C, ADR-064).
//
// One Emit call writes:
//   - an `events` row via state.Store.AppendEvent (cross-process
//     audit source of truth, pgntied via the existing
//     NotifyAuditEvent channel),
//   - a wake-phase counter increment on the per-daemon OpsMetrics
//     registry (so the §12 wake-latency panel surfaces the
//     distribution),
//   - a wake-phase duration observation on the per-daemon histogram
//     (the per-call AppendEvent duration, observed by Emit itself
//     — the per-boot_span Phase(ctx, name) closure was removed
//     from the API, see PR #532 review),
//   - an in-process SSE pub/sub via Broadcaster.PublishTopic on the
//     `wake` topic (so the dashboard's "live wake" surface react
//     within milliseconds without depending on pg_notify),
//   - a slog envelope line with the per-event correlation fields
//     (request_id, wake_id, etc.) — inherited from the per-daemon
//     correlation logger (issue #517 PR-A, pkg/wire/logging.go).
//
// Failure semantics (issue #278 / ADR-064 §"Failure semantics"):
// every Emit failure is best-effort. A failed AppendEvent logs
// Warn, increments the per-daemon wake-phase counter under
// result="failed", and returns silently. The wake lifecycle is
// the source of truth — the audit row is observation.
//
// Why extend pkg/events rather than introduce pkg/wake or
// pkg/lifecycle: pkg/events is already the in-process SSE pub/sub
// seam (broadcaster.go) and the cross-process story is Postgres
// LISTEN. The package's existing doc-comment ("Postgres LISTEN
// is the cross-process story; pkg/events is the in-process wake-up")
// is the contract PR-C earns — same broadcast pattern, same
// in-process semantics, now with a typed driver.
package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/state"
)

// Ops is the narrow counter+histogram surface Platform needs. The
// concrete *wire.OpsMetrics satisfies it (every daemon has one per
// the single-registry pattern, memory wire-opsmetrics-single-registry);
// tests stub it. Defined as an interface so the helper can be
// unit-tested with a stub.
//
// nil ops is allowed — Platform.Emit skips the counter increment
// and the latency observation so unit tests can run without an
// OpsMetrics.
type Ops interface {
	// WakePhaseEmitted increments the per-(daemon, phase, result)
	// counter. phase is the substring after `wake.` (e.g.
	// "boot_started", "readiness_200", "proxy_first_byte"); result
	// is "ok" or "failed" (AppendEvent return).
	WakePhaseEmitted(phase, result string) prometheus.Counter
	// WakePhaseDuration observes the per-(phase, result) histogram.
	// Buckets sized for the wake envelope (queue→admit <100ms; boot
	// <30s; readiness <60s; proxy <5s).
	WakePhaseDuration(phase, result string) prometheus.Observer
	// RecoveryEventEmitted increments the per-(daemon, kind, result)
	// counter for the recovery timeline (Workstream B, issue #1184).
	// kind is the substring after `node.` / `instance.` (e.g.
	// "draining", "recovered", "migrated"); result is "ok" or
	// "failed".
	RecoveryEventEmitted(kind, result string) prometheus.Counter
}

// BroadcasterIf is the in-process pub/sub surface Platform
// publishes to. Production wires *Broadcaster (broadcaster.go);
// tests stub it. nil is allowed — Platform skips the publish.
type BroadcasterIf interface {
	PublishTopic(topic string, payload []byte) int
}

// TopicWake is the SSE topic Platform publishes the wake envelope
// to. Subscribers (the dashboard's live-wake panel — future
// work, deferred to a follow-up PR) receive one Event per Emit
// call. Mirrors the existing Topic constants in broadcaster.go.
const TopicWake = "wake"

// Platform is the per-daemon wake-event fan-out. Constructed once
// per daemon and held on the engine/server. The single Emit method
// is what every wake-timeline site calls.
type Platform struct {
	actor       string
	store       state.Store
	log         *slog.Logger
	ops         Ops
	broadcaster BroadcasterIf
}

// NewPlatform builds a Platform. actor is the literal value written
// to events.actor for every row this Platform emits — the spec §5
// convention is <daemon-name> ("apid", "schedd", "vmmd", etc.).
// Passing actor here rather than as a package-level const enforces
// the convention at the call site, so a future daemon that forgets
// to pass an actor fails to compile rather than silently writing an
// empty string.
//
// nil ops and nil broadcaster are allowed — Emit skips the
// counter increment and the publish path so unit tests can run
// without an OpsMetrics or a Broadcast scaffold.
func NewPlatform(actor string, store state.Store, log *slog.Logger, ops Ops, broadcaster BroadcasterIf) *Platform {
	return &Platform{
		actor:       actor,
		store:       store,
		log:         log,
		ops:         ops,
		broadcaster: broadcaster,
	}
}

// Actor returns the actor string this Platform was constructed
// with. Useful for tests and for the wake-timeline endpoint's
// actor attribution.
func (p *Platform) Actor() string { return p.actor }

// Emit writes one wake-timeline row. The WakeEvent interface is
// the schema — concrete payload structs (QueueAccepted, Admitted,
// BootStarted, etc., in wake.go) implement it. Emit is the only
// sanctioned emission path; callers MUST instantiate a typed
// struct rather than rolling their own map.
//
// Best-effort: every step (marshal, AppendEvent, counter, publish,
// log) is guarded — a failure in one step does not abort the
// others. The wake lifecycle is the source of truth; the audit
// row + counter + publish are observation.
func (p *Platform) Emit(ctx context.Context, ev WakeEvent) {
	if ev == nil {
		return
	}
	kind := ev.Kind()
	at := ev.At()
	subject := ev.Subject()
	payload := ev.Payload()
	if payload == nil {
		payload = map[string]any{}
	}
	phase := wakePhaseFromKind(kind)
	// Marshal happens BEFORE the counter increment so a marshal
	// failure (programmer bug on a payload struct) surfaces in the
	// log without bumping the counter. Same shape as pkg/audit.
	body, err := json.Marshal(payload)
	if err != nil {
		p.log.Error("events: marshal payload",
			"actor", p.actor, "kind", kind, "err", err)
		if p.ops != nil {
			p.ops.WakePhaseEmitted(phase, "failed").Inc()
		}
		return
	}
	// AppendEvent is the canonical events-row writer. Mirrors
	// pkg/audit.Auditor.Emit's best-effort semantics — a failure
	// here logs Warn, increments the counter under result="failed",
	// and returns. The wake lifecycle is the source of truth.
	start := time.Now()
	err = p.store.AppendEvent(ctx, p.actor, kind, subject, body)
	dur := time.Since(start)
	result := "ok"
	if err != nil {
		result = "failed"
	}
	if p.ops != nil {
		p.ops.WakePhaseDuration(phase, result).Observe(dur.Seconds())
		p.ops.WakePhaseEmitted(phase, result).Inc()
	}
	if err != nil {
		p.log.Warn("events: append event",
			"actor", p.actor, "kind", kind, "subject", subject, "err", err)
		return
	}
	// SSE pub/sub — fire-and-forget. Drops on full buffers (the
	// dashboard renders at human speeds; back-pressuring the
	// daemon is the wrong tradeoff).
	if p.broadcaster != nil {
		envelope := map[string]any{
			"at":      at.UTC(),
			"kind":    kind,
			"actor":   p.actor,
			"subject": subject,
			"data":    payload,
		}
		if envBody, marshalErr := json.Marshal(envelope); marshalErr == nil {
			p.broadcaster.PublishTopic(TopicWake, envBody)
		}
	}
	// Per-event slog line. The per-daemon correlation logger
	// (issue #517 PR-A, pkg/wire/logging.go) inherits the envelope
	// from ctx — the request_id / wake_id / app_id fields surface
	// automatically without re-stamping.
	p.log.Info("events: emit",
		"actor", p.actor, "kind", kind, "subject", subject)
}

// EmitRecovery writes one recovery-timeline row. Mirrors Emit's
// shape: best-effort AppendEvent + counter + SSE publish + slog line,
// guarded step-by-step. The recovery arbiter is the source of truth;
// the audit row + counter + publish are observation.
//
// The recovery timeline uses a separate SSE topic (TopicRecovery) so
// dashboard subscribers can filter the two streams independently —
// the wake timeline's TopicWake subscriber shouldn't see node.drained
// rows and vice versa.
func (p *Platform) EmitRecovery(ctx context.Context, ev RecoveryEvent) {
	if ev == nil {
		return
	}
	kind := ev.Kind()
	at := ev.At()
	subject := ev.Subject()
	payload := ev.Payload()
	if payload == nil {
		payload = map[string]any{}
	}
	recoveryKind := recoveryKindFromKind(kind)
	body, err := json.Marshal(payload)
	if err != nil {
		p.log.Error("events: marshal recovery payload",
			"actor", p.actor, "kind", kind, "err", err)
		if p.ops != nil {
			p.ops.RecoveryEventEmitted(recoveryKind, "failed").Inc()
		}
		return
	}
	start := time.Now()
	err = p.store.AppendEvent(ctx, p.actor, kind, subject, body)
	dur := time.Since(start)
	result := "ok"
	if err != nil {
		result = "failed"
	}
	if p.ops != nil {
		p.ops.RecoveryEventEmitted(recoveryKind, result).Inc()
	}
	if err != nil {
		p.log.Warn("events: append recovery event",
			"actor", p.actor, "kind", kind, "subject", subject, "err", err)
		return
	}
	if p.broadcaster != nil {
		envelope := map[string]any{
			"at":      at.UTC(),
			"kind":    kind,
			"actor":   p.actor,
			"subject": subject,
			"data":    payload,
		}
		if envBody, marshalErr := json.Marshal(envelope); marshalErr == nil {
			p.broadcaster.PublishTopic(TopicRecovery, envBody)
		}
	}
	p.log.Info("events: emit recovery",
		"actor", p.actor, "kind", kind, "subject", subject, "dur_ms", dur.Milliseconds())
}

// recoveryKindFromKind strips the `node.` / `instance.` prefix off
// a recovery-timeline kind so the metric label is short. Returns the
// full kind if no prefix matches (future-proof — a kind added by a
// follow-on PR still produces a stable label).
func recoveryKindFromKind(kind string) string {
	for _, prefix := range []string{"node.", "instance."} {
		if strings.HasPrefix(kind, prefix) {
			return strings.TrimPrefix(kind, prefix)
		}
	}
	return kind
}

// wakePhaseFromKind strips the `wake.` prefix and returns the
// phase label suffix. e.g. "wake.boot_started" → "boot_started",
// "wake.readiness_200" → "readiness_200". Returns the kind
// unchanged when the prefix is absent so a non-prefixed kind
// (e.g. legacy bare names) feeds the counter under its full name.
func wakePhaseFromKind(kind string) string {
	const prefix = "wake."
	if len(kind) > len(prefix) && kind[:len(prefix)] == prefix {
		return kind[len(prefix):]
	}
	return kind
}
