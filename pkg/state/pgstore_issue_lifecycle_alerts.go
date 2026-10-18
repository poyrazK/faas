package state

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issues"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func issueLifecyclePolicy(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, app, account string) (api.IssueLifecycleAlertPolicy, error) {
	r, err := q.IssueLifecyclePolicy(ctx, db, sqlc.IssueLifecyclePolicyParams{AppID: issueUUID(app), AccountID: issueUUID(account)})
	return api.IssueLifecycleAlertPolicy{Revision: r.Revision, Environments: r.Environments, CooldownSeconds: r.CooldownSeconds}, mapErr(err)
}

func (s *PgStore) GetIssueLifecycleAlertPolicy(ctx context.Context, app, account string) (api.IssueLifecycleAlertPolicy, error) {
	return issueLifecyclePolicy(ctx, sqlc.New(), s.pool, app, account)
}

func (s *PgStore) SetIssueLifecycleAlertPolicy(ctx context.Context, app, account string, in api.UpdateIssueLifecycleAlertPolicyRequest) (api.IssueLifecycleAlertPolicy, error) {
	empty := api.IssueLifecycleAlertPolicy{}
	environments, err := api.NormalizeIssueLifecycleAlertPolicy(in.Environments, in.CooldownSeconds)
	if err != nil || in.ExpectedRevision == nil || *in.ExpectedRevision < 0 {
		return empty, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	owner, err := q.IssueLockApp(ctx, tx, issueUUID(app))
	if err != nil {
		return empty, mapErr(err)
	}
	if issueID(owner.AccountID) != account {
		return empty, ErrNotFound
	}
	current, err := issueLifecyclePolicy(ctx, q, tx, app, account)
	if err != nil {
		return empty, err
	}
	if current.Revision != *in.ExpectedRevision {
		return empty, ErrIssueLifecycleAlertRevision
	}
	if current.CooldownSeconds == in.CooldownSeconds && slices.Equal(current.Environments, environments) {
		return current, nil
	}
	out := api.IssueLifecycleAlertPolicy{Revision: current.Revision + 1, Environments: environments, CooldownSeconds: in.CooldownSeconds}
	if err = q.IssueLifecyclePutPolicy(ctx, tx, sqlc.IssueLifecyclePutPolicyParams{AppID: owner.ID, Revision: out.Revision, Environments: environments, CooldownSeconds: in.CooldownSeconds}); err != nil {
		return empty, err
	}
	return out, tx.Commit(ctx)
}

// Preview evaluates the supplied draft in one read-only snapshot. It never
// reserves a cooldown, changes the issue, enqueues delivery or contacts Slack.
func (s *PgStore) PreviewIssueLifecycleAlert(ctx context.Context, app, account string, in api.PreviewIssueLifecycleAlertRequest, now time.Time) (api.IssueLifecycleAlertPreview, error) {
	out := api.IssueLifecycleAlertPreview{}
	environments, err := api.NormalizeIssueLifecycleAlertPolicy(in.Environments, in.CooldownSeconds)
	id, idErr := uuid.Parse(in.IssueID)
	if err != nil || idErr != nil || id == uuid.Nil || !issueLifecycleEvent(in.Event) {
		return out, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	policy, err := issueLifecyclePolicy(ctx, q, tx, app, account)
	if err != nil {
		return out, err
	}
	row, err := issueCanonicalRow(ctx, q, tx, app, id.String())
	if err != nil {
		return out, err
	}
	policy.Environments, policy.CooldownSeconds = environments, in.CooldownSeconds
	reason, next, err := issueLifecycleDecision(ctx, q, tx, row, policy, now)
	if err != nil {
		return out, err
	}
	out = api.IssueLifecycleAlertPreview{IssueID: issueID(row.ID), Event: in.Event, Environment: row.Environment, PolicyRevision: policy.Revision, Allowed: reason == "eligible", Reason: reason, NextEligibleAt: next}
	return out, tx.Commit(ctx)
}

func issueLifecycleEvent(action string) bool { return action == "created" || action == "regressed" }

func issueLifecycleDecision(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, row sqlc.AppIssue, policy api.IssueLifecycleAlertPolicy, now time.Time) (string, *time.Time, error) {
	last, err := q.IssueLifecycleLastAlert(ctx, db, sqlc.IssueLifecycleLastAlertParams{AppID: row.AppID, IssueID: row.ID})
	if err != nil {
		return "", nil, err
	}
	reason, next := issues.LifecycleAlertDecision(policy, row.Environment, issueTimePtr(last), now)
	return reason, next, nil
}
