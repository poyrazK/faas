package state

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/redact"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func issueSplitRuleRow(r sqlc.IssueSplitRule) api.IssueSplitRule {
	return api.IssueSplitRule{ID: issueID(r.ID), SourceIssueID: issueID(r.SourceIssueID), TargetIssueID: issueID(r.TargetIssueID), Priority: int(r.Priority), Matcher: api.IssueSplitMatcher{ExceptionType: r.ExceptionType, ErrorCode: r.ErrorCode, Route: r.Route, SourceKind: r.SourceKind}, Enabled: r.Enabled, InitialMovedEventCount: r.InitialMovedEventCount, UnavailableHistoryEventCount: r.UnavailableHistoryEventCount, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time}
}

// CreateIssueSplitRule moves only known retained evidence under the ingestion lock.
func (s *PgStore) CreateIssueSplitRule(ctx context.Context, app, id, actor string, matcher api.IssueSplitMatcher, limits api.IssueLimits, now time.Time) (api.IssueSplitRule, error) {
	if matcher.Validate() != nil || !limits.Enabled {
		return api.IssueSplitRule{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.IssueSplitRule{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.IssueLockApp(ctx, tx, issueUUID(app)); err != nil {
		return api.IssueSplitRule{}, mapErr(err)
	}
	source, err := q.IssueGet(ctx, tx, sqlc.IssueGetParams{AppID: issueUUID(app), ID: issueUUID(id)})
	if err != nil {
		return api.IssueSplitRule{}, mapErr(err)
	}
	if source.SplitSourceIssueID.Valid {
		return api.IssueSplitRule{}, ErrInvalidArgument
	}
	existing, err := q.IssueListSourceSplitRules(ctx, tx, source.ID)
	if err != nil {
		return api.IssueSplitRule{}, err
	}
	for _, rule := range existing {
		if issueSplitRuleRow(rule).Matcher == matcher {
			return issueSplitRuleRow(rule), tx.Commit(ctx)
		}
	}
	if len(existing) >= api.IssueSplitRulesPerIssueMax {
		return api.IssueSplitRule{}, &IssueLimitError{Resource: "split_rules", Limit: api.IssueSplitRulesPerIssueMax, Observed: int64(len(existing) + 1)}
	}
	count, err := q.IssueCount(ctx, tx, source.AppID)
	if err != nil {
		return api.IssueSplitRule{}, err
	}
	if count >= int64(limits.IssuesPerApp) {
		return api.IssueSplitRule{}, &IssueLimitError{Resource: "issues", Limit: int64(limits.IssuesPerApp), Observed: count + 1}
	}
	rule, err := issueCreateSplitRule(ctx, q, tx, source, actor, matcher, len(existing)+1, now)
	if err != nil {
		return api.IssueSplitRule{}, err
	}
	return issueSplitRuleRow(rule), tx.Commit(ctx)
}

func issueCreateSplitRule(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, source sqlc.AppIssue, actor string, m api.IssueSplitMatcher, priority int, now time.Time) (sqlc.IssueSplitRule, error) {
	canonical, err := issueCanonicalRow(ctx, q, tx, issueID(source.AppID), issueID(source.ID))
	if err != nil {
		return sqlc.IssueSplitRule{}, err
	}
	counts, err := q.IssueSplitPreviewCounts(ctx, tx, sqlc.IssueSplitPreviewCountsParams{IssueID: source.ID, ExceptionType: m.ExceptionType, ErrorCode: m.ErrorCode, Route: m.Route, SourceKind: m.SourceKind, SampleLimit: api.IssueSplitPreviewSamplesMax})
	if err != nil {
		return sqlc.IssueSplitRule{}, err
	}
	ruleID := uuid.NewString()
	digest := sha256.Sum256([]byte("gregale-issue-split-rule-v1:" + ruleID))
	branch, err := q.IssueCreateSplitGroup(ctx, tx, sqlc.IssueCreateSplitGroupParams{AccountID: source.AccountID, AppID: source.AppID, Environment: source.Environment, GroupingVersion: source.GroupingVersion, Fingerprint: fmt.Sprintf("%x", digest), Title: issueSplitBranchTitle(source.Title, m, priority), Now: issueTime(now), AssigneeAccountID: canonical.AssigneeAccountID, AssigneeTeamID: canonical.AssigneeTeamID, SourceIssueID: source.ID})
	if err != nil {
		return sqlc.IssueSplitRule{}, err
	}
	moved, err := q.IssueApplySplitRule(ctx, tx, sqlc.IssueApplySplitRuleParams{SourceIssueID: source.ID, TargetIssueID: branch.ID, ExceptionType: m.ExceptionType, ErrorCode: m.ErrorCode, Route: m.Route, SourceKind: m.SourceKind})
	if err != nil {
		return sqlc.IssueSplitRule{}, err
	}
	rule, err := q.IssueCreateSplitRule(ctx, tx, sqlc.IssueCreateSplitRuleParams{ID: issueUUID(ruleID), AppID: source.AppID, Environment: source.Environment, SourceIssueID: source.ID, TargetIssueID: branch.ID, Priority: int32(priority), ExceptionType: m.ExceptionType, ErrorCode: m.ErrorCode, Route: m.Route, SourceKind: m.SourceKind, MovedCount: moved, UnavailableCount: max(0, source.EventCount-counts.RetainedEventCount), Now: issueTime(now)})
	if err != nil {
		return sqlc.IssueSplitRule{}, err
	}
	details := map[string]string{"rule_id": ruleID, "source_issue_id": issueID(source.ID), "target_issue_id": issueID(branch.ID), "moved_event_count": strconv.FormatInt(moved, 10), "unavailable_history_event_count": strconv.FormatInt(rule.UnavailableHistoryEventCount, 10)}
	for _, row := range []sqlc.AppIssue{source, branch} {
		if err = issueRegroupAudit(ctx, q, tx, row, "split_rule_created", actor, details, now); err != nil {
			return sqlc.IssueSplitRule{}, err
		}
	}
	return rule, nil
}

func (s *PgStore) SetIssueSplitRuleEnabled(ctx context.Context, app, source, id, actor string, enabled bool, now time.Time) (api.IssueSplitRule, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.IssueSplitRule{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.IssueLockApp(ctx, tx, issueUUID(app)); err != nil {
		return api.IssueSplitRule{}, mapErr(err)
	}
	rule, err := q.IssueGetSplitRule(ctx, tx, sqlc.IssueGetSplitRuleParams{AppID: issueUUID(app), SourceIssueID: issueUUID(source), ID: issueUUID(id)})
	if err != nil {
		return api.IssueSplitRule{}, mapErr(err)
	}
	rule, err = issueSetSplitRuleEnabled(ctx, q, tx, rule, actor, enabled, now)
	if err != nil {
		return api.IssueSplitRule{}, err
	}
	return issueSplitRuleRow(rule), tx.Commit(ctx)
}

func issueSetSplitRuleEnabled(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, rule sqlc.IssueSplitRule, actor string, enabled bool, now time.Time) (sqlc.IssueSplitRule, error) {
	if rule.Enabled == enabled {
		return rule, nil
	}
	rule, err := q.IssueSetSplitRuleEnabled(ctx, tx, sqlc.IssueSetSplitRuleEnabledParams{ID: rule.ID, Enabled: enabled, Now: issueTime(now)})
	if err != nil {
		return sqlc.IssueSplitRule{}, err
	}
	details := map[string]string{"rule_id": issueID(rule.ID), "source_issue_id": issueID(rule.SourceIssueID), "target_issue_id": issueID(rule.TargetIssueID), "enabled": strconv.FormatBool(enabled)}
	for _, id := range []string{issueID(rule.SourceIssueID), issueID(rule.TargetIssueID)} {
		row, err := q.IssueGet(ctx, tx, sqlc.IssueGetParams{AppID: rule.AppID, ID: issueUUID(id)})
		if err != nil {
			return sqlc.IssueSplitRule{}, err
		}
		if err = issueRegroupAudit(ctx, q, tx, row, "split_rule_updated", actor, details, now); err != nil {
			return sqlc.IssueSplitRule{}, err
		}
	}
	return rule, nil
}

func issueRouteSplitCapture(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, source sqlc.AppIssue, event api.IssueEvent) (sqlc.AppIssue, error) {
	target, err := q.IssueFindSplitTarget(ctx, tx, sqlc.IssueFindSplitTargetParams{SourceIssueID: source.ID, ExceptionType: event.ExceptionType, ErrorCode: event.ErrorCode, Route: event.Route, SourceKind: event.SourceKind})
	if errors.Is(err, pgx.ErrNoRows) {
		return source, nil
	}
	if err != nil {
		return source, err
	}
	row, err := q.IssueGet(ctx, tx, sqlc.IssueGetParams{AppID: source.AppID, ID: target})
	return row, err
}

func issueSplitBranchTitle(source string, m api.IssueSplitMatcher, priority int) string {
	selector := m.ExceptionType
	if m.ErrorCode != "" {
		selector = "code=" + m.ErrorCode
	} else if m.Route != "" {
		selector = "route=" + m.Route
	} else if selector == "" {
		selector = "source=" + m.SourceKind
	}
	title := fmt.Sprintf("Split %d [%s] · %s", priority, strings.ReplaceAll(strings.ReplaceAll(selector, "\n", " "), "\r", " "), source)
	title, _ = redact.New(api.IssueMessageMaxBytes).Apply(title)
	return title
}
