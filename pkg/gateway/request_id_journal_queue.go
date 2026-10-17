package gateway

// Asynchronous, batched request-ID journal — ADR-634 (amends ADR-127) and
// ADR-1521.
//
// The exact public-ID index used to be written synchronously before guest
// work, and a failed write answered 503. Production-us hunt #4 measured what
// that costs: at about 100 rps of plain GETs on a Scale app the apid write
// path fell behind, a third of requests answered "Request correlation is
// temporarily unavailable", and throughput halved. The journal is a debugging
// index, so it must never decide whether a customer's request is served. The
// queue accepts records without blocking and counts what it had to drop.
//
// ADR-1521: one apid RPC, account read and insert per customer request still
// put the control-plane database on the request path (measured 1:1 rows per
// request on production-us). A batcher now gathers queued records for up to
// requestIDJournalBatchLinger or requestIDJournalBatchMax records and hands
// each batch to a bounded pool of writers, so apid work scales with flushes,
// not requests.

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Queue sizing. The capacity absorbs a multi-second apid stall at the rates a
// single gateway serves. The writer count bounds concurrent apid RPCs, and so
// the share of apid's 12-connection database pool the journal can hold: two
// compute gateways at 4 writers each leave apid at least 4 connections for
// its API traffic. Measured on production-us, the synchronous write starved
// that pool (12/12 acquired, ~22 s of acquire wait per second) at ~100 rps.
const (
	requestIDJournalQueueSize    = 16384
	requestIDJournalQueueWorkers = 4
	// requestIDJournalBatchMax is a quarter of apid's 512-record batch cap.
	requestIDJournalBatchMax = 128
	// requestIDJournalBatchLinger is how long the batcher waits for more
	// records after the first; a record reaches apid at most this much later.
	requestIDJournalBatchLinger = 100 * time.Millisecond
	// requestIDJournalFlushTimeout caps one batch; the writer sets its own
	// per-RPC deadlines inside it.
	requestIDJournalFlushTimeout = 30 * time.Second
)

// errRequestIDJournalQueueFull reports a record dropped because the writers
// were behind.
var errRequestIDJournalQueueFull = errors.New("gateway: request ID journal queue full")

// RequestIDJournalBatchResult counts what apid did with the records of one
// batch. Records not counted here are counted as failed.
type RequestIDJournalBatchResult struct {
	// Recorded were written, or were identical retries of existing records.
	Recorded int
	// Rejected were refused by apid: malformed, not owned by the account, or
	// the account's plan keeps no debugger data.
	Rejected int
	// Failed could not be written; only the per-record fallback reports them.
	Failed int
}

// RequestIDJournalBatchWriter persists one batch. An error means nothing in
// the batch was written. It must not keep records after it returns.
type RequestIDJournalBatchWriter func(context.Context, []RequestIDJournalRecord) (RequestIDJournalBatchResult, error)

// RequestIDJournalQueue decouples journal writes from request handling.
type RequestIDJournalQueue struct {
	write    RequestIDJournalBatchWriter
	metrics  *Metrics
	log      *slog.Logger
	ch       chan RequestIDJournalRecord
	once     sync.Once
	maxBatch int
	linger   time.Duration
}

// NewRequestIDJournalQueue wraps write; call Run to start the batcher and
// writers.
func NewRequestIDJournalQueue(write RequestIDJournalBatchWriter, metrics *Metrics, log *slog.Logger) *RequestIDJournalQueue {
	if log == nil {
		log = slog.Default()
	}
	return &RequestIDJournalQueue{
		write: write, metrics: metrics, log: log,
		ch:       make(chan RequestIDJournalRecord, requestIDJournalQueueSize),
		maxBatch: requestIDJournalBatchMax, linger: requestIDJournalBatchLinger,
	}
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

// Run batches and writes queued records until ctx ends. A batch the batcher
// already formed is still written after ctx ends, under its own timeout.
func (q *RequestIDJournalQueue) Run(ctx context.Context) {
	q.once.Do(func() {
		// While every writer is busy the batcher waits here and records keep
		// queueing in q.ch, so the next batch fills at once.
		batches := make(chan []RequestIDJournalRecord)
		var wg sync.WaitGroup
		for range requestIDJournalQueueWorkers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for batch := range batches {
					q.flush(ctx, batch)
				}
			}()
		}
		q.batch(ctx, batches)
		close(batches)
		wg.Wait()
	})
}

// batch forms batches from q.ch and sends them to the writers until ctx ends.
func (q *RequestIDJournalQueue) batch(ctx context.Context, batches chan<- []RequestIDJournalRecord) {
	for {
		select {
		case <-ctx.Done():
			return
		case record := <-q.ch:
			batch := q.fill(ctx, append(make([]RequestIDJournalRecord, 0, q.maxBatch), record))
			select {
			case batches <- batch:
			case <-ctx.Done():
				return
			}
		}
	}
}

// fill adds queued records to batch until it is full or the linger window
// after its first record closes.
func (q *RequestIDJournalQueue) fill(ctx context.Context, batch []RequestIDJournalRecord) []RequestIDJournalRecord {
	timer := time.NewTimer(q.linger)
	defer timer.Stop()
	for len(batch) < q.maxBatch {
		select {
		case record := <-q.ch:
			batch = append(batch, record)
		case <-timer.C:
			return batch
		case <-ctx.Done():
			return batch
		}
	}
	return batch
}

func (q *RequestIDJournalQueue) flush(ctx context.Context, batch []RequestIDJournalRecord) {
	// The requests that produced the records may be long gone; the batch has
	// its own deadline.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), requestIDJournalFlushTimeout)
	started := time.Now()
	result, err := q.write(wctx, batch)
	cancel()
	q.metrics.ObserveRequestIDJournalBatch(time.Since(started), len(batch), result, err)
	if err != nil {
		q.log.Debug("gateway: request ID journal batch failed", "records", len(batch), "err", err)
	}
}
