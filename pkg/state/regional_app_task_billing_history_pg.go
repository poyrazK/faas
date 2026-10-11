package state

// adr: 800
import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RegionalAppTaskBillingHistoryStore = (*PgStore)(nil)

func (s *PgStore) RegionalAppTaskBillingHistoryStart(ctx context.Context) (time.Time, error) {
	start, err := (&sqlc.Queries{}).RegionalAppTaskBillingHistoryStart(ctx, s.pool)
	return start.Time, regionalCapacityError(err)
}

func (s *PgStore) ListRegionalAppTaskBillingIntervals(ctx context.Context, app string, minute time.Time, after int64, limit int) ([]RegionalAppTaskBillingInterval, error) {
	if !validRegionalBillingHistoryPage(app, minute, after, limit) {
		return nil, ErrInvalidArgument
	}
	rows, err := (&sqlc.Queries{}).ListRegionalAppTaskBillingIntervals(ctx, s.pool, sqlc.ListRegionalAppTaskBillingIntervalsParams{
		AppID: uuidToPgtype(app), AfterID: after, BatchLimit: int32(limit),
		MinuteStart: pgtype.Timestamptz{Time: minute.UTC(), Valid: true}, MinuteEnd: pgtype.Timestamptz{Time: minute.Add(time.Minute).UTC(), Valid: true},
	})
	if err != nil {
		return nil, regionalCapacityError(err)
	}
	result := make([]RegionalAppTaskBillingInterval, 0, len(rows))
	for _, row := range rows {
		r, err := regionalAppTaskBillingHistoryRow(row)
		if err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, nil
}

func regionalAppTaskBillingHistoryRow(row sqlc.ListRegionalAppTaskBillingIntervalsRow) (RegionalAppTaskBillingInterval, error) {
	r := RegionalAppTaskBillingInterval{ID: row.ID, AppID: pgUUIDString(row.AppID), TaskID: pgUUIDString(row.TaskID),
		DeploymentID: pgUUIDString(row.DeploymentID), RunID: pgUUIDString(row.RunID), LeaseOwner: row.LeaseOwner,
		StartedAt: row.StartedAt.Time, RecordedAt: row.RecordedAt.Time, CompleteStart: row.CompleteStart,
		EndedAt: exclusiveTime(row.EndedAt), UnknownSince: exclusiveTime(row.UnknownSince), SourceMatches: row.SourceMatches, IssuedAt: exclusiveTime(row.IssuedAt)}
	if r.SourceMatches == 0 {
		return r, nil
	}
	var p runtimequota.CapacityPermit
	if json.Unmarshal(row.PermitClaims, &p) != nil || p.Validate() != nil || !regionalAppTaskBillingPermitMatches(r, p) || r.IssuedAt == nil {
		return RegionalAppTaskBillingInterval{}, ErrConflict
	}
	r.Permit = &p
	if row.InstanceKey.Valid {
		launch, err := regionalLaunchSourceRow(sqlc.RegionalRuntimeLaunchSource{InstanceKey: row.InstanceKey.String, PermitClaims: row.PermitClaims,
			CreatedAt: row.SourceCreatedAt, ResourceClaims: row.ResourceClaims, TaskID: row.SourceTaskID})
		if err != nil {
			return RegionalAppTaskBillingInterval{}, err
		}
		if !regionalAppTaskBillingLaunchMatches(r, launch) {
			return RegionalAppTaskBillingInterval{}, ErrConflict
		}
		r.Launch = &launch
	}
	return r, nil
}
