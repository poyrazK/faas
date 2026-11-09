package gateway

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/requestanalytics"
	"github.com/onebox-faas/faas/pkg/state"
)

// RollupShipFn delivers one batch of per-minute analytics rollups to apid
// under batchID (ADR-20261103113952 amendment 5). apid applies a batch_id
// once, so resending a batch whose answer was lost adds nothing. An empty
// batch asks whether apid takes batches at all.
//
// It wraps ErrRequestAnalyticsRollupsUnsupported when apid takes no batches
// and ErrRequestAnalyticsRollupBatchRefused when apid refuses this batch for
// good; any other error is retried with the same batch_id.
type RollupShipFn func(ctx context.Context, batchID uuid.UUID, rows []state.RequestAnalyticsRollup) error

var (
	// ErrRequestAnalyticsRollupsUnsupported: apid predates gateway rollups or
	// keeps none. The gateway leaves the counting to apid.
	ErrRequestAnalyticsRollupsUnsupported = errors.New("apid takes no gateway analytics rollups")
	// ErrRequestAnalyticsRollupBatchRefused: resending the batch fails again.
	ErrRequestAnalyticsRollupBatchRefused = errors.New("apid refused the analytics rollup batch")
)

// RequestAnalyticsRollupsConfig wires gateway-side analytics counting.
type RequestAnalyticsRollupsConfig struct {
	Ship RollupShipFn
	// MaxRows bounds the buffered rollup rows; 0 means
	// api.RequestAnalyticsGatewayRollupRowsMax.
	MaxRows int
	// BatchRows caps the rows per batch; 0 means
	// api.RequestAnalyticsRollupBatchRowsMax.
	BatchRows int
	// MaxBatches caps the unacknowledged batches kept to resend; 0 means
	// api.RequestAnalyticsGatewayRollupBatchesMax.
	MaxBatches int
	// ProbeInterval is how often a gateway whose apid takes no batches asks
	// again; 0 means api.RequestAnalyticsGatewayRollupProbeInterval.
	ProbeInterval time.Duration
	// OnShipped and OnLost receive request counts, for daemon metrics.
	OnShipped func(int64)
	OnLost    func(int64)
	Now       func() time.Time
}

func (c *RequestAnalyticsRollupsConfig) setDefaults() {
	if c.MaxRows <= 0 {
		c.MaxRows = api.RequestAnalyticsGatewayRollupRowsMax
	}
	if c.BatchRows <= 0 || c.BatchRows > api.RequestAnalyticsRollupBatchRowsMax {
		c.BatchRows = api.RequestAnalyticsRollupBatchRowsMax
	}
	if c.MaxBatches <= 0 {
		c.MaxBatches = api.RequestAnalyticsGatewayRollupBatchesMax
	}
	if c.ProbeInterval <= 0 {
		c.ProbeInterval = api.RequestAnalyticsGatewayRollupProbeInterval
	}
	if c.Now == nil {
		c.Now = time.Now
	}
}

// requestAnalyticsRollups counts each request into its minute as it is
// recorded, ahead of the telemetry ring, so analytics no longer depend on
// every raw row reaching apid. A row it counted is shipped with
// analytics_counted set and apid does not count it again; a row it did not
// count (counting inactive, buffer full) is counted by apid as before.
//
// Counting stays inactive until apid answers an empty batch, so a gateway
// rolled out ahead of its apid, or beside one that keeps no rollups, never
// flags a row apid would then skip.
type requestAnalyticsRollups struct {
	cfg RequestAnalyticsRollupsConfig
	log *slog.Logger
	agg *requestanalytics.Aggregator

	// mu orders count (read) against deactivation (write): no row is
	// flagged once the buffer it went into has been given up.
	mu     sync.RWMutex
	active atomic.Bool

	// The publisher actor owns the rest.
	batches   []rollupBatch
	nextProbe time.Time
	stopped   bool
	// losses receives the requests of batches given up, under the
	// recorder's ringMu, so coverage reports them as telemetry gaps.
	losses func(map[uuid.UUID]int64)
}

type rollupBatch struct {
	id   uuid.UUID
	rows []state.RequestAnalyticsRollup
}

func newRequestAnalyticsRollups(cfg RequestAnalyticsRollupsConfig, log *slog.Logger) *requestAnalyticsRollups {
	cfg.setDefaults()
	if log == nil {
		log = slog.Default()
	}
	return &requestAnalyticsRollups{cfg: cfg, log: log, agg: requestanalytics.NewAggregator(cfg.MaxRows)}
}

