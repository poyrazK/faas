package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ReleaseApprovalStore = (*PgStore)(nil)

func releaseApprovalPolicyFromRow(r sqlc.AppReleaseApprovalPolicy) ReleaseApprovalPolicy {
	return ReleaseApprovalPolicy{
		AppID: pgUUIDString(r.AppID), AccountID: pgUUIDString(r.AccountID),
		RequiredApprovals: int(r.RequiredApprovals), AfterStage: int(r.AfterStage), TimeoutMinutes: int(r.TimeoutMinutes),
		Approvers: r.Approvers, AllowSelfApproval: r.AllowSelfApproval, UpdatedBy: r.UpdatedBy,
		CreatedAt: r.CreatedAt.Time.UTC(), UpdatedAt: r.UpdatedAt.Time.UTC(),
	}
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
		Approvers: p.Approvers, AllowSelfApproval: p.AllowSelfApproval, UpdatedBy: p.UpdatedBy, Now: pgTimestamptzUTC(now),
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
