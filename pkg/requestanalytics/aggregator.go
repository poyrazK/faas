// Package requestanalytics folds requests into per-minute analytics rollups
// (ADR-20261103113952). gatewayd-internal counts every request it serves
// (amendment 5) and apid counts the rows of gateways that do not; both use
// this Aggregator, so a rollup means the same whichever side folded it.
package requestanalytics

import (
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Rollup dimension kinds (request_analytics_minutes.dimension_kind).
const (
	KindRoute        = "route"
	KindCountry      = "country"
	KindReferrerHost = "referrer_host"
	KindUAFamily     = "ua_family"
	KindConsumerID   = "consumer_id"

	// OtherDimension is the value that holds a folded kind's values past
	// RequestAnalyticsRollupDimensionsMax.
	OtherDimension = "__other__"
	// AnonymousConsumer is the consumer_id value of an anonymous request.
	AnonymousConsumer = "__anonymous__"
	// FoldedStatus is the status of every non-route row, whose deployment is
	// the nil UUID: no reader splits those kinds by status or deployment
	// (amendment 3).
	FoldedStatus = 0

	dimensionMaxBytes = api.RequestAnalyticsDimensionMaxBytes // request_analytics_minutes.dimension CHECK
)

// FoldedKinds are the non-route kinds, folded to the busiest values.
var FoldedKinds = [...]string{KindCountry, KindReferrerHost, KindUAFamily, KindConsumerID}

// Row is one request, or Count requests that share every field, to count.
type Row struct {
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

// Outcome is what Add did with a row.
type Outcome int

const (
	// Added counts the row.
	Added Outcome = iota
	// Invalid rows (no count, a status outside 100–599) are not counted.
	Invalid
	// Dropped rows did not fit the buffer bound; their minute lost them.
	Dropped
)

type key struct {
	app        uuid.UUID
	minute     int64
	kind       string
	dimension  string
	method     string
	status     int16
	deployment uuid.UUID
}

type dimensionKey struct {
	app    uuid.UUID
	minute int64
	kind   string
}

// Aggregator buffers per-minute rollups. It is safe for concurrent use.
type Aggregator struct {
	maxRows int

	mu      sync.Mutex
	pending map[key]*state.RequestAnalyticsRollup
	dims    map[dimensionKey]map[string]struct{}
}

// NewAggregator returns an aggregator holding at most maxRows rollup rows;
// maxRows <= 0 means RequestAnalyticsRollupPendingRowsMax.
func NewAggregator(maxRows int) *Aggregator {
	if maxRows <= 0 {
		maxRows = api.RequestAnalyticsRollupPendingRowsMax
	}
	return &Aggregator{
		maxRows: maxRows,
		pending: map[key]*state.RequestAnalyticsRollup{},
		dims:    map[dimensionKey]map[string]struct{}{},
	}
}

// Minute is the UTC minute (Unix seconds) a row counts in.
func Minute(t time.Time) int64 { return t.UTC().Truncate(time.Minute).Unix() }

// Add counts row into its minute: its route row, by method, status and
// deployment, and one row per folded kind, by value alone. The buffer keeps
// up to RequestAnalyticsRollupDimensionsBufferMax values of a kind per app
// minute and folds later ones into __other__ at once; Take keeps the
// RequestAnalyticsRollupDimensionsMax busiest. A row that does not fit the
// bound is Dropped whole.
func (a *Aggregator) Add(row Row) Outcome {
	if row.Count < 1 || row.Status < 100 || row.Status > 599 {
		return Invalid
	}
	minute := Minute(row.ReceivedAt)
	a.mu.Lock()
	defer a.mu.Unlock()
	keys := a.keysLocked(row, minute)
	for _, k := range keys {
		if _, ok := a.pending[k]; !ok && len(a.pending) >= a.maxRows {
			return Dropped
		}
	}
	bucket := LatencyBucket(row.LatencyMS)
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
	return Added
}

func (a *Aggregator) keysLocked(row Row, minute int64) []key {
	keys := make([]key, 0, 1+len(FoldedKinds))
	keys = append(keys, key{
		app: row.AppID, minute: minute, kind: KindRoute,
		dimension: Dimension(row.Route, "__unknown__"), method: api.NormalizeRequestAnalyticsMethod(row.Method),
		status: int16(row.Status), deployment: row.DeploymentID,
	})
	for _, kind := range FoldedKinds {
		var value string
		switch kind {
		case KindCountry:
			value = Dimension(row.Country, "__unknown__")
		case KindReferrerHost:
			value = Dimension(row.ReferrerHost, "__none__")
		case KindUAFamily:
			value = Dimension(row.UAFamily, "__unknown__")
		case KindConsumerID:
			value = Dimension(row.ConsumerID, AnonymousConsumer)
		}
		dk := dimensionKey{app: row.AppID, minute: minute, kind: kind}
		values := a.dims[dk]
		if values == nil {
			values = map[string]struct{}{}
			a.dims[dk] = values
		}
		if _, ok := values[value]; !ok {
			if len(values) >= api.RequestAnalyticsRollupDimensionsBufferMax {
				value = OtherDimension
			} else {
				values[value] = struct{}{}
			}
		}
		keys = append(keys, key{app: row.AppID, minute: minute, kind: kind, dimension: value, status: FoldedStatus})
	}
	return keys
}

// Take removes the minutes at or before ready (Unix seconds), or every
// minute when all is set, and returns them folded: for each app minute and
// folded kind the RequestAnalyticsRollupDimensionsMax busiest values, the
// rest merged into __other__.
func (a *Aggregator) Take(ready int64, all bool) []state.RequestAnalyticsRollup {
	a.mu.Lock()
	taken := map[key]*state.RequestAnalyticsRollup{}
	for k, r := range a.pending {
		if all || k.minute <= ready {
			taken[k] = r
			delete(a.pending, k)
		}
	}
	for dk := range a.dims {
		if all || dk.minute <= ready {
			delete(a.dims, dk)
		}
	}
	a.mu.Unlock()
	return fold(taken)
}

// Len is the number of buffered rollup rows.
func (a *Aggregator) Len() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.pending)
}

