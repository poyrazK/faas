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
// (app_id, deployment_id, route, status, analytics dimensions, cold_boot,
// minute_bucket, latency_bucket) to one representative row + count.
// The bounded latency bucket is part of the key so percentile queries
// retain the request-latency distribution instead of seeing one
// minute-level maximum.

package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
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

	// ShutdownTimeout is one budget for joining an interrupted RPC and flushing
	// all remaining evidence. Values above the API ceiling are clamped.
	ShutdownTimeout time.Duration

	// Now is injectable for tests. nil ⇒ time.Now.
	Now func() time.Time

	// OnDropped and OnShipped receive the number of original requests
	// represented by a batch, rather than only the number of collapsed rows.
	// They are optional hooks for daemon metrics.
	OnDropped func(int64)
	OnShipped func(int64)

	// OnCoverage runs even when idle, after every delivery attempt.
	OnCoverage func(context.Context, RequestTelemetryCoverage) error
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
	maximumShutdown := time.Duration(api.GatewayRequestTelemetryShutdownTimeoutSeconds) * time.Second
	if c.ShutdownTimeout <= 0 || c.ShutdownTimeout > maximumShutdown {
		c.ShutdownTimeout = maximumShutdown
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
// The callback must honor context cancellation. Returning a non-nil error tells the publisher to retry with
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

	// Only the actor owns pending, including its final flush. A canceled RPC
	// retains the exact collapsed payload and event IDs for receiver deduplication.
	pending []RequestTelemetryRow

	lifecycleMu      sync.Mutex
	started, stopped bool
	parentCtx        context.Context
	cancel           context.CancelFunc
	finalCtx         context.Context
	finalCancel      context.CancelFunc
	stopCh           chan struct{}
	doneCh           chan struct{}
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
		doneCh:   make(chan struct{}),
	}
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
// until stopCh closes. Its bounded final flush ships remaining evidence or
// accounts for loss before every Stop caller returns.
func (p *requestTelemetryPublisher) run(ctx context.Context) {
	defer func() {
		p.drainFinal()
		p.finalCancel()
		close(p.doneCh)
	}()

	ticker := time.NewTicker(p.cfg.FlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ctx.Done():
			// HTTP producers can finish after daemon cancellation. Quiesce
			// ordinary shipping until their owner calls Stop after draining.
			<-p.stopCh
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
	if ctx.Err() != nil {
		return
	}
	defer p.reportCoverage(ctx)
	if len(p.pending) == 0 {
		p.pending = collapseRequestTelemetry(p.recorder.DrainBatch(p.cfg.FlushBatchSize))
	}
	if len(p.pending) == 0 {
		return
	}
	if p.ship == nil {
		p.dropPending("request telemetry ship unavailable; dropping batch")
		return
	}
	if p.shipPending(ctx) {
		p.pending = nil
	} else if ctx.Err() == nil {
		p.dropPending("request telemetry ship exhausted retries; dropping batch")
	}
}

// shipPending records every acknowledged row as shipped. A partial response
// keeps only the rejected rows pending, with their original event IDs.
func (p *requestTelemetryPublisher) shipPending(ctx context.Context) bool {
	for attempt := 0; attempt < p.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return false
			case <-time.After(time.Duration(1<<attempt) * 100 * time.Millisecond):
			}
		}
		if ctx.Err() != nil {
			return false
		}
		err := p.ship(ctx, p.pending)
		if err == nil {
			p.recordShipped(requestTelemetryCount(p.pending))
			return true
		}
		var partial *RequestTelemetryDeliveryError
		if errors.As(err, &partial) {
			p.acceptPartialDelivery(partial)
			if len(p.pending) == 0 {
				return true
			}
		}
		if ctx.Err() != nil {
			return false
		}
		p.log.Warn("request telemetry delivery incomplete", "attempt", attempt+1, "err", err)
	}
	return false
}

