package receivers

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/requestanalytics"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

// Rollup dimension kinds (request_analytics_minutes.dimension_kind).
const (
	RollupKindRoute        = requestanalytics.KindRoute
	RollupKindCountry      = requestanalytics.KindCountry
	RollupKindReferrerHost = requestanalytics.KindReferrerHost
	RollupKindUAFamily     = requestanalytics.KindUAFamily
	RollupKindConsumerID   = requestanalytics.KindConsumerID

	rollupOtherDimension     = requestanalytics.OtherDimension
	rollupAnonymousConsumer  = requestanalytics.AnonymousConsumer
	rollupFinalFlushDeadline = 5 * time.Second
	rollupFoldedStatus       = requestanalytics.FoldedStatus
)

var rollupFoldedKinds = requestanalytics.FoldedKinds

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

type rollupWindowKey struct {
	app    uuid.UUID
	minute int64
}

type rollupGapKey struct {
	app    uuid.UUID
	minute int64
}

// RequestAnalyticsRollups folds received telemetry rows into per-minute
// rollups and adds them to Postgres every flush interval
// (ADR-20261103113952). The folding is requestanalytics.Aggregator, shared
// with the gateways that count their own requests (amendment 5); this adds
// replay dedupe, coverage, lost-minute gaps and the Postgres write. It is
// safe for concurrent use by every telemetry stream of the process.
type RequestAnalyticsRollups struct {
	store state.RequestAnalyticsRollupStore
	log   *slog.Logger
	agg   *requestanalytics.Aggregator

	flushMu sync.Mutex
	markers map[uuid.UUID]rollupWindowKey
	marked  map[rollupWindowKey]bool
	mu      sync.Mutex
	// seen and seenRing hold the most recent event IDs, oldest first from
	// seenNext, so a replayed row is counted once.
	seen     map[uuid.UUID]struct{}
	seenRing []uuid.UUID
	seenNext int
	// covered holds apps whose coverage this process has recorded;
	// uncovered holds the earliest pending minute of the others.
	covered   map[uuid.UUID]struct{}
	uncovered map[uuid.UUID]int64
	// gaps holds app minutes whose rollups were lost and are not yet
	// recorded, so analytics can report the window as incomplete.
	gaps map[rollupGapKey]struct{}

	requests *prometheus.CounterVec
	flushes  *prometheus.CounterVec

	// now is the flush clock; tests replace it.
	now func() time.Time
	// marker is the crash marker path (WithCrashMarker); Run's goroutine
	// alone uses it.
	marker string
}

