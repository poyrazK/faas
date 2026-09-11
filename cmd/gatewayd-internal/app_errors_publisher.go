// app_errors_publisher — drains the recorder's ringbuffer and
// ships batches to apid via the unix-socket gRPC
// IncrementAppError streaming RPC (pkg/apidgrpc).
//
// Architecture (ADR-096 §3.5):
//   recorder.Middleware (request hot path)
//       ↓ enqueue
//   recorder's ringbuffer (in-process, O(1) append)
//       ↓ drainBatch on FlushInterval / FlushBatchSize
//   publisher's flush loop (separate goroutine)
//       ↓ open one streaming RPC per batch
//   apid gRPC handler (the dedupe-merge INSERT)
//
// The publisher's hot path NEVER blocks the request handler:
// ringbuffer append is O(1) under a single mutex; the flush
// loop runs on a separate goroutine. A stuck apid connection
// is handled by a per-call timeout and a db_error observation for every
// row in the failed batch. A fifth consecutive failure emits an additional
// high-signal warning while the periodic loop continues with fresh batches.
//
// Each batch owns one streaming RPC. IncrementAppError requires a
// client half-close before the server can finish and acknowledge the
// batch, so that stream cannot be reused by a later flush.

package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
)

// AppErrorsFlushInterval is the publisher's tick interval when
// the ringbuffer is below FlushBatchSize. 5s matches the §12
// dashboard panel's resolution; a tighter interval costs more
// RPC overhead, a looser one delays customer-visible counts.
const AppErrorsFlushInterval = 5 * time.Second

// AppErrorsFlushBatchSize is the trigger threshold — when the
// ringbuffer reaches this size the publisher flushes
// immediately, regardless of the interval. 256 rows is the
// load-bearing balance between per-row RPC overhead (≤ 256)
// and tail latency under a burst (≤ 5s for the next tick).
const AppErrorsFlushBatchSize = 256

// AppErrorsFlushRPCTimeout caps each individual streaming RPC
// (open + send batch + receive responses). 30s covers a cold
// apid start + the dedupe-merge INSERT for a 256-row batch
// (well under 1ms/row on the DB side; the 30s budget is for
// cold-start tail latency).
const AppErrorsFlushRPCTimeout = 30 * time.Second

// AppErrorsFlushMaxConsecutiveFailures is the high-signal warning threshold.
// Every failed batch is already dropped and counted in
// faas_gateway_app_errors_recorded_total{outcome="db_error"}; this threshold
// controls the additional warning and counter reset.
const AppErrorsFlushMaxConsecutiveFailures = 5

// appErrorsPublisher is the drain loop + RPC owner.
type appErrorsPublisher struct {
	rec    *appErrorsRecorder
	client apidgrpc.AppErrorsClient
	ops    *wire.OpsMetrics
	log    *slog.Logger

	// consecutiveFailures is reset on every successful flush.
	// Read + written under stateMu.
	stateMu             sync.Mutex
	consecutiveFailures int

	// NotifyEnqueued is called by the recorder after every
	// enqueue. Used to wake the flush loop early when the
	// ringbuffer crosses FlushBatchSize.
	notifier chan struct{}

	// started guards Run from being called twice.
	started atomic.Bool
}

// newAppErrorsPublisher wires a production publisher.
func newAppErrorsPublisher(rec *appErrorsRecorder, client apidgrpc.AppErrorsClient, ops *wire.OpsMetrics, log *slog.Logger) *appErrorsPublisher {
	return &appErrorsPublisher{
		rec:      rec,
		client:   client,
		ops:      ops,
		log:      log,
		notifier: make(chan struct{}, 1),
	}
}

// NotifyEnqueued is called by the recorder after every
// enqueue. Non-blocking: when the channel is full the call
// is dropped (the FlushInterval will pick up the backlog on
// the next tick anyway).
func (p *appErrorsPublisher) NotifyEnqueued() {
	select {
	case p.notifier <- struct{}{}:
	default:
	}
}

// Run is the drain loop. Blocks until ctx is cancelled.
// Flushes every AppErrorsFlushInterval OR whenever
// NotifyEnqueued is called AND the ringbuffer has crossed
// FlushBatchSize (whichever fires first).
func (p *appErrorsPublisher) Run(ctx context.Context) {
	if !p.started.CompareAndSwap(false, true) {
		// Already running — duplicate call.
		return
	}
	t := time.NewTicker(AppErrorsFlushInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.tryFlush(ctx)
		case <-p.notifier:
			// Wakeup: only flush if the ringbuffer is
			// actually full enough to bother. Avoids a
			// per-request wakeup → tiny batches.
			if p.ringLen() >= AppErrorsFlushBatchSize {
				p.tryFlush(ctx)
			}
		}
	}
}

