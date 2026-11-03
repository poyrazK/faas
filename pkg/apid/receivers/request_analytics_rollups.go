package receivers

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

// Rollup dimension kinds (request_analytics_minutes.dimension_kind).
const (
	RollupKindRoute        = "route"
	RollupKindCountry      = "country"
	RollupKindReferrerHost = "referrer_host"
	RollupKindUAFamily     = "ua_family"
	RollupKindConsumerID   = "consumer_id"

	rollupOtherDimension     = "__other__"
	rollupAnonymousConsumer  = "__anonymous__"
	rollupFinalFlushDeadline = 5 * time.Second
)

var rollupFoldedKinds = [...]string{RollupKindCountry, RollupKindReferrerHost, RollupKindUAFamily, RollupKindConsumerID}

// RequestAnalyticsRollupRow is one received telemetry row, as the rollups
// see it. Count is the row's collapsed request weight.
type RequestAnalyticsRollupRow struct {
	EventID      uuid.UUID
	AccountID    uuid.UUID
	AppID        uuid.UUID
	DeploymentID uuid.UUID
	ReceivedAt   time.Time
	Route        string
	Method       string
	Status       int
	LatencyMS    int
	Count        int
	ColdBoot     bool
	Country      string
	ReferrerHost string
	UAFamily     string
	// ConsumerID is empty for an anonymous request.
	ConsumerID string
}

type rollupKey struct {
	app        uuid.UUID
	minute     int64
	kind       string
	dimension  string
	method     string
	status     int16
	deployment uuid.UUID
}

type rollupDimensionKey struct {
	app    uuid.UUID
	minute int64
	kind   string
}

// RequestAnalyticsRollups folds received telemetry rows into per-minute
// rollups and adds them to Postgres every flush interval
// (ADR-20261103113952). It is safe for concurrent use by every telemetry
// stream of the process.
type RequestAnalyticsRollups struct {
	store state.RequestAnalyticsRollupStore
	log   *slog.Logger

	mu      sync.Mutex
	pending map[rollupKey]*state.RequestAnalyticsRollup
	dims    map[rollupDimensionKey]map[string]struct{}
	// seen and seenRing hold the most recent event IDs, oldest first from
	// seenNext, so a replayed row is counted once.
	seen     map[uuid.UUID]struct{}
	seenRing []uuid.UUID
	seenNext int
	// covered holds apps whose coverage this process has recorded;
	// uncovered holds the earliest pending minute of the others.
	covered   map[uuid.UUID]struct{}
	uncovered map[uuid.UUID]int64

	requests *prometheus.CounterVec
	flushes  *prometheus.CounterVec
}

// NewRequestAnalyticsRollups returns an aggregator writing to store. A nil
// registerer skips metrics.
func NewRequestAnalyticsRollups(store state.RequestAnalyticsRollupStore, reg prometheus.Registerer, log *slog.Logger) *RequestAnalyticsRollups {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	a := &RequestAnalyticsRollups{
		store:     store,
		log:       log,
		pending:   map[rollupKey]*state.RequestAnalyticsRollup{},
		dims:      map[rollupDimensionKey]map[string]struct{}{},
		seen:      map[uuid.UUID]struct{}{},
		seenRing:  make([]uuid.UUID, 0, 1024),
		covered:   map[uuid.UUID]struct{}{},
		uncovered: map[uuid.UUID]int64{},
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "request_analytics_rollup_requests_total",
			Help: "Requests the analytics rollups received, by outcome: added, replayed (an event ID already counted), dropped (buffer full while Postgres fails).",
		}, []string{"outcome"}),
		flushes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "request_analytics_rollup_flushes_total",
			Help: "Analytics rollup flushes to Postgres, by outcome (ok, error).",
		}, []string{"outcome"}),
	}
	if reg != nil {
		reg.MustRegister(a.requests, a.flushes)
	}
	return a
}