// NewRequestAnalyticsRollups returns an aggregator writing to store. A nil
// registerer skips metrics.
func NewRequestAnalyticsRollups(store state.RequestAnalyticsRollupStore, reg prometheus.Registerer, log *slog.Logger) *RequestAnalyticsRollups {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	a := &RequestAnalyticsRollups{
		store:   store,
		markers: map[uuid.UUID]rollupWindowKey{}, marked: map[rollupWindowKey]bool{},
		log:       log,
		agg:       requestanalytics.NewAggregator(0),
		seen:      map[uuid.UUID]struct{}{},
		seenRing:  make([]uuid.UUID, 0, 1024),
		covered:   map[uuid.UUID]struct{}{},
		uncovered: map[uuid.UUID]int64{},
		gaps:      map[rollupGapKey]struct{}{},
		now:       time.Now,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "request_analytics_rollup_requests_total",
			Help: "Requests the analytics rollups received, by outcome: added, replayed (an event ID already counted), dropped (buffer full while Postgres fails), rejected (Postgres refused the row's data).",
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
	counted, _ := a.add(context.Background(), row)
	return counted
}

func (a *RequestAnalyticsRollups) add(ctx context.Context, row RequestAnalyticsRollupRow) (bool, error) {
	if a == nil || row.Count < 1 || row.Status < 100 || row.Status > 599 {
		return false, nil
	}
	minute := requestanalytics.Minute(row.ReceivedAt)
	a.mu.Lock()
	defer a.mu.Unlock()
	if row.EventID != uuid.Nil {
		if _, dup := a.seen[row.EventID]; dup {
			a.requests.WithLabelValues("replayed").Add(float64(row.Count))
			return false, nil
		}
	}
	window := rollupWindowKey{app: row.AppID, minute: minute}
	if _, durable := a.store.(state.RequestAnalyticsWindowStore); durable && !a.marked[window] {
		id := uuid.New()
		if err := a.recordWindow(ctx, id, row.AppID, minute, "pending"); err != nil {
			return false, err
		}
		a.marked[window] = true
		a.markers[id] = window
	}
	switch a.agg.Add(requestanalytics.Row{
		AccountID: row.AccountID, AppID: row.AppID, DeploymentID: row.DeploymentID, ReceivedAt: row.ReceivedAt,
		Route: row.Route, Method: row.Method, Status: row.Status, LatencyMS: row.LatencyMS, Count: row.Count,
		ColdBoot: row.ColdBoot, Country: row.Country, ReferrerHost: row.ReferrerHost, UAFamily: row.UAFamily,
		ConsumerID: row.ConsumerID,
	}) {
	case requestanalytics.Dropped:
		a.requests.WithLabelValues("dropped").Add(float64(row.Count))
		a.gapLocked(row.AppID, minute)
		id := uuid.NewSHA1(row.AppID, []byte("lost:"+strconv.FormatInt(minute, 10)))
		return false, a.recordWindow(ctx, id, row.AppID, minute, "lost")
	case requestanalytics.Invalid:
		return false, nil
	}
	a.rememberLocked(row.EventID)
	a.uncoveredLocked(row.AppID, minute)
	a.requests.WithLabelValues("added").Add(float64(row.Count))
	return true, nil
}

// uncoveredLocked notes minute as the app's earliest pending one unless its
// coverage is recorded.
func (a *RequestAnalyticsRollups) uncoveredLocked(app uuid.UUID, minute int64) {
	if _, ok := a.covered[app]; ok {
		return
	}
	if first, ok := a.uncovered[app]; !ok || minute < first {
		a.uncovered[app] = minute
	}
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

// requestAnalyticsLatencyBucket is the latency_hist index for latencyMS.
func requestAnalyticsLatencyBucket(latencyMS int) int {
	return requestanalytics.LatencyBucket(latencyMS)
}

// Flush writes the pending rollups of the minutes that closed at least
// RequestAnalyticsRollupMinuteGrace ago. Each minute is then written once,
// with one top-N fold per kind, while it is still inside
// RequestAnalyticsSettleLag, which readers report as arriving. Rows of a
// minute that arrive after its write are added by a later flush. Rows
// Postgres refuses for their data are isolated and dropped, and their
// minutes recorded as gaps; on any other error the unwritten rows are merged
// back, as far as the buffer bound allows, so the next flush retries them.
func (a *RequestAnalyticsRollups) Flush(ctx context.Context) error {
	return a.flush(ctx, false)
}

// flush writes the closed minutes, or every pending minute when all is set
// (shutdown).
func (a *RequestAnalyticsRollups) flush(ctx context.Context, all bool) error {
	if a == nil || a.store == nil {
		return nil
	}
	a.flushMu.Lock()
	defer a.flushMu.Unlock()
	ready := a.now().Add(-time.Minute - api.RequestAnalyticsRollupMinuteGrace).Unix()
	a.mu.Lock()
	rows := a.agg.Take(ready, all)
	markers := map[uuid.UUID]rollupWindowKey{}
	for id, w := range a.markers {
		if all || w.minute <= ready {
			markers[id] = w
			delete(a.markers, id)
			delete(a.marked, w)
		}
	}
	completed := false
	defer func() {
		if !completed {
			a.mu.Lock()
			defer a.mu.Unlock()
			for id, w := range markers {
				a.markers[id] = w
				a.marked[w] = true
			}
		}
	}()

	// An app's coverage starts at its earliest pending minute, so it is
	// recorded with the flush that writes that minute.
	uncovered := map[uuid.UUID]int64{}
	for app, minute := range a.uncovered {
		if all || minute <= ready {
			uncovered[app] = minute
			delete(a.uncovered, app)
		}
	}
	a.mu.Unlock()
	if len(rows) == 0 && len(uncovered) == 0 {
		if err := a.flushGaps(ctx); err != nil {
			return err
		}
		if err := a.completeWindows(ctx, markers); err != nil {
			return err
		}
		completed = true
		return nil
	}
	coverage := make([]state.RequestAnalyticsCoverage, 0, len(uncovered))
	for app, minute := range uncovered {
		coverage = append(coverage, state.RequestAnalyticsCoverage{AppID: app, CoveredSince: time.Unix(minute, 0).UTC()})
	}
	sort.Slice(coverage, func(i, j int) bool {
		return coverage[i].AppID.String() < coverage[j].AppID.String()
	})
	lost, unwritten, coverage, err := a.write(ctx, rows, coverage)
	if len(lost) > 0 {
		a.reject(lost)
	}
	if err != nil {
		a.flushes.WithLabelValues("error").Inc()
		a.restore(unwritten, coverage)
		return err
	}
	a.flushes.WithLabelValues("ok").Inc()
	a.mu.Lock()
	for app := range uncovered {
		a.covered[app] = struct{}{}
		delete(a.uncovered, app)
	}
	a.mu.Unlock()
	if err := a.flushGaps(ctx); err != nil {
		return err
	}
	if err := a.completeWindows(ctx, markers); err != nil {
		return err
	}
	completed = true
	return nil
}

// write adds rows, and the coverage with the first batch that lands. When
// Postgres refuses a batch for its data it splits the batch until the
// refused rows stand alone and returns them as lost. On another error it
// stops and returns the rows not yet written, and the coverage if it is not.
func (a *RequestAnalyticsRollups) write(ctx context.Context, rows []state.RequestAnalyticsRollup, coverage []state.RequestAnalyticsCoverage) (lost, unwritten []state.RequestAnalyticsRollup, leftCoverage []state.RequestAnalyticsCoverage, err error) {
	queue := [][]state.RequestAnalyticsRollup{rows}
	for len(queue) > 0 {
		batch := queue[0]
		queue = queue[1:]
		err := a.store.AddRequestAnalyticsRollups(ctx, batch, coverage)
		switch {
		case err == nil:
			coverage = nil
		case !state.IsRequestAnalyticsDataError(err):
			unwritten = append([]state.RequestAnalyticsRollup(nil), batch...)
			for _, rest := range queue {
				unwritten = append(unwritten, rest...)
			}
			return lost, unwritten, coverage, err
		case len(batch) > 1:
			mid := len(batch) / 2
			queue = append([][]state.RequestAnalyticsRollup{batch[:mid], batch[mid:]}, queue...)
		case len(coverage) > 0:
			// The refused write may be the coverage, not the row: retry the
			// row alone and give the coverage up; a later flush records it.
			a.log.Error("request analytics rollup coverage refused; recording it later", "err", err)
			coverage = nil
			queue = append([][]state.RequestAnalyticsRollup{batch}, queue...)
		default:
			a.log.Error("request analytics rollup refused for its data; dropping it", "app_id", firstRollupApp(batch), "err", err)
			lost = append(lost, batch...)
		}
	}
	return lost, nil, coverage, nil
}

func firstRollupApp(rows []state.RequestAnalyticsRollup) string {
	if len(rows) == 0 {
		return ""
	}
	return rows[0].AppID.String()
}

// reject counts rows Postgres refused and marks their minutes as gaps.
func (a *RequestAnalyticsRollups) reject(rows []state.RequestAnalyticsRollup) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, r := range rows {
		// Every request is in exactly one route row, so those count requests.
		if r.DimensionKind == RollupKindRoute {
			a.requests.WithLabelValues("rejected").Add(float64(r.Requests))
		}
		a.gapLocked(r.AppID, r.BucketStart.Unix())
	}
}

func (a *RequestAnalyticsRollups) gapLocked(app uuid.UUID, minute int64) {
	if len(a.gaps) < api.RequestAnalyticsRollupPendingGapsMax {
		a.gaps[rollupGapKey{app: app, minute: minute}] = struct{}{}
	}
}

// flushGaps records the pending gap minutes, keeping them on failure.
func (a *RequestAnalyticsRollups) flushGaps(ctx context.Context) error {
	a.mu.Lock()
	pending := a.gaps
	a.gaps = map[rollupGapKey]struct{}{}
	a.mu.Unlock()
	if len(pending) == 0 {
		return nil
	}
	gaps := make([]state.RequestAnalyticsGap, 0, len(pending))
	for g := range pending {
		gaps = append(gaps, state.RequestAnalyticsGap{AppID: g.app, Minute: time.Unix(g.minute, 0).UTC()})
	}
	if err := a.store.RecordRequestAnalyticsRollupGaps(ctx, gaps); err != nil {
		a.mu.Lock()
		for g := range pending {
			a.gapLocked(g.app, g.minute)
		}
		a.mu.Unlock()
		return err
	}
	return nil
}

// restore merges unwritten rows back into the buffer. Rows past its bound
// are dropped and their minutes recorded as gaps.
func (a *RequestAnalyticsRollups) restore(rows []state.RequestAnalyticsRollup, coverage []state.RequestAnalyticsCoverage) {
	lost := a.agg.Restore(rows)
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, r := range lost {
		if r.DimensionKind == RollupKindRoute {
			a.requests.WithLabelValues("dropped").Add(float64(r.Requests))
		}
		a.gapLocked(r.AppID, r.BucketStart.Unix())
	}
	for _, c := range coverage {
		a.uncoveredLocked(c.AppID, c.CoveredSince.Unix())
	}
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
	a.recoverFromCrash(ctx, interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			final, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollupFinalFlushDeadline)
			if err := a.flush(final, true); err != nil {
				a.log.Error("request analytics rollups final flush failed", "err", err)
			} else if a.marker != "" {
				// A clean stop: the next start has nothing to mark.
				_ = os.Remove(a.marker)
			}
			cancel()
			return
		case <-ticker.C:
			if err := a.Flush(ctx); err != nil {
				if ctx.Err() == nil {
					a.log.Warn("request analytics rollups flush failed; keeping rows for the next flush", "err", err)
				}
				continue
			}
			a.touchMarker()
		}
	}
}

