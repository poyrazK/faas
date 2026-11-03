package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
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
	LatencyMaxMS  int32
	LatencyHist   [api.RequestAnalyticsLatencyBuckets]int64
}

// RequestAnalyticsCoverage is the first minute an app's rollups hold.
type RequestAnalyticsCoverage struct {
	AppID        uuid.UUID
	CoveredSince time.Time
}

// RequestAnalyticsRollupStore adds rollups. The write is additive and all
// or nothing, so a writer that sees an error can keep the rows and retry.
type RequestAnalyticsRollupStore interface {
	AddRequestAnalyticsRollups(context.Context, []RequestAnalyticsRollup, []RequestAnalyticsCoverage) error
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
	return since.Time, since.Valid, nil
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
		arg.LatencyMaxMs[i] = r.LatencyMaxMS
		arg.LatencyHists = append(arg.LatencyHists, r.LatencyHist[:]...)
	}
	return arg
}
