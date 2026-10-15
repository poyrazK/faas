package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/issues"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Called under RecordIssue's app lock, after the unique event insert. Receipt,
// activity and outbox commit with the capture; duplicates never reach this path.
func recordIssueFrequencyAlerts(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, row sqlc.AppIssue, in RecordIssueParams) error {
	policy, err := issueFrequencyPolicy(ctx, q, tx, issueID(row.AppID), issueID(row.AccountID))
	if err != nil {
		return err
	}
	for _, rule := range policy.Rules {
		if !issues.FrequencyRuleMatches(rule, issueRow(row), in.Event.OccurredAt, in.Now) {
			continue
		}
		since := in.Now.Add(-time.Duration(rule.WindowSeconds) * time.Second)
		count, err := q.IssueFrequencyCount(ctx, tx, sqlc.IssueFrequencyCountParams{IssueID: row.ID, Since: issueTime(since), Until: issueTime(in.Now)})
		if err != nil {
			return err
		}
		if count < rule.MinimumEvents {
			continue
		}
		hash := issues.FrequencyRuleHash(rule)
		last, err := q.IssueFrequencyLastAlert(ctx, tx, sqlc.IssueFrequencyLastAlertParams{AppID: row.AppID, RuleKey: rule.Key, RuleHash: hash, IssueID: row.ID})
		if err != nil {
			return err
		}
		if !issues.FrequencyAlertDue(rule, count, issueTimePtr(last), in.Now) {
			continue
		}
		details := map[string]string{
			"rule_key": rule.Key, "policy_revision": fmt.Sprint(policy.Revision), "environment": row.Environment,
			"minimum_events": fmt.Sprint(rule.MinimumEvents), "observed_events": fmt.Sprint(count),
			"window_seconds": fmt.Sprint(rule.WindowSeconds), "cooldown_seconds": fmt.Sprint(rule.CooldownSeconds),
			"window_start": since.UTC().Format(time.RFC3339), "window_end": in.Now.UTC().Format(time.RFC3339),
			"deployment_id": in.Credential.DeploymentID,
		}
		if err = issueActivity(ctx, q, tx, row, "frequency_threshold_reached", "", details, in.Now); err != nil {
			return err
		}
		if err = q.IssueFrequencyRecordAlert(ctx, tx, sqlc.IssueFrequencyRecordAlertParams{AppID: row.AppID, RuleKey: rule.Key, RuleHash: hash, IssueID: row.ID, Now: issueTime(in.Now)}); err != nil {
			return err
		}
	}
	return nil
}
