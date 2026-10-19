package state

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) GetIssueTrackerNotificationPolicy(ctx context.Context, app, account string) (IssueTrackerNotificationPolicy, error) {
	p, err := sqlc.New().IssueTrackerNotificationPolicyGet(ctx, s.pool, sqlc.IssueTrackerNotificationPolicyGetParams{AppID: issueUUID(app), AccountID: issueUUID(account)})
	if errors.Is(err, pgx.ErrNoRows) {
		return IssueTrackerNotificationPolicy{FailureThreshold: api.IssueTrackerSyncNotificationFailuresDefault}, nil
	}
	return IssueTrackerNotificationPolicy{Enabled: p.Enabled, Revision: p.Revision, FailureThreshold: int(p.FailureThreshold)}, err
}
func (s *PgStore) SetIssueTrackerNotificationPolicy(ctx context.Context, app, account string, in IssueTrackerNotificationPolicy) (IssueTrackerNotificationPolicy, error) {
	if in.Revision < 0 || in.FailureThreshold < 1 || in.FailureThreshold > api.IssueTrackerSyncNotificationFailuresMax {
		return in, ErrInvalidArgument
	}
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
	old, err := q.IssueTrackerNotificationPolicyGet(ctx, tx, sqlc.IssueTrackerNotificationPolicyGetParams{AppID: owner.ID, AccountID: owner.AccountID})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return in, err
	}
	if old.Revision != in.Revision {
		return in, api.NewProblem(409, "issue_tracker_notification_policy_changed", "Notification settings changed", "reload the settings before saving")
	}
	row, err := q.IssueTrackerNotificationPolicyPut(ctx, tx, sqlc.IssueTrackerNotificationPolicyPutParams{AppID: owner.ID, AccountID: owner.AccountID, Enabled: in.Enabled, FailureThreshold: int32(in.FailureThreshold)})
	if err != nil {
		return in, err
	}
	return IssueTrackerNotificationPolicy{Enabled: row.Enabled, Revision: row.Revision, FailureThreshold: int(row.FailureThreshold)}, tx.Commit(ctx)
}

func pgTrackerSyncNotifications(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, r IssueTrackerAutoSyncRecord) error {
	setting, err := q.IssueTrackerNotificationPolicyGet(ctx, tx, sqlc.IssueTrackerNotificationPolicyGetParams{AppID: issueUUID(r.AppID), AccountID: issueUUID(r.AccountID)})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !setting.Enabled {
		return nil
	}
	if err != nil {
		return err
	}
	prev := trackerSyncIncident{}
	stored, err := q.IssueTrackerIncidentGet(ctx, tx, issueUUID(r.Policy.LinkID))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil {
		prev = trackerSyncIncident{SettingsRevision: stored.SettingsRevision, Failures: int(stored.Failures), ID: issueID(stored.IncidentID), AnnouncedKind: stored.AnnouncedKind, IssueID: issueID(stored.IssueID), TeamID: issueID(stored.TeamID)}
		for _, id := range stored.RecipientAccountIds {
			prev.Recipients = append(prev.Recipients, issueID(id))
		}
	}
	next, kind := trackerSyncNotificationTransition(prev, IssueTrackerNotificationPolicy{Enabled: true, Revision: setting.Revision, FailureThreshold: int(setting.FailureThreshold)}, r.Policy.Outcome, r.Policy.Reason, r.Policy.PendingState)
	if kind != "" {
		if err = pgTrackerSyncNotice(ctx, q, tx, r, prev, &next, kind, int(setting.FailureThreshold), 0); err != nil {
			return err
		}
	}
	return q.IssueTrackerIncidentPut(ctx, tx, sqlc.IssueTrackerIncidentPutParams{LinkID: issueUUID(r.Policy.LinkID), SettingsRevision: next.SettingsRevision, Failures: int32(next.Failures), IncidentID: issueUUID(next.ID), AnnouncedKind: next.AnnouncedKind, IssueID: issueUUID(next.IssueID), TeamID: issueUUID(next.TeamID), RecipientAccountIds: trackerNotificationUUIDs(next.Recipients)})
}

