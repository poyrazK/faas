// Package sched poller surface (issue #757 / ADR-0NN; commit #8
// of feat-triggers-mega).
//
// poller.go — shared interface + envelope for the broker-side
// data plane that feeds the dispatch tick (commit #14). One file
// per broker (kafka / nats / redis_streams / sqs_compat / queue)
// implements the triggerSource interface; the dispatch tick calls
// Poll on each source every dispatch-window interval and routes
// the resulting SourceRecord slice into the batch envelope that
// pkg/gateway/synth.go's InvokeBatch delivers to the function
// (commit #13).
//
// Why a broker-side interface (rather than a single goroutine per
// trigger): one poller per broker family lets us share connections
// across many triggers of the same kind (a single NATS connection
// can service N durable consumers; segment.io/kafka-go shares
// one reader per topic), and keeps the per-trigger state machine
// small (the trigger only owns the row-level FSM — pending →
// claimed → succeeded/retry/dead_letter).
//
// The broker library selection (commit #1) is pinned at this seam
// because every poller takes the same trigger context + returns the
// same SourceRecord shape — only the Ack/Nack semantics differ per
// broker:
//   - queue       UPDATE invocations.state (in-platform; rows
//     already committed before the poller sees them).
//   - kafka       CommitMessages on Ack; Seek+SetOffset on Nack.
//   - nats        js.Msg.Ack() on Ack; js.Msg.Nak(delay) on Nack.
//   - redis_streams  XAck on Ack; XClaim after visibility-timeout
//     on Nack.
//   - sqs_compat  POST .../delete on Ack; POST .../release on Nack.
package sched

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// SourceRecord is one broker-delivered record a poller hands back
// to the dispatch tick. The dispatch tick wraps this into a batch
// envelope (size OR window OR 6MB cap) and hands it to the gateway
// via InvokeBatch.
//
// Payload is raw JSON; Headers and Metadata are pass-through —
// the function may need them (a kafka consumer-group offset, an
// SQS ApproximateReceiveCount, etc.). The dispatch tick JSON-encodes
// the slice before delivery (commit #13).
//
// ItemIdentifier is the broker-side handle the poller will pass to
// Ack/Nack. Kafka's commit-message offset, NATS' ack token, Redis'
// entry id, SQS' receipt handle. Uniqueness across the broker's
// ledger is the broker's responsibility; uniqueness inside one
// trigger's claim window is the SQL UNIQUE (trigger_id,
// item_identifier) guarantee from migrations/00267_triggers.sql.
type SourceRecord struct {
	ItemIdentifier string            `json:"item_identifier"`
	Payload        []byte            `json:"payload"`
	Headers        map[string]string `json:"headers,omitempty"`
	Metadata       map[string]any    `json:"metadata,omitempty"`
	ReceivedAt     time.Time         `json:"received_at"`
}

// PollResult is what a poller's Poll call returns. Records is the
// broker-delivered slice up to the trigger's batch_size_max. Empty
// slice (not nil) signals "no data right now" — the dispatch tick
// skips the trigger for this window.
//
// Error is set when the broker connection itself failed (broker
// down, auth expired, etc.). The dispatch tick surfaces that via
// the trigger.dlq audit kind with reason='broker_error' for the
// affected records (commits #14/#15).
type PollResult struct {
	Records []SourceRecord
	Error   error
}