// WithCrashMarker makes Run keep a file at path while it runs, touched at
// every flush and removed after a clean final flush (amendment 4). When Run
// starts and finds the file, the previous apid stopped without writing its
// pending rollups (a crash or a kill), and Run marks the minutes they may
// have held as lost, so analytics reports those windows incomplete instead
// of quietly under-counting. An empty path leaves it off.
func (a *RequestAnalyticsRollups) WithCrashMarker(path string) *RequestAnalyticsRollups {
	if a != nil {
		a.marker = path
	}
	return a
}

// recoverFromCrash marks what a previous apid lost and starts the marker.
// The lost rows are the minutes still pending at the marker's last touch,
// and rows added after it, so every app minute with raw telemetry from a
// minute, the grace and a flush interval before that touch until now is
// marked; apid writes raw rows as they arrive, and an app over its ingest
// cap still stores its first rows each minute.
func (a *RequestAnalyticsRollups) recoverFromCrash(ctx context.Context, interval time.Duration) {
	if a.marker == "" {
		return
	}
	if info, err := os.Stat(a.marker); err == nil {
		from := info.ModTime().Add(-time.Minute - api.RequestAnalyticsRollupMinuteGrace - interval)
		if crash, ok := a.store.(state.RequestAnalyticsRollupCrashStore); ok {
			if err := crash.RecordRequestAnalyticsRollupGapsFromTelemetry(ctx, from, a.now()); err != nil {
				a.log.Error("request analytics rollups: could not mark the minutes a stopped apid lost", "from", from, "err", err)
			} else {
				a.log.Warn("request analytics rollups: the previous apid stopped without its final flush; marked the minutes it may have lost", "from", from)
			}
		}
	}
	a.touchMarker()
}