func pgTrackerSyncNotice(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, r IssueTrackerAutoSyncRecord, prev trackerSyncIncident, next *trackerSyncIncident, kind string, threshold, sequence int) error {
	target, err := q.IssueTrackerNotificationTarget(ctx, tx, sqlc.IssueTrackerNotificationTargetParams{LinkID: issueUUID(r.Policy.LinkID), AppID: issueUUID(r.AppID), AccountID: issueUUID(r.AccountID)})
	if err != nil {
		return mapErr(err)
	}
	route := *next
	if kind == "recovered" {
		route = prev
		eligible, e := q.IssueTrackerNotificationRecoveryRecipients(ctx, tx, sqlc.IssueTrackerNotificationRecoveryRecipientsParams{AppID: issueUUID(r.AppID), RecipientIds: trackerNotificationUUIDs(prev.Recipients), TeamID: issueUUID(prev.TeamID)})
		if e != nil {
			return e
		}
		route.Recipients = nil
		for _, id := range eligible {
			route.Recipients = append(route.Recipients, issueID(id))
		}
		if len(route.Recipients) == 0 {
			return nil
		}
	} else {
		route.IssueID = issueID(target.ID)
		route.TeamID = issueID(target.AssigneeTeamID)
		account, team := target.AssigneeAccountID, target.AssigneeTeamID
		if kind == "escalation" {
			route.TeamID = next.TeamID
			account, team = issueUUID(""), issueUUID(next.TeamID)
		} else {
			incident, e := q.IssueTrackerIncidentGet(ctx, tx, issueUUID(r.Policy.LinkID))
			if e != nil && !errors.Is(e, pgx.ErrNoRows) {
				return e
			}
			if e == nil && incident.EscalatedAt.Valid && issueID(incident.IncidentID) == route.ID {
				route.TeamID = issueID(incident.EscalationTeamID)
				account, team = issueUUID(""), incident.EscalationTeamID
			}
		}
		recipients, e := q.IssueTrackerNotificationRecipients(ctx, tx, sqlc.IssueTrackerNotificationRecipientsParams{AppID: issueUUID(r.AppID), AssigneeAccountID: account, TeamID: team})
		if e != nil {
			return e
		}
		route.Recipients = nil
		for _, id := range recipients {
			route.Recipients = append(route.Recipients, issueID(id))
		}
		if len(route.Recipients) == 0 {
			route.TeamID = ""
			route.Recipients = []string{r.AccountID}
		}
	}
	incidentID, _ := uuid.Parse(route.ID)
	dedupKind := kind
	if sequence > 0 {
		dedupKind += "/" + strconv.Itoa(sequence)
	}
	id := uuid.NewSHA1(incidentID, []byte(dedupKind)).String()
	event := "issue.tracker_sync.attention"
	if kind == "recovered" {
		event = "issue.tracker_sync.recovered"
	}
	issueIDNow := issueID(target.ID)
	issuePath := "/dashboard/apps/" + url.PathEscape(target.Slug) + "/issues?issue=" + url.QueryEscape(issueIDNow)
	healthPath := "/dashboard/apps/" + url.PathEscape(target.Slug) + "/issues/" + issueIDNow + "/tracker-sync/" + r.Policy.LinkID
	payloadBody := map[string]any{"id": id, "version": 1, "type": event, "incident_id": route.ID, "kind": kind, "app_id": r.AppID, "issue_id": issueIDNow, "link_id": r.Policy.LinkID, "provider": r.Baseline.Provider, "outcome": r.Policy.Outcome, "reason": r.Policy.Reason, "team_id": route.TeamID, "recipient_account_ids": route.Recipients, "consecutive_failures": next.Failures, "failure_threshold": threshold, "issue_path": issuePath, "health_path": healthPath}
	if sequence > 0 || kind == "escalation" {
		if sequence > 0 {
			payloadBody["reminder_sequence"] = sequence
		}
		payloadBody["triage_path"] = "/dashboard/apps/" + url.PathEscape(target.Slug) + "/issues/tracker-notifications/triage/" + id
	}
	payload, err := json.Marshal(payloadBody)
	if err != nil {
		return err
	}
	inserted, err := q.IssueTrackerNotificationInsert(ctx, tx, sqlc.IssueTrackerNotificationInsertParams{ID: issueUUID(id), LinkID: issueUUID(r.Policy.LinkID), AppID: issueUUID(r.AppID), AccountID: issueUUID(r.AccountID), IssueID: target.ID, IncidentID: issueUUID(route.ID), Kind: kind, Outcome: r.Policy.Outcome, Reason: r.Policy.Reason, TeamID: issueUUID(route.TeamID), RecipientAccountIds: trackerNotificationUUIDs(route.Recipients), Payload: payload, ReminderSequence: int32(sequence)})
	if err != nil {
		return err
	}
	if inserted > 0 {
		if kind != "recovered" {
			next.IssueID, next.TeamID, next.Recipients = route.IssueID, route.TeamID, route.Recipients
		}
		if err = q.IssueSlackEnqueue(ctx, tx, sqlc.IssueSlackEnqueueParams{DeliveryID: issueUUID(uuid.NewSHA1(incidentID, []byte("slack/"+id)).String()), NotificationID: issueUUID(id)}); err != nil {
			return err
		}
		if err = q.IssueAddTransition(ctx, tx, sqlc.IssueAddTransitionParams{ActivityID: issueUUID(id), AccountID: issueUUID(r.AccountID), AppID: issueUUID(r.AppID), Payload: payload}); err != nil {
			return err
		}
	}
	if err = q.IssueTrackerNotificationPrune(ctx, tx, sqlc.IssueTrackerNotificationPruneParams{LinkID: issueUUID(r.Policy.LinkID), RetainedCount: api.IssueTrackerSyncNotificationsMax}); err != nil {
		return err
	}
	return q.IssueTrackerTriagePrune(ctx, tx, issueUUID(r.Policy.LinkID))
}

