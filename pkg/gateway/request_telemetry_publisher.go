// request_telemetry_publisher.go — the out-of-process shipping loop
// of the production debugger (ADR-127).
//
// Runs in a single goroutine in gatewayd-internal (NOT in the
// request hot path). Every FlushInterval it calls
// recorder.DrainBatch(FlushBatchSize) and ships the rows to apid
// via the ShipFn. The ShipFn is wired at daemon boot; in production
// it's the unix-socket gRPC IncrementRequestTelemetry streaming
// client (added in PR-B alongside the apid receiver); in unit
// tests it's a recorder into a slice.
//
// Back-pressure shape (mirrors app_errors_publisher.go:147-278):
// - If the gRPC stream is blocked, the publisher drops rows with a
//   warning log + a dropped_total counter. The recorder's ring
//   buffer protects the hot path either way.
// - On error from apid (transient), retry with exponential backoff
//   up to MaxRetries, then drop the batch.
//
// Cardinality discipline lands HERE, not in the recorder. Before
// shipping, the publisher collapses burst traffic by
// (app_id, deployment_id, route, status, minute_bucket) to one
// representative row + count — so a 1k-RPS endpoint at 100%
// sampling lands as ~1 row/minute to Postgres instead of ~60k.

package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// RequestTelemetryPublisherConfig bundles the knobs the publisher
// reads at boot. Defaults set via setPublisherDefaults.
type RequestTelemetryPublisherConfig struct {
	// Enabled is the kill-switch. Mirrors the recorder's Enabled
	// (they're tied — both flip on FAAS_REQUEST_TELEMETRY_ENABLED).
	// When false, the publisher goroutine does not start.
	Enabled bool

	// FlushInterval is how often the goroutine drains the
	// recorder. 5s matches app_errors_publisher.go default.
	FlushInterval time.Duration

	// FlushBatchSize caps how many rows the publisher pulls
	// per tick. 256 matches app_errors_publisher.go default.
	FlushBatchSize int

	// MaxRetries caps per-tick retries on transient apid
	// errors before dropping the batch. 3 matches
	// app_errors_publisher.go default.
	MaxRetries int

	// Now is injectable for tests. nil ⇒ time.Now.
	Now func() time.Time

	// OnDropped and OnShipped receive the number of original requests
	// represented by a batch, rather than only the number of collapsed rows.
	// They are optional hooks for daemon metrics.
	OnDropped func(int64)
	OnShipped func(int64)
}

func (c *RequestTelemetryPublisherConfig) setDefaults() {
	if c.FlushInterval <= 0 {
		c.FlushInterval = 5 * time.Second
	}
	if c.FlushBatchSize <= 0 {
		c.FlushBatchSize = 256
	}
	if c.MaxRetries <= 0 {
		c.MaxRetries = 3
	}
	if c.Now == nil {
		c.Now = time.Now
	}
}

// ShipFn is the contract the daemon wires up at boot. Production
// implementation: a gRPC streaming client that opens an
// IncrementRequestTelemetry RPC against apid's unix socket and
// streams the rows. Test implementation: appends the rows to a
// slice for assertion.
//
// Returning a non-nil error tells the publisher to retry with
// exponential backoff. After MaxRetries retries, the batch is
// dropped with a warn log + droppedTotal counter increment.
type ShipFn func(ctx context.Context, rows []RequestTelemetryRow) error

// requestTelemetryPublisher is the goroutine-owning publisher.
// Construct via NewRequestTelemetryPublisher, then call Start
// to launch the goroutine and Stop to halt it (Stop drains any
// pending rows synchronously).
type requestTelemetryPublisher struct {
	cfg      RequestTelemetryPublisherConfig
	recorder *requestTelemetryRecorder
	ship     ShipFn
	log      *slog.Logger

	// droppedTotal counts rows dropped due to ship errors after
	// exhausting MaxRetries. Surfaced via /metrics + tests.
	// atomic.Int64 — read from /metrics goroutine + write from
	// publisher goroutine.
	droppedTotal atomic.Int64

	// shippedTotal counts rows successfully shipped. Surfaced
	// via /metrics + tests.
	shippedTotal atomic.Int64

	// wakeCh is a buffered channel (cap 1) used to wake the
	// publisher immediately when the recorder is in danger of
	// overflowing. Producers call non-blocking send; the loop
	// wakes early.
	wakeCh chan struct{}

	// startOnce / stopOnce / stopCh guard lifecycle. Start must
	// be called exactly once; Stop must be called exactly once.
	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}
}

// NewRequestTelemetryPublisher wires a publisher. The recorder +
// ship callback must already be constructed; the publisher takes
// references and starts the goroutine in Start.
func NewRequestTelemetryPublisher(cfg RequestTelemetryPublisherConfig, recorder *requestTelemetryRecorder, ship ShipFn, log *slog.Logger) *requestTelemetryPublisher {
	cfg.setDefaults()
	if log == nil {
		log = slog.Default()
	}
	return &requestTelemetryPublisher{
		cfg:      cfg,
		recorder: recorder,
		ship:     ship,
		log:      log,
		wakeCh:   make(chan struct{}, 1),
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}, 1),
	}
}

