package dashboard_test

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/support"
	"github.com/onebox-faas/faas/pkg/apid/supportportal"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func TestSupportPagesRenderScopedFormsAndEscapeContent(t *testing.T) {
	attack := `<script>alert("private")</script>`
	now := time.Date(2026, 11, 12, 11, 0, 0, 0, time.UTC)
	ticket := api.SupportTicket{ID: "case-id", Subject: attack, Status: "open", Category: "deployment", Revision: 7, CreatedAt: now, UpdatedAt: now}
	staffTicket := support.StaffTicket{Ticket: ticket, Priority: "high", AssigneeID: "agent", Episode: support.Episode{FirstResponseDue: now, ResolutionDue: now.Add(time.Hour)}}
	for _, test := range []struct {
		name         string
		data         supportportal.Data
		want, absent []string
	}{
		{"customer list", supportportal.Data{Base: "/dashboard/orgs/acme/support", Scope: "Organization: acme", PortalView: support.PortalView{Tickets: api.SupportTicketPage{Items: []api.SupportTicket{ticket}}}}, []string{`href="/dashboard/orgs/acme/support/case-id"`, `action="/dashboard/orgs/acme/support"`, `name="category"`, "Start a conversation"}, []string{"Internal note", "Resolution target", "/dashboard/admin/support"}},
		{"customer detail", supportportal.Data{Base: "/dashboard/support", PortalView: support.PortalView{Ticket: &ticket, Messages: api.SupportMessagePage{Items: []api.SupportMessage{{Body: attack}}}}}, []string{`name="expected_revision" value="7"`, `action="/dashboard/support/case-id"`, "Mark resolved", "Send reply", "&lt;script&gt;"}, []string{"Internal note", "Assignment and priority"}},
		{"staff detail", supportportal.Data{Base: "/dashboard/admin/support", StaffMode: true, ActorID: "agent", PortalView: support.PortalView{Role: "agent", Ticket: &ticket, StaffTicket: &staffTicket, Messages: api.SupportMessagePage{Items: []api.SupportMessage{{Author: "note", Body: attack}}}}}, []string{"Internal note · staff only", "Assignment and priority", "First reply: 12 Nov 2026", "Wait for customer"}, []string{"How was your support experience?"}},
		{"staff read only", supportportal.Data{Base: "/dashboard/admin/support", StaffMode: true, ActorID: "someone-else", PortalView: support.PortalView{Role: "agent", Ticket: &ticket, StaffTicket: &staffTicket}}, []string{"Resolution target"}, []string{"Send reply", "Save internal note", "Mark resolved", "Save assignment"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.data.Filters = url.Values{}
			test.data.Keys = map[string]string{"reply": "saved-key"}
			w := httptest.NewRecorder()
			if err := dashboard.Render(w, slog.New(slog.NewTextHandler(io.Discard, nil)), "test-nonce", dashboard.Page{Title: "Support", Body: "support", Data: test.data}); err != nil {
				t.Fatal(err)
			}
			body := w.Body.String()
			if strings.Contains(body, attack) {
				t.Fatal("unescaped support content")
			}
			for _, want := range test.want {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q", want)
				}
			}
			for _, absent := range test.absent {
				if strings.Contains(body, absent) {
					t.Errorf("unexpected %q", absent)
				}
			}
			if !strings.Contains(body, `nonce="test-nonce"`) {
				t.Fatal("CSP nonce missing")
			}
		})
	}
}

func TestSupportResolvedEpisodeAndPausedClock(t *testing.T) {
	now := time.Date(2026, 11, 12, 11, 0, 0, 0, time.UTC)
	ticket := api.SupportTicket{ID: "case-id", Status: "resolved", Revision: 8, CanReopen: true, ReopenUntil: &now}
	d := supportportal.Data{Base: "/dashboard/support", Filters: url.Values{}, Keys: map[string]string{}, PortalView: support.PortalView{Ticket: &ticket, Satisfaction: &api.SupportSatisfaction{EpisodeID: "episode", Eligible: true}}}
	w := httptest.NewRecorder()
	if err := dashboard.Render(w, slog.Default(), "", dashboard.Page{Body: "support", Data: d}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Reopen conversation", `name="episode_id" value="episode"`, "Send rating"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(w.Body.String(), "Send reply") {
		t.Fatal("resolved ticket offered reply")
	}
	episode := support.Episode{WaitSince: &now, FirstResponseDue: now.Add(-time.Hour), ResolutionDue: now.Add(time.Hour)}
	if d.ResponseTarget(episode) != "Waiting for customer" || d.ResolutionTarget(episode) != "Paused while waiting for customer" {
		t.Fatal("paused clocks shown as live deadlines")
	}
}

