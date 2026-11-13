package support

import (
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/support/sqlc"
)

func (s *Service) bootstrapEmails() []string {
	if s.BootstrapEmails != nil {
		return s.BootstrapEmails()
	}
	return []string{}
}
func (x *session) audience() string {
	if x.role != "" {
		return "staff"
	}
	return "customer"
}

// Fanout is part of the support transaction. Receipt replay returns before this
// point, and a unique event/recipient/audience key fences duplicate intents.
func (x *session) notifyEvent(event, ticket, kind string, data any) error {
	audience, notice := "staff", "customer_reply"
	if x.role != "" {
		audience, notice = "customer", "staff_reply"
	}
	switch kind {
	case "created":
		audience, notice = "staff", "ticket_created"
	case "reply", "reopened", "engineering_published":
	case "handoff":
		change, ok := data.(map[string]any)
		if !ok || change["to_assignee_id"] == "" || change["to_assignee_id"] == change["from_assignee_id"] {
			return nil
		}
		audience, notice = "staff", "assigned"
	default:
		return nil
	}
	var recipients []pgtype.UUID
	var err error
	if audience == "customer" {
		recipients, err = x.q.NotificationCustomers(x.ctx, x.tx, q.NotificationCustomersParams{TicketID: uid(ticket), ActorID: uid(x.actor.ID), PageLimit: api.SupportNotificationRecipients + 1})
	} else {
		recipients, err = x.q.NotificationStaff(x.ctx, x.tx, q.NotificationStaffParams{TicketID: uid(ticket), ActorID: uid(x.actor.ID), BootstrapEmails: x.s.bootstrapEmails(), PageLimit: api.SupportNotificationRecipients + 1})
	}
	if err != nil {
		return err
	}
	if len(recipients) > api.SupportNotificationRecipients {
		limit, observed := int64(api.SupportNotificationRecipients), int64(len(recipients))
		problem := api.NewProblem(429, "support_notification_recipients_limited", "Support recipient limit reached", "See the support notification limits in docs/customer-support.md.")
		problem.Limit, problem.Observed, problem.DocsURL = &limit, &observed, "https://gregale.dev/docs/customer-support"
		return problem
	}
	for _, recipient := range recipients {
		if err := x.q.NotificationQueue(x.ctx, x.tx, q.NotificationQueueParams{ID: uid(newID()), EventID: uid(event), TicketID: uid(ticket), RecipientID: recipient, Audience: audience, Kind: notice, CreatedAt: ts(x.now)}); err != nil {
			return err
		}
	}
	return nil
}

func (x *session) unreadCount(ticket string) (int64, error) {
	return x.q.NotificationCount(x.ctx, x.tx, q.NotificationCountParams{RecipientID: uid(x.actor.ID), Audience: x.audience(), Staff: x.role != "", ScopeKey: x.scope.Key, Since: ts(x.now.Add(-api.SupportNotificationRetention)), TicketID: uid(ticket)})
}
func (x *session) unreadCounts(tickets []pgtype.UUID) (map[string]int64, error) {
	counts := map[string]int64{}
	if len(tickets) == 0 {
		return counts, nil
	}
	rows, err := x.q.NotificationTicketCounts(x.ctx, x.tx, q.NotificationTicketCountsParams{RecipientID: uid(x.actor.ID), Audience: x.audience(), TicketIds: tickets, CreatedAt: ts(x.now.Add(-api.SupportNotificationRetention))})
	for _, row := range rows {
		counts[id(row.TicketID)] = row.UnreadCount
	}
	return counts, err
}

func (x *session) notifications(r *http.Request) (api.SupportNotificationPage, error) {
	out := api.SupportNotificationPage{Items: []api.SupportNotification{}}
	before := pgtype.Int8{}
	if value := r.URL.Query().Get("before"); value != "" {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 1 {
			return out, invalid("before must be a positive notification sequence")
		}
		before = pgtype.Int8{Int64: n, Valid: true}
	}
	unread := false
	if value := r.URL.Query().Get("unread"); value != "" {
		var err error
		unread, err = strconv.ParseBool(value)
		if err != nil {
			return out, invalid("unread must be a boolean")
		}
	}
	rows, err := x.q.NotificationList(x.ctx, x.tx, q.NotificationListParams{RecipientID: uid(x.actor.ID), Audience: x.audience(), Staff: x.role != "", ScopeKey: x.scope.Key, Since: ts(x.now.Add(-api.SupportNotificationRetention)), BeforeSequence: before, UnreadOnly: unread, PageLimit: api.SupportPageSize + 1})
	if err != nil {
		return out, err
	}
	if len(rows) > api.SupportPageSize {
		rows = rows[:api.SupportPageSize]
		out.NextCursor = rows[len(rows)-1].Sequence.Int64
	}
	for _, row := range rows {
		n := api.SupportNotification{ID: id(row.ID), Sequence: row.Sequence.Int64, TicketID: id(row.TicketID), Kind: row.Kind, CreatedAt: row.CreatedAt.Time}
		if row.ReadAt.Valid {
			read := row.ReadAt.Time
			n.ReadAt = &read
		}
		out.Items = append(out.Items, n)
	}
	out.UnreadCount, err = x.unreadCount("")
	return out, err
}

// Read state belongs to this actor and audience only. Updating existing rows up
// to the observed sequence is intrinsically idempotent and cannot clear a later
// notification. This operation changes no conversation revision or service clock.
func (x *session) markRead(ticket string, in api.SupportReadRequest) (api.SupportReadResult, error) {
	out := api.SupportReadResult{TicketID: ticket}
	if _, _, err := x.ticket(ticket); err != nil {
		return out, err
	}
	if in.ThroughSequence < 1 {
		return out, invalid("through_sequence must be a positive notification sequence")
	}
	err := x.q.NotificationRead(x.ctx, x.tx, q.NotificationReadParams{ReadAt: ts(x.now), RecipientID: uid(x.actor.ID), Audience: x.audience(), TicketID: uid(ticket), ThroughSequence: pgtype.Int8{Int64: in.ThroughSequence, Valid: true}})
	if err != nil {
		return out, err
	}
	if out.UnreadCount, err = x.unreadCount(ticket); err != nil {
		return out, err
	}
	out.UnreadTotal, err = x.unreadCount("")
	return out, err
}
