package state

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// PreviewIssueSplit reads one immutable original identity in a consistent snapshot.
func (s *PgStore) PreviewIssueSplit(ctx context.Context, app, id string, matcher api.IssueSplitMatcher) (api.IssueSplitPreview, error) {
	out := api.IssueSplitPreview{Matcher: matcher, MatchingEventIDs: []string{}, EligibleEventIDs: []string{}, ApplicationStatus: "new-rule"}
	if err := matcher.Validate(); err != nil {
		return out, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	original, err := q.IssueGet(ctx, tx, sqlc.IssueGetParams{AppID: issueUUID(app), ID: issueUUID(id)})
	if err != nil {
		return out, mapErr(err)
	}
	canonical, err := issueCanonicalRow(ctx, q, tx, app, id)
	if err != nil {
		return out, err
	}
	counts, err := q.IssueSplitPreviewCounts(ctx, tx, sqlc.IssueSplitPreviewCountsParams{
		IssueID: original.ID, ExceptionType: matcher.ExceptionType, ErrorCode: matcher.ErrorCode,
		Route: matcher.Route, SourceKind: matcher.SourceKind, SampleLimit: api.IssueSplitPreviewSamplesMax,
	})
	if err != nil {
		return out, err
	}
	out.OriginalIssueID, out.CanonicalIssueID = issueID(original.ID), issueID(canonical.ID)
	out.LifetimeEventCount, out.RetainedEventCount = original.EventCount, counts.RetainedEventCount
	out.EligibleEventCount, out.ShadowedEventCount = counts.EligibleEventCount, counts.ShadowedEventCount
	for _, eventID := range counts.EligibleEventIds {
		out.EligibleEventIDs = append(out.EligibleEventIDs, issueID(eventID))
	}
	rules, err := q.IssueListSourceSplitRules(ctx, tx, original.ID)
	if err != nil {
		return out, err
	}
	if original.SplitSourceIssueID.Valid {
		out.ApplicationStatus = "derived-branch"
	} else if len(rules) >= api.IssueSplitRulesPerIssueMax {
		out.ApplicationStatus = "rule-limit"
	}
	for _, rule := range rules {
		if issueSplitRuleRow(rule).Matcher == matcher {
			out.ApplicationStatus, out.ExistingRuleID, out.ExistingTargetIssueID = "existing-rule", issueID(rule.ID), issueID(rule.TargetIssueID)
			break
		}
	}
	if out.ApplicationStatus != "new-rule" {
		out.EligibleEventCount = 0
		out.EligibleEventIDs = []string{}
	}
	out.MatchingEventCount = counts.MatchingEventCount
	out.NonmatchingEventCount = counts.RetainedEventCount - counts.MatchingEventCount
	out.UnavailableHistoryEventCount = max(0, original.EventCount-counts.RetainedEventCount)
	for _, eventID := range counts.MatchingEventIds {
		out.MatchingEventIDs = append(out.MatchingEventIDs, issueID(eventID))
	}
	return out, tx.Commit(ctx)
}