// touchMarker creates or touches the crash marker. A marker that cannot be
// written turns the feature off for this run rather than failing analytics.
func (a *RequestAnalyticsRollups) touchMarker() {
	if a.marker == "" {
		return
	}
	now := a.now()
	if os.Chtimes(a.marker, now, now) == nil {
		return
	}
	f, err := os.OpenFile(a.marker, os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		err = f.Close()
	}
	if err == nil {
		err = os.Chtimes(a.marker, now, now)
	}
	if err != nil {
		a.log.Warn("request analytics rollups: crash marker unavailable; a crash's lost minutes will not be marked", "path", a.marker, "err", err)
		a.marker = ""
	}
}

// applyRequestTelemetry attaches the rollups to a telemetry receiver.
func (a *RequestAnalyticsRollups) applyRequestTelemetry(r *requestTelemetryReceiver) { r.rollups = a }

// addRollup counts one received row into the analytics rollups.
func (r *requestTelemetryReceiver) addRollup(ctx context.Context, req *apidpb.IncrementRequestTelemetryRequest, accountID, appID, deploymentID uuid.UUID, eventID string, count int, consumerID pgtype.UUID) error {
	if r.rollups == nil {
		return nil
	}
	eventUUID, _ := uuid.Parse(eventID)
	consumer := ""
	if consumerID.Valid {
		consumer = uuid.UUID(consumerID.Bytes).String()
	}
	method := api.NormalizeRequestAnalyticsMethod(req.GetMethod())
	_, err := r.rollups.add(ctx, RequestAnalyticsRollupRow{
		EventID: eventUUID, AccountID: accountID, AppID: appID, DeploymentID: deploymentID,
		ReceivedAt: msToTime(req.GetReceivedAtUnixMs()), Route: req.GetRouteTemplate(), Method: method,
		Status: int(req.GetHttpStatus()), LatencyMS: int(req.GetLatencyMs()), Count: count,
		ColdBoot: req.GetColdBoot(), Country: req.GetCountry(), ReferrerHost: req.GetReferrerHost(),
		UAFamily: req.GetUaFamily(), ConsumerID: consumer,
	})
	return err
}

// A separate completion transaction is conservative: a crash after counts
// commit leaves the marker pending until replay or retention, never complete.
func (a *RequestAnalyticsRollups) completeWindows(ctx context.Context, markers map[uuid.UUID]rollupWindowKey) error {
	if len(markers) == 0 {
		return nil
	}
	coverage := make([]state.RequestAnalyticsCoverage, 0, len(markers))
	for id, w := range markers {
		coverage = append(coverage, state.RequestAnalyticsCoverage{AppID: w.app, PendingID: id})
	}
	return a.store.AddRequestAnalyticsRollups(ctx, nil, coverage)
}
func (a *RequestAnalyticsRollups) recordWindow(ctx context.Context, id, app uuid.UUID, minute int64, kind string) error {
	store, ok := a.store.(state.RequestAnalyticsWindowStore)
	if !ok {
		return fmt.Errorf("rollup window persistence unavailable")
	}
	bounded, cancel := context.WithTimeout(ctx, api.RequestAnalyticsSampledMeasuresTimeout)
	defer cancel()
	if err := store.RecordRequestAnalyticsWindow(bounded, id, app, time.Unix(minute, 0).UTC(), kind); err != nil {
		return fmt.Errorf("record analytics %s window: %w", kind, err)
	}
	return nil
}
