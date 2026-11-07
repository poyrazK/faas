package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RequestAnalyticsRollup is one per-minute analytics rollup row that apid
// adds to request_analytics_minutes (ADR-20261103113952).
type RequestAnalyticsRollup struct {
	AppID         uuid.UUID
	AccountID     uuid.UUID
	DeploymentID  uuid.UUID
	BucketStart   time.Time
	DimensionKind string
	Dimension     string
	Method        string
	Status        int16
	Requests      int64
	ErrorRequests int64
	ColdBoots     int64
	RequestTimeMS int64
	LatencyMaxMS  int32
	LatencyHist   [api.RequestAnalyticsLatencyBuckets]int64
}

// RequestAnalyticsCoverage is the first minute an app's rollups hold.
type RequestAnalyticsCoverage struct {
	AppID        uuid.UUID
	CoveredSince time.Time
}

// RequestAnalyticsGap is an app minute whose rollups apid lost.
type RequestAnalyticsGap struct {
	AppID  uuid.UUID
	Minute time.Time
}

// RequestAnalyticsRollupStore adds rollups. The write is additive and all
// or nothing, so a writer that sees an error can keep the rows and retry.
// RecordRequestAnalyticsRollupGaps marks the minutes it could not keep.
type RequestAnalyticsRollupStore interface {
	AddRequestAnalyticsRollups(context.Context, []RequestAnalyticsRollup, []RequestAnalyticsCoverage) error
	RecordRequestAnalyticsRollupGaps(context.Context, []RequestAnalyticsGap) error
}

// IsRequestAnalyticsDataError reports whether Postgres refused a rollup
// write for its data (SQLSTATE class 22 data exception or 23 integrity
// violation). Retrying the same rows fails again, so a writer isolates and
// drops them instead of keeping them.
func IsRequestAnalyticsDataError(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || len(pgErr.Code) < 2 {
		return false
	}
	class := pgErr.Code[:2]
	return class == "22" || class == "23"
}

// RecordRequestAnalyticsRollupGaps marks app minutes whose rollups were lost.
func (s *PgStore) RecordRequestAnalyticsRollupGaps(ctx context.Context, gaps []RequestAnalyticsGap) error {
	if len(gaps) == 0 {
		return nil
	}
	arg := sqlc.RecordRequestAnalyticsRollupGapsParams{
		AppIds:       make([]pgtype.UUID, len(gaps)),
		BucketStarts: make([]pgtype.Timestamptz, len(gaps)),
	}
	for i, g := range gaps {
		arg.AppIds[i] = NewPgtypeUUID(g.AppID)
		arg.BucketStarts[i] = pgtype.Timestamptz{Time: g.Minute.UTC().Truncate(time.Minute), Valid: true}
	}
	if err := sqlc.New().RecordRequestAnalyticsRollupGaps(ctx, s.pool, arg); err != nil {
		return fmt.Errorf("record request analytics rollup gaps: %w", mapErr(err))
	}
	return nil
}

// RequestAnalyticsRollupHasGap reports whether apid lost rollups of a
// minute starting inside [from, until).
func (s *PgStore) RequestAnalyticsRollupHasGap(ctx context.Context, appID uuid.UUID, from, until time.Time) (bool, error) {
	return routeAnalyticsRead(ctx, s, func(db sqlc.DBTX) (bool, error) {
		return sqlc.New().RequestAnalyticsRollupHasGap(ctx, db, sqlc.RequestAnalyticsRollupHasGapParams{
			AppID:       NewPgtypeUUID(appID),
			WindowFrom:  pgtype.Timestamptz{Time: from, Valid: true},
			WindowUntil: pgtype.Timestamptz{Time: until, Valid: true},
		})
	})
}

// RequestAnalyticsRollupRouteDeployments reads requests per route, method
// and deployment under the analytics statement budget.
func (s *PgStore) RequestAnalyticsRollupRouteDeployments(ctx context.Context, arg sqlc.RequestAnalyticsRollupRouteDeploymentsParams) ([]sqlc.RequestAnalyticsRollupRouteDeploymentsRow, error) {
	return routeAnalyticsRead(ctx, s, func(db sqlc.DBTX) ([]sqlc.RequestAnalyticsRollupRouteDeploymentsRow, error) {
		return sqlc.New().RequestAnalyticsRollupRouteDeployments(ctx, db, arg)
	})
}

var _ RequestAnalyticsRollupStore = (*PgStore)(nil)