// triggerSource is the per-broker interface every poller file
// implements. The dispatch tick holds one triggerSource per (kind,
// trigger_id) pair and calls Poll/Ack/Nack/Close on it.
//
// The interface intentionally does NOT take the trigger's
// batch_size_max / batch_window_ms — those are enforced by the
// dispatch tick (commit #14) after Poll returns. Keeping the
// poller interface broker-agnostic lets each implementation pick
// its own "reasonable default" (kafka: 1 message; nats: pull-batch
// size = 100; queue: 64 rows; etc.) and the dispatch tick
// truncates-or-extends as needed to honour the per-trigger cap.
//
// Ack semantics:
//
//	Ack(t, ids) → commit success to the broker. After Ack returns,
//	the dispatch tick transitions the matching trigger_records
//	rows to state='succeeded' (commit #15 audit).
//
// Nack semantics:
//
//	Nack(t, ids, reason) → signal broker to redeliver. On
//	poller_queue (in-platform), Nack is a no-op since the rows
//	stay in `invocations` in state='pending' by definition; on
//	external brokers, Nack is the broker-native delay/retry
//	path. The dispatch tick transitions trigger_records to
//	state='retry' first, and on attempts >= max_attempts to
//	'dead_letter' + trigger_dead_letter row.
type triggerSource interface {
	// Kind returns the trigger kind this poller handles. The
	// dispatcher pairs a trigger with its poller by matching on
	// this value.
	Kind() string

	// Poll fetches up to the broker's natural batch limit. The
	// dispatcher may pull multiple times per dispatch window to
	// honour batch_size_max. context must be honored so a
	// scheduled shutdown can drain in-flight Polls.
	Poll(ctx context.Context, t sqlc.Trigger) PollResult

	// Ack tells the broker the records were dispatched and
	// successfully processed by the function. Idempotent — a
	// second Ack for the same id is a no-op for every broker.
	Ack(ctx context.Context, t sqlc.Trigger, ids []string) error

	// Nack signals broker-side retry / re-delivery. Reason is
	// the dispatcher-tagged string ('broker_error',
	// 'poison_record', 'payload_too_large') — each poller
	// decides whether to map it onto broker-native semantics
	// (kafka: log-and-skip; nats: Nak(delay); redis: XClaim;
	// sqs: Release; queue: no-op). Nack failure is logged but
	// does NOT block the dispatch tick — the next Poll will
	// pick up the same record and the retry/dead-letter FSM
	// will progress.
	Nack(ctx context.Context, t sqlc.Trigger, ids []string, reason string) error

	// Close releases broker resources (connections, ack tokens,
	// buffer pools). Called when the trigger is deleted / paused
	// permanently, or when the schedd is shutting down.
	Close() error
}

// pollerRegistry maps a kind string to its triggerSource factory.
// The dispatcher initialises this lazily on the first poll attempt
// for a given kind (commits #8..12 ship one factory each).
//
// Registry lookup misses (no poller for kind X) is a programming
// error — every kind supported by pkg/api/trigger.go MUST have a
// matching poller registration before the dispatcher tick starts.
// The unified pkg/api/trigger.go kind set is the canonical
// vocabulary; if a kind shows up in the SQL triggers.kind CHECK
// without a registered poller, runTriggerTick (commit #14) logs a
// loud error and skips the trigger rather than dispatching
// implicitly-broken records.
type pollerRegistry struct {
	mu        sync.Mutex
	factories map[string]func(t sqlc.Trigger) (triggerSource, error)
}

// registerPoller attaches a kind → factory pair. Called from
// init() blocks at the bottom of each poller_*.go file (commit
// #8 ships the queue one; commits #9-12 ship kafka/nats/redis/sqs).
// Idempotent on Kind collision — last write wins so a test harness
// can swap in a stub.
func registerPoller(kind string, factory func(t sqlc.Trigger) (triggerSource, error)) {
	defaultRegistry.mu.Lock()
	defer defaultRegistry.mu.Unlock()
	if defaultRegistry.factories == nil {
		defaultRegistry.factories = make(map[string]func(t sqlc.Trigger) (triggerSource, error))
	}
	defaultRegistry.factories[kind] = factory
}

// newPollerForTrigger looks up the registered poller factory for
// t.Kind and instantiates one. Returns (nil, false, nil) on miss — the
// dispatcher logs the gap and continues.
//
// Concurrency: the registry is read-mostly (writes happen at init
// time). We don't need an RWMutex; the writers hold the lock
// briefly and readers after init never block.
func newPollerForTrigger(t sqlc.Trigger) (triggerSource, bool, error) {
	defaultRegistry.mu.Lock()
	factory, ok := defaultRegistry.factories[t.Kind]
	defaultRegistry.mu.Unlock()
	if !ok {
		return nil, false, nil
	}
	src, err := factory(t)
	if err != nil {
		return nil, true, err
	}
	return src, true, nil
}

var defaultRegistry = pollerRegistry{}
