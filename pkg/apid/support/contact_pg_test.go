package support

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-20261113180000: support opens a ticket for a customer who may be
// stuck. The customer sees it and is notified; no service clock runs
// until the customer replies.
func TestSupportContactCustomerOpensWaitingTicket(t *testing.T) {
	f := supportDB(t)
	ctx := context.Background()
	in := ContactRequest{AccountID: f.customer.ID, Subject: "Your deployments of shop are failing", Category: "deployment", Body: "We noticed three failed builds. Can we help?", SignalID: 7}
	key := newID()

	// Only staff may contact customers.
	var problem *api.Problem
	if _, err := f.s.ContactCustomer(ctx, f.other, key, in); !errors.As(err, &problem) || problem.Status != 403 {
		t.Fatalf("non-staff contact err = %v", err)
	}
	out, err := f.s.ContactCustomer(ctx, f.manager, key, in)
	if err != nil || out.TicketID == "" || out.Revision != 2 {
		t.Fatalf("contact = %+v, %v", out, err)
	}
	again, err := f.s.ContactCustomer(ctx, f.manager, key, in)
	if err != nil || again.TicketID != out.TicketID {
		t.Fatalf("replayed contact = %+v, %v", again, err)
	}
	changed := in
	changed.Body = "different"
	if _, err := f.s.ContactCustomer(ctx, f.manager, key, changed); !errors.As(err, &problem) || problem.Status != 409 {
		t.Fatalf("reused key err = %v", err)
	}

	// The customer sees a pending ticket that starts with the staff message.
	got := unpack[api.SupportTicket](t, f.call(t, f.customer, false, "get", out.TicketID, "", "", nil, 200))
	if got.Status != "pending" || got.Subject != in.Subject || got.Category != "deployment" {
		t.Fatalf("customer ticket = %+v", got)
	}
	messages := unpack[api.SupportMessagePage](t, f.call(t, f.customer, false, "messages", out.TicketID, "", "", nil, 200))
	if len(messages.Items) != 1 || messages.Items[0].Author != "staff" || messages.Items[0].Body != in.Body {
		t.Fatalf("messages = %+v", messages.Items)
	}
	var audience, kind string
	if err := f.pool.QueryRow(ctx, `SELECT audience, kind FROM support_notifications WHERE ticket_id=$1 AND recipient_id=$2`, out.TicketID, f.customer.ID).Scan(&audience, &kind); err != nil || audience != "customer" || kind != "staff_reply" {
		t.Fatalf("customer notification = %s %s %v", audience, kind, err)
	}

	// Staff see the sender as assignee and a first response at creation,
	// waiting for the customer.
	staff := unpack[StaffTicket](t, f.call(t, f.manager, true, "get", out.TicketID, "", "", nil, 200))
	if staff.AssigneeID != f.manager.ID || staff.Episode.FirstResponseAt == nil || !staff.Episode.FirstResponseAt.Equal(f.now) || staff.Episode.WaitSince == nil {
		t.Fatalf("staff ticket = %+v", staff)
	}

	// The customer's reply resumes the clocks and asks staff for a reply.
	f.now = f.now.Add(time.Hour)
	f.change(t, f.customer, false, out.TicketID, "reply", 2, "Yes please, the build fails on npm ci.")
	staff = unpack[StaffTicket](t, f.call(t, f.manager, true, "get", out.TicketID, "", "", nil, 200))
	if staff.Ticket.Status != "open" || staff.Episode.WaitSince != nil || staff.Episode.NextReplyDue == nil {
		t.Fatalf("after customer reply = %+v", staff)
	}

	// Bad input and unreachable accounts are refused.
	for _, bad := range []ContactRequest{
		{AccountID: "nope", Subject: "s", Category: "deployment", Body: "b"},
		{AccountID: f.customer.ID, Subject: "s", Category: "sales", Body: "b"},
		{AccountID: f.customer.ID, Subject: "", Category: "deployment", Body: "b"},
	} {
		if _, err := f.s.ContactCustomer(ctx, f.manager, newID(), bad); !errors.As(err, &problem) || problem.Status != 400 {
			t.Errorf("%+v: err = %v", bad, err)
		}
	}
	if _, err := f.s.ContactCustomer(ctx, f.manager, newID(), ContactRequest{AccountID: newID(), Subject: "s", Category: "other", Body: "b"}); !errors.As(err, &problem) || problem.Status != 404 {
		t.Fatalf("unknown account err = %v", err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE accounts SET email_verified_at=NULL WHERE id=$1`, f.other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.ContactCustomer(ctx, f.manager, newID(), ContactRequest{AccountID: f.other.ID, Subject: "s", Category: "other", Body: "b"}); !errors.As(err, &problem) || problem.Status != 409 {
		t.Fatalf("unverified account err = %v", err)
	}
}
