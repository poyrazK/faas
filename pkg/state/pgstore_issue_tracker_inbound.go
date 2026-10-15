package state

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) GetIssueTrackerInboundGuard(ctx context.Context, app, account string) (int64, error) {
	v, err := sqlc.New().IssueTrackerInboundGuard(ctx, s.pool, sqlc.IssueTrackerInboundGuardParams{AppID: issueUUID(app), AccountID: issueUUID(account)})
	return v, mapErr(err)
}

func (s *PgStore) ApplyIssueTrackerInbound(ctx context.Context, in IssueTrackerInboundApply) (api.Issue, error) {
	r := in.Record
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.Issue{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	owner, err := q.IssueLockApp(ctx, tx, issueUUID(r.AppID))
	if err != nil {
		return api.Issue{}, mapErr(err)
	}
	if issueID(owner.AccountID) != r.AccountID {
		return api.Issue{}, ErrNotFound
	}
	// Lock policy before the intent fence: workers acquire these in that order.
	stored, err := q.IssueTrackerInboundPolicyLock(ctx, tx, issueUUID(r.Policy.LinkID))
	if err != nil {
		return api.Issue{}, mapErr(err)
	}
	current, err := autoSyncRow(stored)
	if err != nil {
		return api.Issue{}, err
	}
	revision, err := q.IssueTrackerInboundGuardLock(ctx, tx, issueUUID(r.AppID))
	if err != nil {
		return api.Issue{}, mapErr(err)
	}
	if in.GuardRevision <= 0 || revision != in.GuardRevision || current.AppID != r.AppID || current.AccountID != r.AccountID || current.Policy.Revision != r.Policy.Revision || current.Policy.SourceIssueID != r.Policy.SourceIssueID || current.BindingFingerprint != r.BindingFingerprint || current.Baseline != r.Baseline || !current.Policy.Enabled || current.Policy.PendingState != "" || current.ClaimUntil.After(time.Now()) {
		return api.Issue{}, IssueTrackerInboundChanged()
	}
	root, err := autoSyncScope(ctx, q, tx, r.AppID, r.AccountID, r.Policy.SourceIssueID, r.Policy.LinkID)
	if err != nil {
		return api.Issue{}, err
	}
	if issueID(root.ID) != r.Policy.SourceIssueID || root.State != current.Policy.SourceState || root.State != current.Baseline.State || (current.Policy.Outcome == "blocked" && current.Policy.Reason != "fixing_deployment_required" || current.Policy.Outcome == "conflict" && current.Policy.Reason != "provider_changed") {
		return api.Issue{}, IssueTrackerInboundChanged()
	}
	if !validInboundObservation(current, in.Observed) {
		return api.Issue{}, ErrInvalidArgument
	}
	action := api.IssueActionRequest{Action: "reopen"}
	if in.Observed.State == "resolved" {
		if id, e := uuid.Parse(in.FixedDeploymentID); e != nil || id == uuid.Nil {
			return api.Issue{}, ErrInvalidArgument
		}
		action.Action, action.FixedDeploymentID = "resolve", in.FixedDeploymentID
	} else if in.FixedDeploymentID != "" {
		return api.Issue{}, ErrInvalidArgument
	}
	details := map[string]string{"tracker_outbound_paused": "true", "tracker_previous_state": current.Policy.SourceState, "tracker_workflow_state_id": in.Observed.StateID, "tracker_direction": "pull", "tracker_link_id": r.Policy.LinkID, "tracker_provider": in.Observed.Provider, "tracker_ticket_id": in.Observed.ID, "tracker_policy_revision": strconv.FormatInt(current.Policy.Revision, 10), "tracker_intent_revision": strconv.FormatInt(revision, 10)}
	out, err := actOnIssueTxDetails(ctx, q, tx, r.AppID, r.Policy.SourceIssueID, r.AccountID, action, in.Now, details)
	if err != nil {
		return api.Issue{}, err
	}
	data, err := json.Marshal(in.Observed)
	if err != nil {
		return api.Issue{}, err
	}
	if err = q.IssueTrackerInboundApplied(ctx, tx, sqlc.IssueTrackerInboundAppliedParams{LinkID: issueUUID(r.Policy.LinkID), Baseline: data, SourceState: out.State}); err != nil {
		return api.Issue{}, err
	}
	receipt := current
	receipt.Policy.Enabled = false
	receipt.Policy.Revision++
	receipt.Policy.Outcome = "disabled"
	receipt.Policy.Reason = ""
	receipt.Policy.SourceState = out.State
	receipt.Policy.ObservedState = in.Observed.State
	if err = recordIssueTrackerSyncHistory(ctx, q, tx, receipt, "pulled", ""); err != nil {
		return api.Issue{}, err
	}
	return out, tx.Commit(ctx)
}

func validInboundObservation(r IssueTrackerAutoSyncRecord, o api.IssueTrackerProviderStatus) bool {
	b := r.Baseline
	if o.ID == "" || o.ID != b.ID || o.Provider != b.Provider || o.URL != b.URL || o.TeamID != b.TeamID || o.State == b.State || (o.State != "open" && o.State != "resolved") {
		return false
	}
	if o.Provider == "github" {
		return o.TeamID == "" && o.StateID == ""
	}
	if o.Provider != "linear" {
		return false
	}
	expected := r.Policy.LinearOpenStateID
	if o.State == "resolved" {
		expected = r.Policy.LinearResolvedStateID
	}
	return expected != "" && o.StateID == expected
}

func (s *PgStore) ListIssueTrackerInboundDeployments(ctx context.Context, app, account, issue string) ([]IssueTrackerFixingDeployment, error) {
	rows, err := sqlc.New().IssueTrackerInboundDeployments(ctx, s.pool, sqlc.IssueTrackerInboundDeploymentsParams{AppID: issueUUID(app), AccountID: issueUUID(account), IssueID: issueUUID(issue), PageLimit: api.IssuePageSize})
	if err != nil {
		return nil, err
	}
	out := make([]IssueTrackerFixingDeployment, 0, len(rows))
	for _, row := range rows {
		out = append(out, IssueTrackerFixingDeployment{ID: issueID(row.ID), CommitSHA: row.CommitSha.String, CreatedAt: row.CreatedAt.Time, Status: row.Status, LastSeenAt: row.LastSeenAt.Time, ObservedEvents: row.ObservedEvents})
	}
	return out, nil
}
