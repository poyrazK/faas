package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// TelemetryCoverage is internal gateway evidence, never a customer attestation.
type TelemetryCoverage struct {
	NodeName                 string
	BootID                   string
	Sequence                 int64
	Enabled                  bool
	SamplingBasisPoints      int32
	DroppedTotal             int64
	PendingCount             int32
	SourceAt                 time.Time
	AppScoped                bool
	UnattributedDroppedTotal int64
	AppGaps                  []TelemetryAppGap
	// Analytics is set by a gateway that counts its own requests into
	// analytics rollups (ADR-20261103113952 amendments 5 and 6); nil from
	// any other.
	Analytics *AnalyticsCoverage
}

// AnalyticsCoverage is a heartbeat's analytics part: whether the gateway
// counts every request it records, and the cumulative analytics losses it
// could not attribute to an app.
type AnalyticsCoverage struct {
	Counting                 bool
	UnattributedDroppedTotal int64
}

type TelemetryAppGap struct {
	AppID        string `json:"app_id"`
	DroppedCount int64  `json:"dropped_count"`
	PendingCount int32  `json:"pending_count"`
	// AnalyticsDroppedCount is the requests lost to analytics since the last
	// acknowledged heartbeat; AnalyticsPendingCount the requests in rollups
	// past their send time and not yet acknowledged.
	AnalyticsDroppedCount int64 `json:"analytics_dropped_count,omitempty"`
	AnalyticsPendingCount int32 `json:"analytics_pending_count,omitempty"`
}

type TelemetryCoverageStore interface {
	RecordTelemetryCoverage(context.Context, TelemetryCoverage) error
}

func ValidateTelemetryCoverage(c TelemetryCoverage) error {
	boot, err := uuid.Parse(c.BootID)
	if err != nil || boot == uuid.Nil || c.NodeName == "" || len(c.NodeName) > 128 || c.Sequence <= 0 || c.SamplingBasisPoints < 0 || c.SamplingBasisPoints > 10000 || c.DroppedTotal < 0 || c.PendingCount < 0 || c.UnattributedDroppedTotal < 0 || c.SourceAt.IsZero() {
		return ErrInvalidArgument
	}
	if !c.AppScoped && len(c.AppGaps) > 0 {
		return ErrInvalidArgument
	}
	if c.Analytics != nil && (c.Analytics.UnattributedDroppedTotal < 0 || !c.AppScoped) {
		return ErrInvalidArgument
	}
	seen := map[string]bool{}
	var pending int64
	for _, gap := range c.AppGaps {
		app, err := uuid.Parse(gap.AppID)
		if err != nil || app == uuid.Nil || app.String() != gap.AppID || seen[gap.AppID] || gap.DroppedCount < 0 || gap.PendingCount < 0 ||
			gap.AnalyticsDroppedCount < 0 || gap.AnalyticsPendingCount < 0 {
			return ErrInvalidArgument
		}
		seen[gap.AppID] = true
		pending += int64(gap.PendingCount)
	}
	if pending > int64(c.PendingCount) {
		return ErrInvalidArgument
	}
	return nil
}

func (s *PgStore) RecordTelemetryCoverage(ctx context.Context, c TelemetryCoverage) error {
	if err := ValidateTelemetryCoverage(c); err != nil {
		return err
	}
	boot, _ := uuid.Parse(c.BootID)
	gaps := c.AppGaps
	if gaps == nil {
		gaps = []TelemetryAppGap{}
	}
	body, err := json.Marshal(gaps)
	if err != nil {
		return err
	}
	raw := sqlc.RecordTelemetryCoverageParams{
		NodeName: c.NodeName, BootID: NewPgtypeUUID(boot), Sequence: c.Sequence, Enabled: c.Enabled,
		SamplingBasisPoints: c.SamplingBasisPoints, DroppedTotal: c.DroppedTotal, PendingCount: c.PendingCount,
		SourceAt:  pgtype.Timestamptz{Time: c.SourceAt, Valid: true},
		AppScoped: c.AppScoped, UnattributedDroppedTotal: c.UnattributedDroppedTotal, AppGaps: body,
	}
	if c.Analytics == nil {
		return sqlc.New().RecordTelemetryCoverage(ctx, s.pool, raw)
	}
	// The analytics part commits with the raw heartbeat or not at all: the
	// gateway forgets the loss deltas a receipt acknowledges.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin telemetry coverage: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.RecordTelemetryCoverage(ctx, tx, raw); err != nil {
		return err
	}
	if err := q.RecordRequestAnalyticsCoverage(ctx, tx, sqlc.RecordRequestAnalyticsCoverageParams{
		NodeName: c.NodeName, BootID: NewPgtypeUUID(boot), Counting: c.Analytics.Counting,
		UnattributedDroppedTotal: c.Analytics.UnattributedDroppedTotal, AppGaps: body,
	}); err != nil {
		return fmt.Errorf("record analytics coverage: %w", mapErr(err))
	}
	return tx.Commit(ctx)
}

// RequestAnalyticsCompleteness lists why analytics answered by rollups may
// miss requests of an app in [from, until): RequestTelemetryCompleteness's
// reasons, except that a node counting analytics itself over the window is
// judged by its analytics gaps instead of raw delivery (ADR-20261103113952
// amendment 6).
func (s *PgStore) RequestAnalyticsCompleteness(ctx context.Context, appID string, from, until time.Time) ([]string, error) {
	return s.requestAnalyticsCoverageGaps(ctx, appID, from, until, true)
}
