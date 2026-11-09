package receivers

import (
	"context"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
	rollupDimensionMaxBytes  = 512 // request_analytics_minutes.dimension CHECK
	rollupMethodMaxBytes     = 16  // request_analytics_minutes.method CHECK
	rollupAnonymousConsumer  = "__anonymous__"
	rollupFinalFlushDeadline = 5 * time.Second
	// rollupFoldedStatus is the status of every non-route row, whose
	// deployment is the nil UUID: no reader splits those kinds by status or
	// deployment (amendment 3), and keying them by both multiplied their
	// rows about fivefold.
	rollupFoldedStatus = 0
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

type rollupGapKey struct {
	app    uuid.UUID
	minute int64
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
		store:     store,
		log:       log,
		pending:   map[rollupKey]*state.RequestAnalyticsRollup{},
		dims:      map[rollupDimensionKey]map[string]struct{}{},
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
			a.gapLocked(row.AppID, minute)
			return false
		}
	}
	a.rememberLocked(row.EventID)
	bucket := requestAnalyticsLatencyBucket(row.LatencyMS)
	for _, k := range keys {
		r := a.pending[k]
		if r == nil {
			r = &state.RequestAnalyticsRollup{
				AppID: row.AppID, AccountID: row.AccountID, DeploymentID: k.deployment,
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
		latency := int64(max(row.LatencyMS, 0))
		r.RequestTimeMS += latency * n
		r.LatencyMaxMS = max(r.LatencyMaxMS, int32(latency))
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

// keysLocked returns the row's rollup keys: its route, by method, status
// and deployment, and each folded dimension, by value alone. The buffer
// keeps up to RequestAnalyticsRollupDimensionsBufferMax values of a kind per
// app minute, the rest go to __other__ at once, and the flush of the closed
// minute keeps the RequestAnalyticsRollupDimensionsMax busiest of them.
func (a *RequestAnalyticsRollups) keysLocked(row RequestAnalyticsRollupRow, minute int64) []rollupKey {
	base := rollupKey{app: row.AppID, minute: minute, status: int16(row.Status), deployment: row.DeploymentID}
	keys := make([]rollupKey, 0, 1+len(rollupFoldedKinds))
	route := base
	route.kind, route.dimension, route.method = RollupKindRoute, rollupDimension(row.Route, "__unknown__"), rollupText(row.Method, rollupMethodMaxBytes)
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
			if len(values) >= api.RequestAnalyticsRollupDimensionsBufferMax {
				value = rollupOtherDimension
			} else {
				values[value] = struct{}{}
			}
		}
		keys = append(keys, rollupKey{app: row.AppID, minute: minute, kind: kind, dimension: value, status: rollupFoldedStatus})
	}
	return keys
}

func rollupDimension(value, empty string) string {
	value = rollupText(value, rollupDimensionMaxBytes)
	if value == "" {
		return empty
	}
	return value
}

// rollupText makes value storable as Postgres text of at most maxBytes:
// valid UTF-8, no NUL, cut on a rune boundary. A cut through a multi-byte
// rune made Postgres refuse the whole flush (a client's 253-rune Referer
// host was enough).
func rollupText(value string, maxBytes int) string {
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "\uFFFD")
	}
	if strings.IndexByte(value, 0) >= 0 {
		value = strings.ReplaceAll(value, "\x00", "")
	}
	if len(value) <= maxBytes {
		return value
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
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
	ready := a.now().Add(-time.Minute - api.RequestAnalyticsRollupMinuteGrace).Unix()
	a.mu.Lock()
	pending := map[rollupKey]*state.RequestAnalyticsRollup{}
	for k, r := range a.pending {
		if all || k.minute <= ready {
			pending[k] = r
			delete(a.pending, k)
		}
	}
	for dk := range a.dims {
		if all || dk.minute <= ready {
			delete(a.dims, dk)
		}
	}
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
	if len(pending) == 0 && len(uncovered) == 0 {
		return a.flushGaps(ctx)
	}
	rows := foldRollupDimensions(pending)
	// A stable order keeps concurrent replicas' row locks in one order.
	sort.Slice(rows, func(i, j int) bool { return rollupLess(rows[i], rows[j]) })
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
	return a.flushGaps(ctx)
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

// foldRollupDimensions turns the buffer into rows, keeping for each app
// minute and folded kind the RequestAnalyticsRollupDimensionsMax values
// with the most requests and merging the rest into __other__.
func foldRollupDimensions(pending map[rollupKey]*state.RequestAnalyticsRollup) []state.RequestAnalyticsRollup {
	totals := map[rollupDimensionKey]map[string]int64{}
	for k, r := range pending {
		if k.kind == RollupKindRoute {
			continue
		}
		dk := rollupDimensionKey{app: k.app, minute: k.minute, kind: k.kind}
		if totals[dk] == nil {
			totals[dk] = map[string]int64{}
		}
		totals[dk][k.dimension] += r.Requests
	}
	kept := map[rollupDimensionKey]map[string]bool{}
	for dk, values := range totals {
		if len(values) <= api.RequestAnalyticsRollupDimensionsMax {
			continue
		}
		ranked := make([]string, 0, len(values))
		for v := range values {
			ranked = append(ranked, v)
		}
		sort.Slice(ranked, func(i, j int) bool {
			if values[ranked[i]] != values[ranked[j]] {
				return values[ranked[i]] > values[ranked[j]]
			}
			return ranked[i] < ranked[j]
		})
		keep := map[string]bool{}
		for _, v := range ranked[:api.RequestAnalyticsRollupDimensionsMax] {
			keep[v] = true
		}
		kept[dk] = keep
	}
	out := make(map[rollupKey]*state.RequestAnalyticsRollup, len(pending))
	for k, r := range pending {
		if keep, ok := kept[rollupDimensionKey{app: k.app, minute: k.minute, kind: k.kind}]; ok && !keep[k.dimension] {
			k.dimension = rollupOtherDimension
			r.Dimension = rollupOtherDimension
		}
		if cur := out[k]; cur != nil {
			mergeRollup(cur, r)
			continue
		}
		out[k] = r
	}
	rows := make([]state.RequestAnalyticsRollup, 0, len(out))
	for _, r := range out {
		rows = append(rows, *r)
	}
	return rows
}

func mergeRollup(cur, r *state.RequestAnalyticsRollup) {
	cur.Requests += r.Requests
	cur.ErrorRequests += r.ErrorRequests
	cur.ColdBoots += r.ColdBoots
	cur.RequestTimeMS += r.RequestTimeMS
	cur.LatencyMaxMS = max(cur.LatencyMaxMS, r.LatencyMaxMS)
	for i := range cur.LatencyHist {
		cur.LatencyHist[i] += r.LatencyHist[i]
	}
}

// restore merges unwritten rows back into the buffer. Rows past its bound
// are dropped and their minutes recorded as gaps.
func (a *RequestAnalyticsRollups) restore(rows []state.RequestAnalyticsRollup, coverage []state.RequestAnalyticsCoverage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range rows {
		r := &rows[i]
		k := rollupKey{app: r.AppID, minute: r.BucketStart.Unix(), kind: r.DimensionKind, dimension: r.Dimension,
			method: r.Method, status: r.Status, deployment: r.DeploymentID}
		if cur := a.pending[k]; cur != nil {
			mergeRollup(cur, r)
			continue
		}
		if len(a.pending) >= api.RequestAnalyticsRollupPendingRowsMax {
			if r.DimensionKind == RollupKindRoute {
				a.requests.WithLabelValues("dropped").Add(float64(r.Requests))
			}
			a.gapLocked(r.AppID, k.minute)
			continue
		}
		a.pending[k] = r
	}
	for _, c := range coverage {
		minute := c.CoveredSince.Unix()
		if first, ok := a.uncovered[c.AppID]; !ok || minute < first {
			a.uncovered[c.AppID] = minute
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
	r.rollups.Add(RequestAnalyticsRollupRow{
		EventID: eventUUID, AccountID: accountID, AppID: appID, DeploymentID: deploymentID,
		ReceivedAt: msToTime(req.GetReceivedAtUnixMs()), Route: req.GetRouteTemplate(), Method: method,
		Status: int(req.GetHttpStatus()), LatencyMS: int(req.GetLatencyMs()), Count: count,
		ColdBoot: req.GetColdBoot(), Country: req.GetCountry(), ReferrerHost: req.GetReferrerHost(),
		UAFamily: req.GetUaFamily(), ConsumerID: consumer,
	})
}