func (s *PgStore) ListIssueTrackerSyncNotifications(ctx context.Context, app, owner, actor, team string, cur IssueCursor) (IssueTrackerNotificationPage, error) {
	return s.ListIssueTrackerSyncNotificationsView(ctx, app, owner, actor, team, "active", cur)
}

func (s *PgStore) ListIssueTrackerSyncNotificationsView(ctx context.Context, app, owner, actor, team, view string, cur IssueCursor) (IssueTrackerNotificationPage, error) {
	out := IssueTrackerNotificationPage{Items: []IssueTrackerSyncNotification{}}
	if team != "" {
		id, err := uuid.Parse(team)
		if err != nil || id == uuid.Nil {
			return out, ErrInvalidArgument
		}
		team = id.String()
	}
	if view == "" {
		view = "active"
	}
	if view != "active" && view != "snoozed" && view != "all" {
		return out, ErrInvalidArgument
	}
	hash := trackerSyncNotificationCursorHash(app, owner, actor, team, view)
	if !cur.Time.IsZero() && cur.FilterHash != hash || cur.Sort != "" || cur.Tag != "" || cur.MinCustomers != 0 || cur.ImpactWindowEnd != nil {
		return out, ErrInvalidArgument
	}
	rows, err := sqlc.New().IssueTrackerNotificationList(ctx, s.pool, sqlc.IssueTrackerNotificationListParams{InboxView: view, AppID: issueUUID(app), OwnerAccountID: issueUUID(owner), ActorAccountID: issueUUID(actor), TeamFilter: issueUUID(team), CursorTime: issueTime(cur.Time), CursorID: issueUUID(cur.ID), PageLimit: api.IssuePageSize + 1})
	if err != nil {
		return out, err
	}
	for i, r := range rows {
		if i == api.IssuePageSize {
			last := out.Items[len(out.Items)-1]
			out.NextCursor = EncodeIssueCursor(IssueCursor{Time: last.SortAt, ID: last.ID, FilterHash: hash})
			break
		}
		out.Items = append(out.Items, IssueTrackerSyncNotification{ID: issueID(r.ID), LinkID: issueID(r.LinkID), IssueID: issueID(r.IssueID), IssueTitle: r.IssueTitle, Kind: r.Kind, ReminderSequence: int(r.ReminderSequence), Outcome: r.Outcome, Reason: r.Reason, TeamID: issueID(r.TeamID), TeamName: r.TeamName, CreatedAt: r.CreatedAt.Time, SortAt: r.SortAt.Time, Snoozed: r.Snoozed, Resurfaced: r.Resurfaced, SnoozedUntil: trackerSnoozeTime(r.SnoozedUntil), SnoozedBy: issueID(r.SnoozedBy), SnoozedName: r.SnoozedName, SnoozeReason: r.SnoozeReason.String, AcknowledgedBy: issueID(r.AcknowledgedBy), ClaimedBy: issueID(r.ClaimedBy), AcknowledgedName: r.AcknowledgedName, ClaimedName: r.ClaimedName})
	}
	return out, nil
}

func trackerNotificationUUIDs(ids []string) []pgtype.UUID {
	out := make([]pgtype.UUID, 0, len(ids))
	for _, id := range ids {
		out = append(out, issueUUID(id))
	}
	return out
}

func (s *PgStore) CanReadIssueTrackerNotifications(ctx context.Context, app, owner, actor, link string) (bool, error) {
	return sqlc.New().IssueTrackerNotificationCanRead(ctx, s.pool, sqlc.IssueTrackerNotificationCanReadParams{AppID: issueUUID(app), OwnerAccountID: issueUUID(owner), ActorAccountID: issueUUID(actor), LinkID: issueUUID(link)})
}
func (s *PgStore) GetIssueTrackerNotificationHealth(ctx context.Context, app, owner, actor, issue, link string) (IssueTrackerSyncHealth, error) {
	return s.getIssueTrackerSyncHealth(ctx, app, owner, actor, issue, link)
}
