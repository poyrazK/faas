package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// DeleteIssue removes one explicitly selected standalone issue and its
// dependent history. Linked groups must first be split, and split sources
// must first have their derived branches removed.
func (s *PgStore) DeleteIssue(ctx context.Context, app, id string) error {
	_, err := s.DeleteIssueWithOptions(ctx, app, id, false)
	return err
}

// DeleteIssueWithOptions only abandons an unconfirmed provider dispatch when
// explicitly requested and its dispatch lease has expired. Confirmed tickets
// still require attachment before deletion.
func (s *PgStore) DeleteIssueWithOptions(ctx context.Context, app, id string, abandonUnconfirmed bool) ([]string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.IssueLockApp(ctx, tx, issueUUID(app)); err != nil {
		return nil, mapErr(err)
	}
	var selected pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM app_issues WHERE app_id=$1 AND id=$2`, issueUUID(app), issueUUID(id)).Scan(&selected)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var linked bool
	err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM issue_group_links WHERE issue_id=$1 OR canonical_issue_id=$1)
 OR EXISTS (SELECT 1 FROM app_issues WHERE split_source_issue_id=$1)`, selected).Scan(&linked)
	if err != nil {
		return nil, err
	}
	if linked {
		return nil, ErrIssueGroupingConflict
	}
	// The creator claims a receipt before making an external request. Lock
	// every receipt so a ready claim cannot race with this check and deletion.
	// A confirmed ticket must remain recoverable until linked; an expired
	// uncertain dispatch requires explicit abandonment.
	receipts, err := tx.Query(ctx, `SELECT request_id::text,state,COALESCE(recover_after<=now(),false) FROM native_issue_ticket_receipts WHERE issue_id=$1 FOR UPDATE`, selected)
	if err != nil {
		return nil, err
	}
	pending := false
	var abandoned []string
	for receipts.Next() {
		var requestID, ticketState string
		var expired bool
		if err = receipts.Scan(&requestID, &ticketState, &expired); err != nil {
			break
		}
		if ticketState == "created" || (ticketState == "dispatching" && (!abandonUnconfirmed || !expired)) {
			pending = true
		} else if ticketState == "dispatching" {
			abandoned = append(abandoned, requestID)
		}
	}
	if err == nil {
		err = receipts.Err()
	}
	receipts.Close()
	if err != nil {
		return nil, err
	}
	if pending {
		return nil, ErrIssueTicketPending
	}
	if _, err = tx.Exec(ctx, `DELETE FROM app_issues WHERE app_id=$1 AND id=$2`, issueUUID(app), selected); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return abandoned, nil
}
