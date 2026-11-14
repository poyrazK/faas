package support

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/support/sqlc"
)

func notificationFeed(t *testing.T, f *fixture, a Actor, staff bool) api.SupportNotificationPage {
	t.Helper()
	return unpack[api.SupportNotificationPage](t, f.call(t, a, staff, "notifications", "", "", "", nil, 200))
}

func TestSupportNotificationsArePersonalAndReadBoundaryDoesNotClearNewReplies(t *testing.T) {
	f := supportDB(t)
	f.s.BootstrapEmails = func() []string { return []string{f.manager.ID + "@example.com"} }
	ticket := f.create(t, "Customer question")
	staff := notificationFeed(t, f, f.manager, true)
	if staff.UnreadCount != 1 || staff.Items[0].Kind != "ticket_created" {
		t.Fatalf("staff creation notice: %+v", staff)
	}
	f.change(t, f.manager, true, ticket, "reply", 1, "Public answer")
	first := notificationFeed(t, f, f.customer, false)
	if first.UnreadCount != 1 || first.Items[0].Kind != "staff_reply" {
		t.Fatalf("customer reply notice: %+v", first)
	}
	f.change(t, f.manager, true, ticket, "note", 2, "PRIVATE-NOTE")
	if got := notificationFeed(t, f, f.customer, false); got.UnreadCount != 1 || len(got.Items) != 1 {
		t.Fatal("private note generated customer notification")
	}
	f.change(t, f.manager, true, ticket, "reply", 3, "New reply arrived while the page was open")
	boundary := api.SupportReadRequest{ThroughSequence: first.Items[0].Sequence}
	read := unpack[api.SupportReadResult](t, f.call(t, f.customer, false, "read", ticket, "", "", boundary, 202))
	if read.UnreadCount != 1 || read.UnreadTotal != 1 {
		t.Fatalf("cleared unseen reply: %+v", read)
	}
	f.call(t, f.customer, false, "read", ticket, "", "", boundary, 202)
	current := unpack[api.SupportTicket](t, f.call(t, f.customer, false, "get", ticket, "", "", nil, 200))
	if current.Revision != 4 || current.UnreadCount != 1 {
		t.Fatalf("read mutated ticket or lost unread: %+v", current)
	}
	if got := notificationFeed(t, f, f.manager, true); got.UnreadCount != 1 {
		t.Fatal("customer read cleared staff read state")
	}
	f.call(t, f.other, false, "read", ticket, "", "", boundary, 404)
	f.call(t, f.customer, true, "notifications", "", "", "", nil, 403)
	filtered := unpack[api.SupportNotificationPage](t, f.call(t, f.customer, false, "notifications", "", "", "?unread=true", nil, 200))
	if len(filtered.Items) != 1 || filtered.Items[0].ReadAt != nil {
		t.Fatalf("unread filter: %+v", filtered)
	}
}

