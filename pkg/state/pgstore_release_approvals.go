package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ReleaseApprovalStore = (*PgStore)(nil)

func releaseApprovalPolicyFromRow(r sqlc.AppReleaseApprovalPolicy) ReleaseApprovalPolicy {
	return ReleaseApprovalPolicy{
		AppID: pgUUIDString(r.AppID), AccountID: pgUUIDString(r.AccountID),
		RequiredApprovals: int(r.RequiredApprovals), AfterStage: int(r.AfterStage), TimeoutMinutes: int(r.TimeoutMinutes),
		Approvers: r.Approvers, AllowSelfApproval: r.AllowSelfApproval, NotifyChannelIDs: pgUUIDStrings(r.NotifyChannelIds),
		UpdatedBy: r.UpdatedBy, CreatedAt: r.CreatedAt.Time.UTC(), UpdatedAt: r.UpdatedAt.Time.UTC(),
	}
}

func pgUUIDStrings(ids []pgtype.UUID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, pgUUIDString(id))
	}
	return out
}

func pgUUIDList(ids []string) []pgtype.UUID {
	out := make([]pgtype.UUID, 0, len(ids))
	for _, id := range ids {
		out = append(out, mustPgUUID(id))
	}
	return out
}

func releaseApprovalEventFromRow(r sqlc.ReleaseApprovalEvent) ReleaseApprovalEvent {
	e := ReleaseApprovalEvent{
		ID: pgUUIDString(r.ID), AccountID: pgUUIDString(r.AccountID), AppID: pgUUIDString(r.AppID),
		DeploymentID: pgUUIDString(r.DeploymentID), Event: r.Event, ChannelIDs: pgUUIDStrings(r.ChannelIds),
		Payload: json.RawMessage(r.Payload), State: r.State, Attempts: int(r.Attempts),
		NextAttemptAt: r.NextAttemptAt.Time.UTC(), LastError: r.LastError, CreatedAt: r.CreatedAt.Time.UTC(),
	}
	if r.DeliveredAt.Valid {
		at := r.DeliveredAt.Time.UTC()
		e.DeliveredAt = &at
	}
	return e
}

var _ ReleaseApprovalEventStore = (*PgStore)(nil)

