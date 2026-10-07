package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ApplicationStandardRuntimeRefreshStore = (*PgStore)(nil)

func queueStandardRuntimeRefreshTx(ctx context.Context, tx pgx.Tx, e ApplicationStandardEnrollment) error {
	q := sqlc.New()
	if err := q.InvalidateApplicationStandardSnapshots(ctx, tx, mustPgUUID(e.AppID)); err != nil {
		return err
	}
	payload, err := json.Marshal(newStandardRuntimeRefresh(e))
	if err != nil {
		return err
	}
	return q.QueueApplicationStandardRuntimeRefresh(ctx, tx, sqlc.QueueApplicationStandardRuntimeRefreshParams{Payload: string(payload), WakeDelaySeconds: api.ApplicationStandardRuntimeRefreshWakeDelay.Seconds()})
}

func (s *PgStore) GetApplicationStandardRuntimeRefresh(ctx context.Context, orgID, appID string) (ApplicationStandardRuntimeRefreshRequest, error) {
	if !validStandardResourceRead(orgID, appID) {
		return ApplicationStandardRuntimeRefreshRequest{}, ErrInvalidArgument
	}
	payload, err := sqlc.New().GetApplicationStandardRuntimeRefresh(ctx, s.pool, sqlc.GetApplicationStandardRuntimeRefreshParams{OrgID: canonicalStandardUUID(orgID), AppID: canonicalStandardUUID(appID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ApplicationStandardRuntimeRefreshRequest{}, ErrNotFound
	}
	var r ApplicationStandardRuntimeRefreshRequest
	if err != nil {
		return r, err
	}
	err = json.Unmarshal([]byte(payload), &r)
	return r, err
}

func (s *PgStore) CheckApplicationStandardRuntimeRefresh(ctx context.Context, r ApplicationStandardRuntimeRefreshRequest) (bool, error) {
	if !validStandardRuntimeRefresh(r) {
		return false, ErrInvalidArgument
	}
	row, err := sqlc.New().CheckApplicationStandardRuntimeRefresh(ctx, s.pool, sqlc.CheckApplicationStandardRuntimeRefreshParams{AppID: mustPgUUID(r.AppID), OrgID: mustPgUUID(r.Standard.OrgID), DesiredRevision: r.Standard.DesiredRevision})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	e := ApplicationStandardEnrollment{AppID: r.AppID, OrgID: r.Standard.OrgID, DesiredRevision: row.DesiredRevision, PersistedRevision: row.PersistedRevision, EffectiveHash: row.EffectiveHash}
	return standardRuntimeRefreshCurrent(r, e, row.Paused)
}
