package state

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"sort"
	"time"
)

// ErrCustomMetricLimit is returned when a push would exceed an app's
// distinct-name cap (ADR-202). Callers map it to a 422 naming the limit.
var ErrCustomMetricLimit = errors.New("state: app custom metric limit reached")

// PutCustomMetric upserts one pushed gauge.
//
// The distinct-name cap is enforced INSIDE the insert rather than by a
// preceding count. Two concurrent pushes of two different new names would
// both pass a check-then-insert against a limit of one, and the row count
// would settle at two. The `WHERE` on the SELECT source makes the admission
// decision and the write a single atomic statement instead.
//
// An existing name is always allowed through regardless of the cap: it is an
// upsert, it cannot grow the row count, and rejecting it would break an app
// that is merely at its limit and pushing a fresh value for a metric it
// already owns.
func (s *PgStore) PutCustomMetric(ctx context.Context, appID, name string, value float64, observedAt time.Time, distinctLimit int) error {
	return s.PutCustomMetricOfKind(ctx, appID, name, CustomMetricKindGauge, value, observedAt, distinctLimit)
}

// CustomMetricKindStore upserts a metric with an explicit kind (ADR-745).
// The latest write decides the kind, so re-instrumenting a gauge as an OTLP
// counter (or back) takes effect on the next push.
type CustomMetricKindStore interface {
	PutCustomMetricOfKind(ctx context.Context, appID, name, kind string, value float64, observedAt time.Time, distinctLimit int) error
	// AddCustomMetricCounter adds an OTLP delta to a counter's total, so
	// deltas from every instance of an app sum into one monotonic series
	// (ADR-20261031144636). A name that held a gauge restarts from the delta.
	AddCustomMetricCounter(ctx context.Context, appID, name string, delta float64, observedAt time.Time, distinctLimit int) error
}

var (
	_ CustomMetricKindStore = (*PgStore)(nil)
	_ CustomMetricKindStore = (*MemStore)(nil)
)

