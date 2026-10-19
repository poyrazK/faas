package state

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issues"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func compoundDecision(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, row sqlc.AppIssue, rule api.IssueCompoundAlertRule, now time.Time) (api.IssueCompoundAlertPreview, error) {
	empty := api.IssueCompoundAlertPreview{}
	count, err := q.IssueCompoundCount(ctx, db, sqlc.IssueCompoundCountParams{IssueID: row.ID, Since: issueTime(now.Add(-time.Duration(rule.WindowSeconds) * time.Second)), Until: issueTime(now)})
	if err != nil {
		return empty, err
	}
	last, err := q.IssueCompoundLastAlert(ctx, db, sqlc.IssueCompoundLastAlertParams{AppID: row.AppID, RuleKey: rule.Key, RuleHash: issues.CompoundRuleHash(rule), IssueID: row.ID})
	if err != nil {
		return empty, err
	}
	snoozed, err := q.IssueCompoundSnoozed(ctx, db, sqlc.IssueCompoundSnoozedParams{IssueID: row.ID, Now: issueTime(now)})
	if err != nil {
		return empty, err
	}
	return issues.CompoundAlertDecision(rule, issueRow(row), count.ObservedEvents, count.IdentifiedCustomers, snoozed, issueTimePtr(last), now), nil
}

// Runs under the capture's app lock after the unique event insertion. The
// receipt, activity, webhook and native Slack admission share its transaction.
func recordIssueCompoundAlerts(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, row sqlc.AppIssue, in RecordIssueParams) error {
	policy, err := issueCompoundPolicy(ctx, q, tx, issueID(row.AppID), issueID(row.AccountID))
	if err != nil {
		return err
	}
	for _, rule := range policy.Rules {
		if !rule.Enabled || rule.Environment != "" && rule.Environment != row.Environment || !in.Event.OccurredAt.After(in.Now.Add(-time.Duration(rule.WindowSeconds)*time.Second)) || in.Event.OccurredAt.After(in.Now) {
			continue
		}
		decision, err := compoundDecision(ctx, q, tx, row, rule, in.Now)
		if err != nil {
			return err
		}
		if !decision.Allowed {
			continue
		}
		details := map[string]string{
			"rule_key": rule.Key, "policy_revision": fmt.Sprint(policy.Revision), "environment": row.Environment,
			"minimum_events": fmt.Sprint(rule.MinimumEvents), "observed_events": fmt.Sprint(decision.ObservedEvents),
			"minimum_customers": fmt.Sprint(rule.MinimumCustomers), "identified_customers": fmt.Sprint(decision.IdentifiedCustomers),
			"window_seconds": fmt.Sprint(rule.WindowSeconds), "cooldown_seconds": fmt.Sprint(rule.CooldownSeconds),
			"window_start": decision.WindowStart.UTC().Format(time.RFC3339), "window_end": in.Now.UTC().Format(time.RFC3339),
			"deployment_id": in.Credential.DeploymentID,
		}
		if err = issueActivity(ctx, q, tx, row, "compound_threshold_reached", "", details, in.Now); err != nil {
			return err
		}
		if err = q.IssueCompoundRecordAlert(ctx, tx, sqlc.IssueCompoundRecordAlertParams{AppID: row.AppID, RuleKey: rule.Key, RuleHash: issues.CompoundRuleHash(rule), IssueID: row.ID, Now: issueTime(in.Now)}); err != nil {
			return err
		}
	}
	return nil
}

func (s *PgStore) PreviewIssueCompoundAlert(ctx context.Context, app, account string, in api.PreviewIssueCompoundAlertRequest, now time.Time) (api.IssueCompoundAlertPreview, error) {
	empty := api.IssueCompoundAlertPreview{}
	_, err := api.NormalizeIssueCompoundAlertRules([]api.IssueCompoundAlertRule{in.Rule})
	id, idErr := uuid.Parse(in.IssueID)
	if err != nil || idErr != nil || id == uuid.Nil {
		return empty, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return empty, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	policy, err := issueCompoundPolicy(ctx, q, tx, app, account)
	if err != nil {
		return empty, err
	}
	row, err := issueCanonicalRow(ctx, q, tx, app, id.String())
	if err != nil {
		return empty, err
	}
	out, err := compoundDecision(ctx, q, tx, row, in.Rule, now)
	if err != nil {
		return empty, err
	}
	out.PolicyRevision = policy.Revision
	return out, tx.Commit(ctx)
}
