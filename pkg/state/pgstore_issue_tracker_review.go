package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ListIssueTrackerReviews(ctx context.Context, app, account, category string, cur IssueCursor) (IssueTrackerReviewPage, error) {
	out := IssueTrackerReviewPage{Items: []IssueTrackerReview{}}
	hash := issueTrackerReviewHash(app, category)
	if !ValidIssueTrackerReviewCategory(category) || (!cur.Time.IsZero() && cur.FilterHash != hash) || cur.Sort != "" || cur.Tag != "" || cur.MinCustomers != 0 || cur.ImpactWindowEnd != nil {
		return out, ErrInvalidArgument
	}
	rows, err := sqlc.New().IssueTrackerReviewQueue(ctx, s.pool, sqlc.IssueTrackerReviewQueueParams{AppID: issueUUID(app), AccountID: issueUUID(account), Category: category, CursorTime: issueTime(cur.Time), CursorID: issueUUID(cur.ID), PageLimit: api.IssuePageSize + 1})
	if err != nil {
		return out, err
	}
	for i, r := range rows {
		if i == api.IssuePageSize {
			last := out.Items[len(out.Items)-1]
			out.NextCursor = EncodeIssueCursor(IssueCursor{Time: last.CreatedAt, ID: last.LinkID, FilterHash: hash})
			break
		}
		item := IssueTrackerReview{LinkID: issueID(r.LinkID), IssueID: issueID(r.IssueID), IssueTitle: r.IssueTitle, TicketTitle: r.TicketTitle, ExternalKey: r.ExternalKey, Provider: r.Provider, Environment: r.Environment, Direction: r.Direction, Outcome: r.Outcome, Reason: r.Reason, PendingState: r.PendingState, ObservedState: r.ObservedState, SourceState: r.SourceState, IssueState: r.IssueState, Revision: r.Revision, Regrouped: r.Regrouped, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time}
		if r.LastSuccessfulAt.Valid {
			item.LastSuccessfulAt = &r.LastSuccessfulAt.Time
		}
		if r.LastCheckedAt.Valid {
			item.LastCheckedAt = &r.LastCheckedAt.Time
		}
		if r.NextCheckAt.Valid {
			item.NextCheckAt = &r.NextCheckAt.Time
		}
		if r.ClaimUntil.Valid {
			item.ClaimUntil = &r.ClaimUntil.Time
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}
