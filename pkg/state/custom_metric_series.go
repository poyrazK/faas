package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// Labelled custom metric series (ADR-20261108160000 slice C). A series is one
// label set of a metric name; the metric's app_custom_metrics row holds the
// aggregate every name-only reader uses: the sum of its series for counters,
// the maximum of its fresh series for gauges.

// ErrCustomMetricSeriesLimit is returned when a new series would exceed the
// per-metric or per-app series cap.
var ErrCustomMetricSeriesLimit = errors.New("state: custom metric series limit reached")

// CustomMetricSeries is one stored series.
type CustomMetricSeries struct {
	Name       string
	SeriesKey  string
	Labels     map[string]string
	Kind       string
	Value      float64
	ObservedAt time.Time
}

// CustomMetricSeriesWrite is one data point for a series. Delta adds Value to
// the series' stored value (clamped to [0, 1e15]); otherwise Value replaces
// it. SeriesKey "" with no Labels is the unlabelled series of a labelled
// name.
type CustomMetricSeriesWrite struct {
	Name       string
	SeriesKey  string
	Labels     map[string]string
	Kind       string
	Value      float64
	Delta      bool
	ObservedAt time.Time
}

// CustomMetricSeriesLimits bounds a series write: distinct names per app
// (as PutCustomMetricOfKind), series per metric and per app, and the window
// within which a gauge series counts toward the gauge aggregate.
type CustomMetricSeriesLimits struct {
	Names           int
	SeriesPerMetric int
	SeriesPerApp    int
	GaugeFreshness  time.Duration
}

// CustomMetricSeriesSample is one fresh series for the exporter.
type CustomMetricSeriesSample struct {
	AppID     string
	Name      string
	SeriesKey string
	Value     float64
}

// CustomMetricSeriesStore is implemented by both stores.
type CustomMetricSeriesStore interface {
	PutCustomMetricSeries(ctx context.Context, appID string, w CustomMetricSeriesWrite, lim CustomMetricSeriesLimits) error
	ListCustomMetricSeries(ctx context.Context, appID string) ([]CustomMetricSeries, error)
	ListFreshCustomMetricSeries(ctx context.Context, since time.Time, limit int) ([]CustomMetricSeriesSample, error)
}

var (
	_ CustomMetricSeriesStore = (*PgStore)(nil)
	_ CustomMetricSeriesStore = (*MemStore)(nil)
)

func (lim CustomMetricSeriesLimits) validate() error {
	if lim.Names <= 0 || lim.SeriesPerMetric <= 0 || lim.SeriesPerApp <= 0 || lim.GaugeFreshness <= 0 {
		return fmt.Errorf("state: custom metric series limits must be > 0: %+v", lim)
	}
	return nil
}

