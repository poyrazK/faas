package state

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type IssueTrackerCallbackStore interface {
	KnownIssueTrackerCallback(context.Context, issuetracker.CallbackEvent) (bool, error)
	EnqueueIssueTrackerCallback(context.Context, issuetracker.CallbackEvent) (string, error)
	ProcessIssueTrackerCallback(context.Context) (bool, error)
	CleanupIssueTrackerCallbacks(context.Context) error
}

func callbackParams(e issuetracker.CallbackEvent) sqlc.IssueTrackerCallbackPoliciesParams {
	return sqlc.IssueTrackerCallbackPoliciesParams{Provider: e.Provider, TicketID: e.TicketID, InstallationID: e.InstallationID, RepositoryID: e.RepositoryID, TicketUrl: e.TicketURL, AppID: issueUUID(e.AppID), ConnectionRevision: e.ConnectionRevision, WorkspaceID: issueUUID(e.WorkspaceID), TeamID: issueUUID(e.TeamID)}
}
func callbackKnown(rows []sqlc.IssueTrackerCallbackFindRow, e issuetracker.CallbackEvent) (bool, error) {
	known := false
	for _, r := range rows {
		if r.DeliveryID == e.DeliveryID && r.PayloadHash != e.PayloadHash {
			return false, ErrInvalidArgument
		}
		if r.PayloadHash == e.PayloadHash {
			known = true
		}
	}
	return known, nil
}
func (s *PgStore) KnownIssueTrackerCallback(ctx context.Context, e issuetracker.CallbackEvent) (bool, error) {
	rows, err := sqlc.New().IssueTrackerCallbackFind(ctx, s.pool, sqlc.IssueTrackerCallbackFindParams{ScopeKey: e.ScopeKey, DeliveryID: e.DeliveryID, PayloadHash: e.PayloadHash})
	if err != nil {
		return false, err
	}
	return callbackKnown(rows, e)
}
func (s *PgStore) EnqueueIssueTrackerCallback(ctx context.Context, e issuetracker.CallbackEvent) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err = q.IssueTrackerCallbackScopeLock(ctx, tx, e.ScopeKey); err != nil {
		return "", err
	}
	rows, err := q.IssueTrackerCallbackFind(ctx, tx, sqlc.IssueTrackerCallbackFindParams{ScopeKey: e.ScopeKey, DeliveryID: e.DeliveryID, PayloadHash: e.PayloadHash})
	if err != nil {
		return "", err
	}
	known, err := callbackKnown(rows, e)
	if err != nil {
		return "", err
	}
	if known {
		return "duplicate", tx.Commit(ctx)
	}
	policies, err := q.IssueTrackerCallbackPolicies(ctx, tx, callbackParams(e))
	if err != nil {
		return "", err
	}
	if len(policies) == 0 {
		return "ignored", tx.Commit(ctx)
	}
	count, err := q.IssueTrackerCallbackCount(ctx, tx, e.ScopeKey)
	if err != nil {
		return "", err
	}
	if count >= api.IssueTrackerCallbackReceiptsMax {
		return "", api.ErrCapacity("tracker callback receipts are full; polling remains available")
	}
	err = q.IssueTrackerCallbackInsert(ctx, tx, sqlc.IssueTrackerCallbackInsertParams{ScopeKey: e.ScopeKey, DeliveryID: e.DeliveryID, Provider: e.Provider, AppID: issueUUID(e.AppID), ConnectionRevision: e.ConnectionRevision, InstallationID: e.InstallationID, RepositoryID: e.RepositoryID, WorkspaceID: issueUUID(e.WorkspaceID), TeamID: issueUUID(e.TeamID), TicketID: e.TicketID, TicketUrl: e.TicketURL, PayloadHash: e.PayloadHash})
	if err != nil {
		return "", err
	}
	return "accepted", tx.Commit(ctx)
}
func (s *PgStore) ProcessIssueTrackerCallback(ctx context.Context) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.IssueTrackerCallbackNext(ctx, tx)
	if errors.Is(mapErr(err), ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	event := issuetracker.CallbackEvent{Provider: row.Provider, AppID: issueID(row.AppID), ConnectionRevision: row.ConnectionRevision, InstallationID: row.InstallationID, RepositoryID: row.RepositoryID, WorkspaceID: issueID(row.WorkspaceID), TeamID: issueID(row.TeamID), TicketID: row.TicketID, TicketURL: row.TicketUrl}
	policies, err := q.IssueTrackerCallbackPolicies(ctx, tx, callbackParams(event))
	if err != nil {
		return false, err
	}
	for _, p := range policies {
		if p.ClaimUntil.Valid && p.ClaimUntil.Time.After(time.Now()) {
			return false, nil
		}
	}
	for _, p := range policies {
		if err = q.IssueTrackerCallbackSchedule(ctx, tx, p.LinkID); err != nil {
			return false, err
		}
	}
	if err = q.IssueTrackerCallbackComplete(ctx, tx, sqlc.IssueTrackerCallbackCompleteParams{ScopeKey: row.ScopeKey, DeliveryID: row.DeliveryID}); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
func (s *PgStore) CleanupIssueTrackerCallbacks(ctx context.Context) error {
	return sqlc.New().IssueTrackerCallbackCleanup(ctx, s.pool, sqlc.IssueTrackerCallbackCleanupParams{Before: issueTime(time.Now().Add(-api.IssueTrackerCallbackRetention)), BatchSize: api.IssueTrackerCallbackBatch})
}
