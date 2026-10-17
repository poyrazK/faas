package state

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) GetIssueTrackerSourceRevision(ctx context.Context, app, account, issue string) (int64, error) {
	v, err := sqlc.New().IssueTrackerSourceRevision(ctx, s.pool, sqlc.IssueTrackerSourceRevisionParams{AppID: issueUUID(app), AccountID: issueUUID(account), IssueID: issueUUID(issue)})
	return v, mapErr(err)
}

// ApplyIssueTrackerAutoReopen consumes the live claim and advances the baseline
// in the same transaction as the reopening. An uncertain commit is recovered by
// observing this baseline; a stale worker cannot overwrite the committed result.
func (s *PgStore) ApplyIssueTrackerAutoReopen(ctx context.Context, r IssueTrackerAutoSyncRecord, observed api.IssueTrackerProviderStatus) (IssueTrackerAutoSyncRecord, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return r, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	owner, err := q.IssueLockApp(ctx, tx, issueUUID(r.AppID))
	if err != nil {
		return r, mapErr(err)
	}
	if issueID(owner.AccountID) != r.AccountID {
		return r, ErrNotFound
	}
	row, err := q.IssueTrackerInboundPolicyLock(ctx, tx, issueUUID(r.Policy.LinkID))
	if err != nil {
		return r, mapErr(err)
	}
	current, err := autoSyncRow(row)
	if err != nil {
		return r, err
	}
	source, err := lockIssueTrackerSourceRevision(ctx, q, tx, r.AppID, r.AccountID, r.Policy.SourceIssueID)
	if err != nil {
		return r, mapErr(err)
	}
	if !current.Policy.Enabled || current.Policy.Direction == "outbound" || current.Policy.Revision != r.Policy.Revision || current.AppID != r.AppID || current.AccountID != r.AccountID || current.Policy.SourceIssueID != r.Policy.SourceIssueID || current.BindingFingerprint != r.BindingFingerprint || current.Baseline != r.Baseline || current.Policy.Direction != r.Policy.Direction || current.Policy.PendingState != "" || current.SourceRevision <= 0 || current.SourceRevision != r.SourceRevision || source != r.SourceRevision {
		return r, IssueTrackerInboundChanged()
	}
	if r.ClaimID == "" || current.ClaimID != r.ClaimID || !current.ClaimUntil.After(time.Now()) {
		return r, ErrIssueTrackerAutoSyncClaimLost
	}
	if current.Policy.Outcome == "blocked" || current.Policy.Outcome == "conflict" {
		return r, IssueTrackerInboundChanged()
	}
	root, err := autoSyncScope(ctx, q, tx, r.AppID, r.AccountID, r.Policy.SourceIssueID, r.Policy.LinkID)
	if err != nil {
		return r, err
	}
	if issueID(root.ID) != r.Policy.SourceIssueID || root.State != "resolved" || current.Policy.SourceState != "resolved" || current.Baseline.State != "resolved" || observed.State != "open" || !validInboundObservation(current, observed) {
		return r, IssueTrackerInboundChanged()
	}
	now := time.Now().UTC()
	details := map[string]string{"tracker_direction": "pull", "tracker_mode": "automatic", "tracker_link_id": r.Policy.LinkID, "tracker_provider": observed.Provider, "tracker_ticket_id": observed.ID, "tracker_workflow_state_id": observed.StateID, "tracker_policy_revision": strconv.FormatInt(r.Policy.Revision, 10), "tracker_source_revision": strconv.FormatInt(source, 10)}
	if _, err = actOnIssueTxDetails(ctx, q, tx, r.AppID, r.Policy.SourceIssueID, r.AccountID, api.IssueActionRequest{Action: "reopen"}, now, details); err != nil {
		return r, err
	}
	// The reopening is itself source intent. Store the revision after its trigger
	// so our next observation does not mistake our own action for a manual edit.
	source, err = lockIssueTrackerSourceRevision(ctx, q, tx, r.AppID, r.AccountID, r.Policy.SourceIssueID)
	if err != nil {
		return r, err
	}
	data, err := json.Marshal(observed)
	if err != nil {
		return r, err
	}
	row, err = q.IssueTrackerAutoReopenComplete(ctx, tx, sqlc.IssueTrackerAutoReopenCompleteParams{LinkID: issueUUID(r.Policy.LinkID), Revision: r.Policy.Revision, ClaimID: issueUUID(r.ClaimID), Baseline: data, SourceRevision: source, NextCheckAt: issueTime(now.Add(api.IssueTrackerAutoSyncCheckInterval))})
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrIssueTrackerAutoSyncClaimLost
	}
	if err != nil {
		return r, err
	}
	out, err := autoSyncRow(row)
	if err != nil {
		return r, err
	}
	if err = recordIssueTrackerSyncHistory(ctx, q, tx, out, "checked", r.ClaimID); err != nil {
		return r, err
	}
	if err = tx.Commit(ctx); err != nil {
		return r, err
	}
	out.Completed = true
	return out, nil
}

// The effective revision must be read in a separate statement *after* acquiring
// the fence. READ COMMITTED can re-evaluate a locked row after a wait while its
// scalar subquery still uses the statement's earlier MVCC snapshot.
func lockIssueTrackerSourceRevision(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, app, account, issue string) (int64, error) {
	if _, err := q.IssueTrackerSourceRevisionLock(ctx, tx, sqlc.IssueTrackerSourceRevisionLockParams{AppID: issueUUID(app), IssueID: issueUUID(issue)}); err != nil {
		return 0, err
	}
	return q.IssueTrackerSourceRevision(ctx, tx, sqlc.IssueTrackerSourceRevisionParams{AppID: issueUUID(app), AccountID: issueUUID(account), IssueID: issueUUID(issue)})
}
