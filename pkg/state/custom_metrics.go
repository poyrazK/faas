package state

import (
	"context"
	"errors"
	"fmt"
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
}

var (
	_ CustomMetricKindStore = (*PgStore)(nil)
	_ CustomMetricKindStore = (*MemStore)(nil)
)

// PutCustomMetricOfKind is PutCustomMetric with an explicit kind; the cap and
// upsert semantics are identical.
func (s *PgStore) PutCustomMetricOfKind(ctx context.Context, appID, name, kind string, value float64, observedAt time.Time, distinctLimit int) error {
	if distinctLimit <= 0 {
		return fmt.Errorf("state: put custom metric %q: distinct limit must be > 0", name)
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO app_custom_metrics (app_id, name, value, observed_at, kind)
		SELECT $1::uuid, $2::text, $3::double precision, $4::timestamptz, $6::text
		WHERE EXISTS (
			-- Always allow a push to a name the app already holds: it is
			-- an upsert and cannot grow the row count.
			SELECT 1 FROM app_custom_metrics
			 WHERE app_id = $1::uuid AND name = $2::text
		) OR (
			SELECT count(*) FROM app_custom_metrics WHERE app_id = $1::uuid
		) < $5::int
		ON CONFLICT (app_id, name) DO UPDATE
		   SET value = EXCLUDED.value, observed_at = EXCLUDED.observed_at, kind = EXCLUDED.kind
	`, appID, name, value, observedAt, distinctLimit, kind)
	if err != nil {
		return fmt.Errorf("state: put custom metric %q for app %s: %w", name, appID, err)
	}
	// Zero rows means the WHERE admitted nothing: the name is new and the
	// app is at its cap. The statement is otherwise unconditional.
	if tag.RowsAffected() == 0 {
		return ErrCustomMetricLimit
	}
	return nil
}

// ListCustomMetrics returns every stored gauge for an app, name-ordered so
// callers and tests see a stable sequence.
//
// Freshness is deliberately NOT applied here. The caller owns the clock —
// the scaling trigger passes its tick time, and a test passes a fixed one —
// so filtering by wall-clock age in the store would make a back-dated
// evaluation silently wrong.
func (s *PgStore) ListCustomMetrics(ctx context.Context, appID string) ([]CustomMetric, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT name, value, observed_at, kind
		  FROM app_custom_metrics
		 WHERE app_id = $1::uuid
		 ORDER BY name
	`, appID)
	if err != nil {
		return nil, fmt.Errorf("state: list custom metrics for app %s: %w", appID, err)
	}
	defer rows.Close()
	out := make([]CustomMetric, 0, 4)
	for rows.Next() {
		var m CustomMetric
		if err := rows.Scan(&m.Name, &m.Value, &m.ObservedAt, &m.Kind); err != nil {
			return nil, fmt.Errorf("state: scan custom metric for app %s: %w", appID, err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list custom metrics for app %s: %w", appID, err)
	}
	return out, nil
}

// DeleteCustomMetric removes one gauge by name. Deleting a name that does
// not exist is not an error: the caller's intent is "this metric is gone",
// and that is already true.
func (s *PgStore) DeleteCustomMetric(ctx context.Context, appID, name string) error {
	if _, err := s.pool.Exec(ctx, `
		DELETE FROM app_custom_metrics WHERE app_id = $1::uuid AND name = $2::text
	`, appID, name); err != nil {
		return fmt.Errorf("state: delete custom metric %q for app %s: %w", name, appID, err)
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

// ListFreshCustomMetrics returns at most limit fresh gauges across all apps,
// ordered by app and name so a capped scrape is deterministic.
func (s *PgStore) ListFreshCustomMetrics(ctx context.Context, since time.Time, limit int) ([]CustomMetricSample, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT app_id::text, name, value
		  FROM app_custom_metrics
		 WHERE observed_at >= $1::timestamptz
		 ORDER BY app_id, name
		 LIMIT $2::int
	`, since, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list fresh custom metrics: %w", err)
	}
	defer rows.Close()
	var out []CustomMetricSample
	for rows.Next() {
		var sample CustomMetricSample
		if err := rows.Scan(&sample.AppID, &sample.Name, &sample.Value); err != nil {
			return nil, fmt.Errorf("state: scan fresh custom metric: %w", err)
		}
		out = append(out, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list fresh custom metrics: %w", err)
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
			if !metric.ObservedAt.Before(since) {
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
	if _, exists := byName[name]; !exists && len(byName) >= distinctLimit {
		return ErrCustomMetricLimit
	}
	byName[name] = CustomMetric{Name: name, Value: value, ObservedAt: observedAt, Kind: kind}
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
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// DeleteCustomMetric removes one gauge; a missing name is not an error.
func (m *MemStore) DeleteCustomMetric(_ context.Context, appID, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if byName := m.customMetrics[appID]; byName != nil {
		delete(byName, name)
	}
	return nil
}