// PutCustomMetricOfKind is PutCustomMetric with an explicit kind; the cap and
// upsert semantics are identical. A value observed before the stored one is
// dropped, so a delayed or retried export cannot move a metric backwards
// (ADR-20261031144636).
func (s *PgStore) PutCustomMetricOfKind(ctx context.Context, appID, name, kind string, value float64, observedAt time.Time, distinctLimit int) error {
	if distinctLimit <= 0 {
		return fmt.Errorf("state: put custom metric %q: distinct limit must be > 0", name)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin custom metric admission: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	if err = q.LockCustomMetricAdmission(ctx, tx, appID); err != nil {
		return fmt.Errorf("lock custom metric admission: %w", err)
	}
	admitted, err := q.PutCustomMetricOfKind(ctx, tx, sqlc.PutCustomMetricOfKindParams{
		AppID: mustPgUUID(appID), Name: name, Value: value, ObservedAt: pgtype.Timestamptz{Time: observedAt, Valid: true}, Kind: kind, DistinctLimit: int32(distinctLimit), ClockSkewSeconds: int32(api.CustomMetricClockSkew / time.Second),
	})
	if err != nil {
		return fmt.Errorf("put custom metric %q for app %s: %w", name, appID, err)
	}
	if !admitted {
		return ErrCustomMetricLimit
	}
	return tx.Commit(ctx)
}

// AddCustomMetricCounter adds delta to the counter's total under the same
// cap as PutCustomMetricOfKind (AddCustomMetricOfKind for a counter).
func (s *PgStore) AddCustomMetricCounter(ctx context.Context, appID, name string, delta float64, observedAt time.Time, distinctLimit int) error {
	return s.AddCustomMetricOfKind(ctx, appID, name, CustomMetricKindCounter, delta, observedAt, distinctLimit)
}

// CustomMetricDeltaStore adds an OTLP delta to a stored metric
// (ADR-20261108160000): delta counters and histogram totals accumulate into
// a running total, and delta up-down counters move a gauge.
type CustomMetricDeltaStore interface {
	AddCustomMetricOfKind(ctx context.Context, appID, name, kind string, delta float64, observedAt time.Time, distinctLimit int) error
}

var (
	_ CustomMetricDeltaStore = (*PgStore)(nil)
	_ CustomMetricDeltaStore = (*MemStore)(nil)
)

// customMetricMaxTotal bounds an accumulated total below the column's
// finite CHECK; a total that large is a broken producer, not a count.
const customMetricMaxTotal = 1e15

// clampCustomMetricTotal keeps an accumulated value inside the stored
// range: never negative (app_custom_metrics_value_finite) and finite.
func clampCustomMetricTotal(v float64) float64 {
	switch {
	case v < 0 || v != v:
		return 0
	case v > customMetricMaxTotal:
		return customMetricMaxTotal
	}
	return v
}

// AddCustomMetricOfKind adds delta to the stored value of name, inserting it
// as delta when the name is new or was stored with another kind (a kind
// change restarts the total). The result is clamped to [0, 1e15]. The
// distinct-name cap is enforced in the statement as in PutCustomMetricOfKind.
func (s *PgStore) AddCustomMetricOfKind(ctx context.Context, appID, name, kind string, delta float64, observedAt time.Time, distinctLimit int) error {
	if distinctLimit <= 0 {
		return fmt.Errorf("state: add custom metric %q: distinct limit must be > 0", name)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin custom metric admission: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	if err = q.LockCustomMetricAdmission(ctx, tx, appID); err != nil {
		return fmt.Errorf("lock custom metric admission: %w", err)
	}
	admitted, err := q.AddCustomMetricOfKind(ctx, tx, sqlc.AddCustomMetricOfKindParams{AppID: mustPgUUID(appID), Name: name, Kind: kind, Delta: delta, ObservedAt: pgtype.Timestamptz{Time: observedAt, Valid: true}, DistinctLimit: int32(distinctLimit), MaxTotal: customMetricMaxTotal, ClockSkewSeconds: int32(api.CustomMetricClockSkew / time.Second)})
	if err != nil {
		return fmt.Errorf("add custom metric %q for app %s: %w", name, appID, err)
	}
	if !admitted {
		return ErrCustomMetricLimit
	}
	return tx.Commit(ctx)
}

// ListCustomMetrics returns every stored gauge for an app, name-ordered so
// callers and tests see a stable sequence.
//
// Freshness is deliberately NOT applied here. The caller owns the clock —
// the scaling trigger passes its tick time, and a test passes a fixed one —
// so filtering by wall-clock age in the store would make a back-dated
// evaluation silently wrong.
func (s *PgStore) ListCustomMetrics(ctx context.Context, appID string) ([]CustomMetric, error) {
	rows, err := sqlc.New().ListCustomMetrics(ctx, s.pool, mustPgUUID(appID))
	if err != nil {
		return nil, fmt.Errorf("list custom metrics for app %s: %w", appID, err)
	}
	out := make([]CustomMetric, 0, len(rows))
	for _, row := range rows {
		out = append(out, CustomMetric{Name: row.Name, Value: row.Value, ObservedAt: row.ObservedAt.Time, Kind: row.Kind, Labelled: row.Labelled})
	}
	return out, nil
}

// DeleteCustomMetric removes one gauge by name. Deleting a name that does
// not exist is not an error: the caller's intent is "this metric is gone",
// and that is already true.
func (s *PgStore) DeleteCustomMetric(ctx context.Context, appID, name string) error {
	if err := sqlc.New().DeleteCustomMetric(ctx, s.pool, sqlc.DeleteCustomMetricParams{AppID: mustPgUUID(appID), Name: name}); err != nil {
		return fmt.Errorf("delete custom metric %q for app %s: %w", name, appID, err)
	}
	return nil
}

// CustomMetricSample is one app's pushed gauge for the ADR-745 exporter.
type CustomMetricSample struct {
	AppID string
	Name  string
	Value float64
}

// CustomMetricExportStore is the bounded, cross-app read the custom-metric
// exporter scrapes. Only values observed at or after since are returned —
// a stopped pusher must show as a gap, not a flat line (ADR-202 freshness).
type CustomMetricExportStore interface {
	ListFreshCustomMetrics(ctx context.Context, since time.Time, limit int) ([]CustomMetricSample, error)
}

var (
	_ CustomMetricExportStore = (*PgStore)(nil)
	_ CustomMetricExportStore = (*MemStore)(nil)
)

// ListFreshCustomMetrics returns at most limit fresh gauges, and every counter, across all apps,
// ordered by app and name so a capped scrape is deterministic.
func (s *PgStore) ListFreshCustomMetrics(ctx context.Context, since time.Time, limit int) ([]CustomMetricSample, error) {
	rows, err := sqlc.New().ListFreshCustomMetrics(ctx, s.pool, sqlc.ListFreshCustomMetricsParams{
		Since: pgtype.Timestamptz{Time: since, Valid: true}, Until: pgtype.Timestamptz{Time: since.Add(time.Duration(api.CustomMetricFreshnessSeconds) * time.Second), Valid: true}, RowLimit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list fresh custom metrics: %w", err)
	}
	out := make([]CustomMetricSample, 0, len(rows))
	for _, row := range rows {
		out = append(out, CustomMetricSample{AppID: row.AppID, Name: row.Name, Value: row.Value})
	}
	return out, nil
}

// --- MemStore ------------------------------------------------------------

// ListFreshCustomMetrics mirrors the PgStore ordering and cap.
func (m *MemStore) ListFreshCustomMetrics(_ context.Context, since time.Time, limit int) ([]CustomMetricSample, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []CustomMetricSample
	for appID, byName := range m.customMetrics {
		for _, metric := range byName {
			if !metric.ObservedAt.Before(since) || metric.Kind == CustomMetricKindCounter {
				out = append(out, CustomMetricSample{AppID: appID, Name: metric.Name, Value: metric.Value})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AppID != out[j].AppID {
			return out[i].AppID < out[j].AppID
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// PutCustomMetric mirrors the PgStore semantics, including the cap applying
// only to NEW names. The conformance suite runs both implementations against
// the same cases; a MemStore that were merely permissive here would let a
// handler test pass while the production path rejected the same push.
func (m *MemStore) PutCustomMetric(ctx context.Context, appID, name string, value float64, observedAt time.Time, distinctLimit int) error {
	return m.PutCustomMetricOfKind(ctx, appID, name, CustomMetricKindGauge, value, observedAt, distinctLimit)
}

// PutCustomMetricOfKind mirrors the PgStore upsert, including the latest
// write deciding the kind.
func (m *MemStore) PutCustomMetricOfKind(_ context.Context, appID, name, kind string, value float64, observedAt time.Time, distinctLimit int) error {
	if distinctLimit <= 0 {
		return fmt.Errorf("state: put custom metric %q: distinct limit must be > 0", name)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.customMetrics == nil {
		m.customMetrics = map[string]map[string]CustomMetric{}
	}
	byName, ok := m.customMetrics[appID]
	if !ok {
		byName = map[string]CustomMetric{}
		m.customMetrics[appID] = byName
	}
	prev, exists := byName[name]
	if !exists && len(byName) >= distinctLimit {
		return ErrCustomMetricLimit
	}
	if exists && observedAt.Before(prev.ObservedAt) {
		now := time.Now()
		legacyFuture := prev.ObservedAt.After(now.Add(api.CustomMetricClockSkew))
		if !legacyFuture || observedAt.After(now.Add(api.CustomMetricClockSkew)) {
			return nil
		}
	}
	byName[name] = CustomMetric{Name: name, Value: value, ObservedAt: observedAt, Kind: kind}
	return nil
}

// AddCustomMetricCounter mirrors PgStore.
func (m *MemStore) AddCustomMetricCounter(ctx context.Context, appID, name string, delta float64, observedAt time.Time, distinctLimit int) error {
	return m.AddCustomMetricOfKind(ctx, appID, name, CustomMetricKindCounter, delta, observedAt, distinctLimit)
}

// AddCustomMetricOfKind mirrors the PgStore delta upsert.
func (m *MemStore) AddCustomMetricOfKind(_ context.Context, appID, name, kind string, delta float64, observedAt time.Time, distinctLimit int) error {
	if distinctLimit <= 0 {
		return fmt.Errorf("state: add custom metric %q: distinct limit must be > 0", name)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.customMetrics == nil {
		m.customMetrics = map[string]map[string]CustomMetric{}
	}
	byName, ok := m.customMetrics[appID]
	if !ok {
		byName = map[string]CustomMetric{}
		m.customMetrics[appID] = byName
	}
	prev, exists := byName[name]
	if !exists && len(byName) >= distinctLimit {
		return ErrCustomMetricLimit
	}
	value := delta
	if exists && prev.Kind == kind {
		value = prev.Value + delta
	}
	if exists && prev.ObservedAt.After(observedAt) {
		observedAt = prev.ObservedAt
	}
	byName[name] = CustomMetric{Name: name, Value: clampCustomMetricTotal(value), ObservedAt: observedAt, Kind: kind}
	return nil
}

// ListCustomMetrics returns the app's gauges name-ordered, matching PgStore's
// ORDER BY. Map iteration order would otherwise make the two stores
// observably different for the same data.
func (m *MemStore) ListCustomMetrics(_ context.Context, appID string) ([]CustomMetric, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	byName := m.customMetrics[appID]
	out := make([]CustomMetric, 0, len(byName))
	for _, v := range byName {
		v.Labelled = len(m.customMetricSeries[appID][v.Name]) > 0
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// DeleteCustomMetric removes one gauge and, as the PgStore foreign key
// cascades, its series; a missing name is not an error.
func (m *MemStore) DeleteCustomMetric(_ context.Context, appID, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if byName := m.customMetrics[appID]; byName != nil {
		delete(byName, name)
	}
	if bySeries := m.customMetricSeries[appID]; bySeries != nil {
		delete(bySeries, name)
	}
	return nil
}