// tryFlush runs one flush attempt. Errors are logged and
// counted toward the consecutive-failures drop threshold;
// they do NOT abort the loop.
func (p *appErrorsPublisher) tryFlush(parent context.Context) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(parent, AppErrorsFlushRPCTimeout)
	defer cancel()

	batch := p.rec.drainBatch(AppErrorsFlushBatchSize)
	if len(batch) == 0 {
		// Empty drain — observe the duration so the §12
		// FlushDurationP99 panel has data from idle fleet.
		if p.ops != nil {
			p.ops.ObserveAppErrorsFlushDuration(time.Since(start).Seconds())
		}
		return
	}

	err := p.flushBatch(ctx, batch)
	elapsed := time.Since(start).Seconds()
	if p.ops != nil {
		p.ops.ObserveAppErrorsFlushDuration(elapsed)
	}
	if err != nil {
		p.recordFailure(err, batch)
		return
	}
	p.recordSuccess()
}

// flushBatch opens a streaming RPC and ships
// every row in batch. Per-row failures are observed but do
// NOT abort the stream (mirrors the apid handler's
// per-record commit semantics).
func (p *appErrorsPublisher) flushBatch(ctx context.Context, batch []appErrorRow) error {
	stream, err := p.openStream(ctx)
	if err != nil {
		return err
	}
	for i := range batch {
		req := &apidpb.IncrementAppErrorRequest{
			AccountId:         batch[i].AccountID,
			AppId:             batch[i].AppID,
			DeploymentId:      batch[i].DeploymentID,
			RouteTemplate:     batch[i].Route,
			HttpStatus:        int32(batch[i].HTTPStatus),
			ErrorClass:        batch[i].ErrorClass,
			Fingerprint:       batch[i].Fingerprint,
			SampleMessage:     batch[i].SampleMsg,
			HeadersSampleJson: batch[i].HeadersJSON,
			RedactionsApplied: batch[i].Redactions,
			ReceivedAtUnixMs:  batch[i].ReceivedAt.UnixMilli(),
			InstanceId:        batch[i].InstanceID,
		}
		if err := stream.Send(req); err != nil {
			return err
		}
	}
	// Half-close so the server flushes.
	if err := stream.CloseSend(); err != nil {
		return err
	}
	// Drain responses to completion (the server commits
	// each record inside its own tx; the per-record response
	// is the dedupe-merge / inserted signal).
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if resp == nil {
			return nil
		}
		if resp.GetOutcome() == "merged" && p.ops != nil {
			p.ops.ObserveAppErrorsDedupeMerge()
		}
	}
}

// openStream opens a fresh RPC for one batch. flushBatch half-closes
// every stream before draining acknowledgements, so retaining it would
// make the next batch fail with SendMsg called after CloseSend.
func (p *appErrorsPublisher) openStream(ctx context.Context) (apidgrpc.AppErrorStream, error) {
	return p.client.IncrementAppError(ctx)
}

// recordFailure increments the consecutive-failure counter AND
// observes `db_error` for every row in the dropped batch on
// every failure — NOT just after the 5th consecutive one.
//
// Why: the batch was drained from the ringbuffer in tryFlush
// BEFORE being passed to flushBatch, so a failure means those
// rows are already gone. Suppressing the per-row db_error
// observe until the 5th failure means the §12 dashboard misses
// the first 4 failures of every outage, and an operator looking
// at the rate panel sees a clean signal right up until the
// tripwire fires — by which point 5×256 rows have been lost.
//
// At AppErrorsFlushMaxConsecutiveFailures the publisher emits a
// high-signal batch-loss warning and resets the tripwire counter.
func (p *appErrorsPublisher) recordFailure(err error, batch []appErrorRow) {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	p.consecutiveFailures++
	p.log.Warn("app_errors flush failed", "consecutive_failures", p.consecutiveFailures, "err", err)
	if p.ops != nil {
		for range batch {
			p.ops.ObserveAppErrorsRecorded("db_error")
		}
	}
	if p.consecutiveFailures >= AppErrorsFlushMaxConsecutiveFailures {
		// The batch is gone (drained in tryFlush); the per-row
		// db_error observations above are the data-loss signal.
		p.log.Warn("app_errors flush consecutive-failure threshold reached",
			"batch_size", len(batch),
			"consecutive_failures", p.consecutiveFailures)
		p.consecutiveFailures = 0
	}
}

// recordSuccess resets the consecutive-failure counter.
func (p *appErrorsPublisher) recordSuccess() {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	p.consecutiveFailures = 0
}

// ringLen returns the current ringbuffer length. Used by
// Run to decide whether the notifier wakeup is worth a flush.
func (p *appErrorsPublisher) ringLen() int {
	p.rec.ringMu.Lock()
	defer p.rec.ringMu.Unlock()
	return p.rec.len
}

// Close releases the underlying client connection. Safe to call multiple times.
func (p *appErrorsPublisher) Close() error {
	if p.client != nil {
		return p.client.Close()
	}
	return nil
}

// keep imports honest
var _ = slog.Default