func (p *requestTelemetryPublisher) acceptPartialDelivery(partial *RequestTelemetryDeliveryError) {
	remaining := make([]RequestTelemetryRow, 0, len(p.pending))
	var shipped int64
	for _, row := range p.pending {
		if partial.Accepted[row.EventID] {
			shipped += int64(normalizedRequestTelemetryCount(row.Count))
		} else {
			remaining = append(remaining, row)
		}
	}
	p.recordShipped(shipped)
	p.pending = remaining
}

// Partial responses preserve accepted rows when another app is rejected.
type RequestTelemetryDeliveryError struct {
	Accepted map[uuid.UUID]bool
	Cause    error
}

func (e *RequestTelemetryDeliveryError) Error() string { return e.Cause.Error() }
func (e *RequestTelemetryDeliveryError) Unwrap() error { return e.Cause }

func (p *requestTelemetryPublisher) recordDroppedRows(rows []RequestTelemetryRow) {
	p.recorder.ringMu.Lock()
	for _, row := range rows {
		p.recorder.recordAppLossLocked(row)
	}
	p.recorder.ringMu.Unlock()
	p.recordDropped(requestTelemetryCount(rows))
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

// collapseRequestTelemetry collapses burst traffic into one row per
// (app_id, deployment_id, route, method, status, dimensions,
// minute_bucket, latency_bucket) with a Count field that aggregates
// the number of original rows that folded into the bucket. The latency
// bucket is deliberately part of the key: a single minute-level maximum
// makes p95/p99 mathematically impossible after collapse. The bounded
// buckets preserve the distribution with a small, documented quantization
// error while still keeping storage cardinality bounded.
//
// Aggregation rules (PR-B):
//
//   - Key tuple: (AccountID, AppID, DeploymentID, Route, Method,
//     Status, normalized dimensions, MinuteBucket(received_at),
//     LatencyBucket(LatencyMS), WakeID). Minute bucket = received_at. The
//     instance identifier is carried as representative metadata and is
//     cleared when a collapsed bucket spans multiple instances.
//     truncated to the minute so all rows within a 60-second window
//     fold together. LatencyBucket uses fine-grained 10ms buckets for
//     normal request latencies and progressively wider buckets for very
//     slow requests; the maximum quantization error is bounded by the
//     selected bucket width.
//   - LatencyMS in the aggregate: the inclusive upper bound of the
//     latency bucket. Percentile queries expand Count over these bounded
//     representatives, so p50/p95/p99 no longer collapse to a weighted
//     per-minute maximum. Values are conservative by at most one bucket
//     width, which is preferable to silently reporting a false exact
//     percentile.
//   - Count: starts at 1, increments per duplicate key. The
//     CHECK constraint count >= 1 (migrations/00428) keeps a
//     bug from persisting zero.
//   - ColdBoot is part of the bucket key. Warm and cold-boot requests
//     must remain separate so weighted cold-boot counts and percentiles
//     don't mistake every request in a mixed bucket for a cold wake.
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
		if row.EventID == uuid.Nil {
			row.EventID = uuid.New()
		}
		row.Count = normalizedRequestTelemetryCount(row.Count)
		if row.preserveExact {
			out = append(out, row)
			continue
		}
		row.LatencyMS = requestTelemetryLatencyBucketUpperBound(row.LatencyMS)
		row.GuestDurationMS = requestTelemetryLatencyBucketUpperBound(row.GuestDurationMS)
		row.GuestCPUTimeMS = requestTelemetryCPUTimeBucketUpperBound(row.GuestCPUTimeMS)
		row.GuestPeakRSSMB = requestTelemetryMemoryBucketUpperBound(row.GuestPeakRSSMB)
		bucket := row.ReceivedAt.Truncate(time.Minute)
		key := bucketKey{
			FlagEvidenceJSON:                     row.FlagEvidenceJSON,
			AccountID:                            row.AccountID,
			AppID:                                row.AppID,
			DeploymentID:                         row.DeploymentID,
			Route:                                row.Route,
			Method:                               row.Method,
			Status:                               row.Status,
			UAFamily:                             row.UAFamily,
			ReferrerHost:                         row.ReferrerHost,
			Country:                              row.Country,
			WakeID:                               row.WakeID,
			TraceInferred:                        row.TraceInferred,
			GuestRuntime:                         row.GuestRuntime,
			GuestOutcome:                         row.GuestOutcome,
			GuestErrorClass:                      row.GuestErrorClass,
			GuestDurationBucket:                  row.GuestDurationMS,
			GuestCPUBucket:                       row.GuestCPUTimeMS,
			GuestRSSBucket:                       row.GuestPeakRSSMB,
			GuestResourceUsageAvailable:          row.GuestResourceUsageAvailable,
			ConsumerID:                           row.ConsumerID,
			PlatformTenantID:                     row.PlatformTenantID,
			PlatformTenantSurfaceID:              row.PlatformTenantSurfaceID,
			PlatformTenantJWTAuthorizationRuleID: row.PlatformTenantJWTAuthorizationRuleID,
			UsageOutboxed:                        row.UsageOutboxed,
			NodeID:                               row.NodeID,
			Region:                               row.Region,
			CommitSHA:                            row.CommitSHA,
			DeploymentTag:                        row.DeploymentTag,
			DeploymentCreatedAt:                  row.DeploymentCreatedAt,
			ImageDigest:                          row.ImageDigest,
			ColdBoot:                             row.ColdBoot,
			LatencyBucket:                        row.LatencyMS,
			ContentOutcome:                       row.ContentOutcome,
			bucket:                               bucket,
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
		// First non-empty TraceID wins.
		if agg.TraceID == "" && row.TraceID != "" {
			agg.TraceID = row.TraceID
		}
		// Warm traffic can span multiple instances while retaining the same
		// bounded bucket. Keep an instance only when it is unambiguous.
		if agg.InstanceID != row.InstanceID {
			agg.InstanceID = ""
		}
	}
	return out
}

