package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrNativeIssueTicketBusy = errors.New("native ticket request is already claimed or changed")

func nativeTicketRow(r sqlc.NativeIssueTicketReceipt) NativeIssueTicketReceipt {
	return NativeIssueTicketReceipt{AccountID: issueID(r.AccountID), RequestID: issueID(r.RequestID), AppID: issueID(r.AppID), IssueID: issueID(r.IssueID), Hash: r.RequestHash, State: r.State, ClaimID: issueID(r.ClaimID), RecoverAfter: r.RecoverAfter.Time, TicketURL: r.TicketUrl, ProviderID: r.ProviderID, LinkID: issueID(r.LinkID)}
}
func (s *PgStore) ReserveNativeIssueTicket(ctx context.Context, in NativeIssueTicketReceipt) (NativeIssueTicketReceipt, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return in, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	owner, err := q.IssueLockApp(ctx, tx, issueUUID(in.AppID))
	if err != nil {
		return in, mapErr(err)
	}
	if issueID(owner.AccountID) != in.AccountID {
		return in, ErrNotFound
	}
	if _, err = issueCanonicalRow(ctx, q, tx, in.AppID, in.IssueID); err != nil {
		return in, err
	}
	row, err := q.NativeIssueTicketGet(ctx, tx, sqlc.NativeIssueTicketGetParams{AccountID: issueUUID(in.AccountID), RequestID: issueUUID(in.RequestID)})
	if errors.Is(err, pgx.ErrNoRows) {
		n, e := q.NativeIssueTicketCount(ctx, tx, issueUUID(in.IssueID))
		if e != nil {
			return in, e
		}
		if n >= api.IssueTrackerNativeRequestsMax {
			return in, &IssueLimitError{Resource: "native_ticket_requests", Limit: api.IssueTrackerNativeRequestsMax, Observed: n + 1}
		}
		row, err = q.NativeIssueTicketInsert(ctx, tx, sqlc.NativeIssueTicketInsertParams{AccountID: issueUUID(in.AccountID), RequestID: issueUUID(in.RequestID), AppID: issueUUID(in.AppID), IssueID: issueUUID(in.IssueID), RequestHash: in.Hash})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		row, err = q.NativeIssueTicketGet(ctx, tx, sqlc.NativeIssueTicketGetParams{AccountID: issueUUID(in.AccountID), RequestID: issueUUID(in.RequestID)})
	}
	if err != nil {
		return in, mapErr(err)
	}
	return nativeTicketRow(row), tx.Commit(ctx)
}
func (s *PgStore) ClaimNativeIssueTicket(ctx context.Context, in NativeIssueTicketReceipt, recovery bool) (NativeIssueTicketReceipt, error) {
	row, err := sqlc.New().NativeIssueTicketClaim(ctx, s.pool, sqlc.NativeIssueTicketClaimParams{AccountID: issueUUID(in.AccountID), RequestID: issueUUID(in.RequestID), ClaimID: issueUUID(uuid.NewString()), RecoverAfter: issueTime(time.Now().Add(time.Duration(api.IssueTrackerRequestTimeoutSeconds) * time.Second)), Recovery: recovery})
	if errors.Is(err, pgx.ErrNoRows) {
		return in, ErrNativeIssueTicketBusy
	}
	return nativeTicketRow(row), err
}
func (s *PgStore) FinishNativeIssueTicket(ctx context.Context, in NativeIssueTicketReceipt) (NativeIssueTicketReceipt, error) {
	if in.State != "ready" && in.State != "created" {
		return in, ErrInvalidArgument
	}
	row, err := sqlc.New().NativeIssueTicketFinish(ctx, s.pool, sqlc.NativeIssueTicketFinishParams{AccountID: issueUUID(in.AccountID), RequestID: issueUUID(in.RequestID), ClaimID: issueUUID(in.ClaimID), State: in.State, TicketUrl: in.TicketURL, ProviderID: in.ProviderID})
	if errors.Is(err, pgx.ErrNoRows) {
		return in, ErrNativeIssueTicketBusy
	}
	return nativeTicketRow(row), err
}
func (s *PgStore) LinkNativeIssueTicket(ctx context.Context, in NativeIssueTicketReceipt) (NativeIssueTicketReceipt, error) {
	row, err := sqlc.New().NativeIssueTicketLinked(ctx, s.pool, sqlc.NativeIssueTicketLinkedParams{AccountID: issueUUID(in.AccountID), RequestID: issueUUID(in.RequestID), LinkID: issueUUID(in.LinkID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return in, ErrNativeIssueTicketBusy
	}
	return nativeTicketRow(row), err
}
