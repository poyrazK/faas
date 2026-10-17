package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrNoIssueTrackerAutoSyncWork = errors.New("no automatic tracker sync due")
var ErrIssueTrackerAutoSyncClaimLost = errors.New("automatic tracker sync claim changed")

func autoSyncRow(r sqlc.IssueTrackerAutoSync) (IssueTrackerAutoSyncRecord, error) {
	out := IssueTrackerAutoSyncRecord{SourceRevision: r.SourceRevision, PendingSourceRevision: r.PendingSourceRevision, AppID: issueID(r.AppID), AccountID: issueID(r.AccountID), BindingFingerprint: r.BindingFingerprint, ClaimID: issueID(r.ClaimID), ClaimUntil: r.ClaimUntil.Time, Policy: api.IssueTrackerAutoSync{Direction: r.Direction, ObservedState: r.ObservedState, LinearOpenStateID: r.LinearOpenStateID, LinearResolvedStateID: r.LinearResolvedStateID, LinkID: issueID(r.LinkID), Enabled: r.Enabled, Revision: r.Revision, SourceIssueID: issueID(r.SourceIssueID), SourceState: r.SourceState, PendingState: r.PendingState, Outcome: r.Outcome, Reason: r.Reason}}
	if err := json.Unmarshal(r.Baseline, &out.Baseline); err != nil {
		return out, ErrInvalidArgument
	}
	out.Policy.ProviderState = out.Baseline.State
	if r.LastCheckedAt.Valid {
		t := r.LastCheckedAt.Time
		out.Policy.LastCheckedAt = &t
	}
	if r.Enabled && r.Outcome != "conflict" && r.Outcome != "blocked" {
		t := r.NextCheckAt.Time
		out.Policy.NextCheckAt = &t
	}
	return out, nil
}
func autoSyncScope(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, app, account, issue, link string) (sqlc.AppIssue, error) {
	// Scope both the canonical group and the reference before exposing policy data.
	root, err := issueCanonicalRow(ctx, q, tx, app, issue)
	if err != nil {
		return root, err
	}
	if issueID(root.AccountID) != account {
		return root, ErrNotFound
	}
	_, err = q.IssueGetTrackerLink(ctx, tx, sqlc.IssueGetTrackerLinkParams{IssueID: root.ID, ID: issueUUID(link)})
	return root, mapErr(err)
}
func (s *PgStore) GetIssueTrackerAutoSync(ctx context.Context, app, account, issue, link string) (IssueTrackerAutoSyncRecord, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return IssueTrackerAutoSyncRecord{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	root, err := autoSyncScope(ctx, q, tx, app, account, issue, link)
	if err != nil {
		return IssueTrackerAutoSyncRecord{}, err
	}
	row, err := q.IssueTrackerAutoSyncGet(ctx, tx, issueUUID(link))
	out := IssueTrackerAutoSyncRecord{AppID: app, AccountID: account, Policy: api.IssueTrackerAutoSync{Direction: "outbound", LinkID: link, SourceIssueID: issueID(root.ID), Outcome: "disabled"}}
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	} else if err == nil {
		out, err = autoSyncRow(row)
	}
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) ConfigureIssueTrackerAutoSync(ctx context.Context, app, account, issue string, in IssueTrackerAutoSyncRecord, expected int64) (IssueTrackerAutoSyncRecord, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return in, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	owner, err := q.IssueLockApp(ctx, tx, issueUUID(app))
	if err != nil {
		return in, mapErr(err)
	}
	if issueID(owner.AccountID) != account {
		return in, ErrNotFound
	}
	root, err := autoSyncScope(ctx, q, tx, app, account, issue, in.Policy.LinkID)
	if err != nil {
		return in, err
	}
	row, err := q.IssueTrackerInboundPolicyLock(ctx, tx, issueUUID(in.Policy.LinkID))
	revision := int64(0)
	if err == nil {
		revision = row.Revision
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return in, err
	}
	if expected != revision || (in.Policy.Enabled && in.Policy.SourceIssueID != issueID(root.ID)) {
		return in, api.NewProblem(409, "issue_tracker_auto_sync_changed", "Automatic sync policy changed", "read the policy and native status again before configuring")
	}
	previousPending := row.PendingState
	if in.Policy.Enabled {
		if row.PendingState != "" || row.ClaimUntil.Valid && row.ClaimUntil.Time.After(time.Now()) {
			return in, IssueTrackerInboundChanged()
		}
		direction, e := api.NormalizeIssueTrackerSyncDirection(in.Policy.Direction)
		if e != nil {
			return in, e
		}
		in.Policy.Direction = direction
		if direction != "outbound" {
			source, e := lockIssueTrackerSourceRevision(ctx, q, tx, app, account, issueID(root.ID))
			if e != nil {
				return in, e
			}
			if in.SourceRevision <= 0 || source != in.SourceRevision {
				return in, IssueTrackerInboundChanged()
			}
		} else {
			in.SourceRevision = 0
		}
		// Verify the source lifecycle in the configuration transaction as well.
		if root.State != in.Policy.SourceState {
			return in, api.NewProblem(409, "issue_tracker_auto_sync_changed", "Issue state changed", "preview again before enabling automatic sync")
		}
		data, e := json.Marshal(in.Baseline)
		if e != nil {
			return in, e
		}
		row, err = q.IssueTrackerAutoSyncEnable(ctx, tx, sqlc.IssueTrackerAutoSyncEnableParams{LinkID: issueUUID(in.Policy.LinkID), AppID: issueUUID(app), AccountID: issueUUID(account), SourceIssueID: root.ID, BindingFingerprint: in.BindingFingerprint, Baseline: data, SourceState: in.Policy.SourceState, LinearOpenStateID: in.Policy.LinearOpenStateID, LinearResolvedStateID: in.Policy.LinearResolvedStateID, Direction: in.Policy.Direction, SourceRevision: in.SourceRevision, ObservedState: in.Baseline.State})
	} else if revision > 0 {
		row, err = q.IssueTrackerAutoSyncDisable(ctx, tx, issueUUID(in.Policy.LinkID))
	} else {
		return IssueTrackerAutoSyncRecord{AppID: app, AccountID: account, Policy: api.IssueTrackerAutoSync{Direction: "outbound", LinkID: in.Policy.LinkID, SourceIssueID: issueID(root.ID), Outcome: "disabled"}}, tx.Commit(ctx)
	}
	if err != nil {
		return in, err
	}
	out, err := autoSyncRow(row)
	if err != nil {
		return in, err
	}
	event := "enabled"
	receipt := out
	if !in.Policy.Enabled {
		event = "disabled"
		receipt.Policy.PendingState = previousPending
	}
	if err = recordIssueTrackerSyncHistory(ctx, q, tx, receipt, event, ""); err != nil {
		return in, err
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) ClaimIssueTrackerAutoSync(ctx context.Context) (IssueTrackerAutoSyncRecord, error) {
	row, err := sqlc.New().IssueTrackerAutoSyncClaim(ctx, s.pool, sqlc.IssueTrackerAutoSyncClaimParams{ClaimID: issueUUID(uuid.NewString()), ClaimUntil: issueTime(time.Now().Add(api.IssueTrackerAutoSyncLease))})
	if errors.Is(err, pgx.ErrNoRows) {
		return IssueTrackerAutoSyncRecord{}, ErrNoIssueTrackerAutoSyncWork
	}
	if err != nil {
		return IssueTrackerAutoSyncRecord{}, err
	}
	return autoSyncRow(row)
}
func (s *PgStore) CheckIssueTrackerAutoSyncClaim(ctx context.Context, in IssueTrackerAutoSyncRecord) error {
	row, err := sqlc.New().IssueTrackerAutoSyncGet(ctx, s.pool, issueUUID(in.Policy.LinkID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrIssueTrackerAutoSyncClaimLost
	}
	if err != nil {
		return err
	}
	if !row.Enabled || row.Revision != in.Policy.Revision || issueID(row.ClaimID) != in.ClaimID || !row.ClaimUntil.Time.After(time.Now()) {
		return ErrIssueTrackerAutoSyncClaimLost
	}
	return nil
}
func (s *PgStore) MarkIssueTrackerAutoSyncPending(ctx context.Context, in IssueTrackerAutoSyncRecord, wanted string) (IssueTrackerAutoSyncRecord, error) {
	row, err := sqlc.New().IssueTrackerAutoSyncPending(ctx, s.pool, sqlc.IssueTrackerAutoSyncPendingParams{LinkID: issueUUID(in.Policy.LinkID), Revision: in.Policy.Revision, ClaimID: issueUUID(in.ClaimID), PendingState: wanted, PendingSourceRevision: in.PendingSourceRevision})
	if errors.Is(err, pgx.ErrNoRows) {
		return in, ErrIssueTrackerAutoSyncClaimLost
	}
	if err != nil {
		return in, err
	}
	return autoSyncRow(row)
}
func (s *PgStore) FinishIssueTrackerAutoSync(ctx context.Context, in IssueTrackerAutoSyncRecord) (IssueTrackerAutoSyncRecord, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return in, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.IssueLockApp(ctx, tx, issueUUID(in.AppID)); err != nil {
		return in, mapErr(err)
	}
	data, err := json.Marshal(in.Baseline)
	if err != nil {
		return in, err
	}
	row, err := q.IssueTrackerAutoSyncFinish(ctx, tx, sqlc.IssueTrackerAutoSyncFinishParams{LinkID: issueUUID(in.Policy.LinkID), Revision: in.Policy.Revision, ClaimID: issueUUID(in.ClaimID), Baseline: data, SourceRevision: in.SourceRevision, PendingSourceRevision: in.PendingSourceRevision, ObservedState: in.Policy.ObservedState, SourceState: in.Policy.SourceState, PendingState: in.Policy.PendingState, Outcome: in.Policy.Outcome, Reason: in.Policy.Reason, NextCheckAt: issueTime(time.Now().Add(api.IssueTrackerAutoSyncCheckInterval))})
	if errors.Is(err, pgx.ErrNoRows) {
		return in, ErrIssueTrackerAutoSyncClaimLost
	}
	if err != nil {
		return in, err
	}
	out, err := autoSyncRow(row)
	if err != nil {
		return in, err
	}
	if err = recordIssueTrackerSyncHistory(ctx, q, tx, out, "checked", in.ClaimID); err != nil {
		return in, err
	}
	return out, tx.Commit(ctx)
}