// AddRequestAnalyticsRollups writes one flush in a single transaction, in
// statements of at most api.RequestAnalyticsRollupFlushRowsMax rows. The
// caller merges rows with the same key; one statement cannot update a row
// twice.
func (s *PgStore) AddRequestAnalyticsRollups(ctx context.Context, rows []RequestAnalyticsRollup, coverage []RequestAnalyticsCoverage) error {
	if len(rows) == 0 && len(coverage) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin request analytics rollups: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	for start := 0; start < len(rows); start += api.RequestAnalyticsRollupFlushRowsMax {
		end := min(start+api.RequestAnalyticsRollupFlushRowsMax, len(rows))
		if err := q.AddRequestAnalyticsMinutes(ctx, tx, requestAnalyticsMinutesParams(rows[start:end])); err != nil {
			return fmt.Errorf("add request analytics rollups: %w", mapErr(err))
		}
	}
	// The same flush, folded to UTC hours, feeds the hourly tier long
	// windows read (ADR-20261103113952 amendment 2).
	hours := requestAnalyticsHourRows(rows)
	for start := 0; start < len(hours); start += api.RequestAnalyticsRollupFlushRowsMax {
		end := min(start+api.RequestAnalyticsRollupFlushRowsMax, len(hours))
		if err := q.AddRequestAnalyticsHours(ctx, tx, sqlc.AddRequestAnalyticsHoursParams(requestAnalyticsMinutesParams(hours[start:end]))); err != nil {
			return fmt.Errorf("add hourly request analytics rollups: %w", mapErr(err))
		}
	}
	if len(coverage) > 0 {
		arg := sqlc.RecordRequestAnalyticsRollupCoverageParams{
			AppIds:       make([]pgtype.UUID, len(coverage)),
			CoveredSince: make([]pgtype.Timestamptz, len(coverage)),
		}
		for i, c := range coverage {
			arg.AppIds[i] = NewPgtypeUUID(c.AppID)
			arg.CoveredSince[i] = pgtype.Timestamptz{Time: c.CoveredSince.UTC(), Valid: true}
		}
		if err := q.RecordRequestAnalyticsRollupCoverage(ctx, tx, arg); err != nil {
			return fmt.Errorf("record request analytics rollup coverage: %w", mapErr(err))
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit request analytics rollups: %w", err)
	}
	return nil
}

// RequestAnalyticsRollupCoverage returns the first minute the app's rollups
// hold, and false when the app has none.
func (s *PgStore) RequestAnalyticsRollupCoverage(ctx context.Context, appID uuid.UUID) (time.Time, bool, error) {
	since, err := sqlc.New().GetRequestAnalyticsRollupCoverage(ctx, s.pool, NewPgtypeUUID(appID))
	if err != nil {
		if errors.Is(mapErr(err), ErrNotFound) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, fmt.Errorf("read request analytics rollup coverage: %w", mapErr(err))
	}
	return since.CoveredSince.Time, since.CoveredSince.Valid, nil
}

// RequestAnalyticsRollupHoursCoverage returns the first minute the hourly
// tier holds, and false when it holds none for the app.
func (s *PgStore) RequestAnalyticsRollupHoursCoverage(ctx context.Context, appID uuid.UUID) (time.Time, bool, error) {
	row, err := sqlc.New().GetRequestAnalyticsRollupCoverage(ctx, s.pool, NewPgtypeUUID(appID))
	if err != nil {
		if errors.Is(mapErr(err), ErrNotFound) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, fmt.Errorf("read request analytics rollup coverage: %w", mapErr(err))
	}
	return row.HoursCoveredSince.Time, row.HoursCoveredSince.Valid, nil
}

// requestAnalyticsHourRows folds minute rows into UTC hour rows, merging
// rows that share an hour key, in a stable order.
func requestAnalyticsHourRows(rows []RequestAnalyticsRollup) []RequestAnalyticsRollup {
	type key struct {
		app, deployment   uuid.UUID
		hour              int64
		kind, dim, method string
		status            int16
	}
	index := make(map[key]int, len(rows))
	hours := make([]RequestAnalyticsRollup, 0, len(rows))
	for _, r := range rows {
		hour := r.BucketStart.UTC().Truncate(time.Hour)
		k := key{r.AppID, r.DeploymentID, hour.Unix(), r.DimensionKind, r.Dimension, r.Method, r.Status}
		i, ok := index[k]
		if !ok {
			r.BucketStart = hour
			hours = append(hours, r)
			index[k] = len(hours) - 1
			continue
		}
		h := &hours[i]
		h.Requests += r.Requests
		h.ErrorRequests += r.ErrorRequests
		h.ColdBoots += r.ColdBoots
		h.RequestTimeMS += r.RequestTimeMS
		h.LatencyMaxMS = max(h.LatencyMaxMS, r.LatencyMaxMS)
		for b := range h.LatencyHist {
			h.LatencyHist[b] += r.LatencyHist[b]
		}
	}
	return hours
}

func requestAnalyticsMinutesParams(rows []RequestAnalyticsRollup) sqlc.AddRequestAnalyticsMinutesParams {
	n := len(rows)
	arg := sqlc.AddRequestAnalyticsMinutesParams{
		LatencyHists:   make([]int64, 0, n*api.RequestAnalyticsLatencyBuckets),
		AppIds:         make([]pgtype.UUID, n),
		BucketStarts:   make([]pgtype.Timestamptz, n),
		DimensionKinds: make([]string, n),
		Dimensions:     make([]string, n),
		Methods:        make([]string, n),
		Statuses:       make([]int16, n),
		DeploymentIds:  make([]pgtype.UUID, n),
		AccountIds:     make([]pgtype.UUID, n),
		Requests:       make([]int64, n),
		ErrorRequests:  make([]int64, n),
		ColdBoots:      make([]int64, n),
		RequestTimeMs:  make([]int64, n),
		LatencyMaxMs:   make([]int32, n),
	}
	for i, r := range rows {
		arg.AppIds[i] = NewPgtypeUUID(r.AppID)
		arg.BucketStarts[i] = pgtype.Timestamptz{Time: r.BucketStart.UTC(), Valid: true}
		arg.DimensionKinds[i] = r.DimensionKind
		arg.Dimensions[i] = r.Dimension
		arg.Methods[i] = r.Method
		arg.Statuses[i] = r.Status
		arg.DeploymentIds[i] = NewPgtypeUUID(r.DeploymentID)
		arg.AccountIds[i] = NewPgtypeUUID(r.AccountID)
		arg.Requests[i] = r.Requests
		arg.ErrorRequests[i] = r.ErrorRequests
		arg.ColdBoots[i] = r.ColdBoots
		arg.RequestTimeMs[i] = r.RequestTimeMS
		arg.LatencyMaxMs[i] = r.LatencyMaxMS
		arg.LatencyHists = append(arg.LatencyHists, r.LatencyHist[:]...)
	}
	return arg
}

// RequestAnalyticsRollupReader reads the analytics rollups.
type RequestAnalyticsRollupReader interface {
	RequestAnalyticsRollupCoverage(ctx context.Context, appID uuid.UUID) (time.Time, bool, error)
	RequestAnalyticsRollupHoursCoverage(ctx context.Context, appID uuid.UUID) (time.Time, bool, error)
	RequestAnalyticsRollupGroups(ctx context.Context, arg sqlc.RequestAnalyticsRollupGroupsParams) ([]sqlc.RequestAnalyticsRollupGroupsRow, error)
	RequestAnalyticsRollupHasGap(ctx context.Context, appID uuid.UUID, from, until time.Time) (bool, error)
	RequestAnalyticsRollupRouteDeployments(ctx context.Context, arg sqlc.RequestAnalyticsRollupRouteDeploymentsParams) ([]sqlc.RequestAnalyticsRollupRouteDeploymentsRow, error)
}

var _ RequestAnalyticsRollupReader = (*PgStore)(nil)

// RequestAnalyticsRollupGroups reads rollup groups under the analytics
// statement budget, as the raw analytics reads do.
func (s *PgStore) RequestAnalyticsRollupGroups(ctx context.Context, arg sqlc.RequestAnalyticsRollupGroupsParams) ([]sqlc.RequestAnalyticsRollupGroupsRow, error) {
	return routeAnalyticsRead(ctx, s, func(db sqlc.DBTX) ([]sqlc.RequestAnalyticsRollupGroupsRow, error) {
		return sqlc.New().RequestAnalyticsRollupGroups(ctx, db, arg)
	})
}

// RequestAnalyticsHistPercentile is the q-quantile of a rollup latency
// histogram: the upper bound of the first bucket whose cumulative count
// reaches q of the total, as the raw queries take the first latency
// representative that does. It never exceeds maxMS, the largest latency
// the histogram holds, which also stands for the overflow bucket.
func RequestAnalyticsHistPercentile(hist []int64, maxMS int32, q float64) int {
	var total int64
	for _, c := range hist {
		total += c
	}
	if total == 0 {
		return 0
	}
	var cumulative int64
	for i, c := range hist {
		cumulative += c
		if float64(cumulative) >= float64(total)*q {
			if i < len(api.RequestAnalyticsLatencyBoundsMS) {
				return min(api.RequestAnalyticsLatencyBoundsMS[i], int(maxMS))
			}
			break
		}
	}
	return int(maxMS)
}
