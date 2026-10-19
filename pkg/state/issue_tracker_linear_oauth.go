package state

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
)

type LinearOAuthFlow struct {
	AppID, AccountID, IssueID, Hash, Phase string
	ExpectedRevision                       int64
	Sealed                                 []byte
	ExpiresAt                              time.Time
}
type LinearOAuthStore interface {
	IssueTrackerLinearStore
	StartLinearOAuthFlow(context.Context, LinearOAuthFlow) error
	GetLinearOAuthFlow(context.Context, string, string) (LinearOAuthFlow, error)
	ClaimLinearOAuthFlow(context.Context, string, string, string, string) (LinearOAuthFlow, error)
	SaveLinearOAuthFlow(context.Context, LinearOAuthFlow, []byte) error
	DeleteLinearOAuthFlow(context.Context, string, string) error
	CleanupLinearOAuthFlows(context.Context) error
	ClaimLinearOAuthRefresh(context.Context, IssueTrackerLinearRecord) (IssueTrackerLinearRecord, error)
	FinishLinearOAuthRefresh(context.Context, IssueTrackerLinearRecord, []byte, *time.Time, string) (IssueTrackerLinearRecord, error)
	SetLinearOAuthStatus(context.Context, IssueTrackerLinearRecord, string) (IssueTrackerLinearRecord, error)
}

func linearFlowRow(r sqlc.IssueTrackerLinearOauthFlow) LinearOAuthFlow {
	return LinearOAuthFlow{AppID: issueID(r.AppID), AccountID: issueID(r.AccountID), IssueID: issueID(r.IssueID), Hash: r.StateHash, Phase: r.Phase, ExpectedRevision: r.ExpectedRevision, Sealed: r.SealedState, ExpiresAt: r.ExpiresAt.Time}
}
func (s *PgStore) StartLinearOAuthFlow(ctx context.Context, in LinearOAuthFlow) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.IssueTrackerLinearOwner(ctx, tx, sqlc.IssueTrackerLinearOwnerParams{AppID: issueUUID(in.AppID), AccountID: issueUUID(in.AccountID)}); err != nil {
		return mapErr(err)
	}
	row, err := q.IssueTrackerLinearGet(ctx, tx, sqlc.IssueTrackerLinearGetParams{AppID: issueUUID(in.AppID), AccountID: issueUUID(in.AccountID)})
	rev := int64(0)
	if err == nil {
		rev = row.Revision
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if row.OauthStatus == "revoking" {
		return api.ErrValidation("finish Linear revocation or disconnect before starting new consent")
	}
	if rev != in.ExpectedRevision {
		return api.ErrValidation("Linear connection changed; reload before authorizing")
	}
	err = q.LinearOAuthFlowStart(ctx, tx, sqlc.LinearOAuthFlowStartParams{AppID: issueUUID(in.AppID), AccountID: issueUUID(in.AccountID), IssueID: issueUUID(in.IssueID), StateHash: in.Hash, ExpectedRevision: in.ExpectedRevision, SealedState: in.Sealed, ExpiresAt: issueTime(in.ExpiresAt)})
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *PgStore) GetLinearOAuthFlow(ctx context.Context, hash, account string) (LinearOAuthFlow, error) {
	r, e := sqlc.New().LinearOAuthFlowGet(ctx, s.pool, sqlc.LinearOAuthFlowGetParams{StateHash: hash, AccountID: issueUUID(account)})
	return linearFlowRow(r), mapErr(e)
}
func (s *PgStore) ClaimLinearOAuthFlow(ctx context.Context, hash, account, from, to string) (LinearOAuthFlow, error) {
	if (from != "authorizing" || to != "exchanging") && (from != "choosing" || to != "consuming") {
		return LinearOAuthFlow{}, ErrInvalidArgument
	}
	r, e := sqlc.New().LinearOAuthFlowClaim(ctx, s.pool, sqlc.LinearOAuthFlowClaimParams{StateHash: hash, AccountID: issueUUID(account), ExpectedPhase: from, NextPhase: to})
	return linearFlowRow(r), mapErr(e)
}
func (s *PgStore) SaveLinearOAuthFlow(ctx context.Context, r LinearOAuthFlow, sealed []byte) error {
	n, e := sqlc.New().LinearOAuthFlowSave(ctx, s.pool, sqlc.LinearOAuthFlowSaveParams{StateHash: r.Hash, AccountID: issueUUID(r.AccountID), SealedState: sealed})
	if e == nil && n != 1 {
		return ErrNotFound
	}
	return e
}
func (s *PgStore) DeleteLinearOAuthFlow(ctx context.Context, hash, account string) error {
	return sqlc.New().LinearOAuthFlowDelete(ctx, s.pool, sqlc.LinearOAuthFlowDeleteParams{StateHash: hash, AccountID: issueUUID(account)})
}
func (s *PgStore) CleanupLinearOAuthFlows(ctx context.Context) error {
	return sqlc.New().LinearOAuthFlowCleanup(ctx, s.pool, api.IssueTrackerLinearOAuthCleanupBatch)
}
func (s *PgStore) ClaimLinearOAuthRefresh(ctx context.Context, r IssueTrackerLinearRecord) (IssueTrackerLinearRecord, error) {
	row, e := sqlc.New().LinearOAuthRefreshClaim(ctx, s.pool, sqlc.LinearOAuthRefreshClaimParams{AppID: issueUUID(r.AppID), AccountID: issueUUID(r.AccountID), Revision: r.Connection.Revision, Generation: r.CredentialGeneration, ClaimID: issueUUID(uuid.NewString()), ClaimUntil: issueTime(time.Now().Add(api.IssueTrackerLinearOAuthLease)), RefreshBefore: issueTime(time.Now().Add(api.IssueTrackerLinearOAuthRefreshSkew)), GraceAfter: issueTime(time.Now().Add(-api.IssueTrackerLinearOAuthRefreshGrace))})
	return linearConnectionRow(row), mapErr(e)
}
func (s *PgStore) FinishLinearOAuthRefresh(ctx context.Context, r IssueTrackerLinearRecord, sealed []byte, expires *time.Time, status string) (IssueTrackerLinearRecord, error) {
	if status != "ready" && status != "refreshing" && status != "reconnect_required" {
		return r, ErrInvalidArgument
	}
	if status == "ready" && (len(sealed) == 0 || expires == nil) {
		return r, ErrInvalidArgument
	}
	row, e := sqlc.New().LinearOAuthRefreshFinish(ctx, s.pool, sqlc.LinearOAuthRefreshFinishParams{AppID: issueUUID(r.AppID), AccountID: issueUUID(r.AccountID), Revision: r.Connection.Revision, Generation: r.CredentialGeneration, ClaimID: issueUUID(r.RefreshClaimID), SealedCredential: sealed, ExpiresAt: linearExpiry(expires), OauthStatus: status})
	return linearConnectionRow(row), mapErr(e)
}
func (s *PgStore) SetLinearOAuthStatus(ctx context.Context, r IssueTrackerLinearRecord, status string) (IssueTrackerLinearRecord, error) {
	if status != "reconnect_required" && status != "revoking" {
		return r, ErrInvalidArgument
	}
	row, e := sqlc.New().LinearOAuthSetStatus(ctx, s.pool, sqlc.LinearOAuthSetStatusParams{AppID: issueUUID(r.AppID), AccountID: issueUUID(r.AccountID), Revision: r.Connection.Revision, Generation: r.CredentialGeneration, OauthStatus: status})
	return linearConnectionRow(row), mapErr(e)
}