func TestSupportAssignmentAndReplyNotificationsDoNotNotifySelfOrDuplicateReplay(t *testing.T) {
	f := supportDB(t)
	f.call(t, f.manager, true, "staff_save", "", newID(), "", Staff{AccountID: f.other.ID, Role: "agent", Profile: Profile{Available: true, Capacity: 10}}, 202)
	ticket := f.create(t, "Help")
	key := newID()
	change := Change{ExpectedRevision: 1, Priority: "normal", AssigneeID: f.other.ID, Reason: "Assign deployment investigation"}
	f.call(t, f.manager, true, "triage", ticket, key, "", change, 202)
	f.call(t, f.manager, true, "triage", ticket, key, "", change, 202)
	feed := notificationFeed(t, f, f.other, true)
	// An unassigned creation can notify this agent too; assignment itself is unique.
	assigned := 0
	for _, n := range feed.Items {
		if n.Kind == "assigned" {
			assigned++
		}
	}
	if assigned != 1 {
		t.Fatalf("assignment replay duplicated: %+v", feed)
	}
	f.change(t, f.customer, false, ticket, "reply", 2, "More context")
	feed = notificationFeed(t, f, f.other, true)
	if feed.Items[0].Kind != "customer_reply" {
		t.Fatal("assigned agent did not receive customer reply")
	}
	if got := notificationFeed(t, f, f.customer, false); got.UnreadCount != 0 {
		t.Fatal("customer notified about own reply")
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE support_staff SET role='revoked' WHERE account_id=$1`, f.other.ID); err != nil {
		t.Fatal(err)
	}
	f.call(t, f.other, true, "notifications", "", "", "", nil, 403)
}

func TestSupportNotificationDeliveryRetriesWithStableKeyAndCurrentRecipientBinding(t *testing.T) {
	for _, test := range []string{"retry", "changed_email", "read", "expired", "disabled", "suspended", "lease_fence"} {
		t.Run(test, func(t *testing.T) {
			f := supportDB(t)
			ticket := f.create(t, "SECRET-CUSTOMER-TEXT")
			f.change(t, f.manager, true, ticket, "reply", 1, "SECRET-REPLY")
			calls := []NotificationEmail{}
			f.s.Notify = func(_ context.Context, n NotificationEmail) error {
				calls = append(calls, n)
				if test == "retry" || test == "changed_email" {
					if len(calls) == 1 {
						return errors.New("lost handoff acknowledgement")
					}
				}
				return nil
			}
			switch test {
			case "read":
				feed := notificationFeed(t, f, f.customer, false)
				f.call(t, f.customer, false, "read", ticket, "", "", api.SupportReadRequest{ThroughSequence: feed.Items[0].Sequence}, 202)
			case "expired":
				f.now = f.now.Add(api.SupportNotificationEmailTTL + time.Second)
			case "disabled":
				f.s.EmailAllowed = func(context.Context, string, string, string, bool) (bool, error) { return false, nil }
			case "suspended":
				if _, err := f.pool.Exec(t.Context(), `UPDATE accounts SET status='suspended' WHERE id=$1`, f.customer.ID); err != nil {
					t.Fatal(err)
				}
			case "lease_fence":
				queries := q.New()
				old := uid(newID())
				newLease := uid(newID())
				row, err := queries.NotificationClaim(t.Context(), f.pool, q.NotificationClaimParams{AvailableAt: ts(f.now), LeaseID: old, LeaseUntil: ts(f.now.Add(time.Second))})
				if err != nil {
					t.Fatal(err)
				}
				_, err = queries.NotificationClaim(t.Context(), f.pool, q.NotificationClaimParams{AvailableAt: ts(f.now.Add(2 * time.Second)), LeaseID: newLease, LeaseUntil: ts(f.now.Add(time.Minute))})
				if err != nil {
					t.Fatal(err)
				}
				if err := queries.NotificationFinish(t.Context(), f.pool, q.NotificationFinishParams{ID: row.ID, LeaseID: old, EmailState: "accepted", AvailableAt: ts(f.now)}); err != nil {
					t.Fatal(err)
				}
				var actual string
				if err := f.pool.QueryRow(t.Context(), `SELECT lease_id::text FROM support_notifications WHERE id=$1`, id(row.ID)).Scan(&actual); err != nil || actual != id(newLease) {
					t.Fatalf("stale worker finished new lease: %s %v", actual, err)
				}
				return
			}
			if err := f.s.DispatchNotification(t.Context()); err != nil {
				t.Fatal(err)
			}
			if test == "retry" || test == "changed_email" {
				if len(calls) != 1 {
					t.Fatal("first handoff missing")
				}
				if err := f.s.DispatchNotification(t.Context()); err != nil {
					t.Fatal(err)
				}
				if len(calls) != 1 {
					t.Fatal("retry ignored backoff")
				}
				f.now = f.now.Add(api.SupportWorkerInterval)
				if test == "changed_email" {
					if _, err := f.pool.Exec(t.Context(), `UPDATE accounts SET email='new@example.com' WHERE id=$1`, f.customer.ID); err != nil {
						t.Fatal(err)
					}
				}
				if err := f.s.DispatchNotification(t.Context()); err != nil {
					t.Fatal(err)
				}
				if test == "retry" && (len(calls) != 2 || calls[0] != calls[1]) {
					t.Fatalf("retry changed delivery identity: %+v", calls)
				}
				if test == "changed_email" && len(calls) != 1 {
					t.Fatal("ambiguous old handoff redirected to a new address")
				}
			} else if len(calls) != 0 {
				t.Fatalf("ineligible/read notification delivered: %+v", calls)
			}
			var state string
			if err := f.pool.QueryRow(t.Context(), `SELECT email_state FROM support_notifications WHERE audience='customer'`).Scan(&state); err != nil {
				t.Fatal(err)
			}
			want := "skipped"
			if test == "retry" {
				want = "accepted"
			}
			if state != want {
				t.Fatalf("state=%s want %s", state, want)
			}
			for _, n := range calls {
				if strings.Contains(n.Path, "SECRET") || n.MessageID == "" || n.TicketID != ticket || n.Path != "/dashboard/support/"+ticket {
					t.Fatalf("unsafe mail metadata: %+v", n)
				}
			}
		})
	}
}

func TestSupportNotificationWorkersDoNotDoubleClaimAndRetriesAreBounded(t *testing.T) {
	f := supportDB(t)
	ticket := f.create(t, "Help")
	f.change(t, f.manager, true, ticket, "reply", 1, "Answer")
	var calls atomic.Int32
	f.s.Notify = func(context.Context, NotificationEmail) error { calls.Add(1); return nil }
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			if err := f.s.DispatchNotification(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("double claim: %d", calls.Load())
	}
	f.change(t, f.manager, true, ticket, "reply", 2, "Next answer")
	f.s.Notify = func(context.Context, NotificationEmail) error { calls.Add(1); return errors.New("mail unavailable") }
	for range api.SupportNotificationAttempts + 2 {
		if err := f.s.DispatchNotification(t.Context()); err != nil {
			t.Fatal(err)
		}
		f.now = f.now.Add(time.Hour)
	}
	if calls.Load() != 1+api.SupportNotificationAttempts {
		t.Fatalf("unbounded retry: %d", calls.Load())
	}
	var failed int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM support_notifications WHERE email_state='failed'`).Scan(&failed); err != nil || failed != 1 {
		t.Fatalf("terminal failure not recorded: %d %v", failed, err)
	}
}