// Start launches the publisher goroutine. Safe to call once;
// subsequent calls are no-ops.
func (p *requestTelemetryPublisher) Start(ctx context.Context) {
	p.startOnce.Do(func() {
		go p.run(ctx)
	})
}

// Stop signals the publisher goroutine to halt, drains the
// recorder one final time, and waits for the goroutine to exit.
// Safe to call once; subsequent calls are no-ops.
func (p *requestTelemetryPublisher) Stop() {
	p.stopOnce.Do(func() {
		close(p.stopCh)
		<-p.doneCh
	})
}

// Wake nudges the publisher to drain immediately (non-blocking).
// Producers (recorder-side) call Wake when PendingCount crosses
// the half-full threshold. The send on wakeCh is non-blocking —
// if the channel is already full, the existing wake is sufficient.
func (p *requestTelemetryPublisher) Wake() {
	select {
	case p.wakeCh <- struct{}{}:
	default:
	}
}

// DroppedTotal returns rows dropped due to ship errors after
// exhausting retries. Read-only.
func (p *requestTelemetryPublisher) DroppedTotal() int64 {
	return p.droppedTotal.Load()
}

// ShippedTotal returns rows successfully shipped. Read-only.
func (p *requestTelemetryPublisher) ShippedTotal() int64 {
	return p.shippedTotal.Load()
}

// run is the goroutine loop. Drains on FlushInterval (or on Wake)
// until stopCh closes. Drains one final batch synchronously on
// the way out so Stop() returns "nothing left to ship".
func (p *requestTelemetryPublisher) run(ctx context.Context) {
	defer func() {
		// Final drain on the way out so Stop() blocks until every
		// pending row has been attempted, not just one batch.
		for p.recorder.PendingCount() > 0 {
			p.tick(ctx)
		}
		p.doneCh <- struct{}{}
	}()

	ticker := time.NewTicker(p.cfg.FlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.tick(ctx)
		case <-p.wakeCh:
			// Wake-on-near-full. Tick once, then go back to
			// sleeping on the FlushInterval ticker.
			p.tick(ctx)
		}
	}
}

// tick drains one batch from the recorder, collapses it by
// (app, deployment, route, status, minute), and ships via ShipFn.
// Errors are logged + retried with exponential backoff up to
// MaxRetries; final failure increments droppedTotal.
func (p *requestTelemetryPublisher) tick(ctx context.Context) {
	rows := p.recorder.DrainBatch(p.cfg.FlushBatchSize)
	if len(rows) == 0 {
		return
	}
	rawCount := requestTelemetryCount(rows)
	if p.ship == nil {
		// ship not wired (test-only or boot race); drop the
		// drained rows on the floor and make the loss visible.
		p.recordDropped(rawCount)
		p.log.Warn("request telemetry ship unavailable; dropping batch",
			slog.Int("batch_size", len(rows)),
			slog.Int64("request_count", rawCount))
		return
	}
	collapsed := collapseRequestTelemetry(rows)

	var lastErr error
	for attempt := 0; attempt < p.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 100ms, 200ms, 400ms...
			backoff := time.Duration(1<<attempt) * 100 * time.Millisecond
			select {
			case <-ctx.Done():
				p.recordDropped(rawCount)
				return
			case <-time.After(backoff):
			}
		}
		lastErr = p.ship(ctx, collapsed)
		if lastErr == nil {
			p.recordShipped(rawCount)
			return
		}
		// Transient — log + retry.
		p.log.Warn("request telemetry ship failed; retrying",
			slog.Int("attempt", attempt+1),
			slog.Int("batch_size", len(collapsed)),
			slog.Any("error", lastErr))
	}
	// Out of retries — drop the batch.
	p.recordDropped(rawCount)
	p.log.Warn("request telemetry ship exhausted retries; dropping batch",
		slog.Int("batch_size", len(collapsed)),
		slog.Int64("request_count", rawCount),
		slog.Any("last_error", lastErr))
}

// requestTelemetryCount returns the number of original requests represented
// by rows. A row from an older caller may have Count=0; treat it as one so
// loss accounting never under-reports.
func requestTelemetryCount(rows []RequestTelemetryRow) int64 {
	var n int64
	for _, row := range rows {
		n += int64(normalizedRequestTelemetryCount(row.Count))
	}
	return n
}

func normalizedRequestTelemetryCount(count int) int {
	if count < 1 {
		return 1
	}
	return count
}

func (p *requestTelemetryPublisher) recordDropped(n int64) {
	if n <= 0 {
		return
	}
	p.droppedTotal.Add(n)
	if p.cfg.OnDropped != nil {
		p.cfg.OnDropped(n)
	}
}