// Restore merges rows a write did not take back into the buffer. Rows past
// the bound are returned as lost.
func (a *Aggregator) Restore(rows []state.RequestAnalyticsRollup) (lost []state.RequestAnalyticsRollup) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range rows {
		r := &rows[i]
		k := key{app: r.AppID, minute: r.BucketStart.Unix(), kind: r.DimensionKind, dimension: r.Dimension,
			method: r.Method, status: r.Status, deployment: r.DeploymentID}
		if cur := a.pending[k]; cur != nil {
			Merge(cur, r)
			continue
		}
		if len(a.pending) >= a.maxRows {
			lost = append(lost, *r)
			continue
		}
		a.pending[k] = r
	}
	return lost
}

func fold(pending map[key]*state.RequestAnalyticsRollup) []state.RequestAnalyticsRollup {
	totals := map[dimensionKey]map[string]int64{}
	for k, r := range pending {
		if k.kind == KindRoute {
			continue
		}
		dk := dimensionKey{app: k.app, minute: k.minute, kind: k.kind}
		if totals[dk] == nil {
			totals[dk] = map[string]int64{}
		}
		totals[dk][k.dimension] += r.Requests
	}
	kept := map[dimensionKey]map[string]bool{}
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
	out := make(map[key]*state.RequestAnalyticsRollup, len(pending))
	for k, r := range pending {
		if keep, ok := kept[dimensionKey{app: k.app, minute: k.minute, kind: k.kind}]; ok && !keep[k.dimension] {
			k.dimension = OtherDimension
			r.Dimension = OtherDimension
		}
		if cur := out[k]; cur != nil {
			Merge(cur, r)
			continue
		}
		out[k] = r
	}
	rows := make([]state.RequestAnalyticsRollup, 0, len(out))
	for _, r := range out {
		rows = append(rows, *r)
	}
	// A stable order keeps concurrent writers' row locks in one order.
	sort.Slice(rows, func(i, j int) bool { return Less(rows[i], rows[j]) })
	return rows
}

// Merge adds r into cur.
func Merge(cur, r *state.RequestAnalyticsRollup) {
	cur.Requests += r.Requests
	cur.ErrorRequests += r.ErrorRequests
	cur.ColdBoots += r.ColdBoots
	cur.RequestTimeMS += r.RequestTimeMS
	cur.LatencyMaxMS = max(cur.LatencyMaxMS, r.LatencyMaxMS)
	for i := range cur.LatencyHist {
		cur.LatencyHist[i] += r.LatencyHist[i]
	}
}

// Less orders rows by their key.
func Less(x, y state.RequestAnalyticsRollup) bool {
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

// LatencyBucket is the latency_hist index for latencyMS: the first bound at
// or above it, or the overflow bucket.
func LatencyBucket(latencyMS int) int {
	return sort.SearchInts(api.RequestAnalyticsLatencyBoundsMS[:], latencyMS)
}

// Dimension is value made storable, or empty when it has none.
func Dimension(value, empty string) string {
	value = Text(value, dimensionMaxBytes)
	if value == "" {
		return empty
	}
	return value
}

// Text makes value storable as Postgres text of at most maxBytes: valid
// UTF-8, no NUL, cut on a rune boundary. A cut through a multi-byte rune
// made Postgres refuse a whole flush (a client's 253-rune Referer host was
// enough).
func Text(value string, maxBytes int) string {
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "�")
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

// PendingRequests sums, per app, the requests buffered in minutes at or
// before through (Unix seconds): the requests not yet written.
func (a *Aggregator) PendingRequests(through int64) map[uuid.UUID]int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := map[uuid.UUID]int64{}
	for k, r := range a.pending {
		if k.kind == KindRoute && k.minute <= through {
			out[k.app] += r.Requests
		}
	}
	return out
}