func TestSupportOrganizationNotificationsRecheckMembershipAtDelivery(t *testing.T) {
	f := supportDB(t)
	org := newID()
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO orgs(id,slug,plan,deleted_pending,personal_org) VALUES($1,'acme','pro',false,false)`, org); err != nil {
		t.Fatal(err)
	}
	for _, a := range []Actor{f.customer, f.other} {
		if _, err := f.pool.Exec(t.Context(), `INSERT INTO org_memberships(org_id,account_id,role) VALUES($1,$2,'admin')`, org, a.ID); err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest("POST", "/v1/orgs/acme/support/tickets", strings.NewReader(string(raw(api.CreateSupportTicketRequest{Subject: "Org question", Category: "billing", Body: "Help"}))))
	r.SetPathValue("org", "acme")
	r.Header.Set("Idempotency-Key", newID())
	w := httptest.NewRecorder()
	f.s.Serve(w, r, f.customer, "create", false)
	if w.Code != 202 {
		t.Fatal(w.Body)
	}
	ticket := unpack[api.SupportSubmission](t, w).TicketID
	f.change(t, f.manager, true, ticket, "reply", 1, "Organization reply")
	if got := notificationFeed(t, f, f.customer, false); got.UnreadCount != 0 {
		t.Fatal("organization notification leaked into personal feed")
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE org_memberships SET removed_at=now() WHERE org_id=$1 AND account_id=$2`, org, f.other.ID); err != nil {
		t.Fatal(err)
	}
	to := []string{}
	f.s.Notify = func(_ context.Context, n NotificationEmail) error {
		to = append(to, n.To)
		if n.Path != "/dashboard/orgs/acme/support/"+ticket {
			t.Errorf("scope lost: %s", n.Path)
		}
		return nil
	}
	for range 3 {
		if err := f.s.DispatchNotification(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if len(to) != 1 || to[0] != f.customer.ID+"@example.com" {
		t.Fatalf("former member received mail: %v", to)
	}
}

func TestSupportNotificationPermissionLookupOutageIsRetried(t *testing.T) {
	f := supportDB(t)
	ticket := f.create(t, "Help")
	f.change(t, f.manager, true, ticket, "reply", 1, "Answer")
	calls := 0
	available := false
	f.s.Notify = func(context.Context, NotificationEmail) error { calls++; return nil }
	f.s.EmailAllowed = func(context.Context, string, string, string, bool) (bool, error) {
		if !available {
			return false, errors.New("permission lookup temporarily unavailable")
		}
		return true, nil
	}
	if err := f.s.DispatchNotification(t.Context()); err == nil {
		t.Fatal("permission lookup outage was not reported")
	}
	var state string
	if err := f.pool.QueryRow(t.Context(), `SELECT email_state FROM support_notifications`).Scan(&state); err != nil || state != "inflight" || calls != 0 {
		t.Fatalf("lookup outage dropped notification: %s %d %v", state, calls, err)
	}
	available = true
	f.now = f.now.Add(api.SupportNotificationLease)
	if err := f.s.DispatchNotification(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("notification was not retried after permission lookup recovered")
	}
}
