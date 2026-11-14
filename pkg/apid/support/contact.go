package support

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/support/sqlc"
)

// ContactRequest opens a ticket that support starts (ADR-20261113180000):
// staff reach out to a customer who may be stuck before the customer
// writes in.
type ContactRequest struct {
	AccountID string `json:"account_id"`
	Subject   string `json:"subject"`
	Category  string `json:"category"`
	Body      string `json:"body"`
	// SignalID names the attention signal that prompted the contact; it is
	// audit metadata only.
	SignalID int64 `json:"signal_id,omitempty"`
}

// ContactCustomer creates a ticket in the customer's personal support scope
// whose first message is the staff member's. The sender is the assignee,
// the first response counts as given at creation, and the ticket waits for
// the customer, so no service clock runs until the customer replies. The
// customer is notified like any staff reply. key is the idempotency key.
func (s *Service) ContactCustomer(ctx context.Context, a Actor, key string, in ContactRequest) (api.SupportSubmission, error) {
	v, err := s.transact(ctx, a, "", true, true, func(x *session) (any, error) { return x.contact(key, in) })
	if err != nil {
		return api.SupportSubmission{}, err
	}
	switch out := v.(type) {
	case api.SupportSubmission:
		return out, nil
	case map[string]any:
		// A replayed receipt decodes as a map.
		r := api.SupportSubmission{SubmissionID: key}
		r.TicketID, _ = out["ticket_id"].(string)
		if n, ok := out["revision"].(float64); ok {
			r.Revision = int64(n)
		}
		return r, nil
	}
	return api.SupportSubmission{}, errors.New("support: unexpected contact result")
}

func (x *session) contact(key string, in ContactRequest) (any, error) {
	if !uid(in.AccountID).Valid {
		return nil, invalid("account_id must be a UUID")
	}
	in.AccountID = id(uid(in.AccountID))
	if v, e := x.receipt(key, "contact", in.AccountID, in); v != nil || e != nil {
		return v, e
	}
	if !bounded(in.Subject, api.SupportSubjectMaxBytes) || !bounded(in.Body, api.SupportBodyMaxBytes) || !oneOf(in.Category, "deployment", "billing", "account", "incident", "other") {
		return nil, invalid("provide a subject, body and valid support category")
	}
	customer, e := x.q.Account(x.ctx, x.tx, uid(in.AccountID))
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, missing()
	}
	if e != nil {
		return nil, e
	}
	if customer.Status != "active" || !customer.EmailVerifiedAt.Valid {
		return nil, conflict("the customer's account cannot receive support messages (inactive or unverified email)")
	}
	x.scope = Scope{Key: "account:" + in.AccountID, Plan: customer.Plan, AccountID: in.AccountID}
	if e = x.admission(x.scope.Key, false); e != nil {
		return nil, e
	}
	const priority = "normal"
	policy, e := x.choosePolicy(x.scope.Plan, in.Category, priority)
	if e != nil {
		return nil, e
	}
	episode, e := newEpisode(policy, x.scope.Plan, priority, in.Category, x.now)
	if e != nil {
		return nil, e
	}
	// Staff wrote first: the first response is given, and the ticket waits
	// for the customer.
	episode.reply(x.now)
	if e = episode.pause(x.now); e != nil {
		return nil, e
	}
	team, _, e := x.route(policy.TeamID, nil)
	if e != nil {
		return nil, e
	}
	ticketID := newID()
	st := ticketState{Episode: episode, RequiredSkills: []string{}, StaffInitiated: true}
	if e = x.q.TicketInsert(x.ctx, x.tx, q.TicketInsertParams{ID: uid(ticketID), ScopeKey: x.scope.Key, AccountID: uid(in.AccountID), OrgID: uid(""), CreatedBy: uid(x.actor.ID), Subject: in.Subject, Category: in.Category, Priority: priority, TeamID: uid(team), AssigneeID: uid(x.actor.ID), State: raw(st), CreatedAt: ts(x.now)}); e != nil {
		return nil, e
	}
	t := q.SupportTicket{ID: uid(ticketID), Status: "pending", Priority: priority, TeamID: uid(team), AssigneeID: uid(x.actor.ID)}
	if e = x.message(t, "staff", in.Body); e != nil {
		return nil, e
	}
	if e = x.save(t, st); e != nil {
		return nil, e
	}
	data := map[string]any{"policy_id": policy.ID, "policy_revision": policy.Revision, "team_id": team, "assignee_id": x.actor.ID, "episode_id": episode.ID, "initiated_by": "staff"}
	if in.SignalID > 0 {
		data["attention_signal_id"] = in.SignalID
	}
	if e = x.event(ticketID, "created", data); e != nil {
		return nil, e
	}
	// TicketSave moved the new ticket to pending as revision 2.
	result := api.SupportSubmission{TicketID: ticketID, SubmissionID: key, Revision: 2}
	return result, x.remember(key, "contact", in.AccountID, ticketID, in, result)
}
