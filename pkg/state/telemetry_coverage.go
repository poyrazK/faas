package state

import (
	"context"
	"encoding/json"
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
}

type TelemetryAppGap struct {
	AppID        string `json:"app_id"`
	DroppedCount int64  `json:"dropped_count"`
	PendingCount int32  `json:"pending_count"`
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
	seen := map[string]bool{}
	var pending int64
	for _, gap := range c.AppGaps {
		app, err := uuid.Parse(gap.AppID)
		if err != nil || app == uuid.Nil || app.String() != gap.AppID || seen[gap.AppID] || gap.DroppedCount < 0 || gap.PendingCount < 0 {
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
	return sqlc.New().RecordTelemetryCoverage(ctx, s.pool, sqlc.RecordTelemetryCoverageParams{
		NodeName: c.NodeName, BootID: NewPgtypeUUID(boot), Sequence: c.Sequence, Enabled: c.Enabled,
		SamplingBasisPoints: c.SamplingBasisPoints, DroppedTotal: c.DroppedTotal, PendingCount: c.PendingCount,
		SourceAt:  pgtype.Timestamptz{Time: c.SourceAt, Valid: true},
		AppScoped: c.AppScoped, UnattributedDroppedTotal: c.UnattributedDroppedTotal, AppGaps: body,
	})
}