// Add folds one row into the pending rollups. It reports whether the row
// was counted.
func (a *RequestAnalyticsRollups) Add(row RequestAnalyticsRollupRow) bool {
	if a == nil || row.Count < 1 || row.Status < 100 || row.Status > 599 {
		return false
	}
	minute := row.ReceivedAt.UTC().Truncate(time.Minute).Unix()
	a.mu.Lock()
	defer a.mu.Unlock()
	if row.EventID != uuid.Nil {
		if _, dup := a.seen[row.EventID]; dup {
			a.requests.WithLabelValues("replayed").Add(float64(row.Count))
			return false
		}
	}
	keys := a.keysLocked(row, minute)
	for _, k := range keys {
		if _, ok := a.pending[k]; !ok && len(a.pending) >= api.RequestAnalyticsRollupPendingRowsMax {
			a.requests.WithLabelValues("dropped").Add(float64(row.Count))
			return false
		}
	}
	a.rememberLocked(row.EventID)
	bucket := requestAnalyticsLatencyBucket(row.LatencyMS)
	for _, k := range keys {
		r := a.pending[k]
		if r == nil {
			r = &state.RequestAnalyticsRollup{
				AppID: row.AppID, AccountID: row.AccountID, DeploymentID: row.DeploymentID,
				BucketStart: time.Unix(minute, 0).UTC(), DimensionKind: k.kind,
				Dimension: k.dimension, Method: k.method, Status: k.status,
			}
			a.pending[k] = r
		}
		n := int64(row.Count)
		r.Requests += n
		if row.Status >= 400 {
			r.ErrorRequests += n
		}
		if row.ColdBoot {
			r.ColdBoots += n
		}
		r.LatencyMaxMS = max(r.LatencyMaxMS, int32(max(row.LatencyMS, 0)))
		r.LatencyHist[bucket] += n
	}
	if _, ok := a.covered[row.AppID]; !ok {
		if first, ok := a.uncovered[row.AppID]; !ok || minute < first {
			a.uncovered[row.AppID] = minute
		}
	}
	a.requests.WithLabelValues("added").Add(float64(row.Count))
	return true
}

// keysLocked returns the row's rollup keys: its route, and each folded
// dimension, which becomes __other__ once the app's minute already holds
// RequestAnalyticsRollupDimensionsMax values of that kind.
func (a *RequestAnalyticsRollups) keysLocked(row RequestAnalyticsRollupRow, minute int64) []rollupKey {
	base := rollupKey{app: row.AppID, minute: minute, status: int16(row.Status), deployment: row.DeploymentID}
	keys := make([]rollupKey, 0, 1+len(rollupFoldedKinds))
	route := base
	route.kind, route.dimension, route.method = RollupKindRoute, rollupDimension(row.Route, "__unknown__"), row.Method
	keys = append(keys, route)
	for _, kind := range rollupFoldedKinds {
		var value string
		switch kind {
		case RollupKindCountry:
			value = rollupDimension(row.Country, "__unknown__")
		case RollupKindReferrerHost:
			value = rollupDimension(row.ReferrerHost, "__none__")
		case RollupKindUAFamily:
			value = rollupDimension(row.UAFamily, "__unknown__")
		case RollupKindConsumerID:
			value = rollupDimension(row.ConsumerID, rollupAnonymousConsumer)
		}
		dk := rollupDimensionKey{app: row.AppID, minute: minute, kind: kind}
		values := a.dims[dk]
		if values == nil {
			values = map[string]struct{}{}
			a.dims[dk] = values
		}
		if _, ok := values[value]; !ok {
			if len(values) >= api.RequestAnalyticsRollupDimensionsMax {
				value = rollupOtherDimension
			} else {
				values[value] = struct{}{}
			}
		}
		k := base
		k.kind, k.dimension = kind, value
		keys = append(keys, k)
	}
	return keys
}

func rollupDimension(value, empty string) string {
	if value == "" {
		return empty
	}
	if len(value) > 512 {
		return value[:512]
	}
	return value
}

func (a *RequestAnalyticsRollups) rememberLocked(id uuid.UUID) {
	if id == uuid.Nil {
		return
	}
	if len(a.seenRing) < api.RequestAnalyticsRollupSeenEventsMax {
		a.seenRing = append(a.seenRing, id)
	} else {
		delete(a.seen, a.seenRing[a.seenNext])
		a.seenRing[a.seenNext] = id
		a.seenNext = (a.seenNext + 1) % len(a.seenRing)
	}
	a.seen[id] = struct{}{}
}

// requestAnalyticsLatencyBucket is the latency_hist index for latencyMS:
// the first bound at or above it, or the overflow bucket.
func requestAnalyticsLatencyBucket(latencyMS int) int {
	bounds := api.RequestAnalyticsLatencyBoundsMS[:]
	return sort.SearchInts(bounds, latencyMS)
}