// PutCustomMetricSeries writes one series point and recomputes the metric's
// aggregate in one transaction. The metric row is admitted under the name
// cap and locked, so series admission for one name is serialized; the
// per-app series count is checked in the same statement (it can overshoot
// by concurrent writes to different names, which the per-metric cap keeps
// small). A write of another kind than the metric's series drops them: a
// kind change restarts the metric.
func (s *PgStore) PutCustomMetricSeries(ctx context.Context, appID string, w CustomMetricSeriesWrite, lim CustomMetricSeriesLimits) error {
	if err := lim.validate(); err != nil {
		return err
	}
	labels := w.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	labelsJSON, err := json.Marshal(labels)
	if err != nil {
		return fmt.Errorf("state: encode custom metric labels: %w", err)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("state: begin custom metric series write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO app_custom_metrics (app_id, name, value, observed_at, kind)
		SELECT $1::uuid, $2::text, 0, $3::timestamptz, $4::text
		WHERE (SELECT count(*) FROM app_custom_metrics WHERE app_id = $1::uuid) < $5::int
		ON CONFLICT (app_id, name) DO NOTHING
	`, appID, w.Name, w.ObservedAt, w.Kind, lim.Names); err != nil {
		return fmt.Errorf("state: admit custom metric %q: %w", w.Name, err)
	}
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT true FROM app_custom_metrics WHERE app_id = $1::uuid AND name = $2::text FOR UPDATE
	`, appID, w.Name).Scan(&exists); errors.Is(err, pgx.ErrNoRows) {
		return ErrCustomMetricLimit
	} else if err != nil {
		return fmt.Errorf("state: lock custom metric %q: %w", w.Name, err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM app_custom_metric_series
		 WHERE app_id = $1::uuid AND name = $2::text AND kind <> $3::text
	`, appID, w.Name, w.Kind); err != nil {
		return fmt.Errorf("state: reset custom metric %q series: %w", w.Name, err)
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO app_custom_metric_series (app_id, name, series_key, labels, kind, value, observed_at)
		SELECT $1::uuid, $2::text, $3::text, $4::jsonb, $5::text,
		       LEAST(GREATEST($6::double precision, 0), $10::double precision), $7::timestamptz
		WHERE EXISTS (
			SELECT 1 FROM app_custom_metric_series
			 WHERE app_id = $1::uuid AND name = $2::text AND series_key = $3::text
		) OR (
			(SELECT count(*) FROM app_custom_metric_series WHERE app_id = $1::uuid AND name = $2::text) < $8::int
			AND (SELECT count(*) FROM app_custom_metric_series WHERE app_id = $1::uuid) < $9::int
		)
		ON CONFLICT (app_id, name, series_key) DO UPDATE
		   SET value = LEAST(GREATEST(
		           CASE WHEN $11::boolean THEN app_custom_metric_series.value + $6::double precision
		                ELSE $6::double precision END, 0), $10::double precision),
		       labels = EXCLUDED.labels,
		       observed_at = GREATEST(app_custom_metric_series.observed_at, EXCLUDED.observed_at)
	`, appID, w.Name, w.SeriesKey, labelsJSON, w.Kind, w.Value, w.ObservedAt,
		lim.SeriesPerMetric, lim.SeriesPerApp, customMetricMaxTotal, w.Delta)
	if err != nil {
		return fmt.Errorf("state: put custom metric %q series: %w", w.Name, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrCustomMetricSeriesLimit
	}
	if _, err := tx.Exec(ctx, `
		UPDATE app_custom_metrics m
		   SET kind = $3::text,
		       value = LEAST(agg.v, $5::double precision),
		       observed_at = agg.newest
		  FROM (
			SELECT CASE WHEN $3::text = 'counter' THEN coalesce(sum(value), 0)
			            ELSE coalesce(max(value) FILTER (WHERE observed_at >= newest - make_interval(secs => $4::double precision)), 0)
			       END AS v,
			       newest
			  FROM (SELECT value, observed_at, max(observed_at) OVER () AS newest
			          FROM app_custom_metric_series
			         WHERE app_id = $1::uuid AND name = $2::text) s
			 GROUP BY newest
		  ) agg
		 WHERE m.app_id = $1::uuid AND m.name = $2::text
	`, appID, w.Name, w.Kind, lim.GaugeFreshness.Seconds(), customMetricMaxTotal); err != nil {
		return fmt.Errorf("state: aggregate custom metric %q: %w", w.Name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: commit custom metric %q series: %w", w.Name, err)
	}
	return nil
}

// ListCustomMetricSeries returns an app's series ordered by name and key.
func (s *PgStore) ListCustomMetricSeries(ctx context.Context, appID string) ([]CustomMetricSeries, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT name, series_key, labels, kind, value, observed_at
		  FROM app_custom_metric_series
		 WHERE app_id = $1::uuid
		 ORDER BY name, series_key
	`, appID)
	if err != nil {
		return nil, fmt.Errorf("state: list custom metric series for app %s: %w", appID, err)
	}
	defer rows.Close()
	var out []CustomMetricSeries
	for rows.Next() {
		var row CustomMetricSeries
		var labels []byte
		if err := rows.Scan(&row.Name, &row.SeriesKey, &labels, &row.Kind, &row.Value, &row.ObservedAt); err != nil {
			return nil, fmt.Errorf("state: scan custom metric series for app %s: %w", appID, err)
		}
		if err := json.Unmarshal(labels, &row.Labels); err != nil {
			return nil, fmt.Errorf("state: decode custom metric labels for app %s: %w", appID, err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list custom metric series for app %s: %w", appID, err)
	}
	return out, nil
}

// ListFreshCustomMetricSeries returns at most limit series across all apps,
// ordered by app, name and key: gauge series observed at or after since, and
// every counter series, whose total stays true while idle (as
// ListFreshCustomMetrics, ADR-20261104142830).
func (s *PgStore) ListFreshCustomMetricSeries(ctx context.Context, since time.Time, limit int) ([]CustomMetricSeriesSample, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT app_id::text, name, series_key, value
		  FROM app_custom_metric_series
		 WHERE observed_at >= $1::timestamptz OR kind = 'counter'
		 ORDER BY app_id, name, series_key
		 LIMIT $2::int
	`, since, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list fresh custom metric series: %w", err)
	}
	defer rows.Close()
	var out []CustomMetricSeriesSample
	for rows.Next() {
		var row CustomMetricSeriesSample
		if err := rows.Scan(&row.AppID, &row.Name, &row.SeriesKey, &row.Value); err != nil {
			return nil, fmt.Errorf("state: scan fresh custom metric series: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list fresh custom metric series: %w", err)
	}
	return out, nil
}

// PutCustomMetricSeries mirrors the PgStore transaction.
func (m *MemStore) PutCustomMetricSeries(_ context.Context, appID string, w CustomMetricSeriesWrite, lim CustomMetricSeriesLimits) error {
	if err := lim.validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.customMetrics == nil {
		m.customMetrics = map[string]map[string]CustomMetric{}
	}
	if m.customMetricSeries == nil {
		m.customMetricSeries = map[string]map[string]map[string]CustomMetricSeries{}
	}
	byName := m.customMetrics[appID]
	if byName == nil {
		byName = map[string]CustomMetric{}
		m.customMetrics[appID] = byName
	}
	metric, exists := byName[w.Name]
	if !exists {
		if len(byName) >= lim.Names {
			return ErrCustomMetricLimit
		}
		metric = CustomMetric{Name: w.Name, ObservedAt: w.ObservedAt, Kind: w.Kind}
	}
	bySeries := m.customMetricSeries[appID]
	if bySeries == nil {
		bySeries = map[string]map[string]CustomMetricSeries{}
		m.customMetricSeries[appID] = bySeries
	}
	// Work on a copy, so a refused write leaves nothing behind (PgStore
	// rolls its transaction back).
	series := map[string]CustomMetricSeries{}
	for key, row := range bySeries[w.Name] {
		if row.Kind == w.Kind {
			series[key] = row
		}
	}
	prev, seriesExists := series[w.SeriesKey]
	if !seriesExists {
		appSeries := len(series)
		for name, rows := range bySeries {
			if name != w.Name {
				appSeries += len(rows)
			}
		}
		if len(series) >= lim.SeriesPerMetric || appSeries >= lim.SeriesPerApp {
			return ErrCustomMetricSeriesLimit
		}
	}
	value := w.Value
	observed := w.ObservedAt
	if seriesExists {
		if w.Delta {
			value += prev.Value
		}
		if prev.ObservedAt.After(observed) {
			observed = prev.ObservedAt
		}
	}
	labels := make(map[string]string, len(w.Labels))
	for k, v := range w.Labels {
		labels[k] = v
	}
	series[w.SeriesKey] = CustomMetricSeries{Name: w.Name, SeriesKey: w.SeriesKey, Labels: labels, Kind: w.Kind,
		Value: clampCustomMetricTotal(value), ObservedAt: observed}
	bySeries[w.Name] = series

	var newest time.Time
	for _, row := range series {
		if row.ObservedAt.After(newest) {
			newest = row.ObservedAt
		}
	}
	agg := 0.0
	for _, row := range series {
		switch {
		case w.Kind == CustomMetricKindCounter:
			agg += row.Value
		case !row.ObservedAt.Before(newest.Add(-lim.GaugeFreshness)) && row.Value > agg:
			agg = row.Value
		}
	}
	metric.Kind, metric.Value, metric.ObservedAt = w.Kind, clampCustomMetricTotal(agg), newest
	byName[w.Name] = metric
	return nil
}

// ListCustomMetricSeries mirrors the PgStore order.
func (m *MemStore) ListCustomMetricSeries(_ context.Context, appID string) ([]CustomMetricSeries, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []CustomMetricSeries
	for _, rows := range m.customMetricSeries[appID] {
		for _, row := range rows {
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].SeriesKey < out[j].SeriesKey
	})
	return out, nil
}

// ListFreshCustomMetricSeries mirrors the PgStore order and limit.
func (m *MemStore) ListFreshCustomMetricSeries(_ context.Context, since time.Time, limit int) ([]CustomMetricSeriesSample, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []CustomMetricSeriesSample
	for appID, byName := range m.customMetricSeries {
		for _, rows := range byName {
			for _, row := range rows {
				if !row.ObservedAt.Before(since) || row.Kind == CustomMetricKindCounter {
					out = append(out, CustomMetricSeriesSample{AppID: appID, Name: row.Name, SeriesKey: row.SeriesKey, Value: row.Value})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.AppID != b.AppID {
			return a.AppID < b.AppID
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.SeriesKey < b.SeriesKey
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