func (s *PgStore) EnqueueReleaseApprovalEvent(ctx context.Context, e ReleaseApprovalEvent, now time.Time) (bool, error) {
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	_, err := sqlc.New().EnqueueReleaseApprovalEvent(ctx, s.pool, sqlc.EnqueueReleaseApprovalEventParams{
		ID: mustPgUUID(e.ID), AccountID: mustPgUUID(e.AccountID), AppID: mustPgUUID(e.AppID),
		DeploymentID: mustPgUUID(e.DeploymentID), Event: e.Event, ChannelIds: pgUUIDList(e.ChannelIDs),
		Payload: []byte(e.Payload), Now: pgTimestamptzUTC(now),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, mapErr(err)
	}
	return true, nil
}

func (s *PgStore) DueReleaseApprovalEvents(ctx context.Context, now time.Time, limit int) ([]ReleaseApprovalEvent, error) {
	rows, err := sqlc.New().ListDueReleaseApprovalEvents(ctx, s.pool, sqlc.ListDueReleaseApprovalEventsParams{
		Now: pgTimestamptzUTC(now), MaxEvents: int32(limit),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]ReleaseApprovalEvent, 0, len(rows))
	for _, r := range rows {
		out = append(out, releaseApprovalEventFromRow(r))
	}
	return out, nil
}

func (s *PgStore) FinishReleaseApprovalEvent(ctx context.Context, id string, attempt ReleaseApprovalEventAttempt) error {
	delivered := pgtype.Timestamptz{}
	if attempt.State == ReleaseApprovalEventDelivered {
		delivered = pgTimestamptzUTC(attempt.At)
	}
	n, err := sqlc.New().FinishReleaseApprovalEvent(ctx, s.pool, sqlc.FinishReleaseApprovalEventParams{
		ID: mustPgUUID(id), State: attempt.State, LastError: attempt.LastError,
		NextAttemptAt: pgTimestamptzUTC(attempt.NextAttemptAt), DeliveredAt: delivered,
	})
	if err != nil {
		return mapErr(err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func releaseApprovalFromRow(r sqlc.DeploymentReleaseApproval) ReleaseApproval {
	return ReleaseApproval{
		ID: pgUUIDString(r.ID), AccountID: pgUUIDString(r.AccountID), AppID: pgUUIDString(r.AppID),
		DeploymentID: pgUUIDString(r.DeploymentID), ApproverAccountID: pgUUIDStringNullable(r.ApproverAccountID),
		Approver: r.Approver, ApproverRole: r.ApproverRole, Decision: r.Decision, Comment: r.Comment,
		CreatedAt: r.CreatedAt.Time.UTC(),
	}
}

func (s *PgStore) SetReleaseApprovalPolicy(ctx context.Context, p ReleaseApprovalPolicy, now time.Time) (ReleaseApprovalPolicy, error) {
	row, err := sqlc.New().UpsertAppReleaseApprovalPolicy(ctx, s.pool, sqlc.UpsertAppReleaseApprovalPolicyParams{
		AppID: mustPgUUID(p.AppID), AccountID: mustPgUUID(p.AccountID),
		RequiredApprovals: int16(p.RequiredApprovals), AfterStage: int16(p.AfterStage), TimeoutMinutes: int32(p.TimeoutMinutes),
		Approvers: p.Approvers, AllowSelfApproval: p.AllowSelfApproval, NotifyChannelIds: pgUUIDList(p.NotifyChannelIDs),
		UpdatedBy: p.UpdatedBy, Now: pgTimestamptzUTC(now),
	})
	if err != nil {
		return ReleaseApprovalPolicy{}, mapErr(err)
	}
	return releaseApprovalPolicyFromRow(row), nil
}

func (s *PgStore) ReleaseApprovalPolicy(ctx context.Context, appID string) (ReleaseApprovalPolicy, error) {
	row, err := sqlc.New().GetAppReleaseApprovalPolicy(ctx, s.pool, mustPgUUID(appID))
	if err != nil {
		return ReleaseApprovalPolicy{}, mapErr(err)
	}
	return releaseApprovalPolicyFromRow(row), nil
}

func (s *PgStore) DeleteReleaseApprovalPolicy(ctx context.Context, appID string) (ReleaseApprovalPolicy, error) {
	row, err := sqlc.New().DeleteAppReleaseApprovalPolicy(ctx, s.pool, mustPgUUID(appID))
	if err != nil {
		return ReleaseApprovalPolicy{}, mapErr(err)
	}
	return releaseApprovalPolicyFromRow(row), nil
}

func (s *PgStore) RecordReleaseApproval(ctx context.Context, a ReleaseApproval, now time.Time) (ReleaseApproval, error) {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	row, err := sqlc.New().CreateDeploymentReleaseApproval(ctx, s.pool, sqlc.CreateDeploymentReleaseApprovalParams{
		ID: mustPgUUID(a.ID), AccountID: mustPgUUID(a.AccountID), AppID: mustPgUUID(a.AppID),
		DeploymentID: mustPgUUID(a.DeploymentID), ApproverAccountID: mustPgUUID(a.ApproverAccountID),
		Approver: a.Approver, ApproverRole: a.ApproverRole, Decision: a.Decision, Comment: a.Comment,
		Now: pgTimestamptzUTC(now),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ReleaseApproval{}, ErrConflict
	}
	if err != nil {
		return ReleaseApproval{}, mapErr(err)
	}
	return releaseApprovalFromRow(row), nil
}

func (s *PgStore) ListReleaseApprovals(ctx context.Context, deploymentID string) ([]ReleaseApproval, error) {
	rows, err := sqlc.New().ListDeploymentReleaseApprovals(ctx, s.pool, mustPgUUID(deploymentID))
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]ReleaseApproval, 0, len(rows))
	for _, r := range rows {
		out = append(out, releaseApprovalFromRow(r))
	}
	return out, nil
}