// count adds row to its minute and reports whether it did; the caller
// flags the row analytics_counted only then.
func (g *requestAnalyticsRollups) count(row RequestTelemetryRow) bool {
	if g == nil || !g.active.Load() || row.AppID == uuid.Nil || row.ReceivedAt.IsZero() {
		return false
	}
	consumer := ""
	if row.ConsumerID != "" {
		parsed, err := uuid.Parse(row.ConsumerID)
		if err != nil {
			return false // apid turns the row away; it counts nothing
		}
		consumer = parsed.String()
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if !g.active.Load() {
		return false
	}
	return g.agg.Add(requestanalytics.Row{
		AccountID: row.AccountID, AppID: row.AppID, DeploymentID: row.DeploymentID,
		ReceivedAt: row.ReceivedAt, Route: row.Route, Method: row.Method, Status: row.Status,
		LatencyMS: row.LatencyMS, Count: normalizedRequestTelemetryCount(row.Count), ColdBoot: row.ColdBoot,
		Country: row.Country, ReferrerHost: row.ReferrerHost, UAFamily: row.UAFamily, ConsumerID: consumer,
	}) == requestanalytics.Added
}

// deactivate stops counting and gives up what is buffered. The rows it
// counted were flagged, but an apid that takes no batches predates the flag
// and counts them itself, so nothing is recorded as lost.
func (g *requestAnalyticsRollups) deactivate(now time.Time) {
	g.mu.Lock()
	g.active.Store(false)
	g.agg.Take(0, true)
	g.mu.Unlock()
	g.batches = nil
	g.nextProbe = now.Add(g.cfg.ProbeInterval)
}

// tick runs on the publisher actor: it asks an inactive apid again when due,
// or batches the closed minutes and ships every waiting batch in order.
// final batches every minute and gives up what does not ship.
func (g *requestAnalyticsRollups) tick(ctx context.Context, final bool) {
	if g == nil || g.stopped {
		return
	}
	g.stopped = final
	now := g.cfg.Now()
	if !g.active.Load() {
		if !final && !now.Before(g.nextProbe) {
			g.probe(ctx, now)
		}
		return
	}
	if final {
		// Rows recorded from here on stay unflagged for apid to count.
		g.mu.Lock()
		g.active.Store(false)
		g.mu.Unlock()
	}
	ready := now.Add(-time.Minute - api.RequestAnalyticsRollupMinuteGrace).Unix()
	g.enqueue(g.agg.Take(ready, final))
	for len(g.batches) > 0 {
		b := g.batches[0]
		err := g.cfg.Ship(ctx, b.id, b.rows)
		switch {
		case err == nil:
			g.batches = g.batches[1:]
			if g.cfg.OnShipped != nil {
				g.cfg.OnShipped(rollupRequests(b.rows))
			}
			continue
		case errors.Is(err, ErrRequestAnalyticsRollupsUnsupported):
			g.log.Warn("request analytics: apid stopped taking gateway rollups; apid counts requests again", "err", err)
			g.deactivate(now)
			return
		case errors.Is(err, ErrRequestAnalyticsRollupBatchRefused):
			g.log.Error("request analytics: apid refused a rollup batch; its minutes are lost", "batch_id", b.id, "err", err)
			g.batches = g.batches[1:]
			g.lose(b.rows)
			continue
		}
		g.log.Warn("request analytics: rollup batch not delivered; resending", "batch_id", b.id, "pending_batches", len(g.batches), "err", err)
		break
	}
	if final {
		for _, b := range g.batches {
			g.lose(b.rows)
		}
		g.batches = nil
	}
}

func (g *requestAnalyticsRollups) probe(ctx context.Context, now time.Time) {
	err := g.cfg.Ship(ctx, uuid.New(), nil)
	switch {
	case err == nil:
		g.active.Store(true)
		g.log.Info("request analytics: gateway counts requests into rollups")
	case errors.Is(err, ErrRequestAnalyticsRollupsUnsupported):
		g.nextProbe = now.Add(g.cfg.ProbeInterval)
	default:
		// apid unreachable: ask again on a later tick.
		g.nextProbe = now.Add(api.RequestAnalyticsRollupFlushInterval)
	}
}

// enqueue splits rows into batches, giving up the oldest batches past the
// bound.
func (g *requestAnalyticsRollups) enqueue(rows []state.RequestAnalyticsRollup) {
	for len(rows) > 0 {
		n := min(len(rows), g.cfg.BatchRows)
		g.batches = append(g.batches, rollupBatch{id: uuid.New(), rows: rows[:n:n]})
		rows = rows[n:]
	}
	for len(g.batches) > g.cfg.MaxBatches {
		g.log.Error("request analytics: too many undelivered rollup batches; the oldest is lost", "batch_id", g.batches[0].id)
		g.lose(g.batches[0].rows)
		g.batches = g.batches[1:]
	}
}

func (g *requestAnalyticsRollups) lose(rows []state.RequestAnalyticsRollup) {
	if g.losses != nil {
		g.losses(rollupRequestsByApp(rows))
	}
	if g.cfg.OnLost != nil {
		g.cfg.OnLost(rollupRequests(rows))
	}
}

// undelivered sums, per app, the requests of closed minutes not yet
// acknowledged by apid. Coverage reports them as pending, so apid does not
// call a minute complete before its rollup lands.
func (g *requestAnalyticsRollups) undelivered() map[uuid.UUID]int64 {
	if g == nil {
		return nil
	}
	out := g.agg.PendingRequests(g.cfg.Now().Add(-time.Minute).Unix())
	for _, b := range g.batches {
		for app, n := range rollupRequestsByApp(b.rows) {
			out[app] += n
		}
	}
	return out
}

// rollupRequestsByApp is the requests rows carry, per app.
func rollupRequestsByApp(rows []state.RequestAnalyticsRollup) map[uuid.UUID]int64 {
	out := map[uuid.UUID]int64{}
	for _, r := range rows {
		if r.DimensionKind == requestanalytics.KindRoute {
			out[r.AppID] += r.Requests
		}
	}
	return out
}

// rollupRequests is the requests a batch carries: each request has one
// route row.
func rollupRequests(rows []state.RequestAnalyticsRollup) int64 {
	var n int64
	for _, r := range rows {
		if r.DimensionKind == requestanalytics.KindRoute {
			n += r.Requests
		}
	}
	return n
}