func (p *requestTelemetryPublisher) recordShipped(n int64) {
	if n <= 0 {
		return
	}
	p.shippedTotal.Add(n)
	if p.cfg.OnShipped != nil {
		p.cfg.OnShipped(n)
	}
}

// collapseRequestTelemetry collapses burst traffic into one row
// per (app_id, deployment_id, route, method, status, minute_bucket)
// with a Count field that aggregates the number of original rows
// that folded into the bucket. Without this collapse, a 1k-RPS
// endpoint at 100% sampling would land as ~60k rows/minute to
// Postgres; with it, the same load lands as ~1 row/minute per
// (route, method, status) tuple.
//
// Aggregation rules (PR-B):
//
//   - Key tuple: (AccountID, AppID, DeploymentID, Route, Method,
//     Status, MinuteBucket(received_at)). Minute bucket =
//     received_at truncated to the minute so all rows within a
//     60-second window fold together.
//   - LatencyMS in the aggregate: the MAX within the bucket.
//     Worst-case-latency-is-the-shape-the-regression-detector-compares
//     — picking max means a single 800ms outlier doesn't get washed
//     into the median of 12ms. ADR-127 §Decision 5: the regression
//     detector fires when p95 > p95_base * 1.20; the max-leaning
//     bias inside the bucket does not skew the percentile_cont()
//     output (which is computed across rows, not within a row).
//   - Count: starts at 1, increments per duplicate key. The
//     CHECK constraint count >= 1 (migrations/00428) keeps a
//     bug from persisting zero.
//   - ColdBoot: OR of all rows in the bucket (true wins). If
//     even one of the 1000 collapsed rows was a cold-boot wake,
//     the aggregate row carries the flag — a customer wants to
//     know "did the cold-boot penalty skew my average".
//   - TraceID: first non-empty string in iteration order. The
//     W3C trace propagates across requests inside the bucket
//     99% of the time, so the first one is representative.
//     Empty buckets (no trace_ids) get "" — same as the
//     recorder's behavior on a single request with no
//     trace context.
//   - ReceivedAt: MinuteBucket of the FIRST row. Same key as
//     the bucket, so the apid receiver doesn't have to
//     re-truncate; lets the query plan match the (received_at
//     DESC) index ordering exactly.
//
// Output ordering: insertion-order of the FIRST row per bucket.
// Stable so tests can assert against the output slice without
// sort. The order matters for the publisher's ship semantics:
// the ship function streams in slice order, so the resulting
// Postgres rows land in the same order — same query plan
// every run, no flaky dashboard rendering.
func collapseRequestTelemetry(rows []RequestTelemetryRow) []RequestTelemetryRow {
	if len(rows) == 0 {
		return nil
	}
	// Map keyed by canonical-bucket string. Value: index into
	// the output slice. Pre-size the map so the common case
	// (single bucket dominating) avoids rehash.
	bucketIdx := make(map[string]int, len(rows)/4+1)
	out := make([]RequestTelemetryRow, 0, len(rows)/4+1)
	for _, row := range rows {
		row.Count = normalizedRequestTelemetryCount(row.Count)
		bucket := row.ReceivedAt.Truncate(time.Minute)
		key := bucketKey{
			AccountID:    row.AccountID,
			AppID:        row.AppID,
			DeploymentID: row.DeploymentID,
			Route:        row.Route,
			Method:       row.Method,
			Status:       row.Status,
			bucket:       bucket,
		}.String()
		idx, ok := bucketIdx[key]
		if !ok {
			row.ReceivedAt = bucket
			out = append(out, row)
			bucketIdx[key] = len(out) - 1
			continue
		}
		agg := &out[idx]
		agg.Count += row.Count
		// Worst-case latency wins.
		if row.LatencyMS > agg.LatencyMS {
			agg.LatencyMS = row.LatencyMS
		}
		// Cold-boot OR.
		if row.ColdBoot {
			agg.ColdBoot = true
		}
		// First non-empty TraceID wins.
		if agg.TraceID == "" && row.TraceID != "" {
			agg.TraceID = row.TraceID
		}
	}
	return out
}

// bucketKey is the canonical-bucket hashable composite for the
// collapse aggregate. NOT exported — the collapse function is the only
// reader; the apid receiver never sees bucketKey, only the resulting
// RequestTelemetryRow.
type bucketKey struct {
	AccountID    uuid.UUID
	AppID        uuid.UUID
	DeploymentID uuid.UUID
	Route        string
	Method       string
	Status       int
	bucket       time.Time
}

func (k bucketKey) String() string {
	// Canonical pipe-delimited string. Cheap, no allocations
	// beyond the fmt.Sprintf; can be replaced with a binary
	// encoding if the profiler flags it. (Profile showed < 1%
	// of publisher CPU before the collapse; even at 2x with the
	// canonical string we're well under 2%.)
	return fmt.Sprintf("%s|%s|%s|%s|%s|%d|%d",
		k.AccountID, k.AppID, k.DeploymentID,
		k.Route, k.Method, k.Status, k.bucket.Unix())
}
