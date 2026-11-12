package support

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/support/sqlc"
)

// PortalView is an authenticated dashboard snapshot. Staff fields are populated
// only after the same live authorization used by the private JSON endpoints.
type PortalView struct {
	Tickets       api.SupportTicketPage
	Inbox         StaffPage
	Ticket        *api.SupportTicket
	StaffTicket   *StaffTicket
	Messages      api.SupportMessagePage
	Satisfaction  *api.SupportSatisfaction
	Teams         []Team
	Staff         []Staff
	Role          string
	Notifications api.SupportNotificationPage
	ReadThrough   int64
}

// Portal reads through the native workflows, never through dashboard-owned SQL.
func (s *Service) Portal(r *http.Request, a Actor, staff bool) (PortalView, error) {
	v, err := s.transact(r.Context(), a, r.PathValue("org"), staff, false, func(x *session) (any, error) {
		return x.portal(r)
	})
	if err != nil {
		return PortalView{}, err
	}
	return v.(PortalView), nil
}

func (x *session) portal(r *http.Request) (PortalView, error) {
	v := PortalView{Role: x.role}
	query := r.URL.Query()
	query.Set("before", query.Get("notifications_before"))
	query.Set("unread", query.Get("notifications_unread"))
	copy := r.Clone(r.Context())
	copiedURL := *r.URL
	copiedURL.RawQuery = query.Encode()
	copy.URL = &copiedURL
	var notificationErr error
	v.Notifications, notificationErr = x.notifications(copy)
	if notificationErr != nil {
		return v, notificationErr
	}
	if x.role != "" {
		var err error
		if v.Teams, err = x.teams(); err != nil {
			return v, err
		}
		if v.Staff, err = x.staffList(); err != nil {
			return v, err
		}
	}
	if r.PathValue("ticket") != "" {
		return x.portalTicket(r, v)
	}
	p, err := listParams(r)
	if err != nil {
		return v, err
	}
	result, err := x.list(p)
	if err != nil {
		return v, err
	}
	if x.role != "" {
		v.Inbox = result.(StaffPage)
	} else {
		v.Tickets = result.(api.SupportTicketPage)
	}
	return v, nil
}

func (x *session) portalTicket(r *http.Request, v PortalView) (PortalView, error) {
	ticket := r.PathValue("ticket")
	result, err := x.get(ticket)
	if err != nil {
		return v, err
	}
	if x.role != "" {
		detail := result.(StaffTicket)
		v.StaffTicket, v.Ticket = &detail, &detail.Ticket
	} else {
		detail := result.(api.SupportTicket)
		v.Ticket = &detail
		rating, err := x.satisfaction("", ticket, nil)
		if err != nil {
			return v, err
		}
		survey := rating.(api.SupportSatisfaction)
		v.Satisfaction = &survey
	}
	after, err := querySequence(r)
	if err != nil {
		return v, err
	}
	result, err = x.messages(ticket, after)
	if err == nil {
		v.Messages = result.(api.SupportMessagePage)
		state, e := x.q.NotificationTicketState(x.ctx, x.tx, q.NotificationTicketStateParams{RecipientID: uid(x.actor.ID), Audience: x.audience(), TicketID: uid(ticket), CreatedAt: ts(x.now.Add(-api.SupportNotificationRetention))})
		if e != nil {
			return v, e
		}
		v.ReadThrough = state.ThroughSequence
	}
	return v, err
}
