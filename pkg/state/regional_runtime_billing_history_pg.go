package state

// adr: 799
import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RegionalRuntimeBillingHistoryStore = (*PgStore)(nil)

func (s *PgStore) RegionalRuntimeBillingHistoryStart(ctx context.Context) (time.Time, error) {
	start, err := (&sqlc.Queries{}).RegionalRuntimeBillingHistoryStart(ctx, s.pool)
	return start.Time, regionalCapacityError(err)
}

func (s *PgStore) ListRegionalRuntimeBillingIntervals(ctx context.Context, app string, minute time.Time, after int64, limit int) ([]RegionalRuntimeBillingInterval, error) {
	if !validRegionalBillingHistoryPage(app, minute, after, limit) {
		return nil, ErrInvalidArgument
	}
	rows, err := (&sqlc.Queries{}).ListRegionalRuntimeBillingIntervals(ctx, s.pool, sqlc.ListRegionalRuntimeBillingIntervalsParams{
		AppID: uuidToPgtype(app), AfterID: after, BatchLimit: int32(limit),
		MinuteStart: pgtype.Timestamptz{Time: minute.UTC(), Valid: true}, MinuteEnd: pgtype.Timestamptz{Time: minute.Add(time.Minute).UTC(), Valid: true},
	})
	if err != nil {
		return nil, regionalCapacityError(err)
	}
	result := make([]RegionalRuntimeBillingInterval, 0, len(rows))
	for _, row := range rows {
		r, err := regionalBillingHistoryRow(row)
		if err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, nil
}

func regionalBillingHistoryRow(row sqlc.ListRegionalRuntimeBillingIntervalsRow) (RegionalRuntimeBillingInterval, error) {
	r := RegionalRuntimeBillingInterval{ID: row.ID, AppID: pgUUIDString(row.AppID), InstanceID: pgUUIDString(row.InstanceID),
		DeploymentID: exclusiveUUID(row.DeploymentID), NodeID: exclusiveUUID(row.NodeID), RunID: exclusiveUUID(row.RunID),
		StartedAt: row.StartedAt.Time, RecordedAt: row.RecordedAt.Time, CompleteStart: row.CompleteStart,
		EndedAt: exclusiveTime(row.EndedAt), UnknownSince: exclusiveTime(row.UnknownSince), SourceMatches: row.SourceMatches, IssuedAt: exclusiveTime(row.IssuedAt)}
	if r.SourceMatches == 0 {
		return r, nil
	}
	var p runtimequota.CapacityPermit
	if json.Unmarshal(row.PermitClaims, &p) != nil || p.Validate() != nil || !regionalBillingIntervalMatches(r, p) || r.IssuedAt == nil {
		return RegionalRuntimeBillingInterval{}, ErrConflict
	}
	r.Permit = &p
	if row.InstanceKey.Valid {
		launch, err := regionalLaunchSourceRow(sqlc.RegionalRuntimeLaunchSource{InstanceKey: row.InstanceKey.String, PermitClaims: row.PermitClaims,
			CreatedAt: row.SourceCreatedAt, ResourceClaims: row.ResourceClaims, TaskID: row.TaskID})
		if err != nil {
			return RegionalRuntimeBillingInterval{}, err
		}
		r.Launch = &launch
	}
	return r, nil
}