// Flush writes the pending rollups. On error they are merged back, as far
// as the buffer bound allows, so the next flush retries them.
func (a *RequestAnalyticsRollups) Flush(ctx context.Context) error {
	if a == nil || a.store == nil {
		return nil
	}
	a.mu.Lock()
	pending, uncovered := a.pending, a.uncovered
	a.pending = map[rollupKey]*state.RequestAnalyticsRollup{}
	a.dims = map[rollupDimensionKey]map[string]struct{}{}
	a.uncovered = map[uuid.UUID]int64{}
	a.mu.Unlock()
	if len(pending) == 0 && len(uncovered) == 0 {
		return nil
	}
	rows := make([]state.RequestAnalyticsRollup, 0, len(pending))
	for _, r := range pending {
		rows = append(rows, *r)
	}
	// A stable order keeps concurrent replicas' row locks in one order.
	sort.Slice(rows, func(i, j int) bool { return rollupLess(rows[i], rows[j]) })
	coverage := make([]state.RequestAnalyticsCoverage, 0, len(uncovered))
	for app, minute := range uncovered {
		coverage = append(coverage, state.RequestAnalyticsCoverage{AppID: app, CoveredSince: time.Unix(minute, 0).UTC()})
	}
	sort.Slice(coverage, func(i, j int) bool {
		return coverage[i].AppID.String() < coverage[j].AppID.String()
	})
	if err := a.store.AddRequestAnalyticsRollups(ctx, rows, coverage); err != nil {
		a.flushes.WithLabelValues("error").Inc()
		a.restore(pending, uncovered)
		return err
	}
	a.flushes.WithLabelValues("ok").Inc()
	a.mu.Lock()
	for app := range uncovered {
		a.covered[app] = struct{}{}
		delete(a.uncovered, app)
	}
	a.mu.Unlock()
	return nil
}

func (a *RequestAnalyticsRollups) restore(pending map[rollupKey]*state.RequestAnalyticsRollup, uncovered map[uuid.UUID]int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, r := range pending {
		cur := a.pending[k]
		if cur == nil {
			if len(a.pending) >= api.RequestAnalyticsRollupPendingRowsMax {
				a.requests.WithLabelValues("dropped").Add(float64(r.Requests))
				continue
			}
			a.pending[k] = r
			continue
		}
		cur.Requests += r.Requests
		cur.ErrorRequests += r.ErrorRequests
		cur.ColdBoots += r.ColdBoots
		cur.LatencyMaxMS = max(cur.LatencyMaxMS, r.LatencyMaxMS)
		for i := range cur.LatencyHist {
			cur.LatencyHist[i] += r.LatencyHist[i]
		}
	}
	for app, minute := range uncovered {
		if first, ok := a.uncovered[app]; !ok || minute < first {
			a.uncovered[app] = minute
		}
	}
}

func rollupLess(x, y state.RequestAnalyticsRollup) bool {
	if x.AppID != y.AppID {
		return x.AppID.String() < y.AppID.String()
	}
	if !x.BucketStart.Equal(y.BucketStart) {
		return x.BucketStart.Before(y.BucketStart)
	}
	if x.DimensionKind != y.DimensionKind {
		return x.DimensionKind < y.DimensionKind
	}
	if x.Dimension != y.Dimension {
		return x.Dimension < y.Dimension
	}
	if x.Method != y.Method {
		return x.Method < y.Method
	}
	if x.Status != y.Status {
		return x.Status < y.Status
	}
	return x.DeploymentID.String() < y.DeploymentID.String()
}

// Run flushes every interval until ctx ends, then once more with a short
// deadline so a clean shutdown keeps the last interval's rows.
func (a *RequestAnalyticsRollups) Run(ctx context.Context, interval time.Duration) {
	if a == nil {
		return
	}
	if interval <= 0 {
		interval = api.RequestAnalyticsRollupFlushInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			final, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollupFinalFlushDeadline)
			if err := a.Flush(final); err != nil {
				a.log.Error("request analytics rollups final flush failed", "err", err)
			}
			cancel()
			return
		case <-ticker.C:
			if err := a.Flush(ctx); err != nil && ctx.Err() == nil {
				a.log.Warn("request analytics rollups flush failed; keeping rows for the next flush", "err", err)
			}
		}
	}
}

// applyRequestTelemetry attaches the rollups to a telemetry receiver.
func (a *RequestAnalyticsRollups) applyRequestTelemetry(r *requestTelemetryReceiver) { r.rollups = a }

// addRollup counts one received row into the analytics rollups.
func (r *requestTelemetryReceiver) addRollup(req *apidpb.IncrementRequestTelemetryRequest, accountID, appID, deploymentID uuid.UUID, eventID string, count int, consumerID pgtype.UUID) {
	if r.rollups == nil {
		return
	}
	eventUUID, _ := uuid.Parse(eventID)
	consumer := ""
	if consumerID.Valid {
		consumer = uuid.UUID(consumerID.Bytes).String()
	}
	method := req.GetMethod()
	if len(method) > 16 {
		method = method[:16]
	}
	r.rollups.Add(RequestAnalyticsRollupRow{
		EventID: eventUUID, AccountID: accountID, AppID: appID, DeploymentID: deploymentID,
		ReceivedAt: msToTime(req.GetReceivedAtUnixMs()), Route: req.GetRouteTemplate(), Method: method,
		Status: int(req.GetHttpStatus()), LatencyMS: int(req.GetLatencyMs()), Count: count,
		ColdBoot: req.GetColdBoot(), Country: req.GetCountry(), ReferrerHost: req.GetReferrerHost(),
		UAFamily: req.GetUaFamily(), ConsumerID: consumer,
	})
}
