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
	// A rejected request remains durable without occupying a slot. Retrying it
	// reacquires a slot under the same app lock used by reservation/deletion.
	var ticketState string
	var previouslyClaimed bool
	err = tx.QueryRow(ctx, `SELECT state,claim_id IS NOT NULL FROM native_issue_ticket_receipts
 WHERE account_id=$1 AND request_id=$2 AND app_id=$3 AND issue_id=$4 FOR UPDATE`,
		issueUUID(in.AccountID), issueUUID(in.RequestID), issueUUID(in.AppID), issueUUID(in.IssueID)).Scan(&ticketState, &previouslyClaimed)
	if errors.Is(err, pgx.ErrNoRows) {
		return in, ErrNativeIssueTicketBusy
	}
	if err != nil {
		return in, err
	}
	if !recovery && ticketState == "ready" && previouslyClaimed {
		n, err := q.NativeIssueTicketCount(ctx, tx, issueUUID(in.IssueID))
		if err != nil {
			return in, err
		}
		if n >= api.IssueTrackerNativeRequestsMax {
			return in, &IssueLimitError{Resource: "native_ticket_requests", Limit: api.IssueTrackerNativeRequestsMax, Observed: n + 1}
		}
	}
	row, err := q.NativeIssueTicketClaim(ctx, tx, sqlc.NativeIssueTicketClaimParams{AccountID: issueUUID(in.AccountID), RequestID: issueUUID(in.RequestID), ClaimID: issueUUID(uuid.NewString()), RecoverAfter: issueTime(time.Now().Add(time.Duration(api.IssueTrackerRequestTimeoutSeconds) * time.Second)), Recovery: recovery})
	if errors.Is(err, pgx.ErrNoRows) {
		return in, ErrNativeIssueTicketBusy
	}
	if err != nil {
		return in, err
	}
	if err := tx.Commit(ctx); err != nil {
		return in, err
	}
	return nativeTicketRow(row), nil
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
