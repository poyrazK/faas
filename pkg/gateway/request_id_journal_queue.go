package gateway

// Asynchronous request-ID journal — ADR-634 (amends ADR-127).
//
// The exact public-ID index used to be written synchronously before guest
// work, and a failed write answered 503. Production-us hunt #4 measured what
// that costs: at about 100 rps of plain GETs on a Scale app the apid write
// path fell behind, a third of requests answered "Request correlation is
// temporarily unavailable", and throughput halved. The journal is a debugging
// index, so it must never decide whether a customer's request is served. The
// queue accepts records without blocking, writes them with bounded
// concurrency, and counts what it had to drop.

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Queue sizing. The capacity absorbs a multi-second apid stall at the rates a
// single gateway serves. The worker count bounds concurrent apid RPCs, and so
// the share of apid's 12-connection database pool the journal can hold: two
// compute gateways at 4 writers each leave apid at least 4 connections for
// its API traffic. Measured on production-us, the synchronous write starved
// that pool (12/12 acquired, ~22 s of acquire wait per second) at ~100 rps.
const (
	requestIDJournalQueueSize    = 16384
	requestIDJournalQueueWorkers = 4
	requestIDJournalWriteTimeout = 2 * time.Second
)

// errRequestIDJournalQueueFull reports a record dropped because the writers
// were behind.
var errRequestIDJournalQueueFull = errors.New("gateway: request ID journal queue full")

// RequestIDJournalQueue decouples journal writes from request handling.
type RequestIDJournalQueue struct {
	write   RequestIDJournalWriter
	metrics *Metrics
	log     *slog.Logger
	ch      chan RequestIDJournalRecord
	once    sync.Once
}

// NewRequestIDJournalQueue wraps write; call Run to start the writers.
func NewRequestIDJournalQueue(write RequestIDJournalWriter, metrics *Metrics, log *slog.Logger) *RequestIDJournalQueue {
	if log == nil {
		log = slog.Default()
	}
	return &RequestIDJournalQueue{write: write, metrics: metrics, log: log, ch: make(chan RequestIDJournalRecord, requestIDJournalQueueSize)}
}

// Submit enqueues record without blocking. It is a RequestIDJournalWriter, so
// it can be installed with Handler.WithRequestIDJournalWriter.
func (q *RequestIDJournalQueue) Submit(_ context.Context, record RequestIDJournalRecord) error {
	select {
	case q.ch <- record:
		return nil
	default:
		q.metrics.IncRequestIDJournalDropped()
		return errRequestIDJournalQueueFull
	}
}

// Run writes queued records until ctx ends.
func (q *RequestIDJournalQueue) Run(ctx context.Context) {
	q.once.Do(func() {
		var wg sync.WaitGroup
		for range requestIDJournalQueueWorkers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				q.drain(ctx)
			}()
		}
		wg.Wait()
	})
}

func (q *RequestIDJournalQueue) drain(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case record := <-q.ch:
			// The request that produced the record may be long gone; the
			// write has its own deadline.
			wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), requestIDJournalWriteTimeout)
			started := time.Now()
			err := q.write(wctx, record)
			cancel()
			q.metrics.ObserveRequestIDJournalWrite(time.Since(started), err)
			if err != nil {
				q.log.Debug("gateway: request ID journal write failed", "app_id", record.AppID, "err", err)
			}
		}
	}
}
