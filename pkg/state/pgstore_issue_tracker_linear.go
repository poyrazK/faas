package state

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
)

func linearConnectionRow(r sqlc.IssueTrackerLinearConnection) IssueTrackerLinearRecord {
	out := IssueTrackerLinearRecord{AppID: issueID(r.AppID), AccountID: issueID(r.AccountID), SealedCredential: r.SealedCredential, CredentialGeneration: r.CredentialGeneration, RefreshClaimID: issueID(r.RefreshClaimID), RefreshClaimUntil: r.RefreshClaimUntil.Time, RefreshStartedAt: r.RefreshStartedAt.Time, Connection: api.IssueTrackerLinearConnection{AuthMethod: r.AuthMethod, OAuthStatus: r.OauthStatus, WebhookEnabled: r.WebhookEnabled, Connected: r.Connected, Revision: r.Revision, WorkspaceID: issueID(r.WorkspaceID), TeamID: issueID(r.TeamID), ViewerID: issueID(r.ViewerID)}}
	if r.AccessTokenExpiresAt.Valid {
		t := r.AccessTokenExpiresAt.Time
		out.Connection.AccessTokenExpiresAt = &t
	}
	if out.Connection.WebhookEnabled {
		out.Connection.CallbackPath = "/hooks/issue-trackers/linear/" + out.AppID
	}
	return out
}
func (s *PgStore) GetIssueTrackerLinearConnection(ctx context.Context, app, account string) (IssueTrackerLinearRecord, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return IssueTrackerLinearRecord{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.IssueTrackerLinearOwner(ctx, tx, sqlc.IssueTrackerLinearOwnerParams{AppID: issueUUID(app), AccountID: issueUUID(account)}); err != nil {
		return IssueTrackerLinearRecord{}, mapErr(err)
	}
	row, err := q.IssueTrackerLinearGet(ctx, tx, sqlc.IssueTrackerLinearGetParams{AppID: issueUUID(app), AccountID: issueUUID(account)})
	out := IssueTrackerLinearRecord{AppID: app, AccountID: account}
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	} else if err == nil {
		out = linearConnectionRow(row)
	}
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) ConfigureIssueTrackerLinearConnection(ctx context.Context, in IssueTrackerLinearRecord, expected int64) (IssueTrackerLinearRecord, error) {
	if expected < 0 || in.Connection.Connected && (len(in.SealedCredential) == 0 || !issueUUID(in.Connection.WorkspaceID).Valid || !issueUUID(in.Connection.TeamID).Valid || !issueUUID(in.Connection.ViewerID).Valid) {
		return in, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return in, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.IssueTrackerLinearOwner(ctx, tx, sqlc.IssueTrackerLinearOwnerParams{AppID: issueUUID(in.AppID), AccountID: issueUUID(in.AccountID)}); err != nil {
		return in, mapErr(err)
	}
	row, err := q.IssueTrackerLinearGet(ctx, tx, sqlc.IssueTrackerLinearGetParams{AppID: issueUUID(in.AppID), AccountID: issueUUID(in.AccountID)})
	revision := int64(0)
	if err == nil {
		revision = row.Revision
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return in, err
	}
	if revision != expected {
		return in, api.NewProblem(409, "issue_tracker_linear_connection_changed", "Linear connection changed", "read the connection again before changing it")
	}
	if in.Connection.AuthMethod == "" {
		in.Connection.AuthMethod = "api_key"
	}
	if in.Connection.AuthMethod == "oauth" && (in.Connection.OAuthStatus != "ready" || in.Connection.AccessTokenExpiresAt == nil) {
		return in, ErrInvalidArgument
	}
	if in.Connection.Connected && row.OauthStatus == "revoking" {
		return in, api.ErrValidation("finish revoking the old Linear grant or disconnect it before replacing the connection")
	}
	if !in.Connection.Connected {
		in.Connection.WorkspaceID = ""
		in.Connection.TeamID = ""
		in.Connection.ViewerID = ""
		in.SealedCredential = nil
		in.Connection.WebhookEnabled = false
		in.Connection.AuthMethod = "api_key"
		in.Connection.OAuthStatus = ""
		in.Connection.AccessTokenExpiresAt = nil
	}
	row, err = q.IssueTrackerLinearPut(ctx, tx, sqlc.IssueTrackerLinearPutParams{AppID: issueUUID(in.AppID), AccountID: issueUUID(in.AccountID), AuthMethod: in.Connection.AuthMethod, OauthStatus: in.Connection.OAuthStatus, AccessTokenExpiresAt: linearExpiry(in.Connection.AccessTokenExpiresAt), WebhookEnabled: in.Connection.WebhookEnabled, Connected: in.Connection.Connected, WorkspaceID: issueUUID(in.Connection.WorkspaceID), TeamID: issueUUID(in.Connection.TeamID), ViewerID: issueUUID(in.Connection.ViewerID), SealedCredential: in.SealedCredential})
	if err != nil {
		return in, mapErr(err)
	}
	return linearConnectionRow(row), tx.Commit(ctx)
}

func linearExpiry(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return issueTime(*t)
}