// bucketKey is the canonical-bucket hashable composite for the
// collapse aggregate. NOT exported — the collapse function is the only
// reader; the apid receiver never sees bucketKey, only the resulting
// RequestTelemetryRow.
type bucketKey struct {
	FlagEvidenceJSON                     string
	AccountID                            uuid.UUID
	AppID                                uuid.UUID
	DeploymentID                         uuid.UUID
	Route                                string
	Method                               string
	Status                               int
	UAFamily                             string
	ReferrerHost                         string
	Country                              string
	WakeID                               string
	TraceInferred                        bool
	GuestRuntime                         string
	GuestOutcome                         string
	GuestErrorClass                      string
	GuestDurationBucket                  int
	GuestCPUBucket                       int
	GuestRSSBucket                       int
	GuestResourceUsageAvailable          bool
	ConsumerID                           string
	PlatformTenantID                     string
	PlatformTenantSurfaceID              string
	PlatformTenantJWTAuthorizationRuleID string
	UsageOutboxed                        bool
	NodeID                               string
	Region                               string
	CommitSHA                            string
	DeploymentTag                        string
	DeploymentCreatedAt                  string
	ImageDigest                          string
	ColdBoot                             bool
	LatencyBucket                        int
	ContentOutcome                       string
	bucket                               time.Time
}

func (k bucketKey) String() string {
	// Include every dimension in the key; the Go-syntax representation
	// preserves field boundaries and avoids delimiter collisions in route or
	// deployment metadata.
	return fmt.Sprintf("%#v", k)
}

func requestTelemetryMemoryBucketUpperBound(megabytes int) int {
	if megabytes <= 0 {
		return 0
	}
	width := 256
	switch {
	case megabytes <= 64:
		width = 4
	case megabytes <= 256:
		width = 16
	case megabytes <= 1024:
		width = 64
	}
	return ((megabytes + width - 1) / width) * width
}

// requestTelemetryCPUTimeBucketUpperBound keeps CPU/request useful at the
// single-digit and low-double-digit millisecond range while still bounding
// cardinality for unusually expensive handlers.
func requestTelemetryCPUTimeBucketUpperBound(cpuMS int) int {
	if cpuMS <= 0 {
		return 0
	}
	width := 100
	switch {
	case cpuMS <= 100:
		width = 1
	case cpuMS <= 500:
		width = 5
	case cpuMS <= 2_000:
		width = 25
	}
	return ((cpuMS + width - 1) / width) * width
}