func TestSupportKeepsDraftWhenConcurrentResolutionHidesReplyForm(t *testing.T) {
	ticket := api.SupportTicket{ID: "case-id", Status: "resolved", Revision: 2}
	d := supportportal.Data{Base: "/dashboard/support", Filters: url.Values{}, Keys: map[string]string{},
		PortalView: support.PortalView{Ticket: &ticket}, Failed: "reply", Error: "Ticket revision changed",
		Form: url.Values{"body": {"Unsent <script>draft</script>"}, "idempotency_key": {"original-request-key"}, "expected_revision": {"1"}}}
	w := httptest.NewRecorder()
	if err := dashboard.Render(w, slog.Default(), "", dashboard.Page{Body: "support", Data: d}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Your submitted message", "Unsent &lt;script&gt;draft&lt;/script&gt;", "original-request-key", "Revision: 1"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("lost submitted draft field %q", want)
		}
	}
}

func TestSupportUnreadBadgesAndAcknowledgementWaitForFinalMessagePage(t *testing.T) {
	ticket := api.SupportTicket{ID: "case-id", Subject: "Support question", Status: "open", Revision: 3, UnreadCount: 2}
	for _, more := range []bool{false, true} {
		d := supportportal.Data{Base: "/dashboard/support", Filters: url.Values{}, Keys: map[string]string{"read": "read-key"}, CSRF: "csrf", PortalView: support.PortalView{Ticket: &ticket, ReadThrough: 9007199254740993, Notifications: api.SupportNotificationPage{UnreadCount: 2}}}
		if more {
			d.Messages.NextCursor = 50
		}
		w := httptest.NewRecorder()
		if err := dashboard.Render(w, slog.Default(), "nonce", dashboard.Page{Body: "support", Data: d}); err != nil {
			t.Fatal(err)
		}
		html := w.Body.String()
		if !strings.Contains(html, `data-support-ticket-unread>2`) || strings.Contains(html, `data-support-read>`) == more {
			t.Fatalf("incorrect unread/acknowledgement state; more=%v", more)
		}
		if !more && !strings.Contains(html, `name="through_sequence" value="9007199254740993"`) {
			t.Fatal("notification boundary rounded in form")
		}
	}
}

func TestSupportStaffTicketShowsCustomerAccountPanel(t *testing.T) {
	attack := `<script>alert("panel")</script>`
	now := time.Date(2026, 11, 12, 11, 0, 0, 0, time.UTC)
	ticket := api.SupportTicket{ID: "case-id", Subject: "Deploys fail", Status: "open", Revision: 3, CreatedAt: now, UpdatedAt: now}
	staffTicket := support.StaffTicket{Ticket: ticket, Priority: "normal", Episode: support.Episode{FirstResponseDue: now, ResolutionDue: now}}
	panel := &supportportal.CustomerContext{AccountID: "acct-1", OrgSlug: "acme", Plan: "pro", Status: "active", Apps: 2,
		Diagnosis: api.ObsTenantDiagnosis{
			Restrictions:      []api.ObsTenantRestriction{{Kind: api.RestrictionAbuseHold, Reason: attack, Since: &now}},
			FailedDeployments: []api.ObsTenantFailedDeploy{{AppSlug: "shop", ErrorCode: "build_failed", Error: "npm ci exited 1", CreatedAt: now}},
			DomainIssues:      []api.ObsTenantDomainIssue{{Domain: "www.shop.example", Problem: "certificate issuance failed"}},
			EnabledFlags:      []string{"customer_support"},
			CustomerErrors:    api.ObsCustomerErrorSummary{WindowHours: 24, Total: 3, ByCode: []api.ObsCustomerErrorCode{{Code: "plan_excess", Count: 3, LastAt: now}}},
			Unavailable:       []string{"domains"},
		}}
	render := func(d supportportal.Data) string {
		t.Helper()
		d.Filters, d.Keys = url.Values{}, map[string]string{}
		w := httptest.NewRecorder()
		if err := dashboard.Render(w, slog.New(slog.NewTextHandler(io.Discard, nil)), "n", dashboard.Page{Title: "Support", Body: "support", Data: d}); err != nil {
			t.Fatal(err)
		}
		return w.Body.String()
	}
	body := render(supportportal.Data{Base: "/dashboard/admin/support", StaffMode: true, ActorID: "agent", Customer: panel,
		PortalView: support.PortalView{Role: "agent", Ticket: &ticket, StaffTicket: &staffTicket}})
	if strings.Contains(body, attack) {
		t.Fatal("unescaped restriction reason")
	}
	for _, want := range []string{"Customer account", "<code>acct-1</code>", "acme", "abuse_hold", "&lt;script&gt;", "<code>build_failed</code>",
		"npm ci exited 1", "www.shop.example", "certificate issuance failed", "3 refused", "<code>plan_excess</code> × 3",
		"<code>customer_support</code>", "Not loaded: domains", "gregalectl accounts 360 --account-id acct-1"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if !strings.Contains(render(supportportal.Data{Base: "/dashboard/admin/support", StaffMode: true, Customer: &supportportal.CustomerContext{Unavailable: true},
		PortalView: support.PortalView{Role: "agent", Ticket: &ticket, StaffTicket: &staffTicket}}), "Account context could not be loaded") {
		t.Error("unavailable panel not rendered")
	}
	if strings.Contains(render(supportportal.Data{Base: "/dashboard/support", Customer: panel, PortalView: support.PortalView{Ticket: &ticket}}), "Customer account") {
		t.Error("customer view rendered the staff account panel")
	}
}
