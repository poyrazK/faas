package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ListIssueApplicationResponses(ctx context.Context, app, owner, actor string, filter IssueApplicationResponseFilter) (IssueApplicationResponseInbox, error) {
	out := IssueApplicationResponseInbox{Items: []IssueApplicationResponseItem{}}
	if err := filter.Validate(); err != nil {
		return out, err
	}
	rows, err := sqlc.New().IssueApplicationResponseInbox(ctx, s.pool, sqlc.IssueApplicationResponseInboxParams{AppID: issueUUID(app), OwnerAccountID: issueUUID(owner), ActorAccountID: issueUUID(actor), EntitledPlans: issueSlackEntitledPlans(), AfterID: issueUUID(filter.AfterID), MyTeams: filter.MyTeams, ResponseFilter: filter.Status, PageLimit: api.IssuePageSize + 1})
	if err != nil {
		return out, err
	}
	for n, r := range rows {
		if n == api.IssuePageSize {
			out.NextID = out.Items[n-1].IssueID
			break
		}
		snoozed := r.SnoozedUntil.Valid && r.SnoozedUntil.Time.After(r.CheckedAt.Time)
		out.Items = append(out.Items, IssueApplicationResponseItem{IssueID: issueID(r.ID), Environment: r.Environment, Revision: r.Revision, Acknowledged: r.AcknowledgedAt.Valid, Snoozed: snoozed, Resurfaced: r.SnoozedUntil.Valid && !snoozed && !r.AcknowledgedAt.Valid, Escalated: r.EscalatedAt.Valid, SnoozedUntil: trackerSnoozeTime(r.SnoozedUntil), ReminderDueAt: trackerSnoozeTime(r.ReminderDueAt), EscalationDueAt: trackerSnoozeTime(r.EscalationDueAt), ResponsibleTeamID: issueID(r.ResponsibleTeamID), ResponsibleTeamName: r.ResponsibleTeamName, RoutedTeamID: issueID(r.RoutedTeamID), RoutedTeamName: r.RoutedTeamName, Outcome: r.Outcome, DeliveryStatus: r.DeliveryStatus})
	}
	return out, nil
}