// requestTelemetryLatencyBucketUpperBound quantizes a request latency to a
// bounded histogram representative. The SQL percentile queries treat each
// stored row as Count requests at this representative value. Narrow buckets
// cover normal request latencies; wider buckets keep pathological outliers
// from creating unbounded row cardinality.
//
// The function is monotonic and idempotent at bucket boundaries, which is
// important during rolling upgrades when an already-collapsed row is received
// by a newer gateway publisher.
func requestTelemetryLatencyBucketUpperBound(latencyMS int) int {
	if latencyMS <= 0 {
		return 0
	}
	width := 5_000
	switch {
	case latencyMS <= 1_000:
		width = 10
	case latencyMS <= 5_000:
		width = 50
	case latencyMS <= 10_000:
		width = 250
	case latencyMS <= 30_000:
		width = 1_000
	}
	rem := latencyMS % width
	if rem == 0 {
		return latencyMS
	}
	// Protect the addition for the theoretical int overflow case. The
	// database/protobuf path already bounds values to int32; returning the
	// original value is least surprising if an int-sized value reaches this
	// helper in a unit test.
	maxInt := int(^uint(0) >> 1)
	if latencyMS > maxInt-(width-rem) {
		return latencyMS
	}
	return latencyMS + (width - rem)
}

// RequestTelemetryAppGap is a loss delta plus a complete backlog snapshot.
type RequestTelemetryAppGap struct {
	AppID        uuid.UUID
	DroppedCount int64
	PendingCount int
}

// RequestTelemetryCoverage describes completed-request delivery, not client migration.
type RequestTelemetryCoverage struct {
	Enabled                  bool
	SamplingBasisPoints      int
	DroppedTotal             int64
	PendingCount             int
	SourceAt                 time.Time
	AppScoped                bool
	UnattributedDroppedTotal int64
	AppGaps                  []RequestTelemetryAppGap
}

func (p *requestTelemetryPublisher) reportCoverage(ctx context.Context) {
	if p.cfg.OnCoverage == nil {
		return
	}
	p.recorder.ringMu.Lock()
	coverage := RequestTelemetryCoverage{Enabled: p.recorder.cfg.Enabled, SamplingBasisPoints: 10000,
		DroppedTotal: p.DroppedTotal() + p.recorder.OverwrittenTotal(), PendingCount: p.recorder.len, SourceAt: p.cfg.Now().UTC(),
		AppScoped: true, UnattributedDroppedTotal: p.recorder.unattributedLosses}
	gaps := make(map[uuid.UUID]RequestTelemetryAppGap)
	for app, n := range p.recorder.appLosses {
		gaps[app] = RequestTelemetryAppGap{AppID: app, DroppedCount: n}
	}
	for i := 0; i < p.recorder.len; i++ {
		row := p.recorder.ring[(p.recorder.head+i)%len(p.recorder.ring)]
		if row.AppID == uuid.Nil {
			continue
		}
		gap := gaps[row.AppID]
		gap.AppID = row.AppID
		gap.PendingCount++
		gaps[row.AppID] = gap
	}
	for _, gap := range gaps {
		coverage.AppGaps = append(coverage.AppGaps, gap)
	}
	p.recorder.ringMu.Unlock()
	sort.Slice(coverage.AppGaps, func(i, j int) bool { return coverage.AppGaps[i].AppID.String() < coverage.AppGaps[j].AppID.String() })
	if err := p.cfg.OnCoverage(ctx, coverage); err != nil {
		p.log.Warn("request telemetry coverage heartbeat failed", "err", err)
		return
	}
	// Subtract only acknowledged deltas; concurrent losses remain in the journal.
	p.recorder.ringMu.Lock()
	for _, gap := range coverage.AppGaps {
		if n := p.recorder.appLosses[gap.AppID] - gap.DroppedCount; n > 0 {
			p.recorder.appLosses[gap.AppID] = n
		} else {
			delete(p.recorder.appLosses, gap.AppID)
		}
	}
	p.recorder.ringMu.Unlock()
}
